package product

import (
	"time"

	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

// Event is a domain event raised by the Product aggregate. Every event
// carries the SKU, the aggregate version AFTER the change, and when it
// happened (from the caller's clock).
type Event interface {
	EventName() string
	ProductSKU() SKU
	ProductVersion() int64
	OccurredAt() time.Time
}

// Header is embedded in every event.
type Header struct {
	SKU     SKU
	Version int64
	At      time.Time
}

// ProductSKU returns the SKU the event is about.
func (h Header) ProductSKU() SKU { return h.SKU }

// ProductVersion returns the aggregate version after the change.
func (h Header) ProductVersion() int64 { return h.Version }

// OccurredAt returns when the change happened.
func (h Header) OccurredAt() time.Time { return h.At }

// ProductRegistered is raised when a SKU is registered.
type ProductRegistered struct {
	Header
	Description string
}

// EventName returns "ProductRegistered".
func (ProductRegistered) EventName() string { return "ProductRegistered" }

// ProductDescriptionChanged is raised when a description changes.
type ProductDescriptionChanged struct {
	Header
	Description string
}

// EventName returns "ProductDescriptionChanged".
func (ProductDescriptionChanged) EventName() string { return "ProductDescriptionChanged" }

// ProductClassified is raised when a classification is set or replaced.
// It carries the full classification (full-state replacement).
type ProductClassified struct {
	Header
	Classification Classification
	Source         ClassificationSource
}

// EventName returns "ProductClassified".
func (ProductClassified) EventName() string { return "ProductClassified" }

// ProductDimensionsDeclared is raised when declared dimensions are set or
// replaced. It carries the full physical profile after the change.
type ProductDimensionsDeclared struct {
	Header
	Profile PhysicalProfile
}

// EventName returns "ProductDimensionsDeclared".
func (ProductDimensionsDeclared) EventName() string { return "ProductDimensionsDeclared" }

// ProductMeasured is raised when a measurement is recorded. It carries the
// full physical profile after the change.
type ProductMeasured struct {
	Header
	Profile PhysicalProfile
}

// EventName returns "ProductMeasured".
func (ProductMeasured) EventName() string { return "ProductMeasured" }

// ProductPackLevelDeclared is raised when a pack level is declared or one
// of its mutable attributes changes. It carries the full level after the
// change (ADR 0011).
type ProductPackLevelDeclared struct {
	Header
	Def PackLevelDef
}

// EventName returns "ProductPackLevelDeclared".
func (ProductPackLevelDeclared) EventName() string { return "ProductPackLevelDeclared" }

// ProductPackLevelRetired is raised when a pack level is end-dated, either
// by RetirePackLevel or because a new GTIN superseded it.
type ProductPackLevelRetired struct {
	Header
	Level       PackLevel
	GTIN        gs1.GTIN
	EffectiveTo time.Time
	Reason      string
}

// EventName returns "ProductPackLevelRetired".
func (ProductPackLevelRetired) EventName() string { return "ProductPackLevelRetired" }

// ProductPackLevelMeasured is raised when a pack level measurement is
// recorded.
type ProductPackLevelMeasured struct {
	Header
	Level       PackLevel
	GTIN        gs1.GTIN
	Measurement Measurement
}

// EventName returns "ProductPackLevelMeasured".
func (ProductPackLevelMeasured) EventName() string { return "ProductPackLevelMeasured" }
