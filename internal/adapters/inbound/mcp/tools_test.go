package mcp_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
)

func TestGetProduct_ClassifiedWithProfile(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Lithium battery pack", []string{"TemperatureSensitive", "Hazmat"}, "Frozen", 3)
	measuredAt := h.seedProfile(t, "SKU-1")

	got := h.ok(t, "get_product", map[string]any{"sku": "SKU-1"})
	if got["sku"] != "SKU-1" || got["description"] != "Lithium battery pack" || got["version"] != 4.0 {
		t.Fatalf("product = %v", got)
	}
	c, _ := got["classification"].(map[string]any)
	// Tags come back in the stable taxonomy order, not the request order.
	if !reflect.DeepEqual(c["handling_tags"], []any{"Hazmat", "TemperatureSensitive"}) ||
		c["temperature_class"] != "Frozen" || c["dot_hazard_class"] != 3.0 || c["classification_source"] != "native" {
		t.Fatalf("classification = %v", c)
	}
	pp, _ := got["physical_profile"].(map[string]any)
	assertMeasuredProfile(t, pp, measuredAt)
}

// assertMeasuredProfile checks the profile seedProfile produces: declared
// 100 mm long, measured 120 mm (so effective = measured, discrepancy true).
func assertMeasuredProfile(t *testing.T, pp map[string]any, measuredAt time.Time) {
	t.Helper()
	if pp["effective_source"] != "measured" || pp["discrepancy"] != true {
		t.Fatalf("physical_profile = %v", pp)
	}
	measured, _ := pp["measured"].(map[string]any)
	if measured["length_mm"] != 120.0 || measured["volume_mm3"] != 1_200_000.0 || measured["device_id"] != "SCALE-01" ||
		measured["measured_at"] != measuredAt.Format(time.RFC3339Nano) {
		t.Fatalf("measured = %v", measured)
	}
	if eff, _ := pp["effective"].(map[string]any); eff["length_mm"] != 120.0 {
		t.Fatalf("effective = %v, want the measurement", pp["effective"])
	}
	if decl, _ := pp["declared"].(map[string]any); decl["length_mm"] != 100.0 || decl["weight_g"] != 1000.0 {
		t.Fatalf("declared = %v", pp["declared"])
	}
}

func TestGetProduct_UnclassifiedOmitsClassification(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-2", "Plain mug", nil, "", 0)

	got := h.ok(t, "get_product", map[string]any{"sku": "SKU-2"})
	if _, has := got["classification"]; has {
		t.Fatalf("an unclassified product must omit classification: %v", got)
	}
	pp, _ := got["physical_profile"].(map[string]any)
	if pp["effective_source"] != "none" || pp["discrepancy"] != false {
		t.Fatalf("physical_profile = %v", pp)
	}
	for _, absent := range []string{"declared", "measured", "effective"} {
		if _, has := pp[absent]; has {
			t.Fatalf("physical_profile.%s must be omitted when not recorded: %v", absent, pp)
		}
	}
}

func TestGetProduct_Errors(t *testing.T) {
	h := newHarness(t)
	failWith(t, h.session, "get_product", map[string]any{"sku": "NOPE"}, "product-not-found")
	failWith(t, h.session, "get_product", map[string]any{"sku": "has space"}, "invalid-sku")
	failWith(t, h.session, "get_product", map[string]any{"sku": ""}, "invalid-sku")
}

func TestGetProductClassification(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Glass vase", []string{"Fragile", "HighValue"}, "", 0)
	h.seed(t, "SKU-2", "Plain mug", nil, "", 0)

	got := h.ok(t, "get_product_classification", map[string]any{"sku": "SKU-1"})
	want := map[string]any{"sku": "SKU-1", "handling_tags": []any{"Fragile", "HighValue"}, "classification_source": "native", "version": 2.0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("classification = %v, want %v (temperature_class/dot_hazard_class omitted when unset)", got, want)
	}
	failWith(t, h.session, "get_product_classification", map[string]any{"sku": "SKU-2"}, "product-classification-not-found")
	failWith(t, h.session, "get_product_classification", map[string]any{"sku": "SKU-404"}, "product-not-found")
}

func TestGetPhysicalProfile(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Box", nil, "", 0)
	h.seedProfile(t, "SKU-1")

	got := h.ok(t, "get_physical_profile", map[string]any{"sku": "SKU-1"})
	if got["sku"] != "SKU-1" || got["version"] != 3.0 || got["effective_source"] != "measured" || got["discrepancy"] != true {
		t.Fatalf("profile = %v", got)
	}
	if _, has := got["declared"]; !has {
		t.Fatalf("declared missing: %v", got)
	}
	failWith(t, h.session, "get_physical_profile", map[string]any{"sku": "SKU-404"}, "product-not-found")
}

