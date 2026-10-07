package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/product-master/internal/analytics/report"
)

// ReportsServiceName labels the reports binary for logs/spans/metrics,
// distinct from the api's DefaultServiceName.
const ReportsServiceName = "product-master-reports"

// ReportsServer is the inbound HTTP adapter of cmd/product-reports: the
// read-only master data quality report of ADR 0006. It depends only on the
// report.Reader port over the analytical database; it never touches the OLTP
// use cases, and it writes nothing.
type ReportsServer struct {
	Reader report.Reader
	// Now is the clock the default range ends at and the freshness lag is
	// measured against (time.Now when nil).
	Now func() time.Time
}

const dayLayout = "2006-01-02"

// qualityDayDTO, coverageDTO and qualityReportDTO are the wire shapes
// (snake_case like the rest of this API); days are UTC calendar dates and the
// report structs never leak onto the wire.
type qualityDayDTO struct {
	Day                string `json:"day"`
	Registered         int    `json:"registered"`
	Classified         int    `json:"classified"`
	DimensionsDeclared int    `json:"dimensions_declared"`
	Measured           int    `json:"measured"`
	OpenDiscrepancies  int    `json:"open_discrepancies"`
}

type coverageDTO struct {
	Products                int     `json:"products"`
	Classified              int     `json:"classified"`
	DimensionsDeclared      int     `json:"dimensions_declared"`
	Measured                int     `json:"measured"`
	OpenDiscrepancies       int     `json:"open_discrepancies"`
	ClassifiedRatio         float64 `json:"classified_ratio"`
	DimensionsDeclaredRatio float64 `json:"dimensions_declared_ratio"`
	MeasuredRatio           float64 `json:"measured_ratio"`
	DiscrepancyRatio        float64 `json:"discrepancy_ratio"`
}

type qualityReportDTO struct {
	From     time.Time       `json:"from"`
	To       time.Time       `json:"to"`
	Days     []qualityDayDTO `json:"days"`
	Coverage coverageDTO     `json:"coverage"`
}

// NewReportsRouter builds the router of cmd/product-reports: /healthz,
// GET /reports/master-data-quality and GET /reports/freshness. No auth
// middleware (fleet rule), no CORS: the service is cluster-internal.
func NewReportsRouter(s *ReportsServer) http.Handler {
	r := chi.NewRouter()
	r.Use(otelchi.Middleware(ReportsServiceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(ReportsServiceName)))
	r.Get("/healthz", handleHealthz)
	r.Get("/reports/master-data-quality", s.handleMasterDataQuality)
	r.Get("/reports/freshness", s.handleFreshness)
	return r
}

func (s *ReportsServer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// handleMasterDataQuality serves the per-day counts over ?from=&to= and the
// current coverage of the catalogue.
func (s *ReportsServer) handleMasterDataQuality(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rg, err := report.ParseRange(q.Get("from"), q.Get("to"), s.now())
	if err != nil {
		writeReportProblem(w, r, http.StatusBadRequest, "invalid-report-range",
			"from and to must form a valid RFC 3339 range of at most 366 days", err.Error())
		return
	}
	days, err := s.Reader.QualityDays(r.Context(), rg)
	if err != nil {
		writeReportStoreError(w, r, err)
		return
	}
	counts, err := s.Reader.Coverage(r.Context())
	if err != nil {
		writeReportStoreError(w, r, err)
		return
	}
	c := report.ComputeCoverage(counts)
	out := qualityReportDTO{
		From: rg.From, To: rg.To, Days: make([]qualityDayDTO, 0, len(days)),
		Coverage: coverageDTO{
			Products: c.Products, Classified: c.Classified, DimensionsDeclared: c.DimensionsDeclared,
			Measured: c.Measured, OpenDiscrepancies: c.OpenDiscrepancies,
			ClassifiedRatio: c.ClassifiedRatio, DimensionsDeclaredRatio: c.DimensionsDeclaredRatio,
			MeasuredRatio: c.MeasuredRatio, DiscrepancyRatio: c.DiscrepancyRatio,
		},
	}
	for _, d := range days {
		out.Days = append(out.Days, qualityDayDTO{
			Day: d.Day.UTC().Format(dayLayout), Registered: d.Registered, Classified: d.Classified,
			DimensionsDeclared: d.DimensionsDeclared, Measured: d.Measured, OpenDiscrepancies: d.OpenDiscrepancies,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleFreshness serves how far the projection is behind (now - the newest
// applied event time). Both fields are null until the first event is applied.
func (s *ReportsServer) handleFreshness(w http.ResponseWriter, r *http.Request) {
	asOf, err := s.Reader.LastEventAt(r.Context())
	if err != nil {
		writeReportStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report.ComputeFreshness(asOf, s.now()))
}

// writeReportStoreError is the 500 for a failed analytical query. The cause
// is logged, never echoed: it can carry SQL and connection details.
func writeReportStoreError(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "report query failed", "error", err, "path", r.URL.Path)
	writeReportProblem(w, r, http.StatusInternalServerError, "report-store-error",
		"The report could not be served", "the analytical database query failed")
}

func writeReportProblem(w http.ResponseWriter, r *http.Request, status int, slug, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type: problemBaseURI + slug, Title: title, Status: status, Detail: detail, Instance: r.URL.EscapedPath(),
	})
}
