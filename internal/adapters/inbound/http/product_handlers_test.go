package http_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/claudioed/product-master/internal/application/repository"
)

func TestRegisterProduct_Statuses(t *testing.T) {
	f := newFixture(t)
	r := f.mustDo(t, http.MethodPut, "/products/SKU-1", `{"description":"Battery"}`, http.StatusCreated)
	if r.body["sku"] != "SKU-1" || r.body["version"] != float64(1) || r.body["description"] != "Battery" {
		t.Fatalf("body = %s", r.raw)
	}
	pp := r.body["physicalProfile"].(map[string]any)
	if pp["effectiveSource"] != "none" || pp["discrepancy"] != false || r.body["classification"] != nil {
		t.Fatalf("body = %s", r.raw)
	}
	r = f.mustDo(t, http.MethodPut, "/products/SKU-1", `{"description":"Battery 12V"}`, http.StatusOK)
	if r.body["version"] != float64(2) {
		t.Fatalf("body = %s", r.raw)
	}
	r = f.mustDo(t, http.MethodPut, "/products/SKU-1", `{"description":"Battery 12V"}`, http.StatusOK)
	if r.body["version"] != float64(2) {
		t.Fatalf("an identical PUT must not bump the version: %s", r.raw)
	}
	f.mustDo(t, http.MethodPut, "/products/SKU-2", `{}`, http.StatusCreated)

	expectProblem(t, f.do(t, http.MethodPut, "/products/bad%20sku", `{}`), http.StatusBadRequest, "invalid-sku")
	expectProblem(t, f.do(t, http.MethodPut, "/products/a%2Fb", `{}`), http.StatusBadRequest, "invalid-sku")
	expectProblem(t, f.do(t, http.MethodPut, "/products/"+strings.Repeat("x", 65), `{}`), http.StatusBadRequest, "invalid-sku")
	expectProblem(t, f.do(t, http.MethodPut, "/products/SKU-1", `{"description":"`+strings.Repeat("x", 201)+`"}`), http.StatusBadRequest, "invalid-description")
	expectProblem(t, f.do(t, http.MethodPut, "/products/SKU-1", `{"desc":"x"}`), http.StatusBadRequest, "malformed-request")
	expectProblem(t, f.do(t, http.MethodPut, "/products/SKU-1", `{`), http.StatusBadRequest, "malformed-request")
	expectProblem(t, f.do(t, http.MethodPut, "/products/SKU-1", `{} {}`), http.StatusBadRequest, "malformed-request")

	f.repo.saveErr = repository.ErrConcurrentModification
	expectProblem(t, f.do(t, http.MethodPut, "/products/SKU-1", `{"description":"other"}`), http.StatusConflict, "concurrent-modification")
	f.repo.saveErr = errDB
	r = f.do(t, http.MethodPut, "/products/SKU-1", `{"description":"other"}`)
	expectProblem(t, r, http.StatusInternalServerError, "internal-error")
	if r.body["detail"] != "an unexpected error occurred" || r.body["instance"] != "/products/SKU-1" {
		t.Fatalf("a 500 must not leak the cause: %s", r.raw)
	}
}

func TestGetProduct_Statuses(t *testing.T) {
	f := newFixture(t)
	f.mustDo(t, http.MethodPut, "/products/SKU-1", `{"description":"d"}`, http.StatusCreated)
	if r := f.mustDo(t, http.MethodGet, "/products/SKU-1", "", http.StatusOK); r.body["sku"] != "SKU-1" {
		t.Fatalf("body = %s", r.raw)
	}
	r := f.do(t, http.MethodGet, "/products/SKU-UNKNOWN", "")
	expectProblem(t, r, http.StatusNotFound, "product-not-found")
	if r.body["instance"] != "/products/SKU-UNKNOWN" {
		t.Fatalf("instance = %v", r.body["instance"])
	}
	expectProblem(t, f.do(t, http.MethodGet, "/products/bad%20sku", ""), http.StatusBadRequest, "invalid-sku")
	f.repo.getErr = errDB
	expectProblem(t, f.do(t, http.MethodGet, "/products/SKU-1", ""), http.StatusInternalServerError, "internal-error")
}

