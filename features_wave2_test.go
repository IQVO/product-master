package main_test

// Step definitions added by the second BDD coverage wave. They reuse the
// world of features_test.go and only add what the new scenarios need:
// boundary-sized inputs, a measurement helper, event payload and envelope
// assertions, the readiness gate, and further legacy-importer inputs.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/cucumber/godog"

	inboundkafka "github.com/claudioed/product-master/internal/adapters/inbound/kafka"
	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
)

func registerWave2Steps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I send (GET|PUT) "([^"]*)" with body '(.*)'$`, w.iSend)
	sc.Step(`^I register "([^"]*)" with a description of (\d+) characters$`, w.registerWithDescriptionOf)
	sc.Step(`^I record for "([^"]*)" a measurement of (\d+) x (\d+) x (\d+) mm and (\d+) g taken at "([^"]*)"$`, w.recordMeasurement)
	sc.Step(`^the service is draining for shutdown$`, w.draining)

	sc.Step(`^the response content type is "([^"]*)"$`, w.theContentTypeIs)
	sc.Step(`^the last published "([^"]*)" event data is '(.*)'$`, w.lastEventDataIs)
	sc.Step(`^the last outbox message is a CloudEvent on topic "([^"]*)" keyed "([^"]*)" about "([^"]*)" at "([^"]*)" with data schema "([^"]*)"$`, w.lastMessageIs)

	sc.Step(`^inventory-storage publishes a legacy classification of "([^"]*)" with tags "([^"]*)" and temperature class "([^"]*)"$`, w.legacyWithTemperature)
	sc.Step(`^inventory-storage redelivers legacy event number (\d+)$`, w.redeliverLegacyNumber)
}

func (w *world) iSend(method, path, body string) error { return w.call(method, path, body) }

func (w *world) registerWithDescriptionOf(sku string, n int) error {
	b, _ := json.Marshal(map[string]string{"description": strings.Repeat("a", n)})
	return w.call(http.MethodPut, "/products/"+sku, string(b))
}

func (w *world) recordMeasurement(sku string, l, wd, h, g int, at string) error {
	body := fmt.Sprintf(`{"lengthMm":%d,"widthMm":%d,"heightMm":%d,"weightG":%d,"measuredAt":%q}`, l, wd, h, g, at)
	return w.call(http.MethodPut, "/products/"+sku+"/dimensions/measured", body)
}

func (w *world) draining() error {
	w.readiness.SetNotReady()
	return nil
}

func (w *world) theContentTypeIs(want string) error {
	if !strings.HasPrefix(w.contentType, want) {
		return fmt.Errorf("content type = %q, want %q", w.contentType, want)
	}
	return nil
}

// lastEventDataIs compares the CloudEvents data of the newest outbox message
// of the named event with the expected JSON, ignoring key order.
func (w *world) lastEventDataIs(name, want string) error {
	msgs := w.outbox.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if !strings.HasSuffix(msgs[i].EventType, "."+name) {
			continue
		}
		e, err := cloudevents.Decode(msgs[i].Value)
		if err != nil {
			return err
		}
		return sameJSON(e.Data(), want)
	}
	return fmt.Errorf("no %s in the outbox", name)
}

func sameJSON(got []byte, want string) error {
	var g, x any
	if err := json.Unmarshal(got, &g); err != nil {
		return fmt.Errorf("data is not JSON: %s", got)
	}
	if err := json.Unmarshal([]byte(want), &x); err != nil {
		return fmt.Errorf("expected value is not JSON: %s", want)
	}
	if !reflect.DeepEqual(g, x) {
		return fmt.Errorf("data = %s, want %s", got, want)
	}
	return nil
}

// lastMessageIs checks the transport and CloudEvents envelope of the newest
// outbox message: topic, Kafka key, subject, time and data schema.
func (w *world) lastMessageIs(topic, key, subject, at, schema string) error {
	msgs := w.outbox.Messages()
	if len(msgs) == 0 {
		return fmt.Errorf("the outbox is empty")
	}
	last := msgs[len(msgs)-1]
	e, err := cloudevents.Decode(last.Value)
	if err != nil {
		return err
	}
	checks := []struct{ name, got, want string }{
		{"topic", last.Topic, topic},
		{"key", string(last.Key), key},
		{"specversion", e.SpecVersion(), "1.0"},
		{"source", e.Source(), "/warehouse/product-master"},
		{"subject", e.Subject(), subject},
		{"time", e.Time().UTC().Format(time.RFC3339), at},
		{"dataschema", e.DataSchema(), schema},
	}
	for _, c := range checks {
		if c.got != c.want {
			return fmt.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	return nil
}

// legacyWithTemperature publishes a legacy event that carries a temperature
// class, which the shared legacyEvent builder does not.
func (w *world) legacyWithTemperature(sku, tags, temperature string) error {
	w.legacySeq++
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(fmt.Sprintf("legacy-%d", w.legacySeq))
	e.SetSource("/warehouse/inventory-storage")
	e.SetType(inboundkafka.TypeLegacyProductClassified)
	e.SetSubject(sku)
	e.SetTime(bddNow)
	data := map[string]any{"sku": sku, "handling_tags": splitList(tags), "temperature_class": temperature}
	if err := e.SetData("application/json", data); err != nil {
		return err
	}
	value, err := json.Marshal(e)
	if err != nil {
		return err
	}
	w.lastLegacy = value
	w.legacyLog = append(w.legacyLog, value)
	return w.importer.HandleMessage(context.Background(), value)
}

func (w *world) redeliverLegacyNumber(n int) error {
	if n < 1 || n > len(w.legacyLog) {
		return fmt.Errorf("no legacy event number %d (%d published)", n, len(w.legacyLog))
	}
	return w.importer.HandleMessage(context.Background(), w.legacyLog[n-1])
}
