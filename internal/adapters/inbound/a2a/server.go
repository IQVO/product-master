// Package a2a is the inbound Agent2Agent adapter: it exposes this bounded
// context to the fleet's agent ecosystem as a third driving adapter over the
// same application-layer use cases the REST and MCP adapters use. It is built
// on the official A2A Go SDK (a2aproject/a2a-go) and speaks JSON-RPC 2.0 over
// HTTP, with the Agent Card served at the well-known path.
//
// Per docs/adr/0012-a2a-inbound-adapter.md, A2A is the DELEGATION channel
// between bounded contexts: an agent client sends a goal-shaped skill request
// as a structured message part and this agent answers from its own state
// through its own use cases. It is not a data-sync channel -- facts between
// contexts still travel exclusively as CloudEvents on Kafka (ADR 0004),
// including events raised by the write skill through the transactional
// outbox. Downstream contexts keep LOCAL COPIES fed by that topic (ADR 0001);
// an A2A call is never the way to look a product up on the hot path of another
// service.
//
// Like the MCP adapter (ADR 0005), this package depends inward on the
// application layer (use cases and ports) and the domain only -- never on an
// outbound adapter or a sibling inbound adapter. The composition root
// (cmd/a2a) wires concrete repositories into the use cases. Domain structs
// never leak across the message boundary. There is NO auth of any kind
// (fleet-wide revert 2026-09-11; TestNoAuthMiddlewareReintroduced).
package a2a

import (
	"context"
	"iter"
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/product-master/internal/application/usecases"
)

// Config carries the deployment-specific identity of this agent: the absolute
// URL clients use to reach its JSON-RPC endpoint (advertised on the Agent
// Card) and the card version. It is supplied by the composition root from
// A2A_AGENT_URL; the card MUST advertise a URL the fleet's agent clients can
// actually dial (in-cluster: http://<release>-a2a.<ns>.svc.cluster.local:8093/).
type Config struct {
	AgentURL string
	Version  string
}

// Deps is everything the A2A skills need, injected by the composition root.
// It carries the SAME use cases the REST and MCP adapters use: the two reads
// behind GET /products/{sku} and GET /gtins/{gtin}, and the one write the MCP
// surface already offers (PUT /products/{sku}/pack-levels/{level}, ADR 0011).
// The adapter never constructs an outbound adapter itself.
type Deps struct {
	GetProduct       *usecases.GetProduct
	ResolveGTIN      *usecases.ResolveGTIN
	DeclarePackLevel *usecases.DeclarePackLevel
}

// Card builds this agent's Agent Card (ADR 0012). The surface is two curated,
// goal-shaped skills -- not one skill per endpoint, the same curation
// discipline ADR 0005 applied to MCP tools. product_profile composes what a
// delegating agent needs to answer "what is this item and how do I handle
// it"; declare_pack_level is the single state-changing delegation and exists
// only because the MCP surface already offers that write (ADR 0011). Product
// registration, classification, measurement and retiring a pack level are NOT
// offered: they stay REST (and retire_pack_level stays MCP). The card declares
// no security schemes on purpose: this surface is unauthenticated by the same
// fleet-wide decision as REST and MCP; schemes are added here, not in a
// redesign, if that decision is ever reversed.
func Card(cfg Config) *a2a.AgentCard {
	return &a2a.AgentCard{
		Name:        "product-master",
		Description: "Product Master agent for the Warehouse Management System: tells a delegating agent what a SKU is for handling and physically (description, handling classification, declared and measured unit dimensions and weight, packaging hierarchy), looks a product up from a scanned GTIN, and declares one packaging level with its GTIN under this context's invariants. It answers no \"where\" or \"how many\" question: stock is inventory-storage's.",
		Version:     cfg.Version,
		Provider: &a2a.AgentProvider{
			Org: "IQVO",
			URL: "https://github.com/IQVO/product-master",
		},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(cfg.AgentURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Capabilities:       a2a.AgentCapabilities{Streaming: false},
		Skills: []a2a.AgentSkill{
			{
				ID:          skillProductProfile,
				Name:        "Product handling and packaging profile",
				Description: "Composed read of one product: description and version, its handling classification (tags, temperature class, DOT hazard class; absent when unclassified), its physical profile (declared and latest measured unit dimensions, the effective ones and whether they disagree by more than 10%) and its whole packaging hierarchy, retired levels included. Identify the product with exactly one of sku or gtin; a GTIN (8, 12, 13 or 14 digits as printed on the barcode) is resolved to its SKU and the pack level it identifies is returned as identifiedBy.",
				Tags:        []string{"product", "classification", "packaging", "read"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"product_profile","arguments":{"sku":"SKU-1"}}`,
					`{"skill":"product_profile","arguments":{"gtin":"4006381333931"}}`,
				},
			},
			{
				ID:          skillDeclarePackLevel,
				Name:        "Declare a packaging level with its GTIN",
				Description: "Declares one packaging level (EACH, INNER, CASE or PALLET) of a registered SKU with its GTIN, exactly as the REST and MCP surfaces do, and publishes ProductPackLevelDeclared (and ProductPackLevelRetired when it supersedes a level) through the transactional outbox. Every level but EACH names childGtin (an active lower level of this SKU) and qtyOfChild (at least 2). A GTIN identifies exactly one (sku, level), fleet-wide. Idempotent: repeating an identical declaration changes nothing; declaring an active level's GTIN with another quantity is rejected (a new quantity needs a new GTIN). Rejections come back as problem answers and nothing is written.",
				Tags:        []string{"product", "packaging", "write"},
				InputModes:  []string{"application/json"},
				Examples: []string{
					`{"skill":"declare_pack_level","arguments":{"sku":"SKU-1","level":"EACH","gtin":"4006381333931"}}`,
					`{"skill":"declare_pack_level","arguments":{"sku":"SKU-1","level":"CASE","gtin":"24006381333935","childGtin":"4006381333931","qtyOfChild":12,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":9000},"isDespatchUnit":true}}`,
				},
			},
		},
	}
}

// NewExecutor returns the AgentExecutor that maps an incoming message to a
// skill and answers with a single agent Message (a message-only agent in A2A
// terms): both curated skills are transactional request/response work, so
// there is no long-running Task lifecycle to track in this first A2A
// surface. The response carries the caller's contextId (and taskId when the
// client referenced one) so a delegating saga can group its interactions.
func NewExecutor(deps Deps) a2asrv.AgentExecutor {
	return a2asrv.AgentExecutorFunc(func(ctx context.Context, ec *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
		return func(yield func(a2a.Event, error) bool) {
			yield(answer(ctx, deps, ec), nil)
		}
	})
}

// Handler wires the executor into the transport-agnostic A2A request handler
// and wraps it in the JSON-RPC 2.0 HTTP handler -- the only transport this
// agent ships (ADR 0012), mirroring ADR 0005's "Streamable HTTP only"
// minimalism for MCP.
func Handler(executor a2asrv.AgentExecutor) http.Handler {
	return a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))
}
