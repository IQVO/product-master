//go:build integration

// Integration tests for the MCP inbound adapter over the REAL Streamable
// HTTP transport: mcp.Handler(server) mounted on an httptest.Server, driven
// by the SDK's own client (mcp.NewClient + StreamableClientTransport), with
// the REAL Postgres-backed read use cases behind it — exactly the deployment
// shape cmd/mcp serves. This proves the wire contract (initialize,
// tools/list, tools/call) end-to-end against seeded product master data,
// not the tool handlers in isolation. Per docs/adr/0005 the surface is
// read-only: every registered tool is called, and domain rejections must
// surface as tool errors (res.IsError), never transport errors.
//
// Postgres comes from testcontainers (TestMain in this file): one container
// per package run, migrated once into a template database, one private
// clone per test. Never an external DATABASE_URL, never t.Skip.
package mcp_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	inboundmcp "github.com/claudioed/product-master/internal/adapters/inbound/mcp"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/usecases"
)

// One Postgres container serves the whole package, migrated once into a
// template database; each test gets a private clone (milliseconds). See the
// reference shape in wes-work-planning's
// internal/adapters/inbound/mcp/streamable_http_integration_test.go. Never
// an external DATABASE_URL, never t.Skip.
const templateDB = "mcp_migrated_template"

var (
	sharedBaseURL string
	dbSeq         atomic.Uint64
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("product_master_mcp"),
		tcpostgres.WithUsername("mcp"),
		tcpostgres.WithPassword("mcp"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		return 1
	}
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintf(os.Stderr, "terminate postgres container: %v\n", err)
		}
	}()

	sharedBaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres connection string: %v\n", err)
		return 1
	}
	if err := createDatabase(ctx, templateDB); err != nil {
		fmt.Fprintf(os.Stderr, "create template database: %v\n", err)
		return 1
	}
	if err := postgres.RunMigrations(withDB(sharedBaseURL, templateDB)); err != nil {
		fmt.Fprintf(os.Stderr, "migrate template: %v\n", err)
		return 1
	}
	return m.Run()
}

// withDB rewrites the path of a connection URL to the named database.
func withDB(baseURL, name string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		panic(err)
	}
	u.Path = "/" + name
	return u.String()
}

// createDatabase creates an empty database inside the shared container.
func createDatabase(ctx context.Context, name string) error {
	conn, err := pgx.Connect(ctx, sharedBaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	return nil
}

// migratedDB hands the test a connection URL to its own private database
// cloned from the migrated template (a file-level copy: milliseconds).
func migratedDB(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("mcp_%d", dbSeq.Add(1))
	conn, err := pgx.Connect(context.Background(), sharedBaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), fmt.Sprintf(
		"CREATE DATABASE %q WITH TEMPLATE %q", name, templateDB)); err != nil {
		t.Fatalf("clone database: %v", err)
	}
	return withDB(sharedBaseURL, name)
}

// frozenClock pins the write path's ports.Clock so seeded events and rows
// are deterministic.
type frozenClock struct{ now time.Time }

func (c frozenClock) Now() time.Time { return c.now }

var mcpAt = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// seedThroughWriteUseCases writes the products the REST adapter would have
// written (same use cases, real Postgres adapters, transactional outbox), so
// the MCP tools are proven to read what production writes.
func seedThroughWriteUseCases(t *testing.T, ctx context.Context, w usecases.Writer) {
	t.Helper()
	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{
		SKU: "ITCOV-MCP-1", Description: "Frozen peas",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := (&usecases.ClassifyProduct{Writer: w}).Handle(ctx, usecases.ClassifyProductCommand{
		SKU: "ITCOV-MCP-1", HandlingTags: []string{"TemperatureSensitive", "Hazmat"},
		TemperatureClass: "Frozen", DOTHazardClass: 3,
	}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	if _, err := (&usecases.DeclareDimensions{Writer: w}).Handle(ctx, usecases.DeclareDimensionsCommand{
		SKU:             "ITCOV-MCP-1",
		DimensionsInput: usecases.DimensionsInput{LengthMm: 200, WidthMm: 100, HeightMm: 50, WeightG: 500},
	}); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{
		SKU: "ITCOV-MCP-2", Description: "Mug",
	}); err != nil {
		t.Fatalf("register second: %v", err)
	}
}

// newStreamableHarness wires the REAL production stack — Postgres repos,
// UnitOfWork, write use cases for seeding, the read use cases, mcp.NewServer,
// mcp.Handler — serves it over Streamable HTTP and connects the SDK client.
func newStreamableHarness(t *testing.T) *sdkmcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, migratedDB(t))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	w := usecases.Writer{
		Products: postgres.NewProductRepo(pool), Outbox: postgres.NewOutboxRepo(pool),
		Encoder: outboundkafka.NewEncoder(), UoW: postgres.NewUnitOfWork(pool), Clock: frozenClock{mcpAt},
	}
	seedThroughWriteUseCases(t, ctx, w)

	server := inboundmcp.NewServer(inboundmcp.Deps{
		GetProduct:   &usecases.GetProduct{Products: postgres.NewProductRepo(pool)},
		ListProducts: &usecases.ListProducts{Products: postgres.NewProductRepo(pool)},
	})
	hs := httptest.NewServer(inboundmcp.Handler(server))
	t.Cleanup(hs.Close)

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "itcov-test-host", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint: hs.URL, DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool drives tools/call and fails on a transport error, returning the
// result so tests can branch on res.IsError.
func callTool(t *testing.T, session *sdkmcp.ClientSession, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("tools/call %s: %v", name, err)
	}
	return res
}

