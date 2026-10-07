// Package main_test hosts the godog (Cucumber for Go) acceptance suite. It
// drives the REAL chi router over HTTP and the REAL legacy importer with the
// in-memory adapters, wired the way cmd/api wires them, so every scenario in
// features/*.feature is a black-box test of the service.
package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/cucumber/godog"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/product-master/internal/adapters/inbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	"github.com/claudioed/product-master/internal/application/usecases"
)

// TestFeatures runs every Gherkin feature under features/.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// bddNow is the service clock of every scenario.
var bddNow = time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return bddNow }

// world is the per-scenario state.
type world struct {
	server   *httptest.Server
	outbox   *memory.OutboxRepo
	importer *inboundkafka.LegacyImporter

	status int
	body   []byte

	listQuery  string
	legacySeq  int
	lastLegacy []byte
}

func (w *world) start() {
	products, ob, processed := memory.NewProductRepo(), memory.NewOutboxRepo(), memory.NewProcessedEventRepo()
	writer := usecases.Writer{
		Products: products, Outbox: ob, Encoder: outboundkafka.NewEncoder(),
		UoW: memory.NewUnitOfWork(products, ob, processed), Clock: fixedClock{},
	}
	s := &inboundhttp.Server{
		RegisterProduct:   &usecases.RegisterProduct{Writer: writer},
		ClassifyProduct:   &usecases.ClassifyProduct{Writer: writer},
		DeclareDimensions: &usecases.DeclareDimensions{Writer: writer},
		RecordMeasurement: &usecases.RecordMeasurement{Writer: writer},
		GetProduct:        &usecases.GetProduct{Products: products},
		ListProducts:      &usecases.ListProducts{Products: products},
	}
	w.server = httptest.NewServer(inboundhttp.NewRouter(s))
	w.outbox = ob
	w.importer = &inboundkafka.LegacyImporter{
		Import: &usecases.ImportLegacyClassification{Writer: writer, ProcessedEvents: processed},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	w.status, w.body, w.listQuery, w.legacySeq, w.lastLegacy = 0, nil, "", 0, nil
}

func (w *world) stop() {
	if w.server != nil {
		w.server.Close()
		w.server = nil
	}
}

// call issues a request and remembers the response.
func (w *world) call(method, path, body string) error {
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, w.server.URL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	w.status = resp.StatusCode
	w.body, err = io.ReadAll(resp.Body)
	return err
}

// mustCall is call for Given steps: the setup itself must succeed.
func (w *world) mustCall(method, path, body string) error {
	if err := w.call(method, path, body); err != nil {
		return err
	}
	if w.status != http.StatusOK && w.status != http.StatusCreated {
		return fmt.Errorf("setup %s %s = %d %s", method, path, w.status, w.body)
	}
	return nil
}

func (w *world) iPUT(path, body string) error { return w.call(http.MethodPut, path, body) }
func (w *world) iGET(path string) error       { return w.call(http.MethodGet, path, "") }

func (w *world) productIsRegistered(sku, description string) error {
	b, _ := json.Marshal(map[string]string{"description": description})
	return w.mustCall(http.MethodPut, "/products/"+sku, string(b))
}

func splitList(raw string) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (w *world) isClassifiedWith(sku, tags string) error {
	b, _ := json.Marshal(map[string]any{"handlingTags": splitList(tags)})
	return w.mustCall(http.MethodPut, "/products/"+sku+"/classification", string(b))
}

func (w *world) isClassifiedWithTemperature(sku, tags, temperature string) error {
	b, _ := json.Marshal(map[string]any{"handlingTags": splitList(tags), "temperatureClass": temperature})
	return w.mustCall(http.MethodPut, "/products/"+sku+"/classification", string(b))
}

func (w *world) hasDeclaredDimensions(sku string, l, wd, h, g int) error {
	b, _ := json.Marshal(map[string]int{"lengthMm": l, "widthMm": wd, "heightMm": h, "weightG": g})
	return w.mustCall(http.MethodPut, "/products/"+sku+"/dimensions/declared", string(b))
}

func (w *world) wasMeasuredAt(sku, at string) error {
	body := `{"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":"` + at + `"}`
	return w.mustCall(http.MethodPut, "/products/"+sku+"/dimensions/measured", body)
}

func (w *world) theResponseStatusIs(want int) error {
	if w.status != want {
		return fmt.Errorf("status = %d (%s), want %d", w.status, w.body, want)
	}
	return nil
}

// field walks a dotted path into the last JSON response.
func (w *world) field(path string) (any, bool, error) {
	var cur any
	if err := json.Unmarshal(w.body, &cur); err != nil {
		return nil, false, fmt.Errorf("response is not JSON: %s", w.body)
	}
	for _, key := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false, nil
		}
		if cur, ok = obj[key]; !ok {
			return nil, false, nil
		}
	}
	return cur, true, nil
}

