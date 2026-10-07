// Package product holds the Product aggregate: SKU-level product master
// data (ADR 0001). A Product is registered explicitly and then carries an
// optional handling Classification and a PhysicalProfile (ADR 0002). Every
// accepted change increments Version, which guards persistence and rides on
// every published event so downstream local copies can drop stale messages.
//
// The classification taxonomy and its invariants are inherited unchanged
// from inventory-storage ADR 0009/0010 (ADR 0003 moves ownership here).
// Bounded contexts never share Go code, so the concept is restated in this
// context's own types rather than imported.
package product

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxSKULength is the longest SKU this context accepts.
const MaxSKULength = 64

// MaxDescriptionLength is the longest description, in characters (runes).
const MaxDescriptionLength = 200

var (
	// ErrInvalidSKU is returned when a SKU is empty, longer than
	// MaxSKULength, or contains whitespace, a control character or '/'.
	ErrInvalidSKU = errors.New("sku must be 1..64 characters without whitespace, control characters or '/'")
	// ErrInvalidDescription is returned when a description is longer than
	// MaxDescriptionLength characters or contains a control character.
	ErrInvalidDescription = errors.New("description must be at most 200 characters without control characters")
)

// SKU identifies a product. It is the aggregate identity.
type SKU string

// NewSKU validates a SKU.
func NewSKU(value string) (SKU, error) {
	if value == "" || utf8.RuneCountInString(value) > MaxSKULength {
		return "", ErrInvalidSKU
	}
	for _, r := range value {
		if r == '/' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", ErrInvalidSKU
		}
	}
	return SKU(value), nil
}

// String returns the SKU text.
func (s SKU) String() string { return string(s) }

// validateDescription checks the description rules.
func validateDescription(description string) error {
	if utf8.RuneCountInString(description) > MaxDescriptionLength {
		return ErrInvalidDescription
	}
	if strings.IndexFunc(description, unicode.IsControl) >= 0 {
		return ErrInvalidDescription
	}
	return nil
}
