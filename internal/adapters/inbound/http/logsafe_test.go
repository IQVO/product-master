package http

import "testing"

func TestSanitizeLog(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"clean":            "clean",
		"line1\nline2":     "line1line2",
		"line1\r\nline2":   "line1line2",
		"carriage\rreturn": "carriagereturn",
		"/api/v1/reports":  "/api/v1/reports",
		"\n\r\nmixed\r\n":  "mixed",
		"trailing\n":       "trailing",
	}
	for in, want := range cases {
		if got := sanitizeLog(in); got != want {
			t.Errorf("sanitizeLog(%q) = %q, want %q", in, got, want)
		}
	}
}
