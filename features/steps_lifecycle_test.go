package features_test

// Step definitions of lifecycle_doc_rules.feature: the envelope of every event
// type, payload fields that are omitted when empty, and the operations the
// service deliberately does not offer.

import (
	"encoding/json"
	"fmt"
	"net/http"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/cucumber/godog"

	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
)

func registerLifecycleSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I send DELETE \"([^\"]*)\"$`, func(path string) error { return w.call(http.MethodDelete, path, "") })
	sc.Step(`^the response status is a client error$`, w.theResponseIsAClientError)
	sc.Step(`^the last outbox message is a \"([^\"]*)\" CloudEvent about \"([^\"]*)\" with data schema \"([^\"]*)\"$`, w.lastMessageTypeIs)
	sc.Step(`^the last outbox message data has no field \"([^\"]*)\"$`, w.lastMessageDataHasNo)
}

func (w *world) theResponseIsAClientError() error {
	if w.status < 400 || w.status > 499 {
		return fmt.Errorf("status %d, want a 4xx (body %s)", w.status, w.body)
	}
	return nil
}

func (w *world) lastMessageEvent() (ce.Event, error) {
	msgs := w.outbox.Messages()
	if len(msgs) == 0 {
		return ce.Event{}, fmt.Errorf("the outbox is empty")
	}
	return cloudevents.Decode(msgs[len(msgs)-1].Value)
}

func (w *world) lastMessageTypeIs(name, subject, schema string) error {
	e, err := w.lastMessageEvent()
	if err != nil {
		return err
	}
	checks := []struct{ field, got, want string }{
		{"type", e.Type(), "com.warehouse.wms.product-master.product." + name},
		{"subject", e.Subject(), subject},
		{"dataschema", e.DataSchema(), schema},
	}
	for _, c := range checks {
		if c.got != c.want {
			return fmt.Errorf("%s = %q, want %q", c.field, c.got, c.want)
		}
	}
	return nil
}

func (w *world) lastMessageDataHasNo(field string) error {
	e, err := w.lastMessageEvent()
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(e.Data(), &data); err != nil {
		return err
	}
	if v, ok := data[field]; ok {
		return fmt.Errorf("data has %q = %v, want it omitted: %s", field, v, e.Data())
	}
	return nil
}
