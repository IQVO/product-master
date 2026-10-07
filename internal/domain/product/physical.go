package product

import (
	"errors"
	"time"
	"unicode"
	"unicode/utf8"
)

// Bounds for unit dimensions and weight (ADR 0002). They catch unit
// mistakes (metres typed as millimetres), they do not model real limits.
const (
	MaxDimensionMm   int64 = 20000
	MaxWeightG       int64 = 2000000
	MaxDeviceIDChars       = 64
)

var (
	// ErrInvalidDimension is returned when a length, width or height is outside 1..MaxDimensionMm.
	ErrInvalidDimension = errors.New("each dimension must be 1..20000 mm")
	// ErrInvalidWeight is returned when a weight is outside 1..MaxWeightG.
	ErrInvalidWeight = errors.New("weight must be 1..2000000 g")
	// ErrInvalidDeviceID is returned when a device id is too long or has a control character.
	ErrInvalidDeviceID = errors.New("device id must be at most 64 characters without control characters")
	// ErrMissingMeasuredAt is returned when a measurement has no measurement time.
	ErrMissingMeasuredAt = errors.New("measurement requires measuredAt")
	// ErrMeasuredAtInFuture is returned when a measurement is dated after now.
	ErrMeasuredAtInFuture = errors.New("measuredAt is after the current time")
	// ErrStaleMeasurement is returned when a measurement is older than the current one.
	ErrStaleMeasurement = errors.New("measurement is older than the current one")
)

// UnitDimensions is the size and weight of one unit, in whole millimetres
// and grams.
type UnitDimensions struct {
	lengthMm int64
	widthMm  int64
	heightMm int64
	weightG  int64
}

// NewUnitDimensions validates the bounds.
func NewUnitDimensions(lengthMm, widthMm, heightMm, weightG int64) (UnitDimensions, error) {
	for _, d := range []int64{lengthMm, widthMm, heightMm} {
		if d < 1 || d > MaxDimensionMm {
			return UnitDimensions{}, ErrInvalidDimension
		}
	}
	if weightG < 1 || weightG > MaxWeightG {
		return UnitDimensions{}, ErrInvalidWeight
	}
	return UnitDimensions{lengthMm: lengthMm, widthMm: widthMm, heightMm: heightMm, weightG: weightG}, nil
}

// LengthMm returns the length in millimetres.
func (d UnitDimensions) LengthMm() int64 { return d.lengthMm }

// WidthMm returns the width in millimetres.
func (d UnitDimensions) WidthMm() int64 { return d.widthMm }

// HeightMm returns the height in millimetres.
func (d UnitDimensions) HeightMm() int64 { return d.heightMm }

// WeightG returns the weight in grams.
func (d UnitDimensions) WeightG() int64 { return d.weightG }

// VolumeMm3 returns length x width x height in cubic millimetres. The bounds
// keep it below 8e12, well inside int64.
func (d UnitDimensions) VolumeMm3() int64 { return d.lengthMm * d.widthMm * d.heightMm }

// Measurement is a measured UnitDimensions with when, and optionally by
// which device, it was taken.
type Measurement struct {
	dimensions UnitDimensions
	measuredAt time.Time
	deviceID   string
}

// NewMeasurement validates a measurement. now is the service clock, used to
// reject a measurement dated in the future. measuredAt is normalised to UTC
// and truncated to microseconds (the precision Postgres stores), so a
// repeated request compares equal to the persisted reading.
func NewMeasurement(dimensions UnitDimensions, measuredAt time.Time, deviceID string, now time.Time) (Measurement, error) {
	if measuredAt.IsZero() {
		return Measurement{}, ErrMissingMeasuredAt
	}
	if measuredAt.After(now) {
		return Measurement{}, ErrMeasuredAtInFuture
	}
	if utf8.RuneCountInString(deviceID) > MaxDeviceIDChars {
		return Measurement{}, ErrInvalidDeviceID
	}
	for _, r := range deviceID {
		if unicode.IsControl(r) {
			return Measurement{}, ErrInvalidDeviceID
		}
	}
	return Measurement{dimensions: dimensions, measuredAt: normaliseTime(measuredAt), deviceID: deviceID}, nil
}

func normaliseTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

// sameAs reports whether two measurements are the same reading.
func (m Measurement) sameAs(other Measurement) bool {
	return m.dimensions == other.dimensions && m.measuredAt.Equal(other.measuredAt) && m.deviceID == other.deviceID
}

// Dimensions returns the measured dimensions and weight.
func (m Measurement) Dimensions() UnitDimensions { return m.dimensions }

// MeasuredAt returns when the unit was measured (UTC).
func (m Measurement) MeasuredAt() time.Time { return m.measuredAt }

// DeviceID returns the measuring device, or "" for a manual measurement.
func (m Measurement) DeviceID() string { return m.deviceID }

// EffectiveSource names where the effective values come from.
type EffectiveSource string

// The effective sources.
const (
	EffectiveMeasured EffectiveSource = "measured"
	EffectiveDeclared EffectiveSource = "declared"
	EffectiveNone     EffectiveSource = "none"
)

// PhysicalProfile is the declared and the latest measured unit dimensions
// of a product. The zero value is an empty profile.
type PhysicalProfile struct {
	declared *UnitDimensions
	measured *Measurement
}

// Declared returns the declared dimensions, if any.
func (p PhysicalProfile) Declared() (UnitDimensions, bool) {
	if p.declared == nil {
		return UnitDimensions{}, false
	}
	return *p.declared, true
}

// Measured returns the latest measurement, if any.
func (p PhysicalProfile) Measured() (Measurement, bool) {
	if p.measured == nil {
		return Measurement{}, false
	}
	return *p.measured, true
}

// Effective returns the values consumers act on: measured if present,
// else declared.
func (p PhysicalProfile) Effective() (UnitDimensions, bool) {
	if p.measured != nil {
		return p.measured.dimensions, true
	}
	return p.Declared()
}

// EffectiveSource reports where Effective comes from.
func (p PhysicalProfile) EffectiveSource() EffectiveSource {
	if p.measured != nil {
		return EffectiveMeasured
	}
	if p.declared != nil {
		return EffectiveDeclared
	}
	return EffectiveNone
}

// Discrepancy reports whether declared and measured both exist and the
// measured volume or weight differs from the declared one by more than 10 %
// of the declared value (ADR 0002).
func (p PhysicalProfile) Discrepancy() bool {
	if p.declared == nil || p.measured == nil {
		return false
	}
	d, m := *p.declared, p.measured.dimensions
	return exceedsTenPercent(d.VolumeMm3(), m.VolumeMm3()) || exceedsTenPercent(d.weightG, m.weightG)
}

// exceedsTenPercent reports |measured - declared| * 10 > declared.
func exceedsTenPercent(declared, measured int64) bool {
	diff := measured - declared
	return diff*10 > declared || -diff*10 > declared
}

// withDeclared returns a copy of p with declared replaced.
func (p PhysicalProfile) withDeclared(d UnitDimensions) PhysicalProfile {
	p.declared = &d
	return p
}

// withMeasured returns a copy of p with the measurement replaced.
func (p PhysicalProfile) withMeasured(m Measurement) PhysicalProfile {
	p.measured = &m
	return p
}

// RehydratePhysicalProfile rebuilds a profile from persisted parts.
func RehydratePhysicalProfile(declared *UnitDimensions, measured *Measurement) PhysicalProfile {
	return PhysicalProfile{declared: declared, measured: measured}
}

// RehydrateUnitDimensions rebuilds persisted dimensions without validation.
func RehydrateUnitDimensions(lengthMm, widthMm, heightMm, weightG int64) UnitDimensions {
	return UnitDimensions{lengthMm: lengthMm, widthMm: widthMm, heightMm: heightMm, weightG: weightG}
}

// RehydrateMeasurement rebuilds a persisted measurement without validation.
func RehydrateMeasurement(dimensions UnitDimensions, measuredAt time.Time, deviceID string) Measurement {
	return Measurement{dimensions: dimensions, measuredAt: normaliseTime(measuredAt), deviceID: deviceID}
}
