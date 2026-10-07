package product

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func TestNewUnitDimensions(t *testing.T) {
	tests := []struct {
		name       string
		l, w, h, g int64
		wantErr    error
	}{
		{"minimums", 1, 1, 1, 1, nil},
		{"maximums", 20000, 20000, 20000, 2000000, nil},
		{"length 0", 0, 1, 1, 1, ErrInvalidDimension},
		{"width over", 1, 20001, 1, 1, ErrInvalidDimension},
		{"height negative", 1, 1, -5, 1, ErrInvalidDimension},
		{"weight 0", 1, 1, 1, 0, ErrInvalidWeight},
		{"weight over", 1, 1, 1, 2000001, ErrInvalidWeight},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := NewUnitDimensions(tt.l, tt.w, tt.h, tt.g)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if d != (UnitDimensions{}) {
					t.Fatalf("dimensions returned on error: %+v", d)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if d.LengthMm() != tt.l || d.WidthMm() != tt.w || d.HeightMm() != tt.h || d.WeightG() != tt.g {
				t.Fatalf("accessors = %d %d %d %d", d.LengthMm(), d.WidthMm(), d.HeightMm(), d.WeightG())
			}
		})
	}
}

func TestVolumeMm3(t *testing.T) {
	d := RehydrateUnitDimensions(200, 120, 80, 1500)
	if got := d.VolumeMm3(); got != 1920000 {
		t.Fatalf("VolumeMm3 = %d, want 1920000", got)
	}
	big := RehydrateUnitDimensions(20000, 20000, 20000, 1)
	if got := big.VolumeMm3(); got != 8_000_000_000_000 {
		t.Fatalf("max VolumeMm3 = %d", got)
	}
}

func dims(t *testing.T, l, w, h, g int64) UnitDimensions {
	t.Helper()
	d, err := NewUnitDimensions(l, w, h, g)
	if err != nil {
		t.Fatalf("NewUnitDimensions: %v", err)
	}
	return d
}

func TestNewMeasurement(t *testing.T) {
	d := dims(t, 10, 10, 10, 100)
	tests := []struct {
		name     string
		at       time.Time
		deviceID string
		wantErr  error
	}{
		{"now is allowed", t0, "CUBISCAN-03", nil},
		{"past, manual", t0.Add(-time.Hour), "", nil},
		{"64-char device", t0, strings.Repeat("d", 64), nil},
		{"zero time", time.Time{}, "", ErrMissingMeasuredAt},
		{"future", t0.Add(time.Nanosecond), "", ErrMeasuredAtInFuture},
		{"65-char device", t0, strings.Repeat("d", 65), ErrInvalidDeviceID},
		{"control char device", t0, "dev\n1", ErrInvalidDeviceID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewMeasurement(d, tt.at, tt.deviceID, t0)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if m != (Measurement{}) {
					t.Fatalf("measurement returned on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if m.Dimensions() != d || !m.MeasuredAt().Equal(tt.at) || m.DeviceID() != tt.deviceID {
				t.Fatalf("accessors = %+v", m)
			}
		})
	}
}

func TestNewMeasurementNormalisesTime(t *testing.T) {
	sp := time.FixedZone("BRT", -3*3600)
	at := time.Date(2026, 10, 6, 8, 0, 0, 123456789, sp)
	m, err := NewMeasurement(dims(t, 1, 1, 1, 1), at, "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if m.MeasuredAt().Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", m.MeasuredAt().Location())
	}
	if m.MeasuredAt().Nanosecond() != 123456000 {
		t.Fatalf("nanos = %d, want truncation to microseconds", m.MeasuredAt().Nanosecond())
	}
	r := RehydrateMeasurement(dims(t, 1, 1, 1, 1), at, "")
	if !r.sameAs(m) {
		t.Fatal("rehydrated measurement differs from the constructed one")
	}
}

