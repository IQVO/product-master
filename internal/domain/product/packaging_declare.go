package product

import (
	"fmt"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

// PackLevels returns the product's packaging hierarchy.
func (p *Product) PackLevels() PackagingHierarchy { return p.packaging }

// DeclarePackLevel declares a pack level (ADR 0011).
//
//   - A GTIN already on this product is only re-declarable at its own ACTIVE
//     level, and then only with the same content quantity (ErrPackQuantityImmutable
//     otherwise); the flags and declared dimensions may change in place, the
//     identical declaration is no change.
//   - A GTIN that identifies another level, or a retired level, is
//     ErrGtinAlreadyAssigned. Uniqueness across SKUs is the use case's and the
//     repository's check, because it needs the other aggregates.
//   - A new GTIN at a level that is active end-dates the old level
//     (reason "superseded") and declares the new one: two events, two versions.
func (p *Product) DeclarePackLevel(s PackLevelSpec, now time.Time) ([]Event, error) {
	if err := p.checkSpecShape(s); err != nil {
		return nil, err
	}
	if i := p.packaging.indexOf(s.GTIN); i >= 0 {
		return p.redeclare(i, s, now)
	}
	return p.declareNew(s, now)
}

// checkSpecShape runs the checks that need no other level.
func (p *Product) checkSpecShape(s PackLevelSpec) error {
	if s.Level.rank() == 0 {
		return fmt.Errorf("%w: %q", ErrInvalidPackLevel, string(s.Level))
	}
	if s.GTIN.IsZero() {
		return fmt.Errorf("%w: a GTIN is required", ErrInvalidGTIN)
	}
	if err := rejectVariableMeasure(s.GTIN); err != nil {
		return err
	}
	if s.UnitsPerPack < 0 || s.UnitsPerPack > MaxPackQuantity {
		return fmt.Errorf("%w: unitsPerPack must be 1..%d", ErrInvalidPackQuantity, MaxPackQuantity)
	}
	if s.Level == PackLevelEach {
		if s.QtyOfChild != 0 || !s.ChildGTIN.IsZero() || (s.UnitsPerPack != 0 && s.UnitsPerPack != 1) {
			return fmt.Errorf("%w: an EACH level has no child and exactly 1 unit per pack", ErrInvalidPackQuantity)
		}
		return nil
	}
	if s.QtyOfChild < 2 || s.QtyOfChild > MaxPackQuantity {
		return fmt.Errorf("%w: qtyOfChild must be 2..%d for a %s", ErrInvalidPackQuantity, MaxPackQuantity, s.Level)
	}
	if s.ChildGTIN.IsZero() {
		return fmt.Errorf("%w: a %s needs the GTIN of its child level", ErrPackLevelOrder, s.Level)
	}
	return nil
}

// redeclare handles a GTIN this product already holds at defs[i].
func (p *Product) redeclare(i int, s PackLevelSpec, now time.Time) ([]Event, error) {
	cur := p.packaging.defs[i]
	if cur.level != s.Level || !cur.Active() {
		return nil, fmt.Errorf("%w: %s is %s level %s of this product", ErrGtinAlreadyAssigned, s.GTIN, activity(cur), cur.level)
	}
	if cur.childGTIN != s.ChildGTIN || cur.qtyOfChild != s.QtyOfChild || (s.UnitsPerPack != 0 && s.UnitsPerPack != cur.unitsPerPack) {
		return nil, fmt.Errorf("%w: %s at level %s", ErrPackQuantityImmutable, s.GTIN, s.Level)
	}
	next := cur
	next.declared = copyDims(s.Dims)
	next.isOrderable, next.isDespatchUnit = s.IsOrderable, s.IsDespatchUnit
	if next.sameAttributes(cur) {
		return nil, nil
	}
	if s.Dims != nil {
		if err := p.checkContents(cur.childGTIN, cur.qtyOfChild, s.Dims.weightG); err != nil {
			return nil, err
		}
	}
	p.packaging.defs[i] = next
	p.bump()
	return []Event{ProductPackLevelDeclared{Header: p.header(now), Def: next}}, nil
}

func activity(d PackLevelDef) string {
	if d.Active() {
		return "the active"
	}
	return "a retired"
}

// unitsForNew checks a new def against its child level and answers its
// units per pack: 1 for EACH, else qtyOfChild times the child's factor.
func (p *Product) unitsForNew(s PackLevelSpec) (int, error) {
	if s.Level == PackLevelEach {
		return 1, nil
	}
	child, ok := p.packaging.ByGTIN(s.ChildGTIN)
	if !ok || !child.Active() || !child.level.Below(s.Level) {
		return 0, fmt.Errorf("%w: child %s is not an active level of this product below %s", ErrPackLevelOrder, s.ChildGTIN, s.Level)
	}
	units := s.QtyOfChild * child.unitsPerPack
	if units > MaxPackQuantity || (s.UnitsPerPack != 0 && s.UnitsPerPack != units) {
		return 0, fmt.Errorf("%w: %d x %d eaches is %d units per pack, not %d (max %d)", ErrInvalidPackQuantity, s.QtyOfChild, child.unitsPerPack, units, s.UnitsPerPack, MaxPackQuantity)
	}
	if s.Dims != nil {
		if err := p.checkContents(s.ChildGTIN, s.QtyOfChild, s.Dims.weightG); err != nil {
			return 0, err
		}
	}
	return units, nil
}

// declareNew adds a def for a GTIN the product has never held.
func (p *Product) declareNew(s PackLevelSpec, now time.Time) ([]Event, error) {
	units, err := p.unitsForNew(s)
	if err != nil {
		return nil, err
	}
	def := PackLevelDef{
		level: s.Level, gtin: s.GTIN, childGTIN: s.ChildGTIN, qtyOfChild: s.QtyOfChild, unitsPerPack: units,
		declared: copyDims(s.Dims), isOrderable: s.IsOrderable, isDespatchUnit: s.IsDespatchUnit, effectiveFrom: normaliseTime(now),
	}
	var events []Event
	if old := p.packaging.indexActive(s.Level); old >= 0 {
		events = append(events, p.endDate(old, ReasonSuperseded, now))
	}
	p.packaging.defs = append(p.packaging.defs, def)
	p.bump()
	return append(events, ProductPackLevelDeclared{Header: p.header(now), Def: def}), nil
}

// endDate closes defs[i] at now and returns the event.
func (p *Product) endDate(i int, reason string, now time.Time) Event {
	to := normaliseTime(now)
	p.packaging.defs[i].effectiveTo = &to
	p.bump()
	return ProductPackLevelRetired{Header: p.header(now), Level: p.packaging.defs[i].level, GTIN: p.packaging.defs[i].gtin, EffectiveTo: to, Reason: reason}
}

// checkContents rejects weight when it is below qty times the known weight
// of the child level (measured wins over declared; a missing child weight
// means "not known": no check).
func (p *Product) checkContents(childGTIN gs1.GTIN, qty int, weightG int64) error {
	child, ok := p.packaging.ByGTIN(childGTIN)
	if !ok {
		return nil
	}
	childWeight, known := p.knownWeight(child)
	if !known {
		return nil
	}
	if need := int64(qty) * childWeight; weightG < need {
		return fmt.Errorf("%w: %d g is below %d x %d g = %d g", ErrPackDimensionsBelowContents, weightG, qty, childWeight, need)
	}
	return nil
}

// knownWeight is the child's effective weight; an EACH level without
// dimensions of its own falls back to the product's physical profile.
func (p *Product) knownWeight(d PackLevelDef) (int64, bool) {
	if dims, ok := d.EffectiveDimensions(); ok {
		return dims.weightG, true
	}
	if d.level == PackLevelEach {
		if dims, ok := p.profile.Effective(); ok {
			return dims.weightG, true
		}
	}
	return 0, false
}

// RetirePackLevel end-dates the active definition at level. The definition is
// kept (effectiveTo), never deleted. A level with no active definition is
// no change when it has history, ErrPackLevelNotFound when it never had any.
func (p *Product) RetirePackLevel(level PackLevel, reason string, now time.Time) ([]Event, error) {
	if level.rank() == 0 {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPackLevel, string(level))
	}
	if err := validateReason(reason); err != nil {
		return nil, err
	}
	i := p.packaging.indexActive(level)
	if i < 0 {
		if p.packaging.hasLevel(level) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: %s", ErrPackLevelNotFound, level)
	}
	return []Event{p.endDate(i, reason, now)}, nil
}

