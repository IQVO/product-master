package features_test

// Step definitions of the documentation-derived rule features
// (product_doc_rules, packaging_doc_rules, analytics_doc_rules). They reuse
// the world of features_test.go and add: a movable service clock, requests
// with chosen Idempotency-Key headers, oversized/padded bodies, repeated
// characters for boundary-sized inputs, event version assertions, and the
// analytics rig (the production fan-out encoder, the projector's real
// consumer over the in-memory analytical store, and the reports router).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
	kafkago "github.com/segmentio/kafka-go"

	inboundhttp "github.com/claudioed/product-master/internal/adapters/inbound/http"
	inboundkafka "github.com/claudioed/product-master/internal/adapters/inbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/product-master/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/domain/product"
)

func registerDocRuleSteps(sc *godog.ScenarioContext, w *world) {
	// requests
	sc.Step(`^the (PUT|POST) "([^"]*)" with body '(.*)' has been accepted$`, w.hasBeenAccepted)
	sc.Step(`^I (PUT|POST) "([^"]*)" with a JSON body padded with leading spaces to (\d+) bytes: '(.*)'$`, w.iSendPadded)
	sc.Step(`^I send POST "([^"]*)" with body '(.*)'$`, func(path, body string) error { return w.call(http.MethodPost, path, body) })
	sc.Step(`^I register a SKU made of (\d+) times "([^"]*)"$`, w.registerSKUMadeOf)
	sc.Step(`^I register "([^"]*)" with a description made of (\d+) times "([^"]*)"$`, w.registerDescriptionMadeOf)
	sc.Step(`^the products named "([^"]*)" numbered (\d+) to (\d+) are registered$`, w.registerNumbered)
	sc.Step(`^I record for "([^"]*)" a measurement taken at "([^"]*)" by a device id of (\d+) characters$`, w.measureWithDeviceOf)
	sc.Step(`^I retire the "([A-Z]+)" level of "([^"]*)" with a reason of (\d+) characters$`, w.retireWithReasonOf)
	sc.Step(`^I GET the next page of "([^"]*)"$`, w.iGetNextPageOf)
	sc.Step(`^the service clock reads "([^"]*)"$`, w.clockReads)

	// idempotency keys
	sc.Step(`^I POST "([^"]*)" with body '(.*)' and the Idempotency-Key "([^"]*)"$`, w.postWithKey)
	sc.Step(`^I POST "([^"]*)" with body '(.*)' and no Idempotency-Key$`, w.postWithoutKey)
	sc.Step(`^I POST "([^"]*)" with the Idempotency-Key "([^"]*)"$`, func(path, key string) error { return w.do(w.server.URL, http.MethodPost, path, "", []string{key}) })
	sc.Step(`^I POST "([^"]*)" with the Idempotency-Keys "([^"]*)" and "([^"]*)"$`, func(path, a, b string) error { return w.do(w.server.URL, http.MethodPost, path, "", []string{a, b}) })
	sc.Step(`^I POST "([^"]*)" with an Idempotency-Key of (\d+) characters$`, func(path string, n int) error {
		return w.do(w.server.URL, http.MethodPost, path, "", []string{strings.Repeat("k", n)})
	})

	// response assertions
	sc.Step(`^the response field "([^"]*)" is null$`, w.theResponseFieldIsNull)
	sc.Step(`^the response list "([^"]*)" has (\d+) items$`, w.theResponseListHas)
	sc.Step(`^the response header "([^"]*)" is "([^"]*)"$`, w.theResponseHeaderIs)
	sc.Step(`^the response has no header "([^"]*)"$`, w.theResponseHasNoHeader)
	sc.Step(`^the outbox event versions are "([^"]*)"$`, w.theOutboxVersionsAre)

	// analytics
	sc.Step(`^the analytics projector consumes the analytics topic$`, w.analyticsConsumesNew)
	sc.Step(`^the analytics projector consumes the analytics topic newest first$`, w.analyticsConsumesReversed)
	sc.Step(`^the analytics topic is delivered again from the beginning$`, w.analyticsRedelivers)
	sc.Step(`^the analytics projector consumes only the analytics messages of type "([^"]*)"$`, w.analyticsConsumesOnlyType)
	sc.Step(`^the analytics projector consumes a copy of the last analytics message whose subject is "([^"]*)"$`, w.analyticsConsumesWithSubject)
	sc.Step(`^the analytics projector consumes a copy of the last analytics message without its "([^"]*)" data field$`, w.analyticsConsumesWithoutField)
	sc.Step(`^the analytics projector consumes a copy of the last analytics message with the type "([^"]*)"$`, w.analyticsConsumesWithType)
	sc.Step(`^the analytics projector consumes a message that is not a CloudEvent$`, w.analyticsConsumesNonCloudEvent)
	sc.Step(`^I GET report "([^"]*)"$`, func(path string) error { return w.do(w.analytics.server.URL, http.MethodGet, path, "", nil) })
	sc.Step(`^the analytics topic holds (\d+) messages$`, w.analyticsHolds)
	sc.Step(`^every analytics message repeats the id, type and payload of an integration message$`, w.analyticsMatchesIntegration)
	sc.Step(`^(\d+) messages? (?:is|are) dead-lettered$`, w.deadLettered)
	sc.Step(`^the dead-letter reason contains "([^"]*)"$`, w.deadLetterReasonContains)
}

