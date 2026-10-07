package http_test

import (
	"net/http"
	"testing"

	"github.com/claudioed/product-master/internal/application/repository"
)

const declaredBody = `{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500}`

func TestDeclareDimensions_Statuses(t *testing.T) {
	f := newFixture(t)
	path := "/products/SKU-1/dimensions/declared"
	expectProblem(t, f.do(t, http.MethodPut, path, declaredBody), http.StatusNotFound, "product-not-found")
	f.mustDo(t, http.MethodPut, "/products/SKU-1", `{}`, http.StatusCreated)

	r := f.mustDo(t, http.MethodPut, path, declaredBody, http.StatusOK)
	want := `{"declared":{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500,"volumeMm3":1920000},` +
		`"effective":{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500,"volumeMm3":1920000},` +
		`"effectiveSource":"declared","discrepancy":false,"version":2}` + "\n"
	if r.raw != want {
		t.Fatalf("body = %s, want %s", r.raw, want)
	}
	for slug, body := range map[string]string{
		"invalid-dimension": `{"lengthMm":0,"widthMm":120,"heightMm":80,"weightG":1500}`,
		"invalid-weight":    `{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":2000001}`,
		"malformed-request": `{"lengthMm":200,"widthMm":120,"heightMm":80}`,
	} {
		expectProblem(t, f.do(t, http.MethodPut, path, body), http.StatusBadRequest, slug)
	}
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":"x"}`), http.StatusBadRequest, "malformed-request")
	expectProblem(t, f.do(t, http.MethodPut, "/products/bad%20sku/dimensions/declared", declaredBody), http.StatusBadRequest, "invalid-sku")

	f.repo.saveErr = repository.ErrConcurrentModification
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1}`), http.StatusConflict, "concurrent-modification")
	f.repo.saveErr = errDB
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1}`), http.StatusInternalServerError, "internal-error")
}

func TestRecordMeasurement_Statuses(t *testing.T) {
	f := newFixture(t)
	path := "/products/SKU-1/dimensions/measured"
	body := `{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"}`
	expectProblem(t, f.do(t, http.MethodPut, path, body), http.StatusNotFound, "product-not-found")
	f.mustDo(t, http.MethodPut, "/products/SKU-1", `{}`, http.StatusCreated)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/dimensions/declared", declaredBody, http.StatusOK)

	r := f.mustDo(t, http.MethodPut, path, body, http.StatusOK)
	want := `{"declared":{"lengthMm":200,"widthMm":120,"heightMm":80,"weightG":1500,"volumeMm3":1920000},` +
		`"measured":{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"volumeMm3":2034010,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"},` +
		`"effective":{"lengthMm":205,"widthMm":121,"heightMm":82,"weightG":1720,"volumeMm3":2034010},` +
		`"effectiveSource":"measured","discrepancy":true,"version":3}` + "\n"
	if r.raw != want {
		t.Fatalf("body = %s, want %s", r.raw, want)
	}
	if r = f.mustDo(t, http.MethodPut, path, body, http.StatusOK); r.body["version"] != float64(3) {
		t.Fatalf("repeating the measurement must change nothing: %s", r.raw)
	}

	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T13:00:00Z"}`), http.StatusConflict, "stale-measurement")
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T15:00:01Z"}`), http.StatusBadRequest, "measured-at-in-future")
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1}`), http.StatusBadRequest, "malformed-request")
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"yesterday"}`), http.StatusBadRequest, "malformed-request")
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":20001,"weightG":1,"measuredAt":"2026-10-06T14:05:00Z"}`), http.StatusBadRequest, "invalid-dimension")
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T14:06:00Z","deviceId":"a\u0001b"}`), http.StatusBadRequest, "malformed-request")

	f.repo.saveErr = repository.ErrConcurrentModification
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T14:30:00Z"}`), http.StatusConflict, "concurrent-modification")
	f.repo.saveErr = errDB
	expectProblem(t, f.do(t, http.MethodPut, path, `{"lengthMm":1,"widthMm":1,"heightMm":1,"weightG":1,"measuredAt":"2026-10-06T14:30:00Z"}`), http.StatusInternalServerError, "internal-error")
}

func TestPhysicalProfile_Statuses(t *testing.T) {
	f := newFixture(t)
	path := "/products/SKU-1/physical-profile"
	expectProblem(t, f.do(t, http.MethodGet, path, ""), http.StatusNotFound, "product-not-found")
	expectProblem(t, f.do(t, http.MethodGet, "/products/bad%20sku/physical-profile", ""), http.StatusBadRequest, "invalid-sku")
	f.mustDo(t, http.MethodPut, "/products/SKU-1", `{}`, http.StatusCreated)
	if r := f.mustDo(t, http.MethodGet, path, "", http.StatusOK); r.raw != `{"effectiveSource":"none","discrepancy":false,"version":1}`+"\n" {
		t.Fatalf("body = %s", r.raw)
	}
	f.repo.getErr = errDB
	expectProblem(t, f.do(t, http.MethodGet, path, ""), http.StatusInternalServerError, "internal-error")
}