func TestClassification_Statuses(t *testing.T) {
	f := newFixture(t)
	path := "/products/SKU-1/classification"
	expectProblem(t, f.do(t, http.MethodPut, path, `{"handlingTags":["Fragile"]}`), http.StatusNotFound, "product-not-found")
	expectProblem(t, f.do(t, http.MethodGet, path, ""), http.StatusNotFound, "product-not-found")
	f.mustDo(t, http.MethodPut, "/products/SKU-1", `{}`, http.StatusCreated)
	expectProblem(t, f.do(t, http.MethodGet, path, ""), http.StatusNotFound, "product-classification-not-found")

	r := f.mustDo(t, http.MethodPut, path, `{"handlingTags":["TemperatureSensitive","Hazmat"],"temperatureClass":"Frozen","dotHazardClass":3}`, http.StatusCreated)
	want := `{"sku":"SKU-1","handlingTags":["Hazmat","TemperatureSensitive"],"temperatureClass":"Frozen","dotHazardClass":3,"classificationSource":"native","version":2}` + "\n"
	if r.raw != want {
		t.Fatalf("body = %s, want %s", r.raw, want)
	}
	r = f.mustDo(t, http.MethodPut, path, `{"handlingTags":["Fragile"]}`, http.StatusOK)
	if r.raw != `{"sku":"SKU-1","handlingTags":["Fragile"],"classificationSource":"native","version":3}`+"\n" {
		t.Fatalf("body = %s", r.raw)
	}
	if r = f.mustDo(t, http.MethodGet, path, "", http.StatusOK); r.body["version"] != float64(3) {
		t.Fatalf("body = %s", r.raw)
	}
	if r = f.mustDo(t, http.MethodGet, "/products/SKU-1", "", http.StatusOK); r.body["classification"].(map[string]any)["classificationSource"] != "native" {
		t.Fatalf("body = %s", r.raw)
	}

	f.repo.saveErr = repository.ErrConcurrentModification
	expectProblem(t, f.do(t, http.MethodPut, path, `{"handlingTags":["Oversized"]}`), http.StatusConflict, "concurrent-modification")
	f.repo.saveErr = errDB
	expectProblem(t, f.do(t, http.MethodPut, path, `{"handlingTags":["Oversized"]}`), http.StatusInternalServerError, "internal-error")
	f.repo.getErr = errDB
	expectProblem(t, f.do(t, http.MethodGet, path, ""), http.StatusInternalServerError, "internal-error")
	expectProblem(t, f.do(t, http.MethodGet, "/products/bad%20sku/classification", ""), http.StatusBadRequest, "invalid-sku")
}

func TestClassification_EveryInvariantIsA400(t *testing.T) {
	f := newFixture(t)
	f.mustDo(t, http.MethodPut, "/products/SKU-1", `{}`, http.StatusCreated)
	for slug, body := range map[string]string{
		"no-handling-tags":                 `{"handlingTags":[]}`,
		"unknown-handling-tag":             `{"handlingTags":["Sharp"]}`,
		"duplicate-handling-tag":           `{"handlingTags":["Fragile","Fragile"]}`,
		"temperature-class-required":       `{"handlingTags":["TemperatureSensitive"]}`,
		"temperature-class-not-applicable": `{"handlingTags":["Fragile"],"temperatureClass":"Frozen"}`,
		"unknown-temperature-class":        `{"handlingTags":["TemperatureSensitive"],"temperatureClass":"Hot"}`,
		"invalid-dot-hazard-class":         `{"handlingTags":["Hazmat"],"dotHazardClass":10}`,
		"dot-hazard-class-not-applicable":  `{"handlingTags":["Fragile"],"dotHazardClass":3}`,
		"malformed-request":                `{"handlingTags":["Fragile"],"extra":1}`,
	} {
		t.Run(slug, func(t *testing.T) {
			expectProblem(t, f.do(t, http.MethodPut, "/products/SKU-1/classification", body), http.StatusBadRequest, slug)
		})
	}
	t.Run("explicit zero dot class", func(t *testing.T) {
		expectProblem(t, f.do(t, http.MethodPut, "/products/SKU-1/classification", `{"handlingTags":["Hazmat"],"dotHazardClass":0}`), http.StatusBadRequest, "invalid-dot-hazard-class")
	})
	t.Run("invalid sku", func(t *testing.T) {
		expectProblem(t, f.do(t, http.MethodPut, "/products/bad%20sku/classification", `{"handlingTags":["Fragile"]}`), http.StatusBadRequest, "invalid-sku")
	})
	t.Run("null dot class is unset", func(t *testing.T) {
		f.mustDo(t, http.MethodPut, "/products/SKU-1/classification", `{"handlingTags":["Hazmat"],"dotHazardClass":null}`, http.StatusCreated)
	})
}