// ---------------------------------------------------------------- requests

func (w *world) hasBeenAccepted(method, path, body string) error {
	return w.mustCall(method, path, body)
}

func (w *world) iSendPadded(method, path string, size int, body string) error {
	if size < len(body) {
		return fmt.Errorf("body is %d bytes, longer than the requested %d", len(body), size)
	}
	return w.call(method, path, strings.Repeat(" ", size-len(body))+body)
}

func (w *world) registerSKUMadeOf(n int, unit string) error {
	sku := strings.Repeat(unit, n)
	return w.call(http.MethodPut, "/products/"+url.PathEscape(sku), `{"description":"Boundary SKU"}`)
}

func (w *world) registerDescriptionMadeOf(sku string, n int, unit string) error {
	b, _ := json.Marshal(map[string]string{"description": strings.Repeat(unit, n)})
	return w.call(http.MethodPut, "/products/"+sku, string(b))
}

func (w *world) registerNumbered(prefix string, from, to int) error {
	for i := from; i <= to; i++ {
		if err := w.productIsRegistered(fmt.Sprintf("%s%03d", prefix, i), "Catalogue load"); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) measureWithDeviceOf(sku, at string, n int) error {
	body := fmt.Sprintf(`{"lengthMm":10,"widthMm":10,"heightMm":10,"weightG":10,"measuredAt":%q,"deviceId":%q}`, at, strings.Repeat("D", n))
	return w.call(http.MethodPut, "/products/"+sku+"/dimensions/measured", body)
}

func (w *world) retireWithReasonOf(level, sku string, n int) error {
	b, _ := json.Marshal(map[string]string{"reason": strings.Repeat("r", n)})
	return w.call(http.MethodPost, "/products/"+sku+"/pack-levels/"+level+"/retirements", string(b))
}

func (w *world) iGetNextPageOf(path string) error {
	cursor, ok, err := w.field("nextCursor")
	if err != nil || !ok {
		return fmt.Errorf("no nextCursor in %s (%w)", w.body, err)
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return w.iGET(path + sep + "cursor=" + fmt.Sprint(cursor))
}

func (w *world) clockReads(at string) error {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return err
	}
	w.clock.set(t)
	return nil
}

func (w *world) postWithKey(path, body, key string) error {
	return w.do(w.server.URL, http.MethodPost, path, body, []string{key})
}

func (w *world) postWithoutKey(path, body string) error {
	return w.do(w.server.URL, http.MethodPost, path, body, []string{})
}

// ------------------------------------------------------ response assertions

func (w *world) theResponseFieldIsNull(path string) error {
	v, ok, err := w.field(path)
	if err != nil {
		return err
	}
	if !ok || v != nil {
		return fmt.Errorf("field %q = %v (present %v), want null in %s", path, v, ok, w.body)
	}
	return nil
}

func (w *world) theResponseListHas(path string, want int) error {
	v, ok, err := w.field(path)
	if err != nil {
		return err
	}
	list, isList := v.([]any)
	if !ok || !isList {
		return fmt.Errorf("field %q is not a list in %s", path, w.body)
	}
	if len(list) != want {
		return fmt.Errorf("list %q has %d items, want %d", path, len(list), want)
	}
	return nil
}

func (w *world) theResponseHeaderIs(name, want string) error {
	if got := w.header.Get(name); got != want {
		return fmt.Errorf("header %s = %q, want %q", name, got, want)
	}
	return nil
}

func (w *world) theResponseHasNoHeader(name string) error {
	if got := w.header.Values(name); len(got) != 0 {
		return fmt.Errorf("header %s = %v, want none", name, got)
	}
	return nil
}

func (w *world) theOutboxVersionsAre(want string) error {
	var got []string
	for _, m := range w.outbox.Messages() {
		e, err := cloudevents.Decode(m.Value)
		if err != nil {
			return err
		}
		var d struct {
			Version int64 `json:"version"`
		}
		if err := json.Unmarshal(e.Data(), &d); err != nil {
			return err
		}
		got = append(got, strconv.FormatInt(d.Version, 10))
	}
	if strings.Join(got, ",") != strings.Join(splitList(want), ",") {
		return fmt.Errorf("event versions = %v, want %s", got, want)
	}
	return nil
}

// --------------------------------------------------------------- analytics

// analyticsRig is the analytics side of one scenario: the analytics rows the
// production FanoutEncoder wrote (the integration rows go to the outbox the
// other scenarios read), the projector's real consumer over the in-memory
// analytical store, and the reports binary's router over the same store.
type analyticsRig struct {
	w      *world
	mu     sync.Mutex
	rows   []outbox.Message
	fed    int
	dlq    *recordingDLQ
	cons   *inboundkafka.AnalyticsConsumer
	server *httptest.Server
}

func newAnalyticsRig(w *world) *analyticsRig {
	store := analyticsstore.NewMemory()
	r := &analyticsRig{w: w, dlq: &recordingDLQ{}}
	r.cons = &inboundkafka.AnalyticsConsumer{
		Projection: store,
		DLQ:        r.dlq,
		DLQTopic:   outboundkafka.AnalyticsTopic + inboundkafka.DLQSuffix,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        w.clock.Now,
	}
	r.server = httptest.NewServer(inboundhttp.NewReportsRouter(&inboundhttp.ReportsServer{Reader: store, Now: w.clock.Now}))
	return r
}

func (r *analyticsRig) stop() { r.server.Close() }

// encoder is the production FanoutEncoder with the analytics rows siphoned
// into the rig; the integration rows continue to the outbox unchanged.
func (r *analyticsRig) encoder() *tapEncoder {
	return &tapEncoder{rig: r, fan: outboundkafka.NewFanoutEncoder()}
}

type tapEncoder struct {
	rig *analyticsRig
	fan *outboundkafka.FanoutEncoder
}

func (t *tapEncoder) Encode(events ...product.Event) ([]outbox.Message, error) {
	all, err := t.fan.Encode(events...)
	if err != nil {
		return nil, err
	}
	var integration []outbox.Message
	t.rig.mu.Lock()
	defer t.rig.mu.Unlock()
	for _, m := range all {
		if m.Topic == outboundkafka.AnalyticsTopic {
			t.rig.rows = append(t.rig.rows, m)
		} else {
			integration = append(integration, m)
		}
	}
	return integration, nil
}

func (r *analyticsRig) deliver(values ...[]byte) error {
	for _, v := range values {
		if err := r.cons.HandleMessage(context.Background(), kafkago.Message{Topic: outboundkafka.AnalyticsTopic, Value: v}); err != nil {
			return err
		}
	}
	return nil
}

func (r *analyticsRig) snapshot() []outbox.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]outbox.Message(nil), r.rows...)
}