// render prints a JSON value the way the feature files write it: arrays as
// comma-separated items, numbers without a fraction.
func render(v any) string {
	switch t := v.(type) {
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			parts[i] = render(item)
		}
		return strings.Join(parts, ",")
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		return fmt.Sprint(t)
	}
}

func (w *world) theResponseFieldIs(path, want string) error {
	v, ok, err := w.field(path)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("field %q absent in %s", path, w.body)
	}
	if got := render(v); got != want {
		return fmt.Errorf("field %q = %q, want %q (%s)", path, got, want, w.body)
	}
	return nil
}

func (w *world) theResponseFieldIsAbsent(path string) error {
	_, ok, err := w.field(path)
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("field %q present in %s", path, w.body)
	}
	return nil
}

func (w *world) theProblemTypeIs(slug string) error {
	return w.theResponseFieldIs("type", "https://errors.product-master.warehouse-systems.dev/"+slug)
}

func (w *world) theOutboxEventTypesAre(names string) error {
	want := make([]string, 0)
	for _, n := range splitList(names) {
		want = append(want, "com.warehouse.wms.product-master.product."+n)
	}
	var got []string
	for _, m := range w.outbox.Messages() {
		got = append(got, m.EventType)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("outbox = %v, want %v", got, want)
	}
	return nil
}

func (w *world) theOutboxIsEmpty() error { return w.theOutboxEventTypesAre("") }

func (w *world) lastClassifiedDataIs(want string) error {
	msgs := w.outbox.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if !strings.HasSuffix(msgs[i].EventType, ".ProductClassified") {
			continue
		}
		e, err := cloudevents.Decode(msgs[i].Value)
		if err != nil {
			return err
		}
		if got := string(e.Data()); got != want {
			return fmt.Errorf("data = %s, want %s", got, want)
		}
		return nil
	}
	return fmt.Errorf("no ProductClassified in the outbox")
}

func (w *world) iListProductsWithQuery(query string) error {
	w.listQuery = query
	return w.iGET("/products?" + query)
}

func (w *world) iListTheNextPage() error {
	cursor, ok, err := w.field("nextCursor")
	if err != nil || !ok {
		return fmt.Errorf("no nextCursor in %s (%v)", w.body, err)
	}
	return w.iGET("/products?" + w.listQuery + "&cursor=" + fmt.Sprint(cursor))
}

