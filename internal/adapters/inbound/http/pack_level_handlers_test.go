package http_test

import (
	"net/http"
	"testing"

	"github.com/claudioed/product-master/internal/application/repository"
)

// Valid GTINs (mod-10 checked).
const (
	gtinEach  = "4006381333931"
	gtinCase  = "24006381333935"
	gtinCase2 = "34006381333932"
	gtinVar   = "94006381333934"
)

const (
	eachBody  = `{"gtin":"` + gtinEach + `","isOrderable":true}`
	caseBody  = `{"gtin":"` + gtinCase + `","childGtin":"` + gtinEach + `","qtyOfChild":12,"isOrderable":true,"isDespatchUnit":true}`
	innerBody = `{"gtin":"14006381333938","childGtin":"` + gtinEach + `","qtyOfChild":6}`
	case2Body = `{"gtin":"` + gtinCase2 + `","childGtin":"` + gtinEach + `","qtyOfChild":24,"isDespatchUnit":true}`
)

func registered(t *testing.T, f *fixture, sku string) {
	t.Helper()
	f.mustDo(t, http.MethodPut, "/products/"+sku, `{}`, http.StatusCreated)
}

func TestDeclarePackLevel_Statuses(t *testing.T) {
	f := newFixture(t)
	path := "/products/SKU-1/pack-levels/"
	expectProblem(t, f.do(t, http.MethodPut, path+"EACH", eachBody), http.StatusNotFound, "product-not-found")
	registered(t, f, "SKU-1")

	r := f.mustDo(t, http.MethodPut, path+"EACH", `{"gtin":"`+gtinEach+`","isOrderable":true,"dims":{"lengthMm":100,"widthMm":50,"heightMm":40,"weightG":500}}`, http.StatusCreated)
	want := `{"sku":"SKU-1","version":2,"level":"EACH","gtin":"04006381333931","unitsPerPack":1,` +
		`"dims":{"lengthMm":100,"widthMm":50,"heightMm":40,"weightG":500,"volumeMm3":200000},` +
		`"isOrderable":true,"isDespatchUnit":false,"active":true,"effectiveFrom":"2026-10-06T15:00:00Z"}` + "\n"
	if r.raw != want {
		t.Fatalf("body:\n got %s\nwant %s", r.raw, want)
	}
	r = f.mustDo(t, http.MethodPut, path+"CASE", caseBody, http.StatusCreated)
	wantCase := `{"sku":"SKU-1","version":3,"level":"CASE","gtin":"24006381333935","childGtin":"04006381333931","qtyOfChild":12,"unitsPerPack":12,` +
		`"isOrderable":true,"isDespatchUnit":true,"active":true,"effectiveFrom":"2026-10-06T15:00:00Z"}` + "\n"
	if r.raw != wantCase {
		t.Fatalf("body:\n got %s\nwant %s", r.raw, wantCase)
	}

	t.Run("the same declaration again is 200 and changes nothing", func(t *testing.T) {
		if r := f.mustDo(t, http.MethodPut, path+"CASE", caseBody, http.StatusOK); r.body["version"] != float64(3) {
			t.Fatalf("version moved: %s", r.raw)
		}
	})
	t.Run("a new GTIN at the level end-dates the old one", func(t *testing.T) {
		r := f.mustDo(t, http.MethodPut, path+"CASE", case2Body, http.StatusOK)
		if r.body["version"] != float64(5) || r.body["gtin"] != "34006381333932" || r.body["unitsPerPack"] != float64(24) {
			t.Fatalf("body = %s", r.raw)
		}
		list := f.mustDo(t, http.MethodGet, "/products/SKU-1/pack-levels", "", http.StatusOK)
		items := list.body["items"].([]any)
		if len(items) != 3 {
			t.Fatalf("items = %s", list.raw)
		}
		old := items[1].(map[string]any)
		if old["gtin"] != "24006381333935" || old["active"] != false || old["effectiveTo"] != "2026-10-06T15:00:00Z" {
			t.Fatalf("old case = %v", old)
		}
	})
	t.Run("changing the quantity of an active level is 409", func(t *testing.T) {
		expectProblem(t, f.do(t, http.MethodPut, path+"CASE", `{"gtin":"`+gtinCase2+`","childGtin":"`+gtinEach+`","qtyOfChild":36,"isDespatchUnit":true}`), http.StatusConflict, "pack-quantity-immutable")
	})
	t.Run("a GTIN of another level or a retired one is 409", func(t *testing.T) {
		expectProblem(t, f.do(t, http.MethodPut, path+"PALLET", `{"gtin":"`+gtinCase2+`","childGtin":"`+gtinEach+`","qtyOfChild":100}`), http.StatusConflict, "gtin-already-assigned")
		expectProblem(t, f.do(t, http.MethodPut, path+"CASE", caseBody), http.StatusConflict, "gtin-already-assigned")
	})
	t.Run("a GTIN of another SKU is 409", func(t *testing.T) {
		registered(t, f, "SKU-2")
		expectProblem(t, f.do(t, http.MethodPut, "/products/SKU-2/pack-levels/EACH", eachBody), http.StatusConflict, "gtin-already-assigned")
	})
	t.Run("rejections", func(t *testing.T) {
		for _, tt := range []struct {
			name, path, body, slug string
			status                 int
		}{
			{"check digit", path + "EACH", `{"gtin":"4006381333930"}`, "invalid-gtin", 400},
			{"length", path + "EACH", `{"gtin":"12345"}`, "invalid-gtin", 400},
			{"no gtin", path + "EACH", `{}`, "invalid-gtin", 400},
			{"variable measure", path + "CASE", `{"gtin":"` + gtinVar + `","childGtin":"` + gtinEach + `","qtyOfChild":2}`, "variable-measure-unsupported", 400},
			{"unknown level", path + "BOX", eachBody, "invalid-pack-level", 400},
			{"lower case level", path + "case", eachBody, "invalid-pack-level", 400},
			{"case without a child", path + "INNER", `{"gtin":"14006381333938","qtyOfChild":6}`, "pack-level-order", 409},
			{"quantity one", path + "INNER", `{"gtin":"14006381333938","childGtin":"` + gtinEach + `","qtyOfChild":1}`, "invalid-pack-quantity", 400},
			{"wrong direct factor", path + "INNER", `{"gtin":"14006381333938","childGtin":"` + gtinEach + `","qtyOfChild":6,"unitsPerPack":5}`, "invalid-pack-quantity", 400},
			{"light case", path + "INNER", `{"gtin":"14006381333938","childGtin":"` + gtinEach + `","qtyOfChild":6,"dims":{"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":2999}}`, "pack-dimensions-below-contents", 409},
			{"bad dimension", path + "INNER", `{"gtin":"14006381333938","dims":{"lengthMm":0,"widthMm":1,"heightMm":1,"weightG":1}}`, "invalid-dimension", 400},
			{"missing dimension", path + "INNER", `{"gtin":"14006381333938","dims":{"lengthMm":1}}`, "malformed-request", 400},
			{"unknown field", path + "INNER", `{"gtin":"14006381333938","colour":"red"}`, "malformed-request", 400},
			{"not json", path + "INNER", `nope`, "malformed-request", 400},
			{"bad sku", "/products/bad%20sku/pack-levels/EACH", eachBody, "invalid-sku", 400},
		} {
			expectProblem(t, f.do(t, http.MethodPut, tt.path, tt.body), tt.status, tt.slug)
		}
	})
	t.Run("storage errors", func(t *testing.T) {
		f.repo.saveErr = repository.ErrConcurrentModification
		expectProblem(t, f.do(t, http.MethodPut, path+"INNER", innerBody), http.StatusConflict, "concurrent-modification")
		f.repo.saveErr = errDB
		expectProblem(t, f.do(t, http.MethodPut, path+"INNER", innerBody), http.StatusInternalServerError, "internal-error")
		f.repo.saveErr = nil
	})
}