func values(rows []outbox.Message) [][]byte {
	out := make([][]byte, len(rows))
	for i, m := range rows {
		out[i] = m.Value
	}
	return out
}

func (w *world) analyticsConsumesNew() error {
	rows := w.analytics.snapshot()
	pending := rows[w.analytics.fed:]
	w.analytics.fed = len(rows)
	return w.analytics.deliver(values(pending)...)
}

func (w *world) analyticsConsumesReversed() error {
	rows := w.analytics.snapshot()
	pending := rows[w.analytics.fed:]
	w.analytics.fed = len(rows)
	vs := values(pending)
	for i, j := 0, len(vs)-1; i < j; i, j = i+1, j-1 {
		vs[i], vs[j] = vs[j], vs[i]
	}
	return w.analytics.deliver(vs...)
}

func (w *world) analyticsRedelivers() error {
	return w.analytics.deliver(values(w.analytics.snapshot())...)
}

func (w *world) analyticsConsumesOnlyType(name string) error {
	var picked [][]byte
	for _, m := range w.analytics.snapshot() {
		if strings.HasSuffix(m.EventType, "."+name) {
			picked = append(picked, m.Value)
		}
	}
	if len(picked) == 0 {
		return fmt.Errorf("no %s on the analytics topic", name)
	}
	w.analytics.fed = len(w.analytics.snapshot())
	return w.analytics.deliver(picked...)
}

