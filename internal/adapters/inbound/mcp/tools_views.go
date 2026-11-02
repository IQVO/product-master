package mcp

import (
	"time"

	"github.com/claudioed/product-master/internal/domain/product"
)

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

type packLevelView struct {
	Level          string           `json:"level"`
	GTIN           string           `json:"gtin"`
	ChildGTIN      string           `json:"child_gtin,omitempty"`
	QtyOfChild     int              `json:"qty_of_child,omitempty"`
	UnitsPerPack   int              `json:"units_per_pack"`
	Dims           *dimensionsView  `json:"dims,omitempty"`
	Measured       *measurementView `json:"measured,omitempty"`
	IsOrderable    bool             `json:"is_orderable"`
	IsDespatchUnit bool             `json:"is_despatch_unit"`
	Active         bool             `json:"active"`
	EffectiveFrom  string           `json:"effective_from"`
	EffectiveTo    string           `json:"effective_to,omitempty"`
}

type packLevelsOutput struct {
	SKU     string          `json:"sku"`
	Version int64           `json:"version"`
	Items   []packLevelView `json:"items"`
}

type packLevelWriteOutput struct {
	SKU       string        `json:"sku"`
	Version   int64         `json:"version"`
	PackLevel packLevelView `json:"pack_level"`
}

type gtinView struct {
	GTIN          string `json:"gtin"`
	SKU           string `json:"sku"`
	Level         string `json:"level"`
	UnitsPerPack  int    `json:"units_per_pack"`
	ChildGTIN     string `json:"child_gtin,omitempty"`
	Active        bool   `json:"active"`
	EffectiveFrom string `json:"effective_from"`
	EffectiveTo   string `json:"effective_to,omitempty"`
}

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

func toPackLevel(d product.PackLevelDef) packLevelView {
	out := packLevelView{
		Level: string(d.Level()), GTIN: d.GTIN().String(), ChildGTIN: d.ChildGTIN().String(), QtyOfChild: d.QtyOfChild(),
		UnitsPerPack: d.UnitsPerPack(), IsOrderable: d.IsOrderable(), IsDespatchUnit: d.IsDespatchUnit(), Active: d.Active(),
		EffectiveFrom: d.EffectiveFrom().UTC().Format(time.RFC3339Nano),
	}
	if dims, ok := d.DeclaredDimensions(); ok {
		out.Dims = toDimensions(dims)
	}
	if m, ok := d.Measurement(); ok {
		dv := toDimensions(m.Dimensions())
		out.Measured = &measurementView{
			LengthMm: dv.LengthMm, WidthMm: dv.WidthMm, HeightMm: dv.HeightMm, WeightG: dv.WeightG, VolumeMm3: dv.VolumeMm3,
			MeasuredAt: m.MeasuredAt().UTC().Format(time.RFC3339Nano), DeviceID: m.DeviceID(),
		}
	}
	if to, ended := d.EffectiveTo(); ended {
		out.EffectiveTo = to.UTC().Format(time.RFC3339Nano)
	}
	return out
}
