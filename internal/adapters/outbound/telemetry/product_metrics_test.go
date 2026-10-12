package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/product-master/internal/application/ports"
)

// collectCounts runs reader.Collect and returns, for the single instrument
// named name, outcome -> count plus its unit and description.
func collectCounts(t *testing.T, reader *metric.ManualReader, name string) (map[string]int64, string, string) {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	counts := map[string]int64{}
	var unit, description string
	for _, scope := range collected.ScopeMetrics {
		for _, met := range scope.Metrics {
			if met.Name != name {
				continue
			}
			unit, description = met.Unit, met.Description
			sum, ok := met.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is a %T, want an int64 Sum", met.Name, met.Data)
			}
			for _, point := range sum.DataPoints {
				outcome, ok := point.Attributes.Value(outcomeKey)
				if !ok {
					t.Fatal("data point has no outcome attribute")
				}
				counts[outcome.AsString()] = point.Value
			}
		}
	}
	return counts, unit, description
}

// ADR-0013: the Tier 2 product-registration counter is recorded by the
// outbound telemetry adapter behind ports.ProductMetrics, carries the
// dot-separated "outcome" attribute, and is scoped {product}.
func TestProductMetrics_CountsRegistrationsByOutcome(t *testing.T) {
	reader := metric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	m, err := NewProductMetrics()
	if err != nil {
		t.Fatalf("NewProductMetrics: %v", err)
	}

	ctx := context.Background()
	m.ProductRegistration(ctx, ports.ProductRegistrationOutcomeCreated)
	m.ProductRegistration(ctx, ports.ProductRegistrationOutcomeCreated)
	m.ProductRegistration(ctx, ports.ProductRegistrationOutcomeDescriptionChanged)
	m.ProductRegistration(ctx, ports.ProductRegistrationOutcomeUnchanged)
	m.ProductRegistration(ctx, ports.ProductRegistrationOutcomeUnchanged)
	m.ProductRegistration(ctx, ports.ProductRegistrationOutcomeUnchanged)

	counts, unit, description := collectCounts(t, reader, productRegistrationCounterName)
	want := map[string]int64{
		ports.ProductRegistrationOutcomeCreated:            2,
		ports.ProductRegistrationOutcomeDescriptionChanged: 1,
		ports.ProductRegistrationOutcomeUnchanged:          3,
	}
	for outcome, n := range want {
		if counts[outcome] != n {
			t.Fatalf("counts = %v, want %v", counts, want)
		}
	}
	if unit != "{product}" {
		t.Fatalf("unit = %q, want {product}", unit)
	}
	if description == "" {
		t.Fatal("instrument has no description")
	}
}

// NewProductMetrics must satisfy ports.ProductMetrics so the use case never
// needs to see the OTel type.
func TestNewProductMetrics_ImplementsPort(t *testing.T) {
	m, err := NewProductMetrics()
	if err != nil {
		t.Fatalf("NewProductMetrics: %v", err)
	}
	var _ ports.ProductMetrics = m
}
