// Package report is the self-contained read-model region of the analytics
// read side (ADR 0006): the shape of the master data quality report, the
// parameter rules (date range, day windows), the pure ratio and freshness
// logic, the validation of a projectable event, and the writer/reader ports
// the analytical store implements. It imports no other internal package, so
// the OLTP domain can never leak into the projection and the arch test keeps
// this region isolated.
package report

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Kind is which of the five published Product events a fact came from. The
// names are the CloudEvents event names (the last segment of `type`).
type Kind string

// The five Product events the analytics topic carries.
const (
	KindRegistered         Kind = "ProductRegistered"
	KindDescriptionChanged Kind = "ProductDescriptionChanged"
	KindClassified         Kind = "ProductClassified"
	KindDimensionsDeclared Kind = "ProductDimensionsDeclared"
	KindMeasured           Kind = "ProductMeasured"
)

// Kinds returns every projected kind, in catalogue order.
func Kinds() []Kind {
	return []Kind{KindRegistered, KindDescriptionChanged, KindClassified, KindDimensionsDeclared, KindMeasured}
}

// IsProfile reports whether events of kind k carry the full physical profile.
func (k Kind) IsProfile() bool {
	return k == KindDimensionsDeclared || k == KindMeasured
}

// ProfileState is the part of a physical profile the report reads: whether
// declared and measured dimensions are present and whether they disagree.
type ProfileState struct {
	HasDeclared bool
	HasMeasured bool
	Discrepancy bool
}

// ProductEvent is one decoded analytics event ready to project.
type ProductEvent struct {
	Kind    Kind
	EventID string    // CloudEvents id: the idempotency key
	At      time.Time // CloudEvents time: when the event occurred
	SKU     string
	Version int64 // the aggregate version after the change
	// Profile is set exactly for the two profile kinds.
	Profile *ProfileState
}

// ErrInvalidEvent marks an event that can never be projected (the consumer
// dead-letters it).
var ErrInvalidEvent = errors.New("report: event cannot be projected")

// Validate checks the invariants every store relies on. Every failure wraps
// ErrInvalidEvent and is deterministic.
func (e ProductEvent) Validate() error {
	switch {
	case !e.Kind.known():
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidEvent, e.Kind)
	case e.EventID == "":
		return fmt.Errorf("%w: %s without an id", ErrInvalidEvent, e.Kind)
	case e.SKU == "":
		return fmt.Errorf("%w: %s %s without a sku", ErrInvalidEvent, e.Kind, e.EventID)
	case e.Version < 1:
		return fmt.Errorf("%w: %s %s version %d is below 1", ErrInvalidEvent, e.Kind, e.EventID, e.Version)
	case e.At.IsZero():
		return fmt.Errorf("%w: %s %s without a time", ErrInvalidEvent, e.Kind, e.EventID)
	}
	return e.validateProfile()
}

func (e ProductEvent) validateProfile() error {
	if !e.Kind.IsProfile() {
		if e.Profile != nil {
			return fmt.Errorf("%w: %s %s carries a physical profile", ErrInvalidEvent, e.Kind, e.EventID)
		}
		return nil
	}
	p := e.Profile
	switch {
	case p == nil:
		return fmt.Errorf("%w: %s %s without a physical profile", ErrInvalidEvent, e.Kind, e.EventID)
	case e.Kind == KindDimensionsDeclared && !p.HasDeclared:
		return fmt.Errorf("%w: %s %s without declared dimensions", ErrInvalidEvent, e.Kind, e.EventID)
	case e.Kind == KindMeasured && !p.HasMeasured:
		return fmt.Errorf("%w: %s %s without a measurement", ErrInvalidEvent, e.Kind, e.EventID)
	case p.Discrepancy && (!p.HasDeclared || !p.HasMeasured):
		return fmt.Errorf("%w: %s %s has a discrepancy without both declared and measured dimensions", ErrInvalidEvent, e.Kind, e.EventID)
	}
	return nil
}

func (k Kind) known() bool {
	for _, c := range Kinds() {
		if k == c {
			return true
		}
	}
	return false
}

// Firsts is the "first X happened at" candidate this event offers for each
// of the four milestones (nil where it offers none). A store keeps the
// earliest candidate per SKU, so arrival order never matters.
type Firsts struct {
	Registered         *time.Time
	Classified         *time.Time
	DimensionsDeclared *time.Time
	Measured           *time.Time
}

// FirstsOf returns the milestone this event marks: a ProductRegistered marks
// registration, a ProductClassified the first classification, and so on. A
// ProductDescriptionChanged marks none (it only makes the SKU known).
func FirstsOf(e ProductEvent) Firsts {
	at := e.At.UTC()
	var f Firsts
	switch e.Kind {
	case KindRegistered:
		f.Registered = &at
	case KindClassified:
		f.Classified = &at
	case KindDimensionsDeclared:
		f.DimensionsDeclared = &at
	case KindMeasured:
		f.Measured = &at
	case KindDescriptionChanged:
		// existence only
	}
	return f
}

// Projection is the WRITER port: Apply records the event id and folds the
// event into the model in ONE transaction. applied is false when the id was
// already recorded (a replay): nothing changed. An error wrapping
// ErrRejected is deterministic (the store can never accept this event); any
// other error is transient and the same event may be retried.
type Projection interface {
	Apply(ctx context.Context, e ProductEvent) (applied bool, err error)
}

// ErrRejected marks an event the analytical store deterministically refuses
// (a data-exception or integrity-violation class error).
var ErrRejected = errors.New("report: event rejected by the analytical store")

// Reader is the READER port.
type Reader interface {
	// QualityDays returns one row per DayWindow of r, in day order, zeros
	// included.
	QualityDays(ctx context.Context, r Range) ([]QualityDay, error)
	// Coverage counts the current state of every SKU the projection knows.
	Coverage(ctx context.Context) (CoverageCounts, error)
	// LastEventAt is the CloudEvents time of the newest event the projection
	// has applied (nil while it has applied none): the freshness basis.
	LastEventAt(ctx context.Context) (*time.Time, error)
}

// QualityDay is one UTC day of the master data quality report, counted over
// the day's window (the part of the day inside the requested range).
type QualityDay struct {
	// Day is the UTC calendar day, at midnight UTC.
	Day time.Time
	// Registered, Classified, DimensionsDeclared and Measured count the SKUs
	// whose FIRST such event fell in the window.
	Registered         int
	Classified         int
	DimensionsDeclared int
	Measured           int
	// OpenDiscrepancies counts the SKUs whose latest physical-profile state
	// at the window's end disagrees (declared vs measured).
	OpenDiscrepancies int
}

// CoverageCounts is the current state of the catalogue the projection knows.
type CoverageCounts struct {
	Products           int // distinct SKUs seen in any event
	Classified         int
	DimensionsDeclared int // current profile has declared dimensions
	Measured           int // current profile has a measurement
	OpenDiscrepancies  int // current profile disagrees
}
