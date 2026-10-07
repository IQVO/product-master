package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestListProducts_PagesAndFilters(t *testing.T) {
	f := newFixture(t)
	for _, sku := range []string{"SKU-1", "SKU-2", "SKU-3"} {
		f.mustDo(t, http.MethodPut, "/products/"+sku, `{}`, http.StatusCreated)
	}
	f.mustDo(t, http.MethodPut, "/products/SKU-2/classification", `{"handlingTags":["Hazmat"]}`, http.StatusCreated)

	r := f.mustDo(t, http.MethodGet, "/products?limit=1", "", http.StatusOK)
	items := r.body["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["sku"] != "SKU-1" || r.body["nextCursor"] != "U0tVLTE" {
		t.Fatalf("page 1 = %s", r.raw)
	}
	r = f.mustDo(t, http.MethodGet, "/products?limit=5&cursor=U0tVLTE", "", http.StatusOK)
	if items = r.body["items"].([]any); len(items) != 2 || r.body["nextCursor"] != nil {
		t.Fatalf("page 2 = %s", r.raw)
	}
	r = f.mustDo(t, http.MethodGet, "/products?handlingTag=Hazmat", "", http.StatusOK)
	if items = r.body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["sku"] != "SKU-2" {
		t.Fatalf("tag filter = %s", r.raw)
	}
	r = f.mustDo(t, http.MethodGet, "/products?classified=false", "", http.StatusOK)
	if items = r.body["items"].([]any); len(items) != 2 {
		t.Fatalf("classified=false = %s", r.raw)
	}
	r = f.mustDo(t, http.MethodGet, "/products?classified=true&limit=500", "", http.StatusOK)
	if items = r.body["items"].([]any); len(items) != 1 {
		t.Fatalf("classified=true = %s", r.raw)
	}
}

func TestListProducts_EmptyIsAnEmptyArray(t *testing.T) {
	f := newFixture(t)
	if r := f.mustDo(t, http.MethodGet, "/products", "", http.StatusOK); r.raw != `{"items":[]}`+"\n" {
		t.Fatalf("body = %s", r.raw)
	}
}

func TestListProducts_BadQueriesAre400(t *testing.T) {
	f := newFixture(t)
	for _, q := range []string{"limit=0", "limit=501", "limit=-1", "limit=x", "cursor=!!", "handlingTag=Sharp", "classified=yes"} {
		t.Run(q, func(t *testing.T) {
			expectProblem(t, f.do(t, http.MethodGet, "/products?"+q, ""), http.StatusBadRequest, "malformed-request")
		})
	}
	f.repo.listErr = errDB
	expectProblem(t, f.do(t, http.MethodGet, "/products", ""), http.StatusInternalServerError, "internal-error")
}

func TestHealthReadinessAndMetrics(t *testing.T) {
	f := newFixture(t)
	if r := f.mustDo(t, http.MethodGet, "/healthz", "", http.StatusOK); r.body["status"] != "ok" {
		t.Fatalf("healthz = %s", r.raw)
	}
	if r := f.mustDo(t, http.MethodGet, "/readyz", "", http.StatusOK); r.body["status"] != "ready" {
		t.Fatalf("readyz = %s", r.raw)
	}
	if r := f.mustDo(t, http.MethodGet, "/metrics", "", http.StatusOK); !strings.Contains(r.raw, "# metrics") {
		t.Fatalf("metrics = %s", r.raw)
	}
	f.readiness.SetNotReady()
	if r := f.mustDo(t, http.MethodGet, "/readyz", "", http.StatusServiceUnavailable); r.body["status"] != "not_ready" {
		t.Fatalf("readyz while draining = %s", r.raw)
	}
	if r := f.mustDo(t, http.MethodGet, "/healthz", "", http.StatusOK); r.body["status"] != "ok" {
		t.Fatal("liveness must not flip on drain")
	}
}
