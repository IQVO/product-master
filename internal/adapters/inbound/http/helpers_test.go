package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	"github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

var now = time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

// faultyRepo wraps the memory repository and injects errors.
type faultyRepo struct {
	*memory.ProductRepo
	getErr, saveErr, listErr error
}

func (r *faultyRepo) Get(ctx context.Context, sku product.SKU) (*product.Product, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.ProductRepo.Get(ctx, sku)
}

func (r *faultyRepo) Save(ctx context.Context, p *product.Product, loaded int64) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	return r.ProductRepo.Save(ctx, p, loaded)
}

func (r *faultyRepo) List(ctx context.Context, f repository.ListFilter, after product.SKU, limit int) ([]*product.Product, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.ProductRepo.List(ctx, f, after, limit)
}

type fixture struct {
	repo      *faultyRepo
	server    *httptest.Server
	readiness *inboundhttp.Readiness
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	products, ob := memory.NewProductRepo(), memory.NewOutboxRepo()
	repo := &faultyRepo{ProductRepo: products}
	w := usecases.Writer{Products: repo, Outbox: ob, Encoder: kafka.NewEncoder(), UoW: memory.NewUnitOfWork(products, ob), Clock: fixedClock{}}
	readiness := &inboundhttp.Readiness{}
	s := &inboundhttp.Server{
		RegisterProduct:   &usecases.RegisterProduct{Writer: w},
		ClassifyProduct:   &usecases.ClassifyProduct{Writer: w},
		DeclareDimensions: &usecases.DeclareDimensions{Writer: w},
		RecordMeasurement: &usecases.RecordMeasurement{Writer: w},
		GetProduct:        &usecases.GetProduct{Products: repo},
		ListProducts:      &usecases.ListProducts{Products: repo},
		Readiness:         readiness,
		Metrics:           http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "# metrics\n") }),
	}
	srv := httptest.NewServer(inboundhttp.NewRouter(s))
	t.Cleanup(srv.Close)
	return &fixture{repo: repo, server: srv, readiness: readiness}
}

type response struct {
	status      int
	contentType string
	body        map[string]any
	raw         string
}

func (f *fixture) do(t *testing.T, method, path, body string) response {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, reader)
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
	out := response{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// mustDo performs a setup request and fails unless it returned want.
func (f *fixture) mustDo(t *testing.T, method, path, body string, want int) response {
	t.Helper()
	r := f.do(t, method, path, body)
	if r.status != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, r.status, r.raw, want)
	}
	return r
}

// expectProblem asserts an RFC 7807 response with the given status and slug.
func expectProblem(t *testing.T, r response, status int, slug string) {
	t.Helper()
	if r.status != status || r.contentType != "application/problem+json" ||
		r.body["type"] != "https://errors.product-master.warehouse-systems.dev/"+slug ||
		r.body["status"] != float64(status) || r.body["title"] == "" || r.body["detail"] == "" {
		t.Fatalf("got %d %s %s, want %d problem %s", r.status, r.contentType, r.raw, status, slug)
	}
}

var errDB = errors.New("db down")
