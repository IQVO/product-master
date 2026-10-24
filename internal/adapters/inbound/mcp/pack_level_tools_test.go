package mcp_test

import (
	"strings"
	"testing"
)

// Valid GTINs (mod-10 checked).
const (
	gtinEach  = "4006381333931"
	gtinCase  = "24006381333935"
	gtinCase2 = "34006381333932"
	gtinVar   = "94006381333934"
)

func (h *harness) declareEachAndCase(t *testing.T) {
	t.Helper()
	h.seed(t, "SKU-1", "Box", nil, "", 0)
	h.ok(t, "declare_pack_level", map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": gtinEach, "is_orderable": true,
		"dims": map[string]any{"length_mm": 100, "width_mm": 50, "height_mm": 40, "weight_g": 500}})
	h.ok(t, "declare_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": gtinCase, "child_gtin": gtinEach, "qty_of_child": 12, "is_despatch_unit": true})
}

func TestDeclarePackLevel_WritesThroughTheUseCaseAndTheOutbox(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Box", nil, "", 0)
	before := len(h.outbox.Messages())

	got := h.ok(t, "declare_pack_level", map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": gtinEach, "is_orderable": true})
	pl, _ := got["pack_level"].(map[string]any)
	if got["sku"] != "SKU-1" || got["version"] != 2.0 || pl["level"] != "EACH" || pl["gtin"] != "04006381333931" || pl["units_per_pack"] != 1.0 || pl["active"] != true || pl["is_orderable"] != true {
		t.Fatalf("declare each = %v", got)
	}
	got = h.ok(t, "declare_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": gtinCase, "child_gtin": gtinEach, "qty_of_child": 12, "is_despatch_unit": true})
	pl, _ = got["pack_level"].(map[string]any)
	if got["version"] != 3.0 || pl["units_per_pack"] != 12.0 || pl["qty_of_child"] != 12.0 || pl["child_gtin"] != "04006381333931" {
		t.Fatalf("declare case = %v", got)
	}
	msgs := h.outbox.Messages()
	if len(msgs) != before+2 || !strings.HasSuffix(msgs[len(msgs)-1].EventType, ".ProductPackLevelDeclared") {
		t.Fatalf("outbox rows %d -> %d, last %+v: the write tool must enqueue its event", before, len(msgs), msgs[len(msgs)-1])
	}
}

func TestDeclarePackLevel_RepeatingChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.declareEachAndCase(t)
	before := len(h.outbox.Messages())
	again := h.ok(t, "declare_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": gtinCase, "child_gtin": gtinEach, "qty_of_child": 12, "is_despatch_unit": true})
	if again["version"] != 3.0 || len(h.outbox.Messages()) != before {
		t.Fatalf("an identical declaration must change nothing: %v", again)
	}
}

func TestDeclarePackLevel_Rejections(t *testing.T) {
	h := newHarness(t)
	h.declareEachAndCase(t)
	h.seed(t, "SKU-2", "Other", nil, "", 0)
	for _, tt := range []struct {
		name string
		args map[string]any
		slug string
	}{
		{"quantity change", map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": gtinCase, "child_gtin": gtinEach, "qty_of_child": 24, "is_despatch_unit": true}, "pack-quantity-immutable"},
		{"check digit", map[string]any{"sku": "SKU-1", "level": "PALLET", "gtin": "24006381333930", "child_gtin": gtinEach, "qty_of_child": 2}, "invalid-gtin"},
		{"variable measure", map[string]any{"sku": "SKU-1", "level": "PALLET", "gtin": gtinVar, "child_gtin": gtinEach, "qty_of_child": 2}, "variable-measure-unsupported"},
		{"duplicate gtin on another sku", map[string]any{"sku": "SKU-2", "level": "EACH", "gtin": gtinEach}, "gtin-already-assigned"},
		{"unknown level", map[string]any{"sku": "SKU-1", "level": "BOX", "gtin": gtinCase2}, "invalid-pack-level"},
		{"no lower child", map[string]any{"sku": "SKU-1", "level": "PALLET", "gtin": gtinCase2, "qty_of_child": 4}, "pack-level-order"},
		{"quantity one", map[string]any{"sku": "SKU-1", "level": "PALLET", "gtin": gtinCase2, "child_gtin": gtinCase, "qty_of_child": 1}, "invalid-pack-quantity"},
		{"bad dimension", map[string]any{"sku": "SKU-1", "level": "PALLET", "gtin": gtinCase2, "child_gtin": gtinCase, "qty_of_child": 4, "dims": map[string]any{"length_mm": 0, "width_mm": 1, "height_mm": 1, "weight_g": 1}}, "invalid-dimension"},
		{"light pack", map[string]any{"sku": "SKU-1", "level": "PALLET", "gtin": gtinCase2, "child_gtin": gtinEach, "qty_of_child": 100, "dims": map[string]any{"length_mm": 10, "width_mm": 10, "height_mm": 10, "weight_g": 100}}, "pack-dimensions-below-contents"},
		{"unknown sku", map[string]any{"sku": "SKU-404", "level": "EACH", "gtin": gtinCase2}, "product-not-found"},
		{"bad sku", map[string]any{"sku": "bad sku", "level": "EACH", "gtin": gtinCase2}, "invalid-sku"},
	} {
		before := len(h.outbox.Messages())
		failWith(t, h.session, "declare_pack_level", tt.args, tt.slug)
		if len(h.outbox.Messages()) != before {
			t.Errorf("%s: a rejected declaration enqueued an event", tt.name)
		}
	}
}

func TestDeclarePackLevel_ANewGTINSupersedes(t *testing.T) {
	h := newHarness(t)
	h.declareEachAndCase(t)
	got := h.ok(t, "declare_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE", "gtin": gtinCase2, "child_gtin": gtinEach, "qty_of_child": 24, "is_despatch_unit": true})
	if got["version"] != 5.0 {
		t.Fatalf("supersede = %v", got)
	}
	list := h.ok(t, "get_pack_levels", map[string]any{"sku": "SKU-1"})
	items, _ := list["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items = %v", list)
	}
	old := items[1].(map[string]any)
	if old["gtin"] != "24006381333935" || old["active"] != false || old["effective_to"] == nil {
		t.Fatalf("old case = %v", old)
	}
}

