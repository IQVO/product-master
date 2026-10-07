package product

import (
	"errors"
	"strings"
	"testing"
)

func TestNewSKU(t *testing.T) {
	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"simple", "SKU-1", true},
		{"dots underscores colons", "a.b_c:d", true},
		{"one char", "x", true},
		{"exactly 64 runes", strings.Repeat("é", 64), true},
		{"empty", "", false},
		{"65 runes", strings.Repeat("a", 65), false},
		{"space", "SKU 1", false},
		{"tab", "SKU\t1", false},
		{"slash", "SKU/1", false},
		{"control char", "SKU\x01", false},
		{"non-breaking space", "SKU\u00a01", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewSKU(tt.value)
			if tt.ok {
				if err != nil {
					t.Fatalf("NewSKU(%q) unexpected error %v", tt.value, err)
				}
				if got.String() != tt.value {
					t.Fatalf("NewSKU(%q) = %q", tt.value, got)
				}
				return
			}
			if !errors.Is(err, ErrInvalidSKU) {
				t.Fatalf("NewSKU(%q) err = %v, want ErrInvalidSKU", tt.value, err)
			}
			if got != "" {
				t.Fatalf("NewSKU(%q) returned %q on error", tt.value, got)
			}
		})
	}
}

func TestValidateDescription(t *testing.T) {
	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"empty", "", true},
		{"200 runes", strings.Repeat("ç", 200), true},
		{"201 runes", strings.Repeat("a", 201), false},
		{"newline", "line\nbreak", false},
		{"leading control char", "\x07bell", false},
		{"plain", "Lithium battery pack 12V", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDescription(tt.value)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidDescription) {
				t.Fatalf("err = %v, want ErrInvalidDescription", err)
			}
		})
	}
}
