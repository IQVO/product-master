package http

import (
	"time"

	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// Request bodies (apis/openapi.yaml; unknown fields are rejected).

type registerProductRequest struct {
	Description string `json:"description"`
}

type classifyProductRequest struct {
	HandlingTags     []string `json:"handlingTags"`
	TemperatureClass string   `json:"temperatureClass"`
	DOTHazardClass   *int     `json:"dotHazardClass"`
}

// dimensionsRequest uses pointers so a missing required field is told apart
// from an out-of-range zero.
type dimensionsRequest struct {
	LengthMm *int64 `json:"lengthMm"`
	WidthMm  *int64 `json:"widthMm"`
	HeightMm *int64 `json:"heightMm"`
	WeightG  *int64 `json:"weightG"`
}

type measurementRequest struct {
	dimensionsRequest
	MeasuredAt time.Time `json:"measuredAt"`
	DeviceID   string    `json:"deviceId"`
}

// Response bodies.

type dimensionsResponse struct {
	LengthMm  int64 `json:"lengthMm"`
	WidthMm   int64 `json:"widthMm"`
	HeightMm  int64 `json:"heightMm"`
	WeightG   int64 `json:"weightG"`
	VolumeMm3 int64 `json:"volumeMm3"`
}

type measurementResponse struct {
	dimensionsResponse
	MeasuredAt string `json:"measuredAt"`
	DeviceID   string `json:"deviceId,omitempty"`
}

type physicalProfileResponse struct {
	Declared        *dimensionsResponse  `json:"declared,omitempty"`
	Measured        *measurementResponse `json:"measured,omitempty"`
	Effective       *dimensionsResponse  `json:"effective,omitempty"`
	EffectiveSource string               `json:"effectiveSource"`
	Discrepancy     bool                 `json:"discrepancy"`
	Version         int64                `json:"version,omitempty"`
}

type classificationResponse struct {
	HandlingTags         []string `json:"handlingTags"`
	TemperatureClass     string   `json:"temperatureClass,omitempty"`
	DOTHazardClass       *int     `json:"dotHazardClass,omitempty"`
	ClassificationSource string   `json:"classificationSource"`
}

type productClassificationResponse struct {
	SKU string `json:"sku"`
	classificationResponse
	Version int64 `json:"version"`
}

type productResponse struct {
	SKU             string                  `json:"sku"`
	Description     string                  `json:"description"`
	Version         int64                   `json:"version"`
	Classification  *classificationResponse `json:"classification,omitempty"`
	PhysicalProfile physicalProfileResponse `json:"physicalProfile"`
}

type productPageResponse struct {
	Items      []productResponse `json:"items"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

func toDimensions(d product.UnitDimensions) *dimensionsResponse {
	return &dimensionsResponse{LengthMm: d.LengthMm(), WidthMm: d.WidthMm(), HeightMm: d.HeightMm(), WeightG: d.WeightG(), VolumeMm3: d.VolumeMm3()}
}

// toPhysicalProfile renders the profile; version is 0 (omitted) inside a
// product, where the product carries it.
func toPhysicalProfile(p product.PhysicalProfile, version int64) physicalProfileResponse {
	out := physicalProfileResponse{EffectiveSource: string(p.EffectiveSource()), Discrepancy: p.Discrepancy(), Version: version}
	if d, ok := p.Declared(); ok {
		out.Declared = toDimensions(d)
	}
	if m, ok := p.Measured(); ok {
		out.Measured = &measurementResponse{
			dimensionsResponse: *toDimensions(m.Dimensions()),
			MeasuredAt:         m.MeasuredAt().UTC().Format(time.RFC3339Nano),
			DeviceID:           m.DeviceID(),
		}
	}
	if d, ok := p.Effective(); ok {
		out.Effective = toDimensions(d)
	}
	return out
}

func toClassification(c product.Classification, source product.ClassificationSource) classificationResponse {
	tags := c.Tags()
	out := classificationResponse{HandlingTags: make([]string, len(tags)), TemperatureClass: string(c.TemperatureClass()), ClassificationSource: string(source)}
	for i, t := range tags {
		out.HandlingTags[i] = string(t)
	}
	if dot := c.DOTHazardClass(); dot != product.NoDOTHazardClass {
		v := int(dot)
		out.DOTHazardClass = &v
	}
	return out
}

func toProduct(p *product.Product) productResponse {
	out := productResponse{
		SKU:             string(p.SKU()),
		Description:     p.Description(),
		Version:         p.Version(),
		PhysicalProfile: toPhysicalProfile(p.PhysicalProfile(), 0),
	}
	if c, source, err := p.Classification(); err == nil {
		cr := toClassification(c, source)
		out.Classification = &cr
	}
	return out
}

// toProductClassification renders the classification part, or
// product.ErrNotClassified.
func toProductClassification(p *product.Product) (productClassificationResponse, error) {
	c, source, err := p.Classification()
	if err != nil {
		return productClassificationResponse{}, err
	}
	return productClassificationResponse{SKU: string(p.SKU()), classificationResponse: toClassification(c, source), Version: p.Version()}, nil
}

// The packaging hierarchy (ADR 0011).

// packLevelRequest is the body of PUT /products/{sku}/pack-levels/{level}.
// Pointers tell an absent field from an explicit zero.
type packLevelRequest struct {
	GTIN           string             `json:"gtin"`
	ChildGTIN      string             `json:"childGtin"`
	QtyOfChild     int                `json:"qtyOfChild"`
	UnitsPerPack   int                `json:"unitsPerPack"`
	Dims           *dimensionsRequest `json:"dims"`
	IsOrderable    bool               `json:"isOrderable"`
	IsDespatchUnit bool               `json:"isDespatchUnit"`
}

type retirePackLevelRequest struct {
	Reason string `json:"reason"`
}

type packLevelResponse struct {
	Level          string               `json:"level"`
	GTIN           string               `json:"gtin"`
	ChildGTIN      string               `json:"childGtin,omitempty"`
	QtyOfChild     int                  `json:"qtyOfChild,omitempty"`
	UnitsPerPack   int                  `json:"unitsPerPack"`
	Dims           *dimensionsResponse  `json:"dims,omitempty"`
	Measured       *measurementResponse `json:"measured,omitempty"`
	IsOrderable    bool                 `json:"isOrderable"`
	IsDespatchUnit bool                 `json:"isDespatchUnit"`
	Active         bool                 `json:"active"`
	EffectiveFrom  string               `json:"effectiveFrom"`
	EffectiveTo    string               `json:"effectiveTo,omitempty"`
}

// packLevelResource is one level with the product it belongs to (write
// responses).
type packLevelResource struct {
	SKU     string `json:"sku"`
	Version int64  `json:"version"`
	packLevelResponse
}

type packLevelListResponse struct {
	SKU     string              `json:"sku"`
	Version int64               `json:"version"`
	Items   []packLevelResponse `json:"items"`
	// NextCursor is absent on the last page.
	NextCursor string `json:"nextCursor,omitempty"`
}

type gtinResponse struct {
	GTIN          string `json:"gtin"`
	SKU           string `json:"sku"`
	Level         string `json:"level"`
	UnitsPerPack  int    `json:"unitsPerPack"`
	ChildGTIN     string `json:"childGtin,omitempty"`
	Active        bool   `json:"active"`
	EffectiveFrom string `json:"effectiveFrom"`
	EffectiveTo   string `json:"effectiveTo,omitempty"`
}

func toPackLevel(d product.PackLevelDef) packLevelResponse {
	out := packLevelResponse{
		Level: string(d.Level()), GTIN: d.GTIN().String(), ChildGTIN: d.ChildGTIN().String(), QtyOfChild: d.QtyOfChild(),
		UnitsPerPack: d.UnitsPerPack(), IsOrderable: d.IsOrderable(), IsDespatchUnit: d.IsDespatchUnit(), Active: d.Active(),
		EffectiveFrom: d.EffectiveFrom().UTC().Format(time.RFC3339Nano),
	}
	if dims, ok := d.DeclaredDimensions(); ok {
		out.Dims = toDimensions(dims)
	}
	if m, ok := d.Measurement(); ok {
		out.Measured = &measurementResponse{
			dimensionsResponse: *toDimensions(m.Dimensions()),
			MeasuredAt:         m.MeasuredAt().UTC().Format(time.RFC3339Nano),
			DeviceID:           m.DeviceID(),
		}
	}
	if to, ended := d.EffectiveTo(); ended {
		out.EffectiveTo = to.UTC().Format(time.RFC3339Nano)
	}
	return out
}

func toPackLevelResource(p *product.Product, d product.PackLevelDef) packLevelResource {
	return packLevelResource{SKU: string(p.SKU()), Version: p.Version(), packLevelResponse: toPackLevel(d)}
}

func toGTIN(res usecases.GTINResolution) gtinResponse {
	pl := toPackLevel(res.Def)
	return gtinResponse{
		GTIN: pl.GTIN, SKU: string(res.SKU), Level: pl.Level, UnitsPerPack: pl.UnitsPerPack, ChildGTIN: pl.ChildGTIN,
		Active: pl.Active, EffectiveFrom: pl.EffectiveFrom, EffectiveTo: pl.EffectiveTo,
	}
}
