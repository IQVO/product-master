package kafka

import (
	"github.com/google/uuid"

	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/domain/product"
)

// AnalyticsTopic is this service's analytics topic: the data product's own
// stream, consumed only by cmd/product-projector (ADR 0006). Its DLQ is
// AnalyticsTopic + ".dlq".
const AnalyticsTopic = "warehouse.product-master.analytics"

// AnalyticsEncoder encodes the same five Product events as Encoder onto
// AnalyticsTopic, with dataschema
// urn:warehouse:product-master:analytics:<EventName>:v1. The payloads equal
// the integration payloads (ADR 0006 section 2): payloadFor is shared, so an
// event published on one stream can never be missing from the other. Alone it
// mints its own ids (golden tests); production uses FanoutEncoder so both
// messages of one occurrence share one id.
type AnalyticsEncoder struct {
	// NewID mints a CloudEvents id (uuid.NewString when nil).
	NewID func() string
	// Topic overrides AnalyticsTopic (integration tests use a unique one).
	Topic string
}

var _ ports.EventEncoder = (*AnalyticsEncoder)(nil)

// Encode encodes events in order, each under a freshly minted id.
func (e *AnalyticsEncoder) Encode(events ...product.Event) ([]outbox.Message, error) {
	newID := e.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	out := make([]outbox.Message, 0, len(events))
	for _, ev := range events {
		msg, err := e.encodeOne(ev, newID())
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

func (e *AnalyticsEncoder) encodeOne(ev product.Event, id string) (outbox.Message, error) {
	data, err := payloadFor(ev)
	if err != nil {
		return outbox.Message{}, err
	}
	topic := e.Topic
	if topic == "" {
		topic = AnalyticsTopic
	}
	return buildMessage(ev, id, topic, cloudevents.StreamAnalytics, data)
}

// FanoutEncoder is the encoder the use cases get: every domain event becomes
// TWO outbox messages, the integration one first and the analytics one
// second, carrying the SAME CloudEvents id (minted once here, persisted with
// both rows) and the same `type`. Both rows are inserted by the use case's
// single UnitOfWork, so they commit or roll back together with the product;
// a relay retry republishes each row's persisted bytes. outbox_events'
// identity is (event_id, topic), so the shared id is legal.
type FanoutEncoder struct {
	Integration *Encoder
	Analytics   *AnalyticsEncoder
	// NewID mints the shared CloudEvents id (uuid.NewString when nil).
	NewID func() string
}

// NewFanoutEncoder returns a FanoutEncoder over the production topics.
func NewFanoutEncoder() *FanoutEncoder {
	return &FanoutEncoder{Integration: &Encoder{}, Analytics: &AnalyticsEncoder{}, NewID: uuid.NewString}
}

var _ ports.EventEncoder = (*FanoutEncoder)(nil)

// Encode encodes events in order: integration message then analytics
// message per event. It fails, and returns no messages, on an event type the
// streams do not publish.
func (f *FanoutEncoder) Encode(events ...product.Event) ([]outbox.Message, error) {
	newID := f.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	integration, analytics := f.Integration, f.Analytics
	if integration == nil {
		integration = &Encoder{}
	}
	if analytics == nil {
		analytics = &AnalyticsEncoder{}
	}
	out := make([]outbox.Message, 0, 2*len(events))
	for _, ev := range events {
		id := newID()
		in, err := integration.encodeOne(ev, id)
		if err != nil {
			return nil, err
		}
		an, err := analytics.encodeOne(ev, id)
		if err != nil {
			return nil, err
		}
		out = append(out, in, an)
	}
	return out, nil
}