func TestListProducts_FiltersAndPages(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-A", "a", []string{"Hazmat"}, "", 0)
	h.seed(t, "SKU-B", "b", []string{"Fragile"}, "", 0)
	h.seed(t, "SKU-C", "c", nil, "", 0)
	h.seed(t, "SKU-D", "d", []string{"Hazmat", "Fragile"}, "", 0)

	skus := func(page map[string]any) []string {
		var out []string
		for _, item := range page["items"].([]any) {
			out = append(out, item.(map[string]any)["sku"].(string))
		}
		return out
	}

	all := h.ok(t, "list_products", map[string]any{})
	if got := skus(all); !reflect.DeepEqual(got, []string{"SKU-A", "SKU-B", "SKU-C", "SKU-D"}) {
		t.Fatalf("all = %v", got)
	}
	if _, has := all["next_cursor"]; has {
		t.Fatalf("a last page must omit next_cursor: %v", all)
	}

	first := h.ok(t, "list_products", map[string]any{"limit": 2})
	if got := skus(first); !reflect.DeepEqual(got, []string{"SKU-A", "SKU-B"}) || first["next_cursor"] == nil {
		t.Fatalf("first page = %v (cursor %v)", got, first["next_cursor"])
	}
	second := h.ok(t, "list_products", map[string]any{"limit": 2, "cursor": first["next_cursor"]})
	if got := skus(second); !reflect.DeepEqual(got, []string{"SKU-C", "SKU-D"}) {
		t.Fatalf("second page = %v", got)
	}

	if got := skus(h.ok(t, "list_products", map[string]any{"handling_tag": "Hazmat"})); !reflect.DeepEqual(got, []string{"SKU-A", "SKU-D"}) {
		t.Fatalf("handling_tag=Hazmat = %v", got)
	}
	if got := skus(h.ok(t, "list_products", map[string]any{"classified": false})); !reflect.DeepEqual(got, []string{"SKU-C"}) {
		t.Fatalf("classified=false = %v", got)
	}
	if got := skus(h.ok(t, "list_products", map[string]any{"classified": true, "handling_tag": "Fragile"})); !reflect.DeepEqual(got, []string{"SKU-B", "SKU-D"}) {
		t.Fatalf("classified=true&handling_tag=Fragile = %v", got)
	}
}

func TestListProducts_EmptyIsAnEmptyArray(t *testing.T) {
	h := newHarness(t)
	got := h.ok(t, "list_products", map[string]any{"classified": true})
	if items, isSlice := got["items"].([]any); !isSlice || len(items) != 0 {
		t.Fatalf("items = %#v, want []", got["items"])
	}
}

func TestListProducts_MalformedQueries(t *testing.T) {
	h := newHarness(t)
	for name, args := range map[string]map[string]any{
		"unknown tag":    {"handling_tag": "Radioactive"},
		"limit too big":  {"limit": 501},
		"negative limit": {"limit": -1},
		"bad cursor":     {"cursor": "!!not-base64!!"},
	} {
		t.Run(name, func(t *testing.T) {
			failWith(t, h.session, "list_products", args, "malformed-request")
		})
	}
}

// Reading never writes: no outbox row is added and no version moves, so the
// MCP surface can never publish an event (docs/adr/0005).
func TestToolsNeverWrite(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Box", []string{"Hazmat"}, "", 0)
	h.seedProfile(t, "SKU-1")
	before := len(h.outbox.Messages())

	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"get_product", map[string]any{"sku": "SKU-1"}},
		{"list_products", map[string]any{}},
		{"get_product_classification", map[string]any{"sku": "SKU-1"}},
		{"get_physical_profile", map[string]any{"sku": "SKU-1"}},
		{"get_product", map[string]any{"sku": "SKU-404"}},
	} {
		call(t, h.session, c.name, c.args)
	}
	if after := len(h.outbox.Messages()); after != before {
		t.Fatalf("outbox rows %d -> %d: a read tool wrote an event", before, after)
	}
	if got := h.ok(t, "get_product", map[string]any{"sku": "SKU-1"}); got["version"] != 4.0 {
		t.Fatalf("version moved: %v", got["version"])
	}
}

// failingProducts is a ProductRepository whose reads fail with an
// infrastructure error carrying a would-be secret.
type failingProducts struct{}

func (failingProducts) Get(context.Context, product.SKU) (*product.Product, error) {
	return nil, errors.New("postgres dsn secret unreachable")
}

func (failingProducts) Save(context.Context, *product.Product, int64) error {
	return errors.New("boom")
}

func (failingProducts) List(context.Context, repository.ListFilter, product.SKU, int) ([]*product.Product, error) {
	return nil, errors.New("postgres dsn secret unreachable")
}

func TestUnexpectedErrorsAreGenericAndLeakNothing(t *testing.T) {
	session := connectSession(t, depsFor(failingProducts{}))
	for _, name := range []string{"get_product", "get_product_classification", "get_physical_profile"} {
		res := call(t, session, name, map[string]any{"sku": "SKU-1"})
		if !res.IsError || strings.Contains(text(res), "secret") || !strings.HasPrefix(text(res), "internal-error: ") {
			t.Fatalf("%s: %q, want a generic internal-error", name, text(res))
		}
	}
	res := call(t, session, "list_products", map[string]any{})
	if !res.IsError || strings.Contains(text(res), "secret") {
		t.Fatalf("list_products: %q", text(res))
	}
}
