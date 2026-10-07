package product

import (
	"errors"
	"time"
)

// ErrNotClassified is returned when a classification is asked of a product
// that has none.
var ErrNotClassified = errors.New("product classification not found")

// Product is the aggregate root: the master record of one SKU.
//
// Invariants: the SKU is valid and never changes; the description obeys
// its rules; the classification, when present, obeys the closed taxonomy
// (enforced by NewClassification) and has a source; the physical profile's
// parts obey their bounds; the latest measurement is never replaced by an
// older one; Version starts at 1 and increments by one per accepted change.
// A command that changes nothing returns no event and leaves Version as is.
type Product struct {
	sku            SKU
	description    string
	classification *Classification
	source         ClassificationSource
	profile        PhysicalProfile
	version        int64
}

// Register creates a product at version 1 and raises ProductRegistered.
func Register(sku SKU, description string, now time.Time) (*Product, []Event, error) {
	if _, err := NewSKU(string(sku)); err != nil {
		return nil, nil, err
	}
	if err := validateDescription(description); err != nil {
		return nil, nil, err
	}
	p := &Product{sku: sku, description: description, version: 1}
	return p, []Event{ProductRegistered{Header: p.header(now), Description: description}}, nil
}

// Rehydrate rebuilds a persisted product without re-running invariants.
// classification and source are nil/"" for an unclassified product.
func Rehydrate(sku SKU, description string, classification *Classification, source ClassificationSource, profile PhysicalProfile, version int64) *Product {
	return &Product{sku: sku, description: description, classification: classification, source: source, profile: profile, version: version}
}

// RehydrateClassification rebuilds a persisted classification without
// validation (used by repositories).
func RehydrateClassification(tags []HandlingTag, temperatureClass TemperatureClass, dotHazardClass DOTHazardClass) Classification {
	set := make(map[HandlingTag]struct{}, len(tags))
	for _, tag := range tags {
		set[tag] = struct{}{}
	}
	return Classification{tags: set, temperatureClass: temperatureClass, dotHazardClass: dotHazardClass}
}

// SKU returns the identity.
func (p *Product) SKU() SKU { return p.sku }

// Description returns the description.
func (p *Product) Description() string { return p.description }

// Version returns the current version.
func (p *Product) Version() int64 { return p.version }

// Classification returns the classification and its source, or
// ErrNotClassified.
func (p *Product) Classification() (Classification, ClassificationSource, error) {
	if p.classification == nil {
		return Classification{}, "", ErrNotClassified
	}
	return *p.classification, p.source, nil
}

// IsClassified reports whether the product has a classification.
func (p *Product) IsClassified() bool { return p.classification != nil }

// PhysicalProfile returns the physical profile.
func (p *Product) PhysicalProfile() PhysicalProfile { return p.profile }

// ChangeDescription replaces the description. Same text: no change.
func (p *Product) ChangeDescription(description string, now time.Time) ([]Event, error) {
	if err := validateDescription(description); err != nil {
		return nil, err
	}
	if description == p.description {
		return nil, nil
	}
	p.description = description
	p.bump()
	return []Event{ProductDescriptionChanged{Header: p.header(now), Description: description}}, nil
}

// Classify sets or replaces the classification as authored in this context
// (source native). The same classification with the same source is no
// change; confirming a legacy-imported classification makes it native and
// is a change.
func (p *Product) Classify(c Classification, now time.Time) ([]Event, error) {
	return p.setClassification(c, SourceNative, now), nil
}

// ImportLegacyClassification applies a classification imported from
// inventory-storage during the migration (ADR 0003). It never replaces a
// native classification and is no change when the classification is
// identical to the current one.
func (p *Product) ImportLegacyClassification(c Classification, now time.Time) []Event {
	if p.classification != nil && p.source == SourceNative {
		return nil
	}
	return p.setClassification(c, SourceLegacyImport, now)
}

func (p *Product) setClassification(c Classification, source ClassificationSource, now time.Time) []Event {
	if p.classification != nil && p.source == source && p.classification.Equal(c) {
		return nil
	}
	p.classification = &c
	p.source = source
	p.bump()
	return []Event{ProductClassified{Header: p.header(now), Classification: c, Source: source}}
}

// DeclareDimensions sets or replaces the declared dimensions. The same
// values are no change.
func (p *Product) DeclareDimensions(d UnitDimensions, now time.Time) []Event {
	if current, ok := p.profile.Declared(); ok && current == d {
		return nil
	}
	p.profile = p.profile.withDeclared(d)
	p.bump()
	return []Event{ProductDimensionsDeclared{Header: p.header(now), Profile: p.profile}}
}

// RecordMeasurement records the latest measurement. An older measurement
// than the current one is ErrStaleMeasurement; the identical measurement is
// no change; a different reading with the same measuredAt replaces it (a
// correction of that reading).
func (p *Product) RecordMeasurement(m Measurement, now time.Time) ([]Event, error) {
	if current, ok := p.profile.Measured(); ok {
		if m.measuredAt.Before(current.measuredAt) {
			return nil, ErrStaleMeasurement
		}
		if current.sameAs(m) {
			return nil, nil
		}
	}
	p.profile = p.profile.withMeasured(m)
	p.bump()
	return []Event{ProductMeasured{Header: p.header(now), Profile: p.profile}}, nil
}

func (p *Product) bump() { p.version++ }

func (p *Product) header(now time.Time) Header {
	return Header{SKU: p.sku, Version: p.version, At: now.UTC()}
}