func TestRetirePackLevel_Statuses(t *testing.T) {
	f := newFixture(t)
	registered(t, f, "SKU-1")
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/EACH", eachBody, http.StatusCreated)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/CASE", caseBody, http.StatusCreated)
	path := "/products/SKU-1/pack-levels/CASE/retirements"

	r := f.mustDo(t, http.MethodPost, path, `{"reason":"discontinued"}`, http.StatusOK)
	if r.body["active"] != false || r.body["effectiveTo"] != "2026-10-06T15:00:00Z" || r.body["version"] != float64(4) || r.body["gtin"] != "24006381333935" {
		t.Fatalf("body = %s", r.raw)
	}
	t.Run("retiring again is 200 and changes nothing", func(t *testing.T) {
		if r := f.mustDo(t, http.MethodPost, path, `{"reason":"again"}`, http.StatusOK); r.body["version"] != float64(4) || r.body["active"] != false {
			t.Fatalf("body = %s", r.raw)
		}
	})
	t.Run("the body is optional", func(t *testing.T) {
		if r := f.mustDo(t, http.MethodPost, "/products/SKU-1/pack-levels/EACH/retirements", "", http.StatusOK); r.body["version"] != float64(5) || r.body["level"] != "EACH" {
			t.Fatalf("body = %s", r.raw)
		}
	})
	t.Run("rejections", func(t *testing.T) {
		expectProblem(t, f.do(t, http.MethodPost, "/products/SKU-1/pack-levels/PALLET/retirements", ""), http.StatusNotFound, "pack-level-not-found")
		expectProblem(t, f.do(t, http.MethodPost, "/products/NOPE/pack-levels/CASE/retirements", ""), http.StatusNotFound, "product-not-found")
		expectProblem(t, f.do(t, http.MethodPost, "/products/SKU-1/pack-levels/BOX/retirements", ""), http.StatusBadRequest, "invalid-pack-level")
		expectProblem(t, f.do(t, http.MethodPost, "/products/SKU-1/pack-levels/PALLET/retirements", `{"reason":""`), http.StatusBadRequest, "malformed-request")
		expectProblem(t, f.do(t, http.MethodPost, "/products/SKU-1/pack-levels/PALLET/retirements", `{"why":"x"}`), http.StatusBadRequest, "malformed-request")
		long := make([]byte, 201)
		for i := range long {
			long[i] = 'x'
		}
		expectProblem(t, f.do(t, http.MethodPost, "/products/SKU-1/pack-levels/CASE/retirements", `{"reason":"`+string(long)+`"}`), http.StatusBadRequest, "invalid-reason")
		expectProblem(t, f.do(t, http.MethodPost, "/products/bad%20sku/pack-levels/CASE/retirements", ""), http.StatusBadRequest, "invalid-sku")
	})
}

