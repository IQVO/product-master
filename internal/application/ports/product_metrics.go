package ports

import "context"

// Outcomes RegisterProduct reports to ProductMetrics, one per Handle call
// (ADR-0013 Tier 2).
const (
	// ProductRegistrationOutcomeCreated: the SKU was unknown and is now
	// registered at version 1 (ProductRegistered raised).
	ProductRegistrationOutcomeCreated = "created"
	// ProductRegistrationOutcomeDescriptionChanged: the SKU already existed
	// and its description was replaced (ProductDescriptionChanged raised).
	ProductRegistrationOutcomeDescriptionChanged = "description_changed"
	// ProductRegistrationOutcomeUnchanged: the SKU already existed and the
	// submitted description was identical, so no event was raised and
	// Version did not move (hard rule 4).
	ProductRegistrationOutcomeUnchanged = "unchanged"
)

// ProductMetrics is the outbound telemetry port for the business signal
// RegisterProduct produces (ADR-0013 Tier 2): a SKU accepted into (or
// already present in) the product master, by outcome. The OTel
// implementation lives in adapters/outbound/telemetry; the application
// layer never imports OTel. Use cases treat a nil ProductMetrics as
// "record nothing", so wiring it is optional.
type ProductMetrics interface {
	// ProductRegistration adds 1 to outcome's series (one of the
	// ProductRegistrationOutcome* constants).
	ProductRegistration(ctx context.Context, outcome string)
}
