package product

import (
	"errors"
	"fmt"

	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

// The packaging hierarchy of a product (ADR 0011): the pack levels a SKU is
// sold, shipped and received in, each identified by a GS1 GTIN. Eaches are
// the ledger unit everywhere; a pack level is a conversion plus an identity,
// never a second ledger.

// Bounds of a pack quantity. They keep every quantity inside a Postgres
// INTEGER and are plain literals on purpose (the mutation sensor reports
// constant arithmetic as uncovered).
const (
	// MaxPackQuantity bounds qtyOfChild and unitsPerPack.
	MaxPackQuantity = 1000000
	// MaxReasonChars bounds the reason of a retired pack level.
	MaxReasonChars = 200
	// ReasonSuperseded is the reason recorded when declaring a new GTIN at an
	// active level end-dates the old one.
	ReasonSuperseded = "superseded"
)

var (
	// ErrInvalidGTIN is returned for a GTIN of the wrong length or with a wrong mod-10 check digit.
	ErrInvalidGTIN = errors.New("GTIN must be 8, 12, 13 or 14 digits with a valid mod-10 check digit")
	// ErrGtinAlreadyAssigned is returned when a GTIN already identifies another (sku, level), or a
	// retired level of this product: a GTIN identifies exactly one (sku, level), fleet-wide, forever.
	ErrGtinAlreadyAssigned = errors.New("GTIN is already assigned to a pack level")
	// ErrPackQuantityImmutable is returned when a declaration would change the content quantity of an
	// ACTIVE pack level (GS1 immutability): a new quantity needs a new GTIN.
	ErrPackQuantityImmutable = errors.New("the content quantity of an active pack level cannot change; declare a new GTIN at that level")
	// ErrPackLevelOrder is returned when a level above EACH has no active child level on this product
	// that is lower than it.
	ErrPackLevelOrder = errors.New("a pack level needs an active child level that is lower than it")
	// ErrVariableMeasureUnsupported is returned for a GTIN-14 with indicator digit 9.
	ErrVariableMeasureUnsupported = errors.New("variable measure GTINs (indicator digit 9) are not supported")
	// ErrInvalidPackQuantity is returned for a qtyOfChild below 2 (non-EACH), a unitsPerPack that
	// disagrees with the chain, or a quantity over MaxPackQuantity.
	ErrInvalidPackQuantity = errors.New("invalid pack quantity")
	// ErrPackDimensionsBelowContents is returned when a pack level weighs less than its contents.
	ErrPackDimensionsBelowContents = errors.New("pack weight is below the weight of its contents")
	// ErrInvalidPackLevel is returned for a level other than EACH, INNER, CASE or PALLET.
	ErrInvalidPackLevel = errors.New("pack level must be EACH, INNER, CASE or PALLET")
	// ErrPackLevelNotFound is returned when the product has no (active) definition at a level.
	ErrPackLevelNotFound = errors.New("pack level not found")
	// ErrInvalidReason is returned for an empty, over-long or control-character reason.
	ErrInvalidReason = errors.New("reason must be 1..200 characters without control characters")
)

// PackLevel is a packaging level.
type PackLevel string

// The packaging levels, lowest first.
const (
	PackLevelEach   PackLevel = "EACH"
	PackLevelInner  PackLevel = "INNER"
	PackLevelCase   PackLevel = "CASE"
	PackLevelPallet PackLevel = "PALLET"
)

// rank orders the levels; 0 is not a level.
func (l PackLevel) rank() int {
	switch l {
	case PackLevelEach:
		return 1
	case PackLevelInner:
		return 2
	case PackLevelCase:
		return 3
	case PackLevelPallet:
		return 4
	default:
		return 0
	}
}

// ParsePackLevel accepts exactly EACH, INNER, CASE or PALLET.
func ParsePackLevel(s string) (PackLevel, error) {
	l := PackLevel(s)
	if l.rank() == 0 {
		return "", fmt.Errorf("%w: %q", ErrInvalidPackLevel, s)
	}
	return l, nil
}

// Below reports whether l is a lower level than other. An unknown level is
// below nothing and above nothing.
func (l PackLevel) Below(other PackLevel) bool {
	return l.rank() != 0 && other.rank() != 0 && l.rank() < other.rank()
}

// ParseGTIN validates a raw GTIN with this context's own value object
// (never trusted from a client): 8, 12, 13 or 14 digits with a valid mod-10
// check digit, normalised to 14 digits. An indicator digit 9 (variable
// measure) is ErrVariableMeasureUnsupported.
func ParseGTIN(raw string) (gs1.GTIN, error) {
	g, err := gs1.ParseGTIN(raw)
	if err != nil {
		return gs1.GTIN{}, fmt.Errorf("%w: %w", ErrInvalidGTIN, err)
	}
	if err := rejectVariableMeasure(g); err != nil {
		return gs1.GTIN{}, err
	}
	return g, nil
}

func rejectVariableMeasure(g gs1.GTIN) error {
	if gs1.RejectVariableMeasure(g) != nil {
		return fmt.Errorf("%w: %s", ErrVariableMeasureUnsupported, g)
	}
	return nil
}
