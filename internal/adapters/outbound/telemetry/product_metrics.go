package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/product-master/internal/application/ports"
)

// meterName scopes this service's own business instruments, keeping them
// distinct from the ones otelchi, otelpgx (future) and the runtime collector
// register on the same MeterProvider.
const meterName = "github.com/claudioed/product-master"

// productRegistrationCounterName is the Tier 2 business counter (ADR-0013):
// every call to RegisterProduct, by outcome. A rising description_changed
// share relative to created is routine catalogue maintenance; a flat zero
// for created on a service that should be onboarding new SKUs is the signal
// worth alerting on (feed stopped, importer stuck).
const productRegistrationCounterName = "product_master.products.registered"

// outcomeKey is the ADR-0013 attribute naming WHAT happened to the SKU
// being registered: one of the ports.ProductRegistrationOutcome* values.
const outcomeKey = attribute.Key("outcome")

// ProductMetrics implements ports.ProductMetrics against the global
// MeterProvider (a no-op until Setup installs a real one, so a counter
// built before Setup still records once it does).
type ProductMetrics struct {
	counter metric.Int64Counter
}

var _ ports.ProductMetrics = (*ProductMetrics)(nil)

// NewProductMetrics registers the counter. It only fails if the instrument
// name is invalid, a programming error.
func NewProductMetrics() (*ProductMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		productRegistrationCounterName,
		metric.WithDescription("Calls to RegisterProduct, by outcome (created, description_changed, unchanged). A proxy for SKU onboarding and catalogue-maintenance traffic."),
		metric.WithUnit("{product}"),
	)
	if err != nil {
		return nil, err
	}
	return &ProductMetrics{counter: counter}, nil
}

// ProductRegistration implements ports.ProductMetrics: it adds 1 to
// outcome's series.
func (m *ProductMetrics) ProductRegistration(ctx context.Context, outcome string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcome)))
}
