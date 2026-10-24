package mcp_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// update regenerates testdata/tool_registry.golden.json:
//
//	go test ./internal/adapters/inbound/mcp -run TestToolRegistryGolden -update
var update = flag.Bool("update", false, "rewrite the MCP tool registry golden file")

// wantTools is the curated tool set (docs/adr/0005 as amended by ADR 0011):
// six read-only tools and the two write tools of writeTools.
var wantTools = []string{
	"declare_pack_level", "get_pack_levels", "get_physical_profile", "get_product", "get_product_classification",
	"list_products", "resolve_gtin", "retire_pack_level",
}

// writeTools are the ONLY tools allowed to write (docs/adr/0011 amends ADR
// 0005 for these two). Adding a third is a deliberate, reviewed act: a new ADR.
var writeTools = map[string]bool{"declare_pack_level": true, "retire_pack_level": true}

// maxTools is the tool budget. Raising it is a deliberate, reviewed act (an
// ADR 0005 amendment): TestToolSurface still pins the exact curated set.
const maxTools = 8

// writeVerbs are the verbs of product-master's write use cases (and the
// generic mutators). A tool whose name contains one of them as a word is a
// write tool, which the read-only MCP surface must never expose: writes go
// through REST, the transactional outbox and the events consumers rely on.
var writeVerbs = []string{"register", "classify", "declare", "record", "update", "delete", "set", "create", "retire"}

// writeVerbViolations returns one message per tool name carrying a write
// verb as one of its snake_case words, except the allow-listed writeTools.
func writeVerbViolations(names []string) []string {
	var out []string
	for _, name := range names {
		if writeTools[name] {
			continue
		}
		for _, word := range strings.Split(name, "_") {
			for _, verb := range writeVerbs {
				if word == verb {
					out = append(out, name+": contains the write verb "+verb+" (the MCP surface is read-only, docs/adr/0005)")
				}
			}
		}
	}
	return out
}

// The MCP governance charter's mechanical gate: the advertised tool set is
// exactly the curated one, within the budget, snake_case verb_noun with NO
// write verb outside writeTools, read tools annotated read-only, write tools
// annotated as writes, described, and every argument is snake_case and
// documented.
func TestToolSurface(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) > maxTools {
		t.Fatalf("advertised %d tools, over the budget of %d", len(res.Tools), maxTools)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		checkToolMetadata(t, tool)
		checkToolArguments(t, tool)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != strings.Join(wantTools, ",") {
		t.Fatalf("advertised tools %v, want exactly %v", names, wantTools)
	}
	for _, v := range writeVerbViolations(names) {
		t.Error(v)
	}
	for name := range writeTools {
		found := false
		for _, n := range names {
			found = found || n == name
		}
		if !found {
			t.Errorf("write tool %q is allow-listed but not advertised", name)
		}
	}
}

// The write-verb sensor must actually be able to fail: every write verb in
// a tool name is flagged, read names are not.
func TestWriteVerbSensorFailsOnWriteNames(t *testing.T) {
	for _, verb := range writeVerbs {
		for _, name := range []string{verb + "_product", "bulk_" + verb + "_products"} {
			if len(writeVerbViolations([]string{name})) == 0 {
				t.Errorf("write tool name %q was not flagged", name)
			}
		}
	}
	if got := writeVerbViolations(wantTools); len(got) != 0 {
		t.Errorf("curated tool names were flagged: %v", got)
	}
	// the allow-list is exactly the two ADR 0011 tools, by name
	if len(writeTools) != 2 || !writeTools["declare_pack_level"] || !writeTools["retire_pack_level"] {
		t.Errorf("writeTools = %v, want exactly declare_pack_level and retire_pack_level", writeTools)
	}
	if got := writeVerbViolations([]string{"declare_pack_levels", "retire_product", "declare_gtin"}); len(got) != 3 {
		t.Errorf("a look-alike of an allowed write tool must still be flagged: %v", got)
	}
	// Substrings of a word are not verbs: "dataset" is not "set".
	if got := writeVerbViolations([]string{"get_dataset", "list_records_count"}); len(got) != 0 {
		t.Errorf("false positive on non-verb words: %v", got)
	}
}

var (
	toolNaming = regexp.MustCompile(`^[a-z]+(_[a-z]+)+$`)
	argNaming  = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)
)

func checkToolMetadata(t *testing.T, tool *sdk.Tool) {
	t.Helper()
	if !toolNaming.MatchString(tool.Name) {
		t.Errorf("tool %q is not snake_case verb_noun", tool.Name)
	}
	if tool.Description == "" {
		t.Errorf("tool %q has no description", tool.Name)
	}
	if tool.Annotations == nil {
		t.Errorf("tool %q has no annotations", tool.Name)
		return
	}
	if writeTools[tool.Name] {
		// a write tool says so: not read-only, idempotent, closed-world, and a destructive hint stated either way
		if tool.Annotations.ReadOnlyHint || !tool.Annotations.IdempotentHint || tool.Annotations.DestructiveHint == nil ||
			tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
			t.Errorf("write tool %q has the wrong annotations: %+v", tool.Name, tool.Annotations)
		}
		if !strings.HasPrefix(tool.Description, "WRITE.") {
			t.Errorf("write tool %q must announce itself in its description: %q", tool.Name, tool.Description)
		}
		return
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Errorf("tool %q is not annotated read-only: %+v", tool.Name, tool.Annotations)
		return
	}
	if tool.Annotations.DestructiveHint != nil && *tool.Annotations.DestructiveHint {
		t.Errorf("tool %q is annotated destructive", tool.Name)
	}
}

func checkToolArguments(t *testing.T, tool *sdk.Tool) {
	t.Helper()
	schema, isMap := tool.InputSchema.(map[string]any)
	if !isMap {
		t.Errorf("tool %q input schema = %T", tool.Name, tool.InputSchema)
		return
	}
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		t.Errorf("tool %q has no arguments in its schema", tool.Name)
	}
	for arg, p := range props {
		if !argNaming.MatchString(arg) {
			t.Errorf("tool %q argument %q is not snake_case", tool.Name, arg)
		}
		if d, _ := p.(map[string]any)["description"].(string); d == "" {
			t.Errorf("tool %q argument %q has no description", tool.Name, arg)
		}
	}
}

// TestToolRegistryGolden pins the whole advertised registry (names,
// descriptions, annotations, input AND output schemas) byte for byte, so any
// change to what an MCP client sees shows up as a reviewed golden diff.
func TestToolRegistryGolden(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	sort.Slice(res.Tools, func(i, j int) bool { return res.Tools[i].Name < res.Tools[j].Name })
	got, err := json.MarshalIndent(res.Tools, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "tool_registry.golden.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update to create it): %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("the MCP tool registry drifted from %s; review the change and re-run with -update.\ngot:\n%s", path, got)
	}
}