func TestRetirePackLevel(t *testing.T) {
	h := newHarness(t)
	h.declareEachAndCase(t)
	before := len(h.outbox.Messages())

	got := h.ok(t, "retire_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE", "reason": "discontinued"})
	pl, _ := got["pack_level"].(map[string]any)
	if got["version"] != 4.0 || pl["active"] != false || pl["gtin"] != "24006381333935" || pl["effective_to"] == nil {
		t.Fatalf("retire = %v", got)
	}
	msgs := h.outbox.Messages()
	if len(msgs) != before+1 || !strings.HasSuffix(msgs[len(msgs)-1].EventType, ".ProductPackLevelRetired") {
		t.Fatalf("outbox = %d rows, last %+v", len(msgs), msgs[len(msgs)-1])
	}
	if !strings.Contains(string(msgs[len(msgs)-1].Value), `"reason":"discontinued"`) {
		t.Fatalf("event = %s", msgs[len(msgs)-1].Value)
	}
	if again := h.ok(t, "retire_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE"}); again["version"] != 4.0 || len(h.outbox.Messages()) != before+1 {
		t.Fatalf("retiring twice must change nothing: %v", again)
	}
	failWith(t, h.session, "retire_pack_level", map[string]any{"sku": "SKU-1", "level": "PALLET"}, "pack-level-not-found")
	failWith(t, h.session, "retire_pack_level", map[string]any{"sku": "SKU-1", "level": "BOX"}, "invalid-pack-level")
	failWith(t, h.session, "retire_pack_level", map[string]any{"sku": "SKU-404", "level": "CASE"}, "product-not-found")
	failWith(t, h.session, "retire_pack_level", map[string]any{"sku": "SKU-1", "level": "EACH", "reason": strings.Repeat("r", 201)}, "invalid-reason")
}

func TestResolveGTIN(t *testing.T) {
	h := newHarness(t)
	h.declareEachAndCase(t)
	h.ok(t, "retire_pack_level", map[string]any{"sku": "SKU-1", "level": "CASE"})

	got := h.ok(t, "resolve_gtin", map[string]any{"gtin": gtinEach})
	if got["sku"] != "SKU-1" || got["gtin"] != "04006381333931" || got["level"] != "EACH" || got["units_per_pack"] != 1.0 || got["active"] != true {
		t.Fatalf("each = %v", got)
	}
	got = h.ok(t, "resolve_gtin", map[string]any{"gtin": gtinCase})
	if got["level"] != "CASE" || got["units_per_pack"] != 12.0 || got["active"] != false || got["child_gtin"] != "04006381333931" || got["effective_to"] == nil {
		t.Fatalf("a retired GTIN resolves, inactive: %v", got)
	}
	failWith(t, h.session, "resolve_gtin", map[string]any{"gtin": "5901234123457"}, "gtin-not-found")
	failWith(t, h.session, "resolve_gtin", map[string]any{"gtin": "4006381333930"}, "invalid-gtin")
	failWith(t, h.session, "resolve_gtin", map[string]any{"gtin": gtinVar}, "variable-measure-unsupported")
}

func TestGetPackLevels(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Box", nil, "", 0)
	empty := h.ok(t, "get_pack_levels", map[string]any{"sku": "SKU-1"})
	if items, ok := empty["items"].([]any); !ok || len(items) != 0 || empty["version"] != 1.0 {
		t.Fatalf("a product without levels lists an empty array: %v", empty)
	}
	h.declareEachAndCase(t)
	got := h.ok(t, "get_pack_levels", map[string]any{"sku": "SKU-1"})
	items, _ := got["items"].([]any)
	if len(items) != 2 || got["version"] != 3.0 {
		t.Fatalf("levels = %v", got)
	}
	each := items[0].(map[string]any)
	dims, _ := each["dims"].(map[string]any)
	if each["level"] != "EACH" || dims["weight_g"] != 500.0 || dims["volume_mm3"] != 200000.0 {
		t.Fatalf("each = %v", each)
	}
	failWith(t, h.session, "get_pack_levels", map[string]any{"sku": "SKU-404"}, "product-not-found")
	failWith(t, h.session, "get_pack_levels", map[string]any{"sku": "bad sku"}, "invalid-sku")
}
