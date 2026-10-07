package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/product-master/internal/analytics/report"
)

var reportsNow = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

func reportsGet(t *testing.T, s *ReportsServer, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	NewReportsRouter(s).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func seededReportsStore(t *testing.T) *analyticsstore.Memory {
	t.Helper()
	m := analyticsstore.NewMemory()
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	events := []report.ProductEvent{
		{Kind: report.KindRegistered, EventID: "1", At: at("2026-10-05T08:00:00Z"), SKU: "A", Version: 1},
		{Kind: report.KindRegistered, EventID: "2", At: at("2026-10-06T08:00:00Z"), SKU: "B", Version: 1},
		{Kind: report.KindClassified, EventID: "3", At: at("2026-10-06T09:00:00Z"), SKU: "A", Version: 2},
		{Kind: report.KindMeasured, EventID: "4", At: at("2026-10-06T10:00:00Z"), SKU: "A", Version: 3,
			Profile: &report.ProfileState{HasDeclared: true, HasMeasured: true, Discrepancy: true}},
	}
	for _, e := range events {
		if _, err := m.Apply(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func TestReports_MasterDataQuality(t *testing.T) {
	s := &ReportsServer{Reader: seededReportsStore(t), Now: func() time.Time { return reportsNow }}
	rec := reportsGet(t, s, "/reports/master-data-quality?from=2026-10-05T00:00:00Z&to=2026-10-07T00:00:00Z")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d %s: %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	want := `{"from":"2026-10-05T00:00:00Z","to":"2026-10-07T00:00:00Z","days":[` +
		`{"day":"2026-10-05","registered":1,"classified":0,"dimensions_declared":0,"measured":0,"open_discrepancies":0},` +
		`{"day":"2026-10-06","registered":1,"classified":1,"dimensions_declared":0,"measured":1,"open_discrepancies":1}],` +
		`"coverage":{"products":2,"classified":1,"dimensions_declared":1,"measured":1,"open_discrepancies":1,` +
		`"classified_ratio":0.5,"dimensions_declared_ratio":0.5,"measured_ratio":0.5,"discrepancy_ratio":1}}` + "\n"
	if rec.Body.String() != want {
		t.Fatalf("body:\n got %s\nwant %s", rec.Body, want)
	}
}

func TestReports_DefaultRangeIsTheThirtyDaysEndingNow(t *testing.T) {
	s := &ReportsServer{Reader: analyticsstore.NewMemory(), Now: func() time.Time { return reportsNow }}
	rec := reportsGet(t, s, "/reports/master-data-quality")
	var body struct {
		From, To time.Time
		Days     []json.RawMessage
		Coverage map[string]float64
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("status %d, %v: %s", rec.Code, err, rec.Body)
	}
	if !body.To.Equal(reportsNow) || !body.From.Equal(reportsNow.Add(-30*24*time.Hour)) || len(body.Days) != 31 {
		t.Fatalf("range %v..%v with %d days, want 30 days ending now over 31 calendar days", body.From, body.To, len(body.Days))
	}
	if body.Coverage["products"] != 0 || body.Coverage["classified_ratio"] != 0 {
		t.Fatalf("an empty projection must report zero coverage: %v", body.Coverage)
	}
}

func TestReports_InvalidRangesAreProblem400s(t *testing.T) {
	s := &ReportsServer{Reader: analyticsstore.NewMemory(), Now: func() time.Time { return reportsNow }}
	for _, q := range []string{"from=yesterday", "to=2026-10-07", "from=2026-10-07T00:00:00Z&to=2026-10-06T00:00:00Z", "from=2024-01-01T00:00:00Z&to=2026-01-01T00:00:00Z"} {
		rec := reportsGet(t, s, "/reports/master-data-quality?"+q)
		var p problemDetail
		_ = json.Unmarshal(rec.Body.Bytes(), &p)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/problem+json" ||
			p.Type != problemBaseURI+"invalid-report-range" || p.Status != 400 || p.Instance != "/reports/master-data-quality" || p.Detail == "" {
			t.Errorf("%s: %d %+v", q, rec.Code, p)
		}
	}
}

// failingReader fails the method named in fail.
type failingReader struct {
	report.Reader
	fail string
}

var errStore = errors.New("pq: password authentication failed for user reports at 10.0.0.1")

func (f failingReader) QualityDays(ctx context.Context, r report.Range) ([]report.QualityDay, error) {
	if f.fail == "days" {
		return nil, errStore
	}
	return f.Reader.QualityDays(ctx, r)
}

func (f failingReader) Coverage(ctx context.Context) (report.CoverageCounts, error) {
	if f.fail == "coverage" {
		return report.CoverageCounts{}, errStore
	}
	return f.Reader.Coverage(ctx)
}

func (f failingReader) LastEventAt(ctx context.Context) (*time.Time, error) {
	if f.fail == "last" {
		return nil, errStore
	}
	return f.Reader.LastEventAt(ctx)
}

func TestReports_StoreFailuresAre500sThatDoNotLeakTheCause(t *testing.T) {
	for fail, path := range map[string]string{"days": "/reports/master-data-quality", "coverage": "/reports/master-data-quality", "last": "/reports/freshness"} {
		s := &ReportsServer{Reader: failingReader{Reader: analyticsstore.NewMemory(), fail: fail}, Now: func() time.Time { return reportsNow }}
		rec := reportsGet(t, s, path)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "report-store-error") || strings.Contains(rec.Body.String(), "password") {
			t.Errorf("%s: %d %s", fail, rec.Code, rec.Body)
		}
	}
}

func TestReports_Freshness(t *testing.T) {
	empty := &ReportsServer{Reader: analyticsstore.NewMemory(), Now: func() time.Time { return reportsNow }}
	if rec := reportsGet(t, empty, "/reports/freshness"); rec.Code != 200 || rec.Body.String() != `{"as_of":null,"lag_seconds":null}`+"\n" {
		t.Fatalf("empty freshness = %d %s", rec.Code, rec.Body)
	}
	s := &ReportsServer{Reader: seededReportsStore(t), Now: func() time.Time { return time.Date(2026, 10, 6, 10, 1, 30, 0, time.UTC) }}
	rec := reportsGet(t, s, "/reports/freshness")
	if rec.Body.String() != `{"as_of":"2026-10-06T10:00:00Z","lag_seconds":90}`+"\n" {
		t.Fatalf("freshness = %s", rec.Body)
	}
}

func TestReports_HealthzAndNoWriteRoutes(t *testing.T) {
	s := &ReportsServer{Reader: analyticsstore.NewMemory()}
	if rec := reportsGet(t, s, "/healthz"); rec.Code != 200 {
		t.Fatalf("healthz = %d", rec.Code)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := httptest.NewRecorder()
		NewReportsRouter(s).ServeHTTP(rec, httptest.NewRequest(method, "/reports/master-data-quality", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405: the reports API is read-only", method, rec.Code)
		}
	}
	// The real clock is used when Now is nil.
	rec := reportsGet(t, s, "/reports/master-data-quality")
	var body struct{ To time.Time }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if time.Since(body.To) > time.Minute || !reflect.DeepEqual(rec.Code, 200) {
		t.Fatalf("default to = %v (%d)", body.To, rec.Code)
	}
}
