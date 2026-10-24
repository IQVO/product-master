//go:build integration

package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	inboundmcp "github.com/claudioed/product-master/internal/adapters/inbound/mcp"
	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/usecases"
)

func startPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("product_master_test"),
		tcpostgres.WithUsername("product_master_test"),
		tcpostgres.WithPassword("product_master_test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}

// seedThroughWriteUseCases writes a product the way cmd/api's REST adapter
// does (same use cases, Postgres adapters, transactional outbox), so the MCP
// binary is proven to read what the api wrote in the shared database.
func seedThroughWriteUseCases(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	w := usecases.Writer{
		Products: postgres.NewProductRepo(pool), Outbox: postgres.NewOutboxRepo(pool),
		Encoder: outboundkafka.NewEncoder(), UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
	}
	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{SKU: "SKU-1", Description: "Frozen peas"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := (&usecases.ClassifyProduct{Writer: w}).Handle(ctx, usecases.ClassifyProductCommand{
		SKU: "SKU-1", HandlingTags: []string{"TemperatureSensitive", "Hazmat"}, TemperatureClass: "Frozen", DOTHazardClass: 3,
	}); err != nil {
		t.Fatalf("classify: %v", err)
	}
	if _, err := (&usecases.DeclareDimensions{Writer: w}).Handle(ctx, usecases.DeclareDimensionsCommand{
		SKU: "SKU-1", DimensionsInput: usecases.DimensionsInput{LengthMm: 200, WidthMm: 100, HeightMm: 50, WeightG: 500},
	}); err != nil {
		t.Fatalf("declare: %v", err)
	}
	if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{SKU: "SKU-2", Description: "Mug"}); err != nil {
		t.Fatalf("register SKU-2: %v", err)
	}
}

func countOutbox(t *testing.T, url string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
		t.Fatalf("count outbox_events: %v", err)
	}
	return n
}

// The binary migrates a FRESH testcontainers database itself (before any api
// ran), then reads through MCP tools exactly what the write use cases
// persisted, and its reads add no outbox row.
func TestMCPAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps on a fresh database: %v", err)
	}
	t.Cleanup(closeFn)

	seedThroughWriteUseCases(t, url)
	before := countOutbox(t, url)
	if before != 4 {
		t.Fatalf("outbox rows after seeding = %d, want 4 (2 Registered, Classified, DimensionsDeclared)", before)
	}

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	assertSeededReads(t, session)

	if after := countOutbox(t, url); after != before {
		t.Fatalf("outbox rows %d -> %d: the MCP binary wrote an event", before, after)
	}
}

// assertSeededReads reads back, through every MCP tool, what
// seedThroughWriteUseCases persisted.
func assertSeededReads(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: err=%v res=%+v", name, err, res)
		}
		return res.StructuredContent.(map[string]any)
	}

	if p := call("get_product", map[string]any{"sku": "SKU-1"}); p["description"] != "Frozen peas" || p["version"] != 3.0 {
		t.Fatalf("get_product = %v", p)
	}
	c := call("get_product_classification", map[string]any{"sku": "SKU-1"})
	if fmt.Sprint(c["handling_tags"]) != "[Hazmat TemperatureSensitive]" ||
		c["temperature_class"] != "Frozen" || c["dot_hazard_class"] != 3.0 || c["classification_source"] != "native" {
		t.Fatalf("get_product_classification = %v", c)
	}
	pp := call("get_physical_profile", map[string]any{"sku": "SKU-1"})
	if eff, _ := pp["effective"].(map[string]any); pp["effective_source"] != "declared" || eff["volume_mm3"] != 1_000_000.0 {
		t.Fatalf("get_physical_profile = %v", pp)
	}
	if page := call("list_products", map[string]any{"classified": false}); fmt.Sprint(page["items"]) != "[map[description:Mug physical_profile:map[discrepancy:false effective_source:none] sku:SKU-2 version:1]]" {
		t.Fatalf("list_products classified=false = %v", page)
	}
	if hazmat := call("list_products", map[string]any{"handling_tag": "Hazmat"}); len(hazmat["items"].([]any)) != 1 {
		t.Fatalf("list_products handling_tag=Hazmat = %v", hazmat)
	}
}

// The write tools of ADR 0011 over real Postgres through the binary's own
// wiring: the product row, the pack level rows and the outbox rows (the FULL
// CloudEvents type, integration topic only) commit together, and a second SKU
// cannot take the GTIN.
func TestMCPWriteToolsAgainstPostgres(t *testing.T) {
	url := startPostgres(t)
	ctx := context.Background()
	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)
	seedThroughWriteUseCases(t, url)
	before := countOutbox(t, url)

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any, wantErr bool) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError != wantErr {
			t.Fatalf("%s: err=%v res=%+v", name, err, res)
		}
		sc, _ := res.StructuredContent.(map[string]any)
		return sc
	}

	call("declare_pack_level", map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": "4006381333931"}, false)
	call("declare_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": "24006381333935", "child_gtin": "4006381333931", "qty_of_child": 12}, false)
	call("declare_pack_level", map[string]any{"sku": "SKU-2", "level": "EACH", "gtin": "4006381333931"}, true) // duplicate GTIN, fleet-wide
	if got := call("resolve_gtin", map[string]any{"gtin": "24006381333935"}, false); got["sku"] != "SKU-1" || got["units_per_pack"] != 12.0 {
		t.Fatalf("resolve_gtin = %v", got)
	}
	call("retire_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE", "reason": "discontinued"}, false)

	// three events, ONE integration row each (no analytics row), none for the refused declaration
	if after := countOutbox(t, url); after != before+3 {
		t.Fatalf("outbox rows %d -> %d, want +3", before, after)
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `SELECT event_type, topic FROM outbox_events ORDER BY id DESC LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := []string{"ProductPackLevelRetired", "ProductPackLevelDeclared", "ProductPackLevelDeclared"}
	for i := 0; rows.Next(); i++ {
		var typ, topic string
		if err := rows.Scan(&typ, &topic); err != nil {
			t.Fatal(err)
		}
		if typ != "com.warehouse.wms.product-master.product."+want[i] || topic != outboundkafka.Topic {
			t.Fatalf("row %d = %s on %s", i, typ, topic)
		}
	}
}
