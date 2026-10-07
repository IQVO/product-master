package kafka

import (
	"context"
	"errors"
	"log/slog"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/product-master/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/product-master/internal/application/usecases"
)

// LegacyTopic is inventory-storage's integration topic.
const LegacyTopic = "warehouse.inventory.events"

// TypeLegacyProductClassified is the only type the importer acts on,
// byte-identical to inventory-storage's AsyncAPI. Every other type on the
// topic is ignored.
const TypeLegacyProductClassified = "com.warehouse.wms.inventory-storage.product.ProductClassified"

// legacyClassifiedData mirrors inventory-storage's v1 ProductClassified
// payload (restated here, never imported from that service).
type legacyClassifiedData struct {
	SKU              string   `json:"sku"`
	HandlingTags     []string `json:"handling_tags"`
	TemperatureClass string   `json:"temperature_class"`
	DOTHazardClass   *int     `json:"dot_hazard_class"`
}

// Importer is the use case the consumer drives
// (usecases.ImportLegacyClassification).
type Importer interface {
	Handle(ctx context.Context, eventID, sku string, tags []string, temperatureClass string, dotHazardClass int) (usecases.ImportOutcome, error)
}

// LegacyImporter consumes LegacyTopic under a stable consumer group and
// imports every legacy classification (ADR 0003).
type LegacyImporter struct {
	Reader Reader
	Import Importer
	Logger *slog.Logger
	Retry  RetryPolicy

	sleep sleepFunc // test hook; nil => real, ctx-cancellable sleep
}

// NewLegacyImporter constructs a LegacyImporter reading LegacyTopic from
// brokers under groupID (from LEGACY_IMPORT_CONSUMER_GROUP, never a
// literal). Constructing the reader does not dial.
func NewLegacyImporter(brokers []string, groupID string, importer Importer, logger *slog.Logger) *LegacyImporter {
	return NewLegacyImporterForTopic(brokers, LegacyTopic, groupID, importer, logger)
}

// NewLegacyImporterForTopic is NewLegacyImporter on an explicit topic, so an
// integration test can point the identical logic at a throwaway topic.
func NewLegacyImporterForTopic(brokers []string, topic, groupID string, importer Importer, logger *slog.Logger) *LegacyImporter {
	return &LegacyImporter{
		Reader: kafkago.NewReader(readerConfig(brokers, topic, groupID)),
		Import: importer,
		Logger: defaultLogger(logger),
	}
}

// Run consumes until ctx is cancelled or the reader fails. A message's
// offset is committed only after HandleMessage returned nil; a transient
// failure retries the SAME message with capped exponential backoff.
func (c *LegacyImporter) Run(ctx context.Context) error {
	loop := consumeLoop{
		reader: c.Reader,
		handle: func(ctx context.Context, msg kafkago.Message) error { return c.HandleMessage(ctx, msg.Value) },
		logger: defaultLogger(c.Logger),
		name:   "legacy classification importer",
		retry:  c.Retry,
		sleep:  c.sleep,
	}
	return loop.run(ctx)
}

// Close releases the reader.
func (c *LegacyImporter) Close() error { return c.Reader.Close() }

// HandleMessage decodes one message and imports it. It returns nil for
// everything deterministic (logged) and a non-nil error ONLY for a transient
// failure, after the unit of work rolled back.
func (c *LegacyImporter) HandleMessage(ctx context.Context, value []byte) error {
	logger := defaultLogger(c.Logger)
	e, err := cloudevents.Decode(value)
	if err != nil {
		logger.WarnContext(ctx, "skipping a message that is not a valid CloudEvent", "topic", LegacyTopic, "error", err)
		return nil
	}
	if e.Type() != TypeLegacyProductClassified {
		return nil
	}
	var data legacyClassifiedData
	if err := e.DataAs(&data); err != nil {
		logger.WarnContext(ctx, "skipping a malformed legacy ProductClassified payload", "event_id", e.ID(), "error", err)
		return nil
	}
	dot := 0
	if data.DOTHazardClass != nil {
		dot = *data.DOTHazardClass
	}
	outcome, err := c.Import.Handle(ctx, e.ID(), data.SKU, data.HandlingTags, data.TemperatureClass, dot)
	if errors.Is(err, usecases.ErrInvalidLegacyImport) {
		logger.WarnContext(ctx, "skipping an invalid legacy classification", "event_id", e.ID(), "sku", data.SKU, "error", err)
		return nil
	}
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "legacy classification processed", "event_id", e.ID(), "sku", data.SKU, "outcome", string(outcome))
	return nil
}
