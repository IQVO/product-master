package product

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseHandlingTag(t *testing.T) {
	for _, tag := range []HandlingTag{Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue} {
		got, err := ParseHandlingTag(string(tag))
		if err != nil || got != tag {
			t.Fatalf("ParseHandlingTag(%q) = %q, %v", tag, got, err)
		}
	}
	for _, bad := range []string{"", "hazmat", "Heavy"} {
		got, err := ParseHandlingTag(bad)
		if !errors.Is(err, ErrUnknownHandlingTag) || got != "" {
			t.Fatalf("ParseHandlingTag(%q) = %q, %v", bad, got, err)
		}
	}
}

func TestParseTemperatureClass(t *testing.T) {
	for _, tc := range []TemperatureClass{Ambient, Chilled, Frozen} {
		got, err := ParseTemperatureClass(string(tc))
		if err != nil || got != tc {
			t.Fatalf("ParseTemperatureClass(%q) = %q, %v", tc, got, err)
		}
	}
	for _, bad := range []string{"", "frozen", "Hot"} {
		got, err := ParseTemperatureClass(bad)
		if !errors.Is(err, ErrUnknownTemperatureClass) || got != NoTemperatureClass {
			t.Fatalf("ParseTemperatureClass(%q) = %q, %v", bad, got, err)
		}
	}
}

func TestParseClassificationSource(t *testing.T) {
	for _, s := range []ClassificationSource{SourceNative, SourceLegacyImport} {
		got, err := ParseClassificationSource(string(s))
		if err != nil || got != s {
			t.Fatalf("ParseClassificationSource(%q) = %q, %v", s, got, err)
		}
	}
	got, err := ParseClassificationSource("manual")
	if !errors.Is(err, ErrUnknownClassificationSource) || got != "" {
		t.Fatalf("ParseClassificationSource(manual) = %q, %v", got, err)
	}
}

func TestNewClassification(t *testing.T) {
	tests := []struct {
		name    string
		tags    []HandlingTag
		temp    TemperatureClass
		dot     DOTHazardClass
		wantErr error
	}{
		{"single tag", []HandlingTag{Fragile}, NoTemperatureClass, NoDOTHazardClass, nil},
		{"all tags", []HandlingTag{HighValue, Oversized, TemperatureSensitive, Fragile, Hazmat}, Chilled, 9, nil},
		{"hazmat without dot", []HandlingTag{Hazmat}, NoTemperatureClass, NoDOTHazardClass, nil},
		{"hazmat dot 1", []HandlingTag{Hazmat}, NoTemperatureClass, 1, nil},
		{"hazmat dot 9", []HandlingTag{Hazmat}, NoTemperatureClass, 9, nil},
		{"no tags", nil, NoTemperatureClass, NoDOTHazardClass, ErrNoHandlingTags},
		{"empty slice", []HandlingTag{}, NoTemperatureClass, NoDOTHazardClass, ErrNoHandlingTags},
		{"unknown tag", []HandlingTag{"Heavy"}, NoTemperatureClass, NoDOTHazardClass, ErrUnknownHandlingTag},
		{"duplicate tag", []HandlingTag{Fragile, Fragile}, NoTemperatureClass, NoDOTHazardClass, ErrDuplicateHandlingTag},
		{"temperature required", []HandlingTag{TemperatureSensitive}, NoTemperatureClass, NoDOTHazardClass, ErrTemperatureClassRequired},
		{"unknown temperature", []HandlingTag{TemperatureSensitive}, "Hot", NoDOTHazardClass, ErrUnknownTemperatureClass},
		{"temperature not applicable", []HandlingTag{Fragile}, Frozen, NoDOTHazardClass, ErrTemperatureClassNotApplicable},
		{"dot not applicable", []HandlingTag{Fragile}, NoTemperatureClass, 3, ErrDOTHazardClassNotApplicable},
		{"dot 10", []HandlingTag{Hazmat}, NoTemperatureClass, 10, ErrInvalidDOTHazardClass},
		{"dot negative", []HandlingTag{Hazmat}, NoTemperatureClass, -1, ErrInvalidDOTHazardClass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClassification(tt.tags, tt.temp, tt.dot)
			if tt.wantErr != nil {
				assertClassificationError(t, c, err, tt.wantErr)
				return
			}
			assertClassification(t, c, err, tt.tags, tt.temp, tt.dot)
		})
	}
}

func assertClassificationError(t *testing.T, c Classification, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if c.tags != nil {
		t.Fatalf("classification returned on error: %+v", c)
	}
}

func assertClassification(t *testing.T, c Classification, err error, tags []HandlingTag, temp TemperatureClass, dot DOTHazardClass) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if c.TemperatureClass() != temp || c.DOTHazardClass() != dot {
		t.Fatalf("got temp %q dot %d", c.TemperatureClass(), c.DOTHazardClass())
	}
	for _, tag := range tags {
		if !c.HasTag(tag) {
			t.Fatalf("missing tag %q", tag)
		}
	}
}

func TestClassificationTagsStableOrder(t *testing.T) {
	c, err := NewClassification([]HandlingTag{HighValue, TemperatureSensitive, Hazmat}, Frozen, NoDOTHazardClass)
	if err != nil {
		t.Fatal(err)
	}
	want := []HandlingTag{Hazmat, TemperatureSensitive, HighValue}
	if got := c.Tags(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Tags() = %v, want %v", got, want)
	}
	if c.HasTag(Fragile) {
		t.Fatal("HasTag(Fragile) = true")
	}
}

func mustClassification(t *testing.T, tags []HandlingTag, temp TemperatureClass, dot DOTHazardClass) Classification {
	t.Helper()
	c, err := NewClassification(tags, temp, dot)
	if err != nil {
		t.Fatalf("NewClassification: %v", err)
	}
	return c
}

func TestClassificationEqual(t *testing.T) {
	base := mustClassification(t, []HandlingTag{Hazmat, TemperatureSensitive}, Frozen, 3)
	tests := []struct {
		name  string
		other Classification
		want  bool
	}{
		{"same, different input order", mustClassification(t, []HandlingTag{TemperatureSensitive, Hazmat}, Frozen, 3), true},
		{"different temperature", mustClassification(t, []HandlingTag{Hazmat, TemperatureSensitive}, Chilled, 3), false},
		{"different dot", mustClassification(t, []HandlingTag{Hazmat, TemperatureSensitive}, Frozen, 4), false},
		{"extra tag", mustClassification(t, []HandlingTag{Hazmat, TemperatureSensitive, Fragile}, Frozen, 3), false},
		{"same count, different tag", RehydrateClassification([]HandlingTag{Hazmat, Fragile}, Frozen, 3), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := base.Equal(tt.other); got != tt.want {
				t.Fatalf("Equal = %v, want %v", got, tt.want)
			}
			if got := tt.other.Equal(base); got != tt.want {
				t.Fatalf("reverse Equal = %v, want %v", got, tt.want)
			}
		})
	}
}