func validateReason(reason string) error {
	if reason == "" || utf8.RuneCountInString(reason) > MaxReasonChars {
		return ErrInvalidReason
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return ErrInvalidReason
		}
	}
	return nil
}

// RecordPackLevelMeasurement records the latest measurement of the active
// level (ADR 0002 declared-versus-measured, per level). An older reading
// than the current one is ErrStaleMeasurement, the identical reading is no
// change, and a measured weight below the contents is
// ErrPackDimensionsBelowContents.
func (p *Product) RecordPackLevelMeasurement(level PackLevel, m Measurement, now time.Time) ([]Event, error) {
	if level.rank() == 0 {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPackLevel, string(level))
	}
	i := p.packaging.indexActive(level)
	if i < 0 {
		return nil, fmt.Errorf("%w: no active %s level", ErrPackLevelNotFound, level)
	}
	cur := p.packaging.defs[i]
	if cur.measured != nil {
		if m.measuredAt.Before(cur.measured.measuredAt) {
			return nil, ErrStaleMeasurement
		}
		if cur.measured.sameAs(m) {
			return nil, nil
		}
	}
	if err := p.checkContents(cur.childGTIN, cur.qtyOfChild, m.dimensions.weightG); err != nil {
		return nil, err
	}
	p.packaging.defs[i].measured = &m
	p.bump()
	return []Event{ProductPackLevelMeasured{Header: p.header(now), Level: cur.level, GTIN: cur.gtin, Measurement: m}}, nil
}
