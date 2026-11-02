package product

import (
	"sort"
	"time"

	"github.com/claudioed/product-master/internal/domain/shared/gs1"
)

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
