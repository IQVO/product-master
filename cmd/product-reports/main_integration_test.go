//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/product-master/internal/analytics/report"
	"github.com/claudioed/product-master/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// stopWithSIGTERM delivers SIGTERM to this process until run() returns.
// run() takes no context: it listens for SIGINT/SIGTERM itself
// (signal.NotifyContext). The test's own handler is registered FIRST so a
// signal arriving before run() reaches its NotifyContext can never kill the
// test binary (Go's default SIGTERM disposition), and the signal is retried
// until run() observes one. The drain contract is the documented one:
// srv.Shutdown bounded by shutdownTimeout, exit nil.
func stopWithSIGTERM(t *testing.T, done <-chan error) error {
	t.Helper()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		select {
		case err := <-done:
			return err
		case <-sigCh:
			// Swallowed by the test's own handler; run() may not have been
			// listening yet. Fall through and send another.
		case <-time.After(300 * time.Millisecond):
		}
	}
	t.Fatal("run() did not return after SIGTERM")
	return nil
}

// The real entry point over an analytically-migrated EMPTY database seeded
// through the repo's own analytics writer (analyticsstore.Projection.Apply,
// ADR 0006): /healthz answers (this binary's documented probe -- it serves
// no /readyz), the master data quality report and the freshness report
// answer FROM the seeded facts, and SIGTERM drains to a clean nil exit.
func TestReportsServeTheProjectionAndStopCleanly(t *testing.T) {
	url := pgtest.EmptyURL(t)
	if err := analyticsstore.RunMigrations(url); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	writer, err := analyticsstore.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	projection := analyticsstore.NewProjection(writer)

	// Two SKUs' journeys, folded the way the projector folds them: SKU-1
	// walks all four milestones and ends with a discrepancy; SKU-2 is only
	// registered and renamed (ProductDescriptionChanged marks no milestone).
	const sku1, sku2 = "SKU-ITEST-REP-1", "SKU-ITEST-REP-2"
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	seed := []report.ProductEvent{
		{Kind: report.KindRegistered, EventID: "rep-e1", At: t0, SKU: sku1, Version: 1},
		{Kind: report.KindClassified, EventID: "rep-e2", At: t0.Add(time.Hour), SKU: sku1, Version: 2},
		{Kind: report.KindDimensionsDeclared, EventID: "rep-e3", At: t0.Add(2 * time.Hour), SKU: sku1, Version: 3,
			Profile: &report.ProfileState{HasDeclared: true}},
		{Kind: report.KindMeasured, EventID: "rep-e4", At: t0.Add(3 * time.Hour), SKU: sku1, Version: 4,
			Profile: &report.ProfileState{HasDeclared: true, HasMeasured: true, Discrepancy: true}},
		{Kind: report.KindRegistered, EventID: "rep-e5", At: t0, SKU: sku2, Version: 1},
		{Kind: report.KindDescriptionChanged, EventID: "rep-e6", At: t0.Add(30 * time.Minute), SKU: sku2, Version: 2},
	}
	for _, e := range seed {
		applied, err := projection.Apply(ctx, e)
		if err != nil || !applied {
			t.Fatalf("seed %s %s: applied=%v err=%v", e.Kind, e.EventID, applied, err)
		}
	}

	t.Setenv("ANALYTICS_DATABASE_URL", url)
	addr := freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "127.0.0.1:1")
	done := make(chan error, 1)
	go func() { done <- run() }()

	waitUntil(t, "/healthz 200", func() bool {
		code, _ := getJSON(t, "http://"+addr+"/healthz")
		return code == http.StatusOK
	})

	// GET /reports/master-data-quality: one row per UTC day of the range,
	// counting FIRST milestones per SKU; coverage is the CURRENT state
	// (docs/docs/api-reference/rest/get-master-data-quality.api.mdx).
	code, body := getJSON(t, "http://"+addr+"/reports/master-data-quality?from=2026-11-13T00:00:00Z&to=2026-11-14T00:00:00Z")
	days, _ := body["days"].([]any)
	if code != http.StatusOK || len(days) != 1 {
		t.Fatalf("master-data-quality = %d %v", code, body)
	}
	day, _ := days[0].(map[string]any)
	for field, want := range map[string]float64{
		"registered":          2,
		"classified":          1,
		"dimensions_declared": 1,
		"measured":            1,
		"open_discrepancies":  1,
	} {
		if day[field] != want {
			t.Fatalf("day %v = %v, want %v (body %v)", field, day[field], want, body)
		}
	}
	if day["day"] != "2026-11-13" {
		t.Fatalf("day = %v, want 2026-11-13 (body %v)", day["day"], body)
	}
	coverage, _ := body["coverage"].(map[string]any)
	for field, want := range map[string]float64{
		"products": 2, "classified": 1, "dimensions_declared": 1, "measured": 1, "open_discrepancies": 1,
		"classified_ratio": 0.5, "dimensions_declared_ratio": 0.5, "measured_ratio": 0.5, "discrepancy_ratio": 1,
	} {
		if coverage[field] != want {
			t.Fatalf("coverage %v = %v, want %v (body %v)", field, coverage[field], want, body)
		}
	}

	// GET /reports/freshness: as_of is the CloudEvents time of the newest
	// applied event; lag_seconds is now minus as_of, never negative
	// (docs/docs/api-reference/rest/get-reports-freshness.api.mdx).
	code, body = getJSON(t, "http://"+addr+"/reports/freshness")
	if code != http.StatusOK || body["as_of"] != "2026-11-13T12:00:00Z" {
		t.Fatalf("freshness = %d %v", code, body)
	}
	lag, ok := body["lag_seconds"].(float64)
	if !ok || lag < 0 {
		t.Fatalf("lag_seconds = %v, want a non-negative number (body %v)", body["lag_seconds"], body)
	}

	if err := stopWithSIGTERM(t, done); err != nil {
		t.Fatalf("a SIGTERMed reports server must drain cleanly, got %v", err)
	}
}
