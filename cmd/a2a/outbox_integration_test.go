//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	inbounda2a "github.com/claudioed/product-master/internal/adapters/inbound/a2a"
	"github.com/claudioed/product-master/internal/adapters/outbound/clock"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/usecases"
)

type outboxRow struct {
	topic, eventType string
	published        bool
}

func outboxRows(t *testing.T, pool *pgxpool.Pool) []outboxRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT topic, event_type, published_at IS NOT NULL FROM outbox_events ORDER BY id`)
	if err != nil {
		t.Fatalf("query outbox: %v", err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.topic, &r.eventType, &r.published); err != nil {
			t.Fatalf("scan outbox row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate outbox: %v", err)
	}
	return out
}

// problemStatus returns the in-band problem status of an agent reply, or 0
// when the reply carries data instead.
func problemStatus(t *testing.T, resp a2a.SendMessageResult) int {
	t.Helper()
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) == 0 {
		t.Fatalf("reply = %T, want a message", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env struct {
		Problem *struct {
			Status int `json:"status"`
		} `json:"problem"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v", err)
	}
	if env.Problem == nil {
		return 0
	}
	return env.Problem.Status
}

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
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return url
}

// seedProducts registers SKU-1 and SKU-2 the way cmd/api's REST adapter does
// (same use cases, Postgres adapters, transactional outbox).
func seedProducts(t *testing.T, url string) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	w := usecases.Writer{
		Products: postgres.NewProductRepo(pool), Outbox: postgres.NewOutboxRepo(pool),
		Encoder: outboundkafka.NewFanoutEncoder(), UoW: postgres.NewUnitOfWork(pool), Clock: clock.System{},
	}
	for sku, description := range map[string]string{"SKU-1": "Frozen peas", "SKU-2": "Mug"} {
		if _, err := (&usecases.RegisterProduct{Writer: w}).Handle(ctx, usecases.RegisterProductCommand{SKU: sku, Description: description}); err != nil {
			t.Fatalf("register %s: %v", sku, err)
		}
	}
	return pool
}

// declarer returns a func that sends declare_pack_level over A2A JSON-RPC
// through cmd/a2a's real router and returns the in-band problem status (0 when
// the reply carries data).
func declarer(t *testing.T, deps inbounda2a.Deps) func(args map[string]any) int {
	t.Helper()
	ctx := context.Background()
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: "http://placeholder/", Version: "itest"})
	srv := httptest.NewServer(newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), card))
	t.Cleanup(srv.Close)
	client, err := a2aclient.NewFromCard(ctx, inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/a2a", Version: "itest"}))
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return func(args map[string]any) int {
		t.Helper()
		msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": "declare_pack_level", "arguments": args}))
		resp, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
		if err != nil {
			t.Fatalf("SendMessage declare_pack_level: %v", err)
		}
		return problemStatus(t, resp)
	}
}

// TestA2ADeclarePackLevel_WritesUnpublishedOutboxRows is the end-to-end proof
// that a DELEGATED declaration is a first-class write: it boots a real
// Postgres (testcontainers), wires cmd/a2a's production composition
// (buildDeps: migrations, pool, Writer with the Postgres unit of work), sends
// the declare_pack_level skill over A2A JSON-RPC through the real router, and
// asserts the transactional outbox holds an UNPUBLISHED integration-topic row
// per event -- the same rows REST and MCP produce (ADR 0004, ADR 0011). The
// relay stays in cmd/api, so rows (not Kafka messages) are the contract of
// this binary. A rejected declaration is answered as a problem and stores
// nothing. A2A never becomes a data-sync channel: the facts leave only as
// outbox rows.
func TestA2ADeclarePackLevel_WritesUnpublishedOutboxRows(t *testing.T) {
	ctx := context.Background()
	url := startPostgres(t)

	// The binary migrates a FRESH database itself, before any api ran.
	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps on a fresh database: %v", err)
	}
	t.Cleanup(closeFn)
	pool := seedProducts(t, url)
	seeded := len(outboxRows(t, pool))
	declare := declarer(t, deps)

	// Rejections: a bad check digit (400), a missing SKU (404): nothing stored.
	wantStatus(t, "a bad check digit", declare(map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": "4006381333932"}), 400)
	wantStatus(t, "an unknown SKU", declare(map[string]any{"sku": "SKU-404", "level": "EACH", "gtin": "4006381333931"}), 404)
	if rows := outboxRows(t, pool); len(rows) != seeded {
		t.Fatalf("rejected declarations stored %d outbox rows, want 0", len(rows)-seeded)
	}

	wantStatus(t, "declare EACH", declare(map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": "4006381333931"}), 0)
	wantStatus(t, "declare CASE", declare(map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": "24006381333935", "childGtin": "4006381333931", "qtyOfChild": 12}), 0)
	// The GTIN is taken fleet-wide: a second SKU is refused (409), nothing stored.
	wantStatus(t, "a GTIN held by another SKU", declare(map[string]any{"sku": "SKU-2", "level": "EACH", "gtin": "4006381333931"}), 409)

	assertUnpublishedDeclaredRows(t, outboxRows(t, pool)[seeded:])
}

func wantStatus(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: problem status = %d, want %d", what, got, want)
	}
}

func assertUnpublishedDeclaredRows(t *testing.T, rows []outboxRow) {
	t.Helper()
	if len(rows) != 2 {
		t.Fatalf("outbox rows after two declarations = %+v, want exactly 2", rows)
	}
	for i, row := range rows {
		if row.topic != outboundkafka.Topic || row.eventType != "com.warehouse.wms.product-master.product.ProductPackLevelDeclared" {
			t.Errorf("row %d = %s on %s, want ProductPackLevelDeclared on the integration topic", i, row.eventType, row.topic)
		}
		if row.published {
			t.Errorf("row %d is already published; this binary must not run the relay", i)
		}
	}
}
