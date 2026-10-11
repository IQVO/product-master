//go:build integration

// Integration tests for the REST inbound adapter over REAL infrastructure:
// the real chi router (NewRouter) with the real Postgres-backed use cases,
// served by httptest and driven end to end with real HTTP requests. These
// are integration tests in the fleet's sense: they execute the real wire
// contract — POST/PUT lifecycle (201/200), GET round trips, RFC 7807
// problem documents with the repo's problem slugs — against the real
// persistence the production composition root uses, with no in-memory repo
// fakes anywhere in the path.
//
// Postgres comes from testcontainers (TestMain in this file): one container
// per package run, migrated once into a template database, one private
// clone per test. Never an external DATABASE_URL, never t.Skip.
package http_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/postgres"
	"github.com/claudioed/product-master/internal/application/usecases"
)

// One Postgres container serves the whole package, migrated once into a
// template database; each test gets a private clone (milliseconds). Never
// an external DATABASE_URL, never t.Skip.
const restTemplateDB = "http_migrated_template"

var (
	restBaseURL string
	restDBSeq   atomic.Uint64
)

func TestMain(m *testing.M) {
	os.Exit(runRESTTests(m))
}

func runRESTTests(m *testing.M) int {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("product_master_http"),
		tcpostgres.WithUsername("http"),
		tcpostgres.WithPassword("http"),
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

	restBaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres connection string: %v\n", err)
		return 1
	}
	if err := restCreateDatabase(ctx, restTemplateDB); err != nil {
		fmt.Fprintf(os.Stderr, "create template database: %v\n", err)
		return 1
	}
	u, err := url.Parse(restBaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse base url: %v\n", err)
		return 1
	}
	u.Path = "/" + restTemplateDB
	if err := postgres.RunMigrations(u.String()); err != nil {
		fmt.Fprintf(os.Stderr, "migrate template: %v\n", err)
		return 1
	}
	return m.Run()
}

// restCreateDatabase creates an empty database inside the shared container.
func restCreateDatabase(ctx context.Context, name string) error {
	conn, err := pgx.Connect(ctx, restBaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		return fmt.Errorf("create database %s: %w", name, err)
	}
	return nil
}

// restMigratedDB hands the test a connection URL to its own private
// database cloned from the migrated template.
func restMigratedDB(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("http_%d", restDBSeq.Add(1))
	conn, err := pgx.Connect(context.Background(), restBaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), fmt.Sprintf(
		"CREATE DATABASE %q WITH TEMPLATE %q", name, restTemplateDB)); err != nil {
		t.Fatalf("clone database: %v", err)
	}
	u, err := url.Parse(restBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// restClock pins the write path's ports.Clock.
type restClock struct{ now time.Time }

func (c restClock) Now() time.Time { return c.now }

var restAt = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// newRESTServer wires the real production stack over a private migrated
// database — real Postgres repos, real UnitOfWork, real use cases, real chi
// router — and serves it over httptest. No Idempotency-Key middleware
// exists in this repo, so the inventory-storage idempotency shape does not
// apply; this is the plain resource lifecycle.
func newRESTServer(t *testing.T) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, restMigratedDB(t))
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	w := usecases.Writer{
		Products: postgres.NewProductRepo(pool), Outbox: postgres.NewOutboxRepo(pool),
		Encoder: outboundkafka.NewEncoder(), UoW: postgres.NewUnitOfWork(pool), Clock: restClock{restAt},
	}
	s := &inboundhttp.Server{
		RegisterProduct:   &usecases.RegisterProduct{Writer: w},
		ClassifyProduct:   &usecases.ClassifyProduct{Writer: w},
		DeclareDimensions: &usecases.DeclareDimensions{Writer: w},
		RecordMeasurement: &usecases.RecordMeasurement{Writer: w},
		GetProduct:        &usecases.GetProduct{Products: postgres.NewProductRepo(pool)},
		ListProducts:      &usecases.ListProducts{Products: postgres.NewProductRepo(pool)},
		Readiness:         &inboundhttp.Readiness{},
	}
	srv := httptest.NewServer(inboundhttp.NewRouter(s))
	t.Cleanup(srv.Close)
	return srv
}

// restDo issues one JSON request and returns status, headers and decoded
// body.
func restDo(t *testing.T, srv *httptest.Server, method, path, body string) (int, string, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, srv.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return resp.StatusCode, resp.Header.Get("Content-Type"), decoded
}

// restExpectProblem asserts the repo's RFC 7807 error shape: status,
// content type and the problem-slug type URI.
func restExpectProblem(t *testing.T, status int, contentType string, body map[string]any, wantStatus int, slug string) {
	t.Helper()
	if status != wantStatus || contentType != "application/problem+json" ||
		body["type"] != "https://errors.product-master.warehouse-systems.dev/"+slug ||
		body["status"] != float64(wantStatus) || body["title"] == "" || body["detail"] == "" {
		t.Fatalf("got %d %s %v, want %d problem %s", status, contentType, body, wantStatus, slug)
	}
}

