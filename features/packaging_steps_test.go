package features_test

// Step definitions of packaging.feature (ADR 0011): the pack levels with GS1
// GTINs. They drive the same router as every other scenario; the Givens use
// the REST routes too, so a scenario only says what the operator did.

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/cucumber/godog"
)

func registerPackagingSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I POST \"([^\"]*)\"$`, func(path string) error { return w.call(http.MethodPost, path, "") })
	sc.Step(`^I POST \"([^\"]*)\" with body '(.*)'$`, func(path, body string) error { return w.call(http.MethodPost, path, body) })
	sc.Step(`^\"([^\"]*)\" has the each GTIN \"([^\"]*)\"$`, w.hasEachGTIN)
	sc.Step(`^\"([^\"]*)\" has a case of (\d+) eaches with GTIN \"([^\"]*)\"$`, w.hasCaseOf)
	sc.Step(`^\"([^\"]*)\" has a case of (\d+) eaches weighing (\d+) g with GTIN \"([^\"]*)\"$`, w.hasHeavyCaseOf)
	sc.Step(`^the outbox event types end with \"([^\"]*)\"$`, w.theOutboxEventTypesEndWith)
	sc.Step(`^the outbox holds (\d+) messages$`, w.theOutboxHolds)
}

func (w *world) hasEachGTIN(sku, gtin string) error {
	return w.mustCall(http.MethodPut, "/products/"+sku+"/pack-levels/EACH", fmt.Sprintf(`{"gtin":%q,"isOrderable":true}`, gtin))
}

func (w *world) hasCaseOf(sku string, qty int, gtin string) error {
	eachGTIN, err := w.eachGTINOf(sku)
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`{"gtin":%q,"childGtin":%q,"qtyOfChild":%d,"isOrderable":true,"isDespatchUnit":true}`, gtin, eachGTIN, qty)
	return w.mustCall(http.MethodPut, "/products/"+sku+"/pack-levels/CASE", body)
}

func (w *world) hasHeavyCaseOf(sku string, qty, grams int, gtin string) error {
	eachGTIN, err := w.eachGTINOf(sku)
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`{"gtin":%q,"childGtin":%q,"qtyOfChild":%d,"dims":{"lengthMm":400,"widthMm":300,"heightMm":200,"weightG":%d}}`, gtin, eachGTIN, qty, grams)
	return w.mustCall(http.MethodPut, "/products/"+sku+"/pack-levels/CASE", body)
}

// eachGTINOf reads the SKU's active each GTIN back through the API.
func (w *world) eachGTINOf(sku string) (string, error) {
	if err := w.call(http.MethodGet, "/products/"+sku+"/pack-levels", ""); err != nil {
		return "", err
	}
	v, ok, err := w.field("items")
	items, _ := v.([]any)
	if err != nil || !ok || len(items) == 0 {
		return "", fmt.Errorf("%s has no pack levels yet: %s", sku, w.body)
	}
	first, _ := items[0].(map[string]any)
	if first["level"] != "EACH" {
		return "", fmt.Errorf("%s has no each level: %s", sku, w.body)
	}
	return fmt.Sprint(first["gtin"]), nil
}

func (w *world) theOutboxEventTypesEndWith(names string) error {
	want := splitList(names)
	msgs := w.outbox.Messages()
	if len(msgs) < len(want) {
		return fmt.Errorf("outbox holds %d messages, want at least %d", len(msgs), len(want))
	}
	tail := msgs[len(msgs)-len(want):]
	for i, m := range tail {
		if wantType := "com.warehouse.wms.product-master.product." + want[i]; m.EventType != wantType {
			var got []string
			for _, x := range tail {
				got = append(got, strings.TrimPrefix(x.EventType, "com.warehouse.wms.product-master.product."))
			}
			return fmt.Errorf("outbox ends with %v, want %v", got, want)
		}
	}
	return nil
}

func (w *world) theOutboxHolds(n int) error {
	if got := len(w.outbox.Messages()); got != n {
		return fmt.Errorf("outbox holds %d messages, want %d", got, n)
	}
	return nil
}
