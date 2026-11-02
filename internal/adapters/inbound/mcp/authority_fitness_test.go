// Agentic Standard v2 (AS-1): every MCP tool declares exactly one authority
// level. A tool that forgets, or that mutates state while claiming a read
// level, fails the build.
package mcp_test

import (
	"fmt"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// authorityKey is the pinned tools/list _meta key (contracts G2).
const authorityKey = "com.warehouse/authority"

// authorityOf returns the level a tool declares and whether it is a valid
// level string (L1..L4). L4 is a valid LEVEL; authorityViolation forbids it
// for write tools in this context (no ADR grants it).
func authorityOf(tool *sdk.Tool) (string, bool) {
	level, ok := tool.Meta[authorityKey].(string)
	if !ok {
		return "", false
	}
	switch level {
	case "L1", "L2", "L3", "L4":
		return level, true
	}
	return level, false
}

// authorityViolation returns why tool breaks the authority rule, or "".
func authorityViolation(tool *sdk.Tool) string {
	level, ok := authorityOf(tool)
	if !ok {
		return fmt.Sprintf("must declare exactly one authority level L1..L4 in Meta[%q], got %v", authorityKey, tool.Meta[authorityKey])
	}
	readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
	switch {
	case !readOnly && level != "L3":
		return fmt.Sprintf("has ReadOnlyHint=false so it must be L3 (L4 is forbidden here), got %s", level)
	case readOnly && level != "L1" && level != "L2":
		return fmt.Sprintf("is read-only so it must be L1 or L2, got %s", level)
	}
	return ""
}

// TestEveryAgentToolDeclaresAuthority (MCP half; the A2A half lives in the
// a2a package under the same name) lists EVERY registered tool over the real
// in-memory session and asserts: exactly one valid level per tool; every tool
// whose ReadOnlyHint is false is L3; every read-only tool is L1 or L2.
func TestEveryAgentToolDeclaresAuthority(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) == 0 {
		t.Fatal("no tools registered; the sensor is looking at nothing")
	}
	want := map[string]string{
		"get_product":                "L1",
		"list_products":              "L1",
		"get_product_classification": "L1",
		"get_physical_profile":       "L1",
		"resolve_gtin":               "L1",
		"get_pack_levels":            "L1",
		"declare_pack_level":         "L3",
		"retire_pack_level":          "L3",
	}
	seen := map[string]bool{}
	for _, tool := range res.Tools {
		if v := authorityViolation(tool); v != "" {
			t.Errorf("tool %q %s", tool.Name, v)
		}
		wantLevel, known := want[tool.Name]
		if !known {
			t.Errorf("unexpected tool %q on the registry", tool.Name)
			continue
		}
		if level, _ := authorityOf(tool); level != wantLevel {
			t.Errorf("tool %q authority = %q, want %q", tool.Name, level, wantLevel)
		}
		seen[tool.Name] = true
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("tool %q missing from the registry", name)
		}
	}
}
