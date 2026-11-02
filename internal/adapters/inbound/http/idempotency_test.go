package http_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/claudioed/product-master/internal/adapters/inbound/http/page"
)

const retirePath = "/products/SKU-1/pack-levels/EACH/retirements"

func retireFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.mustDo(t, http.MethodPut, "/products/SKU-1", `{}`, http.StatusCreated)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/EACH", `{"gtin":"04006381333931","isOrderable":true}`, http.StatusCreated)
	return f
}

func key(k string) map[string]string { return map[string]string{"Idempotency-Key": k} }

func TestRetire_ReplayIsByteIdenticalAndMarked(t *testing.T) {
	f := retireFixture(t)
	first := f.doWith(t, http.MethodPost, retirePath, `{"reason":"gone"}`, key("k-1"))
	if first.status != http.StatusOK || first.replayed != "" {
		t.Fatalf("first = %d replayed=%q %s", first.status, first.replayed, first.raw)
	}
	again := f.doWith(t, http.MethodPost, retirePath, `{"reason":"gone"}`, key("k-1"))
	if again.status != first.status || again.raw != first.raw || again.replayed != "true" {
		t.Fatalf("replay = %d replayed=%q %s", again.status, again.replayed, again.raw)
	}
}

func TestRetire_KeyProblems(t *testing.T) {
	f := retireFixture(t)
	expectProblem(t, f.doWith(t, http.MethodPost, retirePath, "", nil), http.StatusBadRequest, "idempotency-key-required")
	for _, bad := range []string{strings.Repeat("k", 129), "café", "tab\tkey"} {
		expectProblem(t, f.doWith(t, http.MethodPost, retirePath, "", key(bad)), http.StatusBadRequest, "idempotency-key-invalid")
	}
	if r := f.doWith(t, http.MethodPost, retirePath, "", key(strings.Repeat("k", 128))); r.status != http.StatusOK {
		t.Fatalf("128-char key = %d %s", r.status, r.raw)
	}
}

func TestRetire_SameKeyDifferentRequestIs409(t *testing.T) {
	f := retireFixture(t)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/INNER", `{"gtin":"14006381333938","childGtin":"04006381333931","qtyOfChild":6,"isOrderable":true}`, http.StatusCreated)
	f.doWith(t, http.MethodPost, retirePath, `{"reason":"a"}`, key("k-2"))
	expectProblem(t, f.doWith(t, http.MethodPost, retirePath, `{"reason":"b"}`, key("k-2")), http.StatusConflict, "idempotency-key-reused")
	expectProblem(t, f.doWith(t, http.MethodPost, "/products/SKU-1/pack-levels/INNER/retirements", `{"reason":"a"}`, key("k-2")), http.StatusConflict, "idempotency-key-reused")
}

func TestRetire_4xxIsCachedAnd5xxIsNot(t *testing.T) {
	f := retireFixture(t)
	notFound := "/products/SKU-1/pack-levels/PALLET/retirements"
	a := f.doWith(t, http.MethodPost, notFound, "", key("k-4"))
	b := f.doWith(t, http.MethodPost, notFound, "", key("k-4"))
	expectProblem(t, a, http.StatusNotFound, "pack-level-not-found")
	if b.status != http.StatusNotFound || b.replayed != "true" || b.raw != a.raw {
		t.Fatalf("4xx replay = %d %q %s", b.status, b.replayed, b.raw)
	}
	f.repo.saveErr = errDB
	expectProblem(t, f.doWith(t, http.MethodPost, retirePath, "", key("k-5")), http.StatusInternalServerError, "internal-error")
	f.repo.saveErr = nil
	if r := f.doWith(t, http.MethodPost, retirePath, "", key("k-5")); r.status != http.StatusOK || r.replayed != "" {
		t.Fatalf("retry after 5xx = %d replayed=%q %s", r.status, r.replayed, r.raw)
	}
}

func TestRetire_ConcurrentSameKeyRunsOnce(t *testing.T) {
	f := retireFixture(t)
	var wg sync.WaitGroup
	results := make([]response, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = f.doWith(t, http.MethodPost, retirePath, `{"reason":"x"}`, key("k-6"))
		}()
	}
	wg.Wait()
	fresh := 0
	for _, r := range results {
		if r.status != http.StatusOK {
			t.Fatalf("status = %d %s", r.status, r.raw)
		}
		if r.replayed == "" {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("%d executions, want exactly 1", fresh)
	}
}

func TestRetiredRoutesAreGone(t *testing.T) {
	f := retireFixture(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/products/SKU-1/pack-levels/EACH:retire"},
		{http.MethodGet, retirePath},
	} {
		r := f.doWith(t, c.method, c.path, "", key("gone"))
		if r.status != http.StatusNotFound && r.status != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 404 or 405", c.method, c.path, r.status)
		}
	}
}

func TestListPackLevels_WalksEveryPage(t *testing.T) {
	f := retireFixture(t)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/INNER", `{"gtin":"14006381333938","childGtin":"04006381333931","qtyOfChild":6,"isOrderable":true}`, http.StatusCreated)
	f.mustDo(t, http.MethodPost, retirePath, "", http.StatusOK)
	f.mustDo(t, http.MethodPut, "/products/SKU-1/pack-levels/EACH", `{"gtin":"04006381333948","isOrderable":true}`, http.StatusOK)

	seen := map[string]bool{}
	path := "/products/SKU-1/pack-levels?limit=1"
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("paging does not terminate")
		}
		r := f.mustDo(t, http.MethodGet, path, "", http.StatusOK)
		for _, it := range r.body["items"].([]any) {
			gtin := it.(map[string]any)["gtin"].(string)
			if seen[gtin] {
				t.Fatalf("duplicate %s", gtin)
			}
			seen[gtin] = true
		}
		next, _ := r.body["nextCursor"].(string)
		if next == "" {
			break
		}
		path = "/products/SKU-1/pack-levels?limit=1&cursor=" + next
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d definitions, want 3: %v", len(seen), seen)
	}
	for _, q := range []string{"limit=0", "cursor=!!", "after=x"} {
		want := map[string]string{"limit=0": "invalid-limit", "cursor=!!": "invalid-cursor", "after=x": "unknown-query-parameter"}[q]
		expectProblem(t, f.do(t, http.MethodGet, "/products/SKU-1/pack-levels?"+q, ""), http.StatusBadRequest, want)
	}
}

func TestListProducts_WalksEveryPageWithoutGaps(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 7; i++ {
		f.mustDo(t, http.MethodPut, fmt.Sprintf("/products/SKU-%d", i), `{}`, http.StatusCreated)
	}
	var got []string
	path := "/products?limit=3"
	for {
		r := f.mustDo(t, http.MethodGet, path, "", http.StatusOK)
		for _, it := range r.body["items"].([]any) {
			got = append(got, it.(map[string]any)["sku"].(string))
		}
		next, _ := r.body["nextCursor"].(string)
		if next == "" {
			break
		}
		if _, err := page.Decode(next); err != nil {
			t.Fatal(err)
		}
		path = "/products?limit=3&cursor=" + next
	}
	if len(got) != 7 {
		t.Fatalf("got %v", got)
	}
}
