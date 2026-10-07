package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME read use cases the REST adapter uses
// (GET /products, GET /products/{sku}, .../classification,
// .../physical-profile); the adapter never constructs an outbound adapter
// itself, and it holds no write use case at all (docs/adr/0005).
type Deps struct {
	GetProduct   *usecases.GetProduct
	ListProducts *usecases.ListProducts
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

// --- outputs (the REST bodies with snake_case names) --------------------------

type dimensionsView struct {
	LengthMm  int64 `json:"length_mm"`
	WidthMm   int64 `json:"width_mm"`
	HeightMm  int64 `json:"height_mm"`
	WeightG   int64 `json:"weight_g"`
	VolumeMm3 int64 `json:"volume_mm3"`
}

type measurementView struct {
	LengthMm   int64  `json:"length_mm"`
	WidthMm    int64  `json:"width_mm"`
	HeightMm   int64  `json:"height_mm"`
	WeightG    int64  `json:"weight_g"`
	VolumeMm3  int64  `json:"volume_mm3"`
	MeasuredAt string `json:"measured_at"`
	DeviceID   string `json:"device_id,omitempty"`
}

type physicalProfileView struct {
	Declared        *dimensionsView  `json:"declared,omitempty"`
	Measured        *measurementView `json:"measured,omitempty"`
	Effective       *dimensionsView  `json:"effective,omitempty"`
	EffectiveSource string           `json:"effective_source"`
	Discrepancy     bool             `json:"discrepancy"`
}

type classificationView struct {
	HandlingTags         []string `json:"handling_tags"`
	TemperatureClass     string   `json:"temperature_class,omitempty"`
	DOTHazardClass       *int     `json:"dot_hazard_class,omitempty"`
	ClassificationSource string   `json:"classification_source"`
}

type productView struct {
	SKU             string              `json:"sku"`
	Description     string              `json:"description"`
	Version         int64               `json:"version"`
	Classification  *classificationView `json:"classification,omitempty"`
	PhysicalProfile physicalProfileView `json:"physical_profile"`
}

type productPageView struct {
	Items      []productView `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

type productClassificationView struct {
	SKU                  string   `json:"sku"`
	HandlingTags         []string `json:"handling_tags"`
	TemperatureClass     string   `json:"temperature_class,omitempty"`
	DOTHazardClass       *int     `json:"dot_hazard_class,omitempty"`
	ClassificationSource string   `json:"classification_source"`
	Version              int64    `json:"version"`
}

type physicalProfileOutput struct {
	SKU             string           `json:"sku"`
	Declared        *dimensionsView  `json:"declared,omitempty"`
	Measured        *measurementView `json:"measured,omitempty"`
	Effective       *dimensionsView  `json:"effective,omitempty"`
	EffectiveSource string           `json:"effective_source"`
	Discrepancy     bool             `json:"discrepancy"`
	Version         int64            `json:"version"`
}

// --- mapping ------------------------------------------------------------------

func toDimensions(d product.UnitDimensions) *dimensionsView {
	return &dimensionsView{LengthMm: d.LengthMm(), WidthMm: d.WidthMm(), HeightMm: d.HeightMm(), WeightG: d.WeightG(), VolumeMm3: d.VolumeMm3()}
}

func toPhysicalProfile(p product.PhysicalProfile) physicalProfileView {
	out := physicalProfileView{EffectiveSource: string(p.EffectiveSource()), Discrepancy: p.Discrepancy()}
	if d, ok := p.Declared(); ok {
		out.Declared = toDimensions(d)
	}
	if m, ok := p.Measured(); ok {
		d := m.Dimensions()
		out.Measured = &measurementView{
			LengthMm: d.LengthMm(), WidthMm: d.WidthMm(), HeightMm: d.HeightMm(), WeightG: d.WeightG(), VolumeMm3: d.VolumeMm3(),
			MeasuredAt: m.MeasuredAt().UTC().Format(time.RFC3339Nano),
			DeviceID:   m.DeviceID(),
		}
	}
	if d, ok := p.Effective(); ok {
		out.Effective = toDimensions(d)
	}
	return out
}

func toClassification(c product.Classification, source product.ClassificationSource) classificationView {
	tags := c.Tags()
	out := classificationView{HandlingTags: make([]string, len(tags)), TemperatureClass: string(c.TemperatureClass()), ClassificationSource: string(source)}
	for i, t := range tags {
		out.HandlingTags[i] = string(t)
	}
	if dot := c.DOTHazardClass(); dot != product.NoDOTHazardClass {
		v := int(dot)
		out.DOTHazardClass = &v
	}
	return out
}

func toProduct(p *product.Product) productView {
	out := productView{SKU: string(p.SKU()), Description: p.Description(), Version: p.Version(), PhysicalProfile: toPhysicalProfile(p.PhysicalProfile())}
	if c, source, err := p.Classification(); err == nil {
		cv := toClassification(c, source)
		out.Classification = &cv
	}
	return out
}

// --- handlers -----------------------------------------------------------------

func (d Deps) getProduct(ctx context.Context, in skuInput) (productView, error) {
	p, err := d.GetProduct.Handle(ctx, in.SKU)
	if err != nil {
		return productView{}, mapError(err)
	}
	return toProduct(p), nil
}

func (d Deps) listProducts(ctx context.Context, in listProductsInput) (productPageView, error) {
	page, err := d.ListProducts.Handle(ctx, usecases.ListProductsQuery{
		Limit: in.Limit, Cursor: in.Cursor, HandlingTag: in.HandlingTag, Classified: in.Classified,
	})
	if err != nil {
		return productPageView{}, mapError(err)
	}
	out := productPageView{Items: make([]productView, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, p := range page.Items {
		out.Items = append(out.Items, toProduct(p))
	}
	return out, nil
}

func (d Deps) getProductClassification(ctx context.Context, in skuInput) (productClassificationView, error) {
	p, err := d.GetProduct.Handle(ctx, in.SKU)
	if err != nil {
		return productClassificationView{}, mapError(err)
	}
	c, source, err := p.Classification()
	if err != nil {
		return productClassificationView{}, mapError(err)
	}
	cv := toClassification(c, source)
	return productClassificationView{
		SKU: string(p.SKU()), HandlingTags: cv.HandlingTags, TemperatureClass: cv.TemperatureClass,
		DOTHazardClass: cv.DOTHazardClass, ClassificationSource: cv.ClassificationSource, Version: p.Version(),
	}, nil
}

func (d Deps) getPhysicalProfile(ctx context.Context, in skuInput) (physicalProfileOutput, error) {
	p, err := d.GetProduct.Handle(ctx, in.SKU)
	if err != nil {
		return physicalProfileOutput{}, mapError(err)
	}
	pv := toPhysicalProfile(p.PhysicalProfile())
	return physicalProfileOutput{
		SKU: string(p.SKU()), Declared: pv.Declared, Measured: pv.Measured, Effective: pv.Effective,
		EffectiveSource: pv.EffectiveSource, Discrepancy: pv.Discrepancy, Version: p.Version(),
	}, nil
}

// --- registry -----------------------------------------------------------------

// registerTools adds the four read-only tools. Every tool is annotated
// read-only, idempotent and closed-world; TestToolSurface pins the set and
// fails the build on any write-verb name (docs/adr/0005).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld}

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
