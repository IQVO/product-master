package product

import (
	"errors"
	"fmt"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"

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

// PackLevelSpec is the input of DeclarePackLevel. UnitsPerPack 0 means
// "derive it from the child level"; a given value must match the chain.
type PackLevelSpec struct {
	Level          PackLevel
	GTIN           gs1.GTIN
	ChildGTIN      gs1.GTIN
	QtyOfChild     int
	UnitsPerPack   int
	Dims           *UnitDimensions
	IsOrderable    bool
	IsDespatchUnit bool
}

// PackLevelDef is one declared pack level of a product: its identity (GTIN),
// its place in the chain (child GTIN and the quantities), declared and
// measured dimensions (ADR 0002) and when it is effective.
type PackLevelDef struct {
	level          PackLevel
	gtin           gs1.GTIN
	childGTIN      gs1.GTIN
	qtyOfChild     int
	unitsPerPack   int
	declared       *UnitDimensions
	measured       *Measurement
	isOrderable    bool
	isDespatchUnit bool
	effectiveFrom  time.Time
	effectiveTo    *time.Time
}

// Level returns the packaging level.
func (d PackLevelDef) Level() PackLevel { return d.level }

// GTIN returns the 14-digit GTIN identifying this level.
func (d PackLevelDef) GTIN() gs1.GTIN { return d.gtin }

// ChildGTIN returns the GTIN of the child level; the zero value for EACH.
func (d PackLevelDef) ChildGTIN() gs1.GTIN { return d.childGTIN }

// QtyOfChild returns how many children make one of this level; 0 for EACH.
func (d PackLevelDef) QtyOfChild() int { return d.qtyOfChild }

// UnitsPerPack returns the direct factor to eaches (1 for EACH).
func (d PackLevelDef) UnitsPerPack() int { return d.unitsPerPack }

// DeclaredDimensions returns the declared dimensions, if any.
func (d PackLevelDef) DeclaredDimensions() (UnitDimensions, bool) {
	if d.declared == nil {
		return UnitDimensions{}, false
	}
	return *d.declared, true
}

// Measurement returns the latest measurement, if any.
func (d PackLevelDef) Measurement() (Measurement, bool) {
	if d.measured == nil {
		return Measurement{}, false
	}
	return *d.measured, true
}

// EffectiveDimensions returns the measured dimensions if present, else the
// declared ones (ADR 0002).
func (d PackLevelDef) EffectiveDimensions() (UnitDimensions, bool) {
	if d.measured != nil {
		return d.measured.dimensions, true
	}
	return d.DeclaredDimensions()
}

// IsOrderable reports whether the level can be ordered from the supplier.
func (d PackLevelDef) IsOrderable() bool { return d.isOrderable }

// IsDespatchUnit reports whether the level ships as a unit.
func (d PackLevelDef) IsDespatchUnit() bool { return d.isDespatchUnit }

// EffectiveFrom returns when the level became effective (UTC).
func (d PackLevelDef) EffectiveFrom() time.Time { return d.effectiveFrom }

// EffectiveTo returns when the level was end-dated, if it was (UTC).
func (d PackLevelDef) EffectiveTo() (time.Time, bool) {
	if d.effectiveTo == nil {
		return time.Time{}, false
	}
	return *d.effectiveTo, true
}

// Active reports whether the level is not end-dated.
func (d PackLevelDef) Active() bool { return d.effectiveTo == nil }

// sameAttributes reports whether two defs of one GTIN agree on everything
// but their measurement (the measurement has its own command).
func (d PackLevelDef) sameAttributes(o PackLevelDef) bool {
	return d.isOrderable == o.isOrderable && d.isDespatchUnit == o.isDespatchUnit && sameDims(d.declared, o.declared)
}

// copyDims detaches optional dimensions from the caller's variable.
func copyDims(d *UnitDimensions) *UnitDimensions {
	if d == nil {
		return nil
	}
	c := *d
	return &c
}

func sameDims(a, b *UnitDimensions) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// RehydratePackLevelDef rebuilds a persisted pack level without
// re-running invariants. spec.Level, GTIN, ChildGTIN, QtyOfChild,
// UnitsPerPack, Dims and the two flags are the declared state.
func RehydratePackLevelDef(spec PackLevelSpec, measured *Measurement, effectiveFrom time.Time, effectiveTo *time.Time) PackLevelDef {
	return PackLevelDef{
		level: spec.Level, gtin: spec.GTIN, childGTIN: spec.ChildGTIN, qtyOfChild: spec.QtyOfChild, unitsPerPack: spec.UnitsPerPack,
		declared: spec.Dims, measured: measured, isOrderable: spec.IsOrderable, isDespatchUnit: spec.IsDespatchUnit,
		effectiveFrom: effectiveFrom, effectiveTo: effectiveTo,
	}
}

// PackagingHierarchy is every pack level a product ever declared, retired
// ones included: an end-dated level is kept so an in-flight ASN that names its
// GTIN still resolves. At most one level per PackLevel is active.
type PackagingHierarchy struct {
	defs []PackLevelDef
}

// RehydratePackagingHierarchy rebuilds a persisted hierarchy.
func RehydratePackagingHierarchy(defs []PackLevelDef) PackagingHierarchy {
	return PackagingHierarchy{defs: append([]PackLevelDef(nil), defs...)}
}

// Levels returns a copy of every level, lowest level first and, within a
// level, in the order they were declared.
func (h PackagingHierarchy) Levels() []PackLevelDef {
	out := append([]PackLevelDef(nil), h.defs...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].level.rank() < out[j].level.rank() })
	return out
}

// Active returns the active definition at level, if any.
func (h PackagingHierarchy) Active(level PackLevel) (PackLevelDef, bool) {
	i := h.indexActive(level)
	if i < 0 {
		return PackLevelDef{}, false
	}
	return h.defs[i], true
}

// ByGTIN returns the definition (active or retired) identified by gtin.
func (h PackagingHierarchy) ByGTIN(gtin gs1.GTIN) (PackLevelDef, bool) {
	i := h.indexOf(gtin)
	if i < 0 {
		return PackLevelDef{}, false
	}
	return h.defs[i], true
}

func (h PackagingHierarchy) indexOf(gtin gs1.GTIN) int {
	for i, d := range h.defs {
		if d.gtin == gtin {
			return i
		}
	}
	return -1
}

func (h PackagingHierarchy) indexActive(level PackLevel) int {
	for i, d := range h.defs {
		if d.level == level && d.Active() {
			return i
		}
	}
	return -1
}

func (h PackagingHierarchy) hasLevel(level PackLevel) bool {
	for _, d := range h.defs {
		if d.level == level {
			return true
		}
	}
	return false
}

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
