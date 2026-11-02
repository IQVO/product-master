package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/product-master/internal/application/usecases"
)

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME use cases the REST adapter uses: the reads
// (GET /products, GET /products/{sku}, .../classification,
// .../physical-profile, GET /gtins/{gtin}) and, since ADR 0011, exactly two
// writes (PUT /products/{sku}/pack-levels/{level} and its /retirements). The
// adapter never constructs an outbound adapter itself.
type Deps struct {
	GetProduct   *usecases.GetProduct
	ListProducts *usecases.ListProducts
	ResolveGTIN  *usecases.ResolveGTIN

	// The two write tools (ADR 0011 amends ADR 0005 for them only).
	DeclarePackLevel *usecases.DeclarePackLevel
	RetirePackLevel  *usecases.RetirePackLevel
}

// --- inputs -------------------------------------------------------------------

type skuInput struct {
	SKU string `json:"sku" jsonschema:"the product's SKU (1..64 characters, no whitespace, control characters or '/'), e.g. SKU-1"`
}

type listProductsInput struct {
	Limit       int    `json:"limit,omitempty" jsonschema:"page size, 1..500; omitted or 0 means 100"`
	Cursor      string `json:"cursor,omitempty" jsonschema:"the opaque next_cursor returned by the previous page; omitted means the first page"`
	HandlingTag string `json:"handling_tag,omitempty" jsonschema:"keep only products whose classification carries this tag: Hazmat, Fragile, TemperatureSensitive, Oversized or HighValue"`
	Classified  *bool  `json:"classified,omitempty" jsonschema:"true keeps only classified products, false only unclassified ones; omitted keeps both"`
}

type gtinInput struct {
	GTIN string `json:"gtin" jsonschema:"a GTIN of 8, 12, 13 or 14 digits with a valid mod-10 check digit, as printed on the barcode, e.g. 4006381333931; it is normalised to 14 digits"`
}

type dimsInput struct {
	LengthMm int64 `json:"length_mm" jsonschema:"length of one pack in millimetres, 1..20000"`
	WidthMm  int64 `json:"width_mm" jsonschema:"width of one pack in millimetres, 1..20000"`
	HeightMm int64 `json:"height_mm" jsonschema:"height of one pack in millimetres, 1..20000"`
	WeightG  int64 `json:"weight_g" jsonschema:"weight of one pack in grams, 1..2000000"`
}

type declarePackLevelInput struct {
	SKU            string     `json:"sku" jsonschema:"the product's SKU (1..64 characters, no whitespace, control characters or '/'), e.g. SKU-1"`
	Level          string     `json:"level" jsonschema:"the packaging level: EACH, INNER, CASE or PALLET"`
	GTIN           string     `json:"gtin" jsonschema:"the GTIN of this level (8, 12, 13 or 14 digits, valid mod-10 check digit; indicator digit 9 is rejected)"`
	ChildGTIN      string     `json:"child_gtin,omitempty" jsonschema:"the GTIN of the next lower level this one is made of; required for every level but EACH, omitted for EACH"`
	QtyOfChild     int        `json:"qty_of_child,omitempty" jsonschema:"how many child packs make one of this level, at least 2; required for every level but EACH, omitted for EACH"`
	UnitsPerPack   int        `json:"units_per_pack,omitempty" jsonschema:"optional direct factor to eaches; omitted means qty_of_child times the child's factor, a given value must equal that"`
	Dims           *dimsInput `json:"dims,omitempty" jsonschema:"optional declared dimensions and weight of one pack; omitted means none declared"`
	IsOrderable    bool       `json:"is_orderable,omitempty" jsonschema:"optional; true when this level can be ordered from the supplier; omitted means false"`
	IsDespatchUnit bool       `json:"is_despatch_unit,omitempty" jsonschema:"optional; true when this level ships as a unit; omitted means false"`
}

type retirePackLevelInput struct {
	SKU    string `json:"sku" jsonschema:"the product's SKU (1..64 characters, no whitespace, control characters or '/'), e.g. SKU-1"`
	Level  string `json:"level" jsonschema:"the packaging level to end-date: EACH, INNER, CASE or PALLET"`
	Reason string `json:"reason,omitempty" jsonschema:"optional reason, 1..200 characters without control characters; omitted means retired"`
}

// --- registry -----------------------------------------------------------------

// registerTools adds the six read-only tools and the two write tools of ADR
// 0011. Every read tool is annotated read-only, idempotent and closed-world;
// the two write tools are the ONLY exceptions, are annotated as writes
// (idempotent, closed-world) and go through the same use cases, outbox and
// events as REST. TestToolSurface pins the set and fails the build on any
// other write-verb name (ADR 0005 as amended by ADR 0011).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}
	d.registerReadTools(server, readOnly)
	d.registerWriteTools(server, &closedWorld)
}