func TestREST_ProductLifecycleEndToEnd(t *testing.T) {
	srv := newRESTServer(t)

	// Register: 201, the aggregate's birth.
	status, ct, body := restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-1", `{"description":"Battery"}`)
	if status != http.StatusCreated || ct != "application/json" || body["sku"] != "ITCOV-HTTP-1" ||
		body["description"] != "Battery" || body["version"] != float64(1) {
		t.Fatalf("register = %d %s %v, want 201 with the registered product", status, ct, body)
	}

	// Re-register with a new description: 200, a replace not a create.
	status, _, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-1", `{"description":"Battery 12V"}`)
	if status != http.StatusOK || body["version"] != float64(2) {
		t.Fatalf("re-register = %d %v, want 200 v2", status, body)
	}

	// Classify: 201 the first time, 200 when replaced.
	status, _, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-1/classification",
		`{"handlingTags":["Hazmat","TemperatureSensitive"],"temperatureClass":"Frozen","dotHazardClass":3}`)
	if status != http.StatusCreated {
		t.Fatalf("classify = %d %v, want 201", status, body)
	}
	status, _, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-1/classification",
		`{"handlingTags":["Fragile"]}`)
	if status != http.StatusOK {
		t.Fatalf("re-classify = %d %v, want 200", status, body)
	}

	// Declare dimensions and take a measurement.
	status, _, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-1/dimensions/declared",
		`{"lengthMm":200,"widthMm":100,"heightMm":50,"weightG":500}`)
	if status != http.StatusOK {
		t.Fatalf("declare dimensions = %d %v, want 200", status, body)
	}
	status, _, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-1/dimensions/measured",
		fmt.Sprintf(`{"lengthMm":205,"widthMm":101,"heightMm":52,"weightG":510,"measuredAt":%q,"deviceId":"CUBISCAN-03"}`,
			restAt.Add(-time.Hour).Format(time.RFC3339Nano)))
	if status != http.StatusOK {
		t.Fatalf("record measurement = %d %v, want 200", status, body)
	}

	// GET round trip: the product carries everything written above,
	// persisted in real Postgres (v6: register, re-register, classify,
	// re-classify, declare, measure).
	status, ct, body = restDo(t, srv, http.MethodGet, "/products/ITCOV-HTTP-1", "")
	if status != http.StatusOK || ct != "application/json" {
		t.Fatalf("get = %d %s, want 200 application/json", status, ct)
	}
	if body["description"] != "Battery 12V" {
		t.Fatalf("get description = %v", body["description"])
	}
	classification, _ := body["classification"].(map[string]any)
	if classification == nil || classification["classificationSource"] != "native" {
		t.Fatalf("get classification = %v", classification)
	}
	profile, _ := body["physicalProfile"].(map[string]any)
	if profile == nil || profile["effectiveSource"] != "measured" {
		t.Fatalf("get physicalProfile = %v (measured must win over declared)", profile)
	}

	// The classified view round trips too.
	status, _, body = restDo(t, srv, http.MethodGet, "/products/ITCOV-HTTP-1/classification", "")
	if status != http.StatusOK || fmt.Sprint(body["handlingTags"]) != "[Fragile]" {
		t.Fatalf("get classification view = %d %v", status, body)
	}

	// The list endpoint sees it.
	status, _, body = restDo(t, srv, http.MethodGet, "/products", "")
	if status != http.StatusOK {
		t.Fatalf("list = %d", status)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items = %v, want the one product", body)
	}
}

func TestREST_ValidationAndDomainRejectionsAreProblemDocuments(t *testing.T) {
	srv := newRESTServer(t)

	// Unknown SKU on a read: 404 product-not-found.
	status, ct, body := restDo(t, srv, http.MethodGet, "/products/ITCOV-404", "")
	restExpectProblem(t, status, ct, body, http.StatusNotFound, "product-not-found")

	// Malformed JSON body: 400 malformed-request.
	status, ct, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-2", `{not json`)
	restExpectProblem(t, status, ct, body, http.StatusBadRequest, "malformed-request")

	// Control character in the description: 400 invalid-description.
	status, ct, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-2", "{\"description\":\"bad\\u0007desc\"}")
	restExpectProblem(t, status, ct, body, http.StatusBadRequest, "invalid-description")

	// Register, then classify without any tag: 400 no-handling-tags.
	if status, _, _ = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-2", `{"description":"Mug"}`); status != http.StatusCreated {
		t.Fatalf("register = %d", status)
	}
	status, ct, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-2/classification", `{"handlingTags":[]}`)
	restExpectProblem(t, status, ct, body, http.StatusBadRequest, "no-handling-tags")

	// Temperature class without the tag: 400 temperature-class-not-applicable.
	status, ct, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-2/classification",
		`{"handlingTags":["Fragile"],"temperatureClass":"Frozen"}`)
	restExpectProblem(t, status, ct, body, http.StatusBadRequest, "temperature-class-not-applicable")

	// Classification of an unknown SKU: 404 product-not-found (validated
	// input, unknown aggregate).
	status, ct, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-404/classification", `{"handlingTags":["Fragile"]}`)
	restExpectProblem(t, status, ct, body, http.StatusNotFound, "product-not-found")

	// An older measurement than the recorded one: 409 stale-measurement.
	newer := restAt.Add(-30 * time.Minute).Format(time.RFC3339Nano)
	older := restAt.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	if status, _, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-2/dimensions/measured",
		fmt.Sprintf(`{"lengthMm":100,"widthMm":50,"heightMm":25,"weightG":250,"measuredAt":%q}`, newer)); status != http.StatusOK {
		t.Fatalf("first measurement = %d %v", status, body)
	}
	status, ct, body = restDo(t, srv, http.MethodPut, "/products/ITCOV-HTTP-2/dimensions/measured",
		fmt.Sprintf(`{"lengthMm":90,"widthMm":45,"heightMm":22,"weightG":200,"measuredAt":%q}`, older))
	restExpectProblem(t, status, ct, body, http.StatusConflict, "stale-measurement")

	// The rejected writes left the aggregate exactly where the accepted
	// ones put it: v2 (register + one measurement).
	status, _, body = restDo(t, srv, http.MethodGet, "/products/ITCOV-HTTP-2", "")
	if status != http.StatusOK || body["version"] != float64(2) {
		t.Fatalf("after rejections get = %d %v, want v2", status, body)
	}
}