func TestMeasurementSameAs(t *testing.T) {
	base := RehydrateMeasurement(dims(t, 1, 2, 3, 4), t0, "D1")
	tests := []struct {
		name  string
		other Measurement
		want  bool
	}{
		{"identical", RehydrateMeasurement(dims(t, 1, 2, 3, 4), t0, "D1"), true},
		{"same instant other zone", RehydrateMeasurement(dims(t, 1, 2, 3, 4), t0.In(time.FixedZone("X", 3600)), "D1"), true},
		{"other dims", RehydrateMeasurement(dims(t, 1, 2, 3, 5), t0, "D1"), false},
		{"other time", RehydrateMeasurement(dims(t, 1, 2, 3, 4), t0.Add(time.Second), "D1"), false},
		{"other device", RehydrateMeasurement(dims(t, 1, 2, 3, 4), t0, "D2"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := base.sameAs(tt.other); got != tt.want {
				t.Fatalf("sameAs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPhysicalProfileEmpty(t *testing.T) {
	var p PhysicalProfile
	if _, ok := p.Declared(); ok {
		t.Fatal("Declared ok on empty profile")
	}
	if _, ok := p.Measured(); ok {
		t.Fatal("Measured ok on empty profile")
	}
	if _, ok := p.Effective(); ok {
		t.Fatal("Effective ok on empty profile")
	}
	if p.EffectiveSource() != EffectiveNone || p.Discrepancy() {
		t.Fatalf("source %q discrepancy %v", p.EffectiveSource(), p.Discrepancy())
	}
}

func TestPhysicalProfileEffective(t *testing.T) {
	declared := dims(t, 200, 120, 80, 1500)
	measured := RehydrateMeasurement(dims(t, 205, 121, 82, 1720), t0, "C")

	onlyDeclared := RehydratePhysicalProfile(&declared, nil)
	if e, ok := onlyDeclared.Effective(); !ok || e != declared || onlyDeclared.EffectiveSource() != EffectiveDeclared {
		t.Fatalf("declared-only effective = %+v %v %q", e, ok, onlyDeclared.EffectiveSource())
	}
	if onlyDeclared.Discrepancy() {
		t.Fatal("discrepancy without measurement")
	}

	onlyMeasured := RehydratePhysicalProfile(nil, &measured)
	if e, ok := onlyMeasured.Effective(); !ok || e != measured.Dimensions() || onlyMeasured.EffectiveSource() != EffectiveMeasured {
		t.Fatalf("measured-only effective = %+v %v %q", e, ok, onlyMeasured.EffectiveSource())
	}
	if onlyMeasured.Discrepancy() {
		t.Fatal("discrepancy without declared")
	}

	both := RehydratePhysicalProfile(&declared, &measured)
	if e, _ := both.Effective(); e != measured.Dimensions() || both.EffectiveSource() != EffectiveMeasured {
		t.Fatalf("both effective = %+v %q", e, both.EffectiveSource())
	}
	if d, ok := both.Declared(); !ok || d != declared {
		t.Fatal("Declared lost")
	}
	if m, ok := both.Measured(); !ok || !m.sameAs(measured) {
		t.Fatal("Measured lost")
	}
}

func TestDiscrepancy(t *testing.T) {
	// declared volume 1000 mm3 (10x10x10), weight 1000 g.
	declared := dims(t, 10, 10, 10, 1000)
	tests := []struct {
		name     string
		measured UnitDimensions
		want     bool
	}{
		{"identical", dims(t, 10, 10, 10, 1000), false},
		{"weight +10% exactly", dims(t, 10, 10, 10, 1100), false},
		{"weight +10% plus 1g", dims(t, 10, 10, 10, 1101), true},
		{"weight -10% exactly", dims(t, 10, 10, 10, 900), false},
		{"weight -10% minus 1g", dims(t, 10, 10, 10, 899), true},
		{"volume +10% exactly", dims(t, 11, 10, 10, 1000), false},
		{"volume +20%", dims(t, 12, 10, 10, 1000), true},
		{"volume -10% exactly", dims(t, 9, 10, 10, 1000), false},
		{"volume -20%", dims(t, 8, 10, 10, 1000), true},
		{"rotated, same volume", dims(t, 10, 10, 10, 1000), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := RehydrateMeasurement(tt.measured, t0, "")
			p := RehydratePhysicalProfile(&declared, &m)
			if got := p.Discrepancy(); got != tt.want {
				t.Fatalf("Discrepancy = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExceedsTenPercent(t *testing.T) {
	tests := []struct {
		declared, measured int64
		want               bool
	}{
		{100, 110, false},
		{100, 111, true},
		{100, 90, false},
		{100, 89, true},
		{100, 100, false},
		{10, 12, true},
	}
	for _, tt := range tests {
		if got := exceedsTenPercent(tt.declared, tt.measured); got != tt.want {
			t.Fatalf("exceedsTenPercent(%d, %d) = %v, want %v", tt.declared, tt.measured, got, tt.want)
		}
	}
}

func TestWithDeclaredAndMeasuredDoNotMutateReceiver(t *testing.T) {
	var p PhysicalProfile
	d := dims(t, 1, 1, 1, 1)
	q := p.withDeclared(d)
	if _, ok := p.Declared(); ok {
		t.Fatal("withDeclared mutated the receiver")
	}
	m := RehydrateMeasurement(d, t0, "")
	r := q.withMeasured(m)
	if _, ok := q.Measured(); ok {
		t.Fatal("withMeasured mutated the receiver")
	}
	if _, ok := r.Declared(); !ok {
		t.Fatal("withMeasured dropped declared")
	}
}