// registerReadTools registers the six read-only tools.
func (d Deps) registerReadTools(server *mcp.Server, readOnly *mcp.ToolAnnotations) {
	addTool(server, &mcp.Tool{
		Name: "get_product",
		Description: "Read one product by SKU: description, version, its handling classification (omitted when unclassified) " +
			"and its physical profile (declared and latest measured unit dimensions/weight, the effective one used by " +
			"the warehouse, and whether they disagree by more than 10%). Read-only; fails with product-not-found for an " +
			"unknown SKU and invalid-sku for a malformed one.",
		Annotations: readOnly,
	}, d.getProduct)

	addTool(server, &mcp.Tool{
		Name: "list_products",
		Description: "List products in ascending SKU order, a page at a time (default 100, max 500), optionally only those whose " +
			"classification carries handling_tag and/or only classified (classified=true) or unclassified (classified=false) " +
			"ones. Pass the returned next_cursor to get the next page; it is absent on the last page. Read-only; a bad limit, " +
			"cursor or tag fails with malformed-request.",
		Annotations: readOnly,
	}, d.listProducts)

	addTool(server, &mcp.Tool{
		Name: "get_product_classification",
		Description: "Read the handling classification of one SKU: handling_tags in stable order (Hazmat, Fragile, " +
			"TemperatureSensitive, Oversized, HighValue), temperature_class (Ambient, Chilled, Frozen; only with " +
			"TemperatureSensitive), dot_hazard_class (1..9; only with Hazmat, omitted when not recorded), " +
			"classification_source (native, or legacy-import while inventory-storage's value has not been confirmed here) " +
			"and the product version. Read-only; fails with product-not-found or product-classification-not-found.",
		Annotations: readOnly,
	}, d.getProductClassification)

	addTool(server, &mcp.Tool{
		Name: "get_physical_profile",
		Description: "Read the physical profile of one SKU: declared dimensions (length_mm, width_mm, height_mm, weight_g, " +
			"volume_mm3), the latest measurement (same fields plus measured_at and device_id), the effective dimensions " +
			"(measured wins over declared; effective_source is measured, declared or none) and discrepancy (declared and " +
			"measured differ by more than 10%). Read-only; fails with product-not-found for an unknown SKU.",
		Annotations: readOnly,
	}, d.getPhysicalProfile)

	addTool(server, &mcp.Tool{
		Name: "resolve_gtin",
		Description: "Resolve a GTIN (EAN/UPC/GTIN-14 as printed on a barcode; 8, 12, 13 or 14 digits, normalised to 14) to the SKU and " +
			"packaging level it identifies: sku, level (EACH, INNER, CASE, PALLET), units_per_pack (eaches in one pack), child_gtin, " +
			"active and the effective_from / effective_to dates. A retired GTIN still resolves, with active false. Read-only; fails with " +
			"gtin-not-found for a GTIN no pack level holds, invalid-gtin for a bad length or check digit and variable-measure-unsupported " +
			"for indicator digit 9.",
		Annotations: readOnly,
	}, d.resolveGTIN)

	addTool(server, &mcp.Tool{
		Name: "get_pack_levels",
		Description: "Read the packaging hierarchy of one SKU: every pack level it ever declared, retired ones included, lowest level first. " +
			"Each item has level, gtin, child_gtin and qty_of_child (absent for EACH), units_per_pack (direct factor to eaches), declared " +
			"dims, the latest measured dimensions, is_orderable, is_despatch_unit, active and the effective dates. Read-only; fails with " +
			"product-not-found for an unknown SKU.",
		Annotations: readOnly,
	}, d.getPackLevels)

}

// registerWriteTools registers the two ADR 0011 write tools. Both are
// idempotent and closed-world; only retire_pack_level is destructive (it ends
// a GTIN for good).
func (d Deps) registerWriteTools(server *mcp.Server, closedWorld *bool) {
	notDestructive, destructive := false, true
	additiveWrite := &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &notDestructive, OpenWorldHint: closedWorld}
	endDatingWrite := &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: &destructive, OpenWorldHint: closedWorld}

	addTool(server, &mcp.Tool{
		Name: "declare_pack_level",
		Description: "WRITE. Declare one packaging level of a registered SKU with its GTIN (ADR 0011): level EACH, INNER, CASE or PALLET; every " +
			"level but EACH names its child_gtin (an active lower level of this SKU) and qty_of_child (at least 2). A GTIN identifies exactly one " +
			"(sku, level), fleet-wide. The content quantity of an ACTIVE level never changes: declaring the same gtin with another quantity fails " +
			"with pack-quantity-immutable; a new quantity needs a NEW gtin at that level, which end-dates the old level (kept, reason superseded). " +
			"Repeating an identical declaration changes nothing. Raises ProductPackLevelDeclared (and ProductPackLevelRetired when it supersedes). " +
			"Fails with invalid-gtin, variable-measure-unsupported, invalid-pack-level, invalid-pack-quantity, pack-level-order, " +
			"pack-dimensions-below-contents, gtin-already-assigned or product-not-found.",
		Annotations: additiveWrite,
	}, d.declarePackLevel)

	addTool(server, &mcp.Tool{
		Name: "retire_pack_level",
		Description: "WRITE. End-date the active definition of one packaging level of a SKU (effective_to = now). The definition and its GTIN " +
			"are kept: the GTIN still resolves, with active false, and is never reassigned. Retiring a level that is already retired changes " +
			"nothing. Levels above it are not retired: re-declare them. Raises ProductPackLevelRetired. Fails with pack-level-not-found when the " +
			"SKU never had that level, invalid-pack-level, invalid-reason or product-not-found.",
		Annotations: endDatingWrite,
	}, d.retirePackLevel)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
