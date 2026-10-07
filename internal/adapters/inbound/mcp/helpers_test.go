package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/product-master/internal/adapters/inbound/mcp"
	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/usecases"
)

// harness is a connected in-memory MCP client over the real server wired to
// in-memory repos, exactly as cmd/mcp wires them (minus Postgres). The write
// use cases exist ONLY here, to seed data the way the REST adapter would:
// the MCP surface itself has none.
type harness struct {
	session  *sdk.ClientSession
	outbox   *memory.OutboxRepo
	register *usecases.RegisterProduct
	classify *usecases.ClassifyProduct
	declare  *usecases.DeclareDimensions
	measure  *usecases.RecordMeasurement
}

func depsFor(products ports.ProductRepository) inboundmcp.Deps {
	return inboundmcp.Deps{
		GetProduct:   &usecases.GetProduct{Products: products},
		ListProducts: &usecases.ListProducts{Products: products},
	}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	products, ob, processed := memory.NewProductRepo(), memory.NewOutboxRepo(), memory.NewProcessedEventRepo()
	w := usecases.Writer{
		Products: products, Outbox: ob, Encoder: outboundkafka.NewEncoder(),
		UoW: memory.NewUnitOfWork(products, ob, processed), Clock: clock.System{},
	}
	return &harness{
		session:  connectSession(t, depsFor(products)),
		outbox:   ob,
		register: &usecases.RegisterProduct{Writer: w},
		classify: &usecases.ClassifyProduct{Writer: w},
		declare:  &usecases.DeclareDimensions{Writer: w},
		measure:  &usecases.RecordMeasurement{Writer: w},
	}
}

// seed registers sku and, when tags is non-empty, classifies it.
func (h *harness) seed(t *testing.T, sku, description string, tags []string, temperature string, dot int) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.register.Handle(ctx, usecases.RegisterProductCommand{SKU: sku, Description: description}); err != nil {
		t.Fatalf("register %s: %v", sku, err)
	}
	if len(tags) == 0 {
		return
	}
	if _, err := h.classify.Handle(ctx, usecases.ClassifyProductCommand{SKU: sku, HandlingTags: tags, TemperatureClass: temperature, DOTHazardClass: dot}); err != nil {
		t.Fatalf("classify %s: %v", sku, err)
	}
}

// seedProfile declares 100x100x100 mm / 1000 g and records a measurement
// 20% longer (a discrepancy), measured an hour ago.
func (h *harness) seedProfile(t *testing.T, sku string) time.Time {
	t.Helper()
	ctx := context.Background()
	if _, err := h.declare.Handle(ctx, usecases.DeclareDimensionsCommand{SKU: sku, DimensionsInput: usecases.DimensionsInput{LengthMm: 100, WidthMm: 100, HeightMm: 100, WeightG: 1000}}); err != nil {
		t.Fatalf("declare %s: %v", sku, err)
	}
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	if _, err := h.measure.Handle(ctx, usecases.RecordMeasurementCommand{
		SKU: sku, DimensionsInput: usecases.DimensionsInput{LengthMm: 120, WidthMm: 100, HeightMm: 100, WeightG: 1000},
		MeasuredAt: at, DeviceID: "SCALE-01",
	}); err != nil {
		t.Fatalf("measure %s: %v", sku, err)
	}
	return at
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// fail calls a tool and requires an isError result whose text starts with
// the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