func TestRecordPackLevelMeasurement_Statuses(t *testing.T) {
	f := newFixture(t)
	registered(t, f, "SKU-1")
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/EACH", `{"gtin":"`+gtinEach+`","dims":{"lengthMm":100,"widthMm":50,"heightMm":40,"weightG":500}}`, http.StatusCreated)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/CASE", caseBody, http.StatusCreated)
	path := "/products/SKU-1/pack-levels/CASE/measured"
	body := `{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T14:05:00Z","deviceId":"CUBISCAN-03"}`

	r := f.mustDo(t, http.MethodPut, path, body, http.StatusOK)
	m, _ := r.body["measured"].(map[string]any)
	if r.body["version"] != float64(4) || m["weightG"] != float64(6900) || m["deviceId"] != "CUBISCAN-03" || m["volumeMm3"] != float64(410*310*210) {
		t.Fatalf("body = %s", r.raw)
	}
	if r := f.mustDo(t, http.MethodPut, path, body, http.StatusOK); r.body["version"] != float64(4) {
		t.Fatalf("repeating must change nothing: %s", r.raw)
	}
	for _, tt := range []struct {
		name, path, body, slug string
		status                 int
	}{
		{"stale", path, `{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T13:00:00Z"}`, "stale-measurement", 409},
		{"light", path, `{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":5999,"measuredAt":"2026-10-06T14:30:00Z"}`, "pack-dimensions-below-contents", 409},
		{"future", path, `{"lengthMm":410,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-07T14:30:00Z"}`, "measured-at-in-future", 400},
		{"bad dimension", path, `{"lengthMm":0,"widthMm":310,"heightMm":210,"weightG":6900,"measuredAt":"2026-10-06T14:30:00Z"}`, "invalid-dimension", 400},
		{"no level", "/products/SKU-1/pack-levels/PALLET/measured", body, "pack-level-not-found", 404},
		{"unknown level", "/products/SKU-1/pack-levels/BOX/measured", body, "invalid-pack-level", 400},
		{"unknown product", "/products/NOPE/pack-levels/CASE/measured", body, "product-not-found", 404},
		{"malformed", path, `{}`, "malformed-request", 400},
	} {
		expectProblem(t, f.do(t, http.MethodPut, tt.path, tt.body), tt.status, tt.slug)
	}
}

