// Agentic Standard v2 (AS-3): the MCP server offers at least one resource
// and at least one workflow prompt, discoverable through the protocol's own
// listing calls and readable through the in-memory client, so a host needs no
// out-of-band knowledge (ADR 0014).
package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestSurface_PackLevelsResourceTemplateIsListable pins that the pack-levels
// resource template is discoverable via resources/templates/list.
func TestSurface_PackLevelsResourceTemplateIsListable(t *testing.T) {
	h := newHarness(t)
	templates, err := h.session.ListResourceTemplates(t.Context(), nil)
	if err != nil {
		t.Fatalf("list resource templates: %v", err)
	}
	for _, tpl := range templates.ResourceTemplates {
		if tpl.URITemplate == "warehouse://product-master/skus/{sku}/pack-levels" {
			if tpl.MIMEType != "application/json" {
				t.Errorf("template MIMEType = %q, want application/json", tpl.MIMEType)
			}
			return
		}
	}
	t.Fatalf("pack-levels template not discoverable: %+v", templates.ResourceTemplates)
}

// TestSurface_PackLevelsResourceRead reads the resource for one seeded SKU
// and asserts it returns the same bounded view get_pack_levels returns.
func TestSurface_PackLevelsResourceRead(t *testing.T) {
	h := newHarness(t)
	h.seed(t, "SKU-1", "Lithium battery pack", []string{"Hazmat"}, "", 0)
	h.ok(t, "declare_pack_level", map[string]any{"sku": "SKU-1", "level": "EACH", "gtin": "4006381333931"})

	res, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{
		URI: "warehouse://product-master/skus/SKU-1/pack-levels",
	})
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}
	if len(res.Contents) == 0 || res.Contents[0].Text == "" {
		t.Fatalf("empty resource contents: %+v", res.Contents)
	}
	var body struct {
		SKU   string `json:"sku"`
		Items []struct {
			Level string `json:"level"`
			GTIN  string `json:"gtin"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &body); err != nil {
		t.Fatalf("resource body is not the pack-levels JSON view: %v (%s)", err, res.Contents[0].Text)
	}
	if body.SKU != "SKU-1" || len(body.Items) != 1 || body.Items[0].Level != "EACH" || body.Items[0].GTIN != "04006381333931" {
		t.Fatalf("resource body = %s, want the one declared EACH level of SKU-1 (GTIN normalised to 14 digits)", res.Contents[0].Text)
	}
}

// TestSurface_UnknownResourceIsRejected pins that reading a URI outside the
// template is an error.
func TestSurface_UnknownResourceIsRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.session.ReadResource(t.Context(), &sdk.ReadResourceParams{URI: "warehouse://product-master/not-a-template"}); err == nil {
		t.Fatal("reading an unknown resource URI must be rejected")
	}
}

// TestSurface_PromptIsListableAndGettable pins prompt discovery and content:
// the SOP names tools, never calls them.
func TestSurface_PromptIsListableAndGettable(t *testing.T) {
	h := newHarness(t)
	prompts, err := h.session.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts.Prompts) == 0 {
		t.Fatal("server advertises no prompts via prompts/list")
	}
	found := false
	for _, p := range prompts.Prompts {
		if p.Name == "declare_pack_levels" {
			found = true
		}
	}
	if !found {
		t.Fatalf("declare_pack_levels prompt not listed: %+v", prompts.Prompts)
	}

	res, err := h.session.GetPrompt(t.Context(), &sdk.GetPromptParams{Name: "declare_pack_levels"})
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if len(res.Messages) == 0 || res.Messages[0].Content == nil {
		t.Fatal("declare_pack_levels prompt returned no messages")
	}
	sop := res.Messages[0].Content.(*sdk.TextContent).Text
	for _, want := range []string{"get_pack_levels", "declare_pack_level", "retire_pack_level", "Escalate"} {
		if !strings.Contains(sop, want) {
			t.Errorf("SOP does not mention %q", want)
		}
	}

	// The optional sku argument scopes the SOP when supplied.
	scoped, err := h.session.GetPrompt(t.Context(), &sdk.GetPromptParams{
		Name:      "declare_pack_levels",
		Arguments: map[string]string{"sku": "SKU-9"},
	})
	if err != nil {
		t.Fatalf("get prompt with sku: %v", err)
	}
	if text := scoped.Messages[0].Content.(*sdk.TextContent).Text; !strings.Contains(text, "SKU-9") {
		t.Errorf("SOP with sku argument does not mention SKU-9")
	}
}

// TestSurface_UnknownPromptIsRejected pins that getting a prompt the server
// does not have is a protocol error.
func TestSurface_UnknownPromptIsRejected(t *testing.T) {
	h := newHarness(t)
	if _, err := h.session.GetPrompt(t.Context(), &sdk.GetPromptParams{Name: "no_such_prompt"}); err == nil {
		t.Fatal("getting an unknown prompt must be rejected")
	}
}
