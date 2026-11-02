package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// declarePackLevelsSOP is the operational standard-operating-procedure the
// declare_pack_levels prompt hands to the model (Agentic Standard v2, AS-3;
// ADR 0014). Per the charter, prompts encode how to interpret results, when
// to escalate, and what "done" means; this one names tools but never calls
// them and writes nothing on its own.
const declarePackLevelsSOP = `You are helping declare the packaging hierarchy of a product in product-master. Use only the MCP tools; never assume state. This context enforces the GTIN and hierarchy invariants; whether a packaging change is right for a product is a human call, so declare exactly what you were asked to declare.

Procedure:
1. Read the product first: call get_pack_levels(sku). You must know which levels already exist and which are active before declaring anything.
2. Declare bottom-up with declare_pack_level(sku, level, gtin, ...). The level is EACH, INNER, CASE or PALLET. Start with the EACH and its GTIN; every level above it names child_gtin (an ACTIVE lower level of the same SKU) and qty_of_child (at least 2).
3. A GTIN is 8, 12, 13 or 14 digits with a valid mod-10 check digit, as printed on the barcode; indicator digit 9 (variable measure) is rejected. A GTIN identifies exactly one (sku, level) fleet-wide: a GTIN already assigned elsewhere fails with gtin-already-assigned — never "make one up"; ask for the real code.
4. Declared dims (length_mm, width_mm, height_mm, weight_g of ONE pack) are optional but when given must fit the contents; pack-dimensions-below-contents means the numbers are wrong, not that the rule is negotiable.
5. Repeating an identical declaration changes nothing (idempotent). The content quantity of an ACTIVE level never changes: pack-quantity-immutable means a new quantity needs a NEW GTIN at that level, which end-dates the old one (kept, reason superseded). Confirm with the human before superseding an active level.
6. To end-date a level, call retire_pack_level(sku, level, reason). The GTIN keeps resolving with active false and is never reassigned. Retiring never cascades: levels above must be re-declared explicitly.

Interpretation:
- pack-level-order means you tried to skip a level or reference an inactive child: fix the order, do not retry the same call.
- A rejection is the domain protecting an invariant (GTIN uniqueness, hierarchy order, quantity immutability). Report it; do not retry with invented values.

Escalate to a human when: the physical pack count on the label disagrees with what you were told, a needed GTIN is missing or already assigned, or superseding an active level would change what ordering or despatch units mean for other systems.

Done means: the requested levels are declared (or a rejection is reported with its reason), get_pack_levels shows the hierarchy you expect, and every human decision point was surfaced, not decided.`

// registerPrompts adds the workflow prompt (the operator SOP for this
// context). Prompts are L1 by nature (they carry procedure text, no writes),
// so they carry no tool Meta.
func (d Deps) registerPrompts(server *mcp.Server) {
	server.AddPrompt(&mcp.Prompt{
		Name:        "declare_pack_levels",
		Description: "Standard operating procedure for declaring or retiring a product's packaging levels (GTINs, hierarchy, quantities) without breaking the invariants.",
		Arguments: []*mcp.PromptArgument{{
			Name:        "sku",
			Description: "optional SKU to scope the SOP to; omitted means the procedure applies to whichever product you were asked about",
			Required:    false,
		}},
	}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		sop := declarePackLevelsSOP
		if sku := strings.TrimSpace(req.Params.Arguments["sku"]); sku != "" {
			sop = fmt.Sprintf("Product in scope: %s.\n\n%s", sku, sop)
		}
		return &mcp.GetPromptResult{
			Description: "How to declare packaging levels: read the hierarchy first, declare bottom-up with real GTINs, respect quantity immutability, retire explicitly, and escalate label mismatches.",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: sop},
			}},
		}, nil
	})
}
