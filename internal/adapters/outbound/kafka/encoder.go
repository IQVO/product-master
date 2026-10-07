// Package kafka is the outbound Kafka adapter: it encodes Product domain
// events into CloudEvents 1.0 outbox messages (Encoder) and writes drained
// outbox rows to the broker (RelaySink). Envelopes are built ONLY through
// internal/adapters/kafka/cloudevents. Payloads are exactly the ones pinned
// in apis/asyncapi.yaml.
package kafka

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/domain/product"
)

// Topic is this service's integration-event topic.
const Topic = "warehouse.product-master.events"

// Entity is the `<entity>` segment of every published type:
// com.warehouse.wms.product-master.product.<EventName>.
const Entity = "product"

// schemaVersion is the dataschema version of every payload below
// (urn:warehouse:product-master:events:<EventName>:v1).
const schemaVersion = 1

// Wire payloads (CloudEvents `data`, snake_case JSON). Optional fields are
// omitted when unset.

type descriptionData struct {
	SKU         string `json:"sku"`
	Description string `json:"description"`
	Version     int64  `json:"version"`
}

type classifiedData struct {
	SKU                  string   `json:"sku"`
	HandlingTags         []string `json:"handling_tags"`
	TemperatureClass     string   `json:"temperature_class,omitempty"`
	DOTHazardClass       int      `json:"dot_hazard_class,omitempty"`
	ClassificationSource string   `json:"classification_source"`
	Version              int64    `json:"version"`
}

type dimensionsData struct {
	LengthMm  int64 `json:"length_mm"`
	WidthMm   int64 `json:"width_mm"`
	HeightMm  int64 `json:"height_mm"`
	WeightG   int64 `json:"weight_g"`
	VolumeMm3 int64 `json:"volume_mm3"`
}

type measuredData struct {
	dimensionsData
	MeasuredAt string `json:"measured_at"`
	DeviceID   string `json:"device_id,omitempty"`
}

type physicalProfileData struct {
	SKU             string          `json:"sku"`
	Declared        *dimensionsData `json:"declared,omitempty"`
	Measured        *measuredData   `json:"measured,omitempty"`
	Effective       *dimensionsData `json:"effective,omitempty"`
	EffectiveSource string          `json:"effective_source"`
	Discrepancy     bool            `json:"discrepancy"`
	Version         int64           `json:"version"`
}

// Encoder implements ports.EventEncoder: each domain event becomes one
// outbox.Message holding the CloudEvents bytes, the Kafka key (the SKU) and
// the content-type header. The CloudEvents `id` is minted HERE, once, and
// persisted with the outbox row, so a relay retry republishes the same id.
type Encoder struct {
	// NewID mints a CloudEvents id; uuid.NewString when nil.
	NewID func() string
	// Topic overrides the destination topic (Topic when empty).
	Topic string
}

// NewEncoder returns an Encoder minting random UUID v4 ids.
func NewEncoder() *Encoder { return &Encoder{NewID: uuid.NewString} }

var _ ports.EventEncoder = (*Encoder)(nil)

// Encode encodes events in order. An event type it does not publish is a
// programming error and fails the whole call.
func (e *Encoder) Encode(events ...product.Event) ([]outbox.Message, error) {
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

func (e *Encoder) encodeOne(ev product.Event, id string) (outbox.Message, error) {
	data, err := payloadFor(ev)
	if err != nil {
		return outbox.Message{}, err
	}
	topic := e.Topic
	if topic == "" {
		topic = Topic
	}
	return buildMessage(ev, id, topic, cloudevents.StreamEvents, data)
}

// buildMessage wraps data in the CloudEvents envelope of ev on stream
// (cloudevents.StreamEvents or StreamAnalytics, which only changes the
// dataschema) and returns the outbox row for topic. Both encoders share it, so
// the two messages of one occurrence differ only in topic and dataschema.
func buildMessage(ev product.Event, id, topic, stream string, data any) (outbox.Message, error) {
	sku := string(ev.ProductSKU())
	value, err := cloudevents.New(cloudevents.Spec{
		ID:        id,
		Entity:    Entity,
		EventName: ev.EventName(),
		Subject:   sku,
		Time:      ev.OccurredAt(),
		Stream:    stream,
		Version:   schemaVersion,
		Data:      data,
	})
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode %s: %w", ev.EventName(), err)
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID:    id,
		Topic:      topic,
		EventType:  cloudevents.Type(Entity, ev.EventName()),
		Subject:    sku,
		Key:        []byte(sku),
		DataSchema: cloudevents.DataSchema(stream, ev.EventName(), schemaVersion),
		Value:      value,
		Headers:    []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

func payloadFor(ev product.Event) (any, error) {
	switch e := ev.(type) {
	case product.ProductRegistered:
		return descriptionData{SKU: string(e.SKU), Description: e.Description, Version: e.Version}, nil
	case product.ProductDescriptionChanged:
		return descriptionData{SKU: string(e.SKU), Description: e.Description, Version: e.Version}, nil
	case product.ProductClassified:
		return classified(e), nil
	case product.ProductDimensionsDeclared:
		return profile(e.Header, e.Profile), nil
	case product.ProductMeasured:
		return profile(e.Header, e.Profile), nil
	default:
		return nil, fmt.Errorf("kafka encoder: %s is not a published event", ev.EventName())
	}
}

func classified(e product.ProductClassified) classifiedData {
	tags := e.Classification.Tags()
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = string(t)
	}
	return classifiedData{
		SKU:                  string(e.SKU),
		HandlingTags:         names,
		TemperatureClass:     string(e.Classification.TemperatureClass()),
		DOTHazardClass:       int(e.Classification.DOTHazardClass()),
		ClassificationSource: string(e.Source),
		Version:              e.Version,
	}
}

func dimensions(d product.UnitDimensions) *dimensionsData {
	return &dimensionsData{LengthMm: d.LengthMm(), WidthMm: d.WidthMm(), HeightMm: d.HeightMm(), WeightG: d.WeightG(), VolumeMm3: d.VolumeMm3()}
}

func profile(h product.Header, p product.PhysicalProfile) physicalProfileData {
	out := physicalProfileData{
		SKU:             string(h.SKU),
		EffectiveSource: string(p.EffectiveSource()),
		Discrepancy:     p.Discrepancy(),
		Version:         h.Version,
	}
	if d, ok := p.Declared(); ok {
		out.Declared = dimensions(d)
	}
	if m, ok := p.Measured(); ok {
		out.Measured = &measuredData{
			dimensionsData: *dimensions(m.Dimensions()),
			MeasuredAt:     m.MeasuredAt().UTC().Format(time.RFC3339Nano),
			DeviceID:       m.DeviceID(),
		}
	}
	if d, ok := p.Effective(); ok {
		out.Effective = dimensions(d)
	}
	return out
}