// okCall drives tools/call and fails unless it succeeded, returning the
// structured content.
func okCall(t *testing.T, session *sdkmcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res := callTool(t, session, name, args)
	if res.IsError {
		t.Fatalf("tools/call %s returned a tool error: %s", name, resultText(res))
	}
	out, _ := res.StructuredContent.(map[string]any)
	if out == nil {
		t.Fatalf("tools/call %s returned no structured content: %+v", name, res)
	}
	return out
}

// failCall drives tools/call and fails unless it surfaced a TOOL error
// carrying want, never a transport error.
func failCall(t *testing.T, session *sdkmcp.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := callTool(t, session, name, args)
	if !res.IsError {
		t.Fatalf("tools/call %s must surface a tool error, got success: %+v", name, res.StructuredContent)
	}
	if got := resultText(res); !strings.Contains(got, want) {
		t.Fatalf("tools/call %s error = %q, want it to carry %q", name, got, want)
	}
}

// resultText renders a tool result's error/content as one string.
func resultText(res *sdkmcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestStreamableHTTP_ListToolsExposesTheReadOnlyContract(t *testing.T) {
	session := newStreamableHarness(t)
	ctx := context.Background()

	list, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range list.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"get_product", "list_products", "get_product_classification", "get_physical_profile"} {
		if !names[want] {
			t.Fatalf("tools/list must expose %q, got %v", want, names)
		}
	}
	// Every tool of this surface is read-only (docs/adr/0005) and must say
	// so, so a model host can gate writes away from this server.
	for _, tool := range list.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s must carry ReadOnlyHint=true", tool.Name)
		}
	}
}

func TestStreamableHTTP_CallToolReadsWhatPostgresPersisted(t *testing.T) {
	session := newStreamableHarness(t)

	// get_product: the classified, dimensioned product written by the seed.
	p := okCall(t, session, "get_product", map[string]any{"sku": "ITCOV-MCP-1"})
	if p["description"] != "Frozen peas" || p["version"] != 3.0 {
		t.Fatalf("get_product = %v", p)
	}
	classification, _ := p["classification"].(map[string]any)
	if classification == nil || classification["classification_source"] != "native" {
		t.Fatalf("get_product classification = %v", classification)
	}

	// get_product_classification: tags in the domain's stable order, frozen,
	// DOT 3.
	c := okCall(t, session, "get_product_classification", map[string]any{"sku": "ITCOV-MCP-1"})
	if fmt.Sprint(c["handling_tags"]) != "[Hazmat TemperatureSensitive]" ||
		c["temperature_class"] != "Frozen" || c["dot_hazard_class"] != 3.0 {
		t.Fatalf("get_product_classification = %v", c)
	}

	// get_physical_profile: declared dimensions, effective = declared.
	pp := okCall(t, session, "get_physical_profile", map[string]any{"sku": "ITCOV-MCP-1"})
	declared, _ := pp["declared"].(map[string]any)
	if pp["effective_source"] != "declared" || declared == nil || declared["volume_mm3"] != 1_000_000.0 {
		t.Fatalf("get_physical_profile = %v", pp)
	}

	// list_products: one classified product carries Hazmat; the unclassified
	// one comes back alone under classified=false.
	hazmat := okCall(t, session, "list_products", map[string]any{"handling_tag": "Hazmat"})
	if items, _ := hazmat["items"].([]any); len(items) != 1 {
		t.Fatalf("list_products handling_tag=Hazmat = %v", hazmat)
	}
	unclassified := okCall(t, session, "list_products", map[string]any{"classified": false})
	if items, _ := unclassified["items"].([]any); len(items) != 1 {
		t.Fatalf("list_products classified=false = %v", unclassified)
	}
}

func TestStreamableHTTP_RejectionsAreToolErrorsNotTransportErrors(t *testing.T) {
	session := newStreamableHarness(t)

	// Unknown SKU: a domain rejection with the REST problem slug.
	failCall(t, session, "get_product", map[string]any{"sku": "ITCOV-404"}, "product-not-found")
	// Malformed SKU (a '/'): invalid input, still a tool error.
	failCall(t, session, "get_product", map[string]any{"sku": "bad/sku"}, "invalid-sku")
	// Classified-only read of the unclassified product.
	failCall(t, session, "get_product_classification", map[string]any{"sku": "ITCOV-MCP-2"}, "product-classification-not-found")
	// Out-of-range page size: malformed-request, not a protocol failure.
	failCall(t, session, "list_products", map[string]any{"limit": 501}, "malformed-request")
	// Unclassified physical profile still succeeds (profile is optional).
	pp := okCall(t, session, "get_physical_profile", map[string]any{"sku": "ITCOV-MCP-2"})
	if pp["effective_source"] != "none" {
		t.Fatalf("get_physical_profile on the unclassified product = %v", pp)
	}
}