func (w *world) theListedSKUsAre(want string) error {
	var page struct {
		Items []struct {
			SKU string `json:"sku"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.body, &page); err != nil {
		return err
	}
	got := make([]string, len(page.Items))
	for i, it := range page.Items {
		got[i] = it.SKU
	}
	if strings.Join(got, ",") != strings.Join(splitList(want), ",") {
		return fmt.Errorf("listed %v, want %s", got, want)
	}
	return nil
}

func (w *world) thereIsANextPage() error { return w.theResponseFieldIsPresent("nextCursor") }

func (w *world) theResponseFieldIsPresent(path string) error {
	if _, ok, err := w.field(path); err != nil || !ok {
		return fmt.Errorf("field %q absent in %s", path, w.body)
	}
	return nil
}

func (w *world) thereIsNoNextPage() error { return w.theResponseFieldIsAbsent("nextCursor") }

// legacyEvent builds the exact CloudEvent inventory-storage publishes.
func (w *world) legacyEvent(sku, tags string, dot int) ([]byte, error) {
	w.legacySeq++
	data := map[string]any{"sku": sku, "handling_tags": splitList(tags)}
	if dot != 0 {
		data["dot_hazard_class"] = dot
	}
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(fmt.Sprintf("legacy-%d", w.legacySeq))
	e.SetSource("/warehouse/inventory-storage")
	e.SetType(inboundkafka.TypeLegacyProductClassified)
	e.SetSubject(sku)
	e.SetTime(bddNow)
	e.SetDataSchema("urn:warehouse:inventory-storage:events:ProductClassified:v1")
	if err := e.SetData("application/json", data); err != nil {
		return nil, err
	}
	return json.Marshal(e)
}

func (w *world) publishLegacy(sku, tags string, dot int) error {
	value, err := w.legacyEvent(sku, tags, dot)
	if err != nil {
		return err
	}
	w.lastLegacy = value
	return w.importer.HandleMessage(context.Background(), value)
}

func (w *world) legacyClassification(sku, tags string) error { return w.publishLegacy(sku, tags, 0) }

func (w *world) legacyClassificationWithDOT(sku, tags string, dot int) error {
	return w.publishLegacy(sku, tags, dot)
}

func (w *world) redeliverLegacy() error {
	return w.importer.HandleMessage(context.Background(), w.lastLegacy)
}

// InitializeScenario registers every step.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.start()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.stop()
		return ctx, err
	})

	sc.Step(`^I PUT "([^"]*)" with body '(.*)'$`, w.iPUT)
	sc.Step(`^I GET "([^"]*)"$`, w.iGET)
	sc.Step(`^the product "([^"]*)" is registered with description "([^"]*)"$`, w.productIsRegistered)
	sc.Step(`^"([^"]*)" is classified with tags "([^"]*)"$`, w.isClassifiedWith)
	sc.Step(`^"([^"]*)" is classified with tags "([^"]*)" and temperature class "([^"]*)"$`, w.isClassifiedWithTemperature)
	sc.Step(`^"([^"]*)" has declared dimensions (\d+) x (\d+) x (\d+) mm and (\d+) g$`, w.hasDeclaredDimensions)
	sc.Step(`^"([^"]*)" was measured at "([^"]*)"$`, w.wasMeasuredAt)

	sc.Step(`^the response status is (\d+)$`, w.theResponseStatusIs)
	sc.Step(`^the response field "([^"]*)" is "([^"]*)"$`, w.theResponseFieldIs)
	sc.Step(`^the response field "([^"]*)" is absent$`, w.theResponseFieldIsAbsent)
	sc.Step(`^the problem type is "([^"]*)"$`, w.theProblemTypeIs)
	sc.Step(`^the outbox event types are "([^"]*)"$`, w.theOutboxEventTypesAre)
	sc.Step(`^the outbox is empty$`, w.theOutboxIsEmpty)
	sc.Step(`^the last published ProductClassified data is '(.*)'$`, w.lastClassifiedDataIs)

	sc.Step(`^I list products with query "([^"]*)"$`, w.iListProductsWithQuery)
	sc.Step(`^I list the next page$`, w.iListTheNextPage)
	sc.Step(`^the listed SKUs are "([^"]*)"$`, w.theListedSKUsAre)
	sc.Step(`^there is a next page$`, w.thereIsANextPage)
	sc.Step(`^there is no next page$`, w.thereIsNoNextPage)

	sc.Step(`^inventory-storage publishes a legacy classification of "([^"]*)" with tags "([^"]*)"$`, w.legacyClassification)
	sc.Step(`^inventory-storage publishes a legacy classification of "([^"]*)" with tags "([^"]*)" and DOT hazard class (\d+)$`, w.legacyClassificationWithDOT)
	sc.Step(`^inventory-storage redelivers the same legacy event$`, w.redeliverLegacy)
}