func TestResolveGTIN_Statuses(t *testing.T) {
	f := newFixture(t)
	registered(t, f, "SKU-1")
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/EACH", eachBody, http.StatusCreated)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/CASE", caseBody, http.StatusCreated)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/CASE", case2Body, http.StatusOK) // end-dates gtinCase

	// the 13-digit and the 14-digit spelling of the each GTIN resolve identically
	for _, spelling := range []string{gtinEach, "0" + gtinEach} {
		r := f.mustDo(t, http.MethodGet, "/gtins/"+spelling, "", http.StatusOK)
		want := `{"gtin":"04006381333931","sku":"SKU-1","level":"EACH","unitsPerPack":1,"active":true,"effectiveFrom":"2026-10-06T15:00:00Z"}` + "\n"
		if r.raw != want {
			t.Fatalf("%s:\n got %s\nwant %s", spelling, r.raw, want)
		}
	}
	r := f.mustDo(t, http.MethodGet, "/gtins/"+gtinCase2, "", http.StatusOK)
	if r.body["level"] != "CASE" || r.body["unitsPerPack"] != float64(24) || r.body["childGtin"] != "04006381333931" || r.body["active"] != true {
		t.Fatalf("body = %s", r.raw)
	}
	r = f.mustDo(t, http.MethodGet, "/gtins/"+gtinCase, "", http.StatusOK)
	if r.body["active"] != false || r.body["effectiveTo"] != "2026-10-06T15:00:00Z" || r.body["unitsPerPack"] != float64(12) {
		t.Fatalf("a retired GTIN still resolves, inactive: %s", r.raw)
	}
	expectProblem(t, f.do(t, http.MethodGet, "/gtins/5901234123457", ""), http.StatusNotFound, "gtin-not-found")
	expectProblem(t, f.do(t, http.MethodGet, "/gtins/4006381333930", ""), http.StatusBadRequest, "invalid-gtin")
	expectProblem(t, f.do(t, http.MethodGet, "/gtins/abc", ""), http.StatusBadRequest, "invalid-gtin")
	expectProblem(t, f.do(t, http.MethodGet, "/gtins/"+gtinVar, ""), http.StatusBadRequest, "variable-measure-unsupported")
	f.repo.getErr = errDB
	expectProblem(t, f.do(t, http.MethodGet, "/gtins/"+gtinEach, ""), http.StatusInternalServerError, "internal-error")
}

func TestListPackLevels_Statuses(t *testing.T) {
	f := newFixture(t)
	expectProblem(t, f.do(t, http.MethodGet, "/products/SKU-1/pack-levels", ""), http.StatusNotFound, "product-not-found")
	registered(t, f, "SKU-1")
	r := f.mustDo(t, http.MethodGet, "/products/SKU-1/pack-levels", "", http.StatusOK)
	if r.raw != `{"sku":"SKU-1","version":1,"items":[]}`+"\n" {
		t.Fatalf("a product without pack levels lists an empty array: %s", r.raw)
	}
	expectProblem(t, f.do(t, http.MethodGet, "/products/bad%20sku/pack-levels", ""), http.StatusBadRequest, "invalid-sku")
}

func TestPackLevelRoutesAllowCORSPreflightForPost(t *testing.T) {
	f := newFixture(t)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, f.server.URL+"/products/SKU-1/pack-levels/CASE/retirements", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got != http.MethodPost {
		t.Fatalf("Access-Control-Allow-Methods = %q", got)
	}
}