// lastRowCopy returns the newest analytics message as an editable CloudEvent.
func (w *world) lastRowCopy() (map[string]any, error) {
	rows := w.analytics.snapshot()
	if len(rows) == 0 {
		return nil, fmt.Errorf("the analytics topic is empty")
	}
	var ev map[string]any
	if err := json.Unmarshal(rows[len(rows)-1].Value, &ev); err != nil {
		return nil, err
	}
	return ev, nil
}

func (w *world) deliverEdited(edit func(ev map[string]any)) error {
	ev, err := w.lastRowCopy()
	if err != nil {
		return err
	}
	edit(ev)
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return w.analytics.deliver(b)
}

func (w *world) analyticsConsumesWithSubject(subject string) error {
	return w.deliverEdited(func(ev map[string]any) { ev["subject"] = subject })
}

func (w *world) analyticsConsumesWithoutField(field string) error {
	return w.deliverEdited(func(ev map[string]any) {
		if data, ok := ev["data"].(map[string]any); ok {
			delete(data, field)
		}
	})
}

func (w *world) analyticsConsumesWithType(name string) error {
	return w.deliverEdited(func(ev map[string]any) { ev["type"] = cloudevents.Type("product", name) })
}

func (w *world) analyticsConsumesNonCloudEvent() error {
	return w.analytics.deliver([]byte(`{"sku":"SKU-1","note":"not a CloudEvents 1.0 envelope"}`))
}

func (w *world) analyticsHolds(want int) error {
	if got := len(w.analytics.snapshot()); got != want {
		return fmt.Errorf("the analytics topic holds %d messages, want %d", got, want)
	}
	return nil
}

// analyticsMatchesIntegration checks ADR 0006 section 2 and 3: the analytics
// message of an occurrence has the SAME CloudEvents id, type and data as its
// integration message, on the other topic.
func (w *world) analyticsMatchesIntegration() error {
	byID := map[string]outbox.Message{}
	for _, m := range w.outbox.Messages() {
		byID[m.EventID] = m
	}
	rows := w.analytics.snapshot()
	if len(rows) == 0 {
		return fmt.Errorf("the analytics topic is empty")
	}
	for _, a := range rows {
		i, ok := byID[a.EventID]
		if !ok {
			return fmt.Errorf("analytics %s %s has no integration message with the same id", a.EventType, a.EventID)
		}
		ae, err := cloudevents.Decode(a.Value)
		if err != nil {
			return err
		}
		ie, err := cloudevents.Decode(i.Value)
		if err != nil {
			return err
		}
		if a.EventType != i.EventType || a.Topic == i.Topic || string(ae.Data()) != string(ie.Data()) {
			return fmt.Errorf("analytics %s on %s differs from the integration message on %s", a.EventType, a.Topic, i.Topic)
		}
		if !strings.Contains(ae.DataSchema(), ":analytics:") {
			return fmt.Errorf("analytics dataschema = %q", ae.DataSchema())
		}
	}
	return nil
}

type recordingDLQ struct {
	mu   sync.Mutex
	msgs []kafkago.Message
}

func (d *recordingDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.msgs = append(d.msgs, msgs...)
	return nil
}

func (d *recordingDLQ) Close() error { return nil }

func (w *world) deadLettered(want int) error {
	d := w.analytics.dlq
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.msgs) != want {
		return fmt.Errorf("%d messages dead-lettered, want %d", len(d.msgs), want)
	}
	return nil
}

func (w *world) deadLetterReasonContains(want string) error {
	d := w.analytics.dlq
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, m := range d.msgs {
		for _, h := range m.Headers {
			if h.Key == "x-dlq-error" && strings.Contains(string(h.Value), want) {
				return nil
			}
		}
	}
	return fmt.Errorf("no dead-letter reason contains %q", want)
}
