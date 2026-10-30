package a2a

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// problemTypeBase namespaces this context's problem types: the SAME base URI
// the REST adapter uses, so a slug means one thing over every surface.
const problemTypeBase = "https://errors.product-master.warehouse-systems.dev/"

// problem is the RFC 7807-shaped rejection an agent client receives instead
// of a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func problemMalformedRequest(detail string) *problem {
	return &problem{Type: problemTypeBase + "malformed-skill-request", Title: "Malformed skill request", Detail: detail, Status: http.StatusBadRequest}
}

func problemUnknownSkill(skill string) *problem {
	return &problem{Type: problemTypeBase + "unknown-skill", Title: "Unknown skill", Detail: fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill), Status: http.StatusBadRequest}
}

func problemInvalidArgument(err error) *problem {
	return &problem{Type: problemTypeBase + "invalid-argument", Title: "Invalid skill argument", Detail: err.Error(), Status: http.StatusBadRequest}
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field and Summary, keeping
// the envelope flat and versioned as one contract.
type skillDTO struct {
	ProductProfile    *productProfile    `json:"productProfile,omitempty"`
	PackLevelDeclared *packLevelDeclared `json:"packLevelDeclared,omitempty"`
	summary           string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// --- problem catalogue (the REST adapter's, same status classification) ------

// catalogued is the fixed (status, slug, title) of one error category.
type catalogued struct {
	status int
	slug   string
	title  string
}

// problemCatalogue maps every typed error a skill can meet to its problem, in
// match order. It mirrors internal/adapters/inbound/http/errors.go entry for
// entry (status, slug, title) for the errors these two skills can raise, so a
// delegating agent sees the outcome a REST caller would. Inbound adapters do
// not import each other, hence the copy; TestProblemCatalogue pins each row.
var problemCatalogue = []struct {
	err error
	p   catalogued
}{
	{product.ErrInvalidSKU, catalogued{http.StatusBadRequest, "invalid-sku", "SKU is invalid"}},
	{product.ErrInvalidDimension, catalogued{http.StatusBadRequest, "invalid-dimension", "Dimension out of range"}},
	{product.ErrInvalidWeight, catalogued{http.StatusBadRequest, "invalid-weight", "Weight out of range"}},
	{product.ErrInvalidPackLevel, catalogued{http.StatusBadRequest, "invalid-pack-level", "Pack level must be EACH, INNER, CASE or PALLET"}},
	{product.ErrVariableMeasureUnsupported, catalogued{http.StatusBadRequest, "variable-measure-unsupported", "Variable measure GTINs (indicator digit 9) are not supported"}},
	{product.ErrInvalidGTIN, catalogued{http.StatusBadRequest, "invalid-gtin", "GTIN is invalid"}},
	{product.ErrInvalidPackQuantity, catalogued{http.StatusBadRequest, "invalid-pack-quantity", "Pack quantity is invalid"}},
	{product.ErrInvalidReason, catalogued{http.StatusBadRequest, "invalid-reason", "Reason must be 1 to 200 characters without control characters"}},
	{repository.ErrProductNotFound, catalogued{http.StatusNotFound, "product-not-found", "Product not found"}},
	{repository.ErrGtinNotFound, catalogued{http.StatusNotFound, "gtin-not-found", "GTIN not found"}},
	{product.ErrPackLevelNotFound, catalogued{http.StatusNotFound, "pack-level-not-found", "Pack level not found"}},
	{product.ErrGtinAlreadyAssigned, catalogued{http.StatusConflict, "gtin-already-assigned", "The GTIN already identifies a pack level"}},
	{product.ErrPackQuantityImmutable, catalogued{http.StatusConflict, "pack-quantity-immutable", "The content quantity of an active pack level cannot change; declare a new GTIN at that level"}},
	{product.ErrPackLevelOrder, catalogued{http.StatusConflict, "pack-level-order", "A pack level needs an active child level that is lower than it"}},
	{product.ErrPackDimensionsBelowContents, catalogued{http.StatusConflict, "pack-dimensions-below-contents", "The pack weighs less than its contents"}},
	{repository.ErrConcurrentModification, catalogued{http.StatusConflict, "concurrent-modification", "The resource was modified by another request; re-fetch the latest version and retry"}},
}

// --- DTOs (camelCase, the names the domain error texts already use) ----------

type dimensionsView struct {
	LengthMm  int64 `json:"lengthMm"`
	WidthMm   int64 `json:"widthMm"`
	HeightMm  int64 `json:"heightMm"`
	WeightG   int64 `json:"weightG"`
	VolumeMm3 int64 `json:"volumeMm3"`
}

type measurementView struct {
	dimensionsView
	MeasuredAt string `json:"measuredAt"`
	DeviceID   string `json:"deviceId,omitempty"`
}

type physicalProfileView struct {
	Declared        *dimensionsView  `json:"declared,omitempty"`
	Measured        *measurementView `json:"measured,omitempty"`
	Effective       *dimensionsView  `json:"effective,omitempty"`
	EffectiveSource string           `json:"effectiveSource"`
	Discrepancy     bool             `json:"discrepancy"`
}

type classificationView struct {
	HandlingTags         []string `json:"handlingTags"`
	TemperatureClass     string   `json:"temperatureClass,omitempty"`
	DOTHazardClass       *int     `json:"dotHazardClass,omitempty"`
	ClassificationSource string   `json:"classificationSource"`
}

type packLevelView struct {
	Level          string           `json:"level"`
	GTIN           string           `json:"gtin"`
	ChildGTIN      string           `json:"childGtin,omitempty"`
	QtyOfChild     int              `json:"qtyOfChild,omitempty"`
	UnitsPerPack   int              `json:"unitsPerPack"`
	Dims           *dimensionsView  `json:"dims,omitempty"`
	Measured       *measurementView `json:"measured,omitempty"`
	IsOrderable    bool             `json:"isOrderable"`
	IsDespatchUnit bool             `json:"isDespatchUnit"`
	Active         bool             `json:"active"`
	EffectiveFrom  string           `json:"effectiveFrom"`
	EffectiveTo    string           `json:"effectiveTo,omitempty"`
}

// productProfile is the DTO of the product_profile skill: the product read
// model and its packaging hierarchy side by side. IdentifiedBy is set only
// when the product was found by GTIN: the pack level that GTIN identifies.
type productProfile struct {
	SKU             string              `json:"sku"`
	Description     string              `json:"description"`
	Version         int64               `json:"version"`
	Classification  *classificationView `json:"classification,omitempty"`
	PhysicalProfile physicalProfileView `json:"physicalProfile"`
	PackLevels      []packLevelView     `json:"packLevels"`
	IdentifiedBy    *packLevelView      `json:"identifiedBy,omitempty"`
	summary         string
}

// packLevelDeclared is the DTO of the declare_pack_level skill. Created is
// true when the level is new on the product, false when an existing one was
// declared again, changed or superseded.
type packLevelDeclared struct {
	SKU       string        `json:"sku"`
	Version   int64         `json:"version"`
	Created   bool          `json:"created"`
	PackLevel packLevelView `json:"packLevel"`
	summary   string
}

// --- mapping -------------------------------------------------------------------

func toDimensions(d product.UnitDimensions) *dimensionsView {
	return &dimensionsView{LengthMm: d.LengthMm(), WidthMm: d.WidthMm(), HeightMm: d.HeightMm(), WeightG: d.WeightG(), VolumeMm3: d.VolumeMm3()}
}

func toMeasurement(m product.Measurement) *measurementView {
	return &measurementView{
		dimensionsView: *toDimensions(m.Dimensions()),
		MeasuredAt:     m.MeasuredAt().UTC().Format(time.RFC3339Nano),
		DeviceID:       m.DeviceID(),
	}
}

func toPhysicalProfile(p product.PhysicalProfile) physicalProfileView {
	out := physicalProfileView{EffectiveSource: string(p.EffectiveSource()), Discrepancy: p.Discrepancy()}
	if d, ok := p.Declared(); ok {
		out.Declared = toDimensions(d)
	}
	if m, ok := p.Measured(); ok {
		out.Measured = toMeasurement(m)
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
		out.Measured = toMeasurement(m)
	}
	if to, ended := d.EffectiveTo(); ended {
		out.EffectiveTo = to.UTC().Format(time.RFC3339Nano)
	}
	return out
}

// toProductProfile maps the aggregate into the skill DTO and its one-line
// summary. identifiedBy is the pack level a GTIN lookup resolved, or nil.
func toProductProfile(p *product.Product, identifiedBy *product.PackLevelDef) productProfile {
	out := productProfile{
		SKU: string(p.SKU()), Description: p.Description(), Version: p.Version(),
		PhysicalProfile: toPhysicalProfile(p.PhysicalProfile()),
	}
	if c, source, err := p.Classification(); err == nil {
		cv := toClassification(c, source)
		out.Classification = &cv
	}
	levels := p.PackLevels().Levels()
	out.PackLevels = make([]packLevelView, len(levels))
	for i, l := range levels {
		out.PackLevels[i] = toPackLevel(l)
	}
	if identifiedBy != nil {
		pl := toPackLevel(*identifiedBy)
		out.IdentifiedBy = &pl
	}
	out.summary = profileSummary(out)
	return out
}

func profileSummary(p productProfile) string {
	handling := "unclassified"
	if c := p.Classification; c != nil {
		handling = strings.Join(c.HandlingTags, "+")
		if c.TemperatureClass != "" {
			handling += " (" + c.TemperatureClass + ")"
		}
	}
	out := fmt.Sprintf("%s %q v%d: %s; %d pack level(s)", p.SKU, p.Description, p.Version, handling, len(p.PackLevels))
	if p.IdentifiedBy != nil {
		out += fmt.Sprintf("; GTIN %s is the %s level", p.IdentifiedBy.GTIN, p.IdentifiedBy.Level)
	}
	return out
}

func toPackLevelDeclared(res usecases.DeclarePackLevelResult) packLevelDeclared {
	out := packLevelDeclared{SKU: string(res.Product.SKU()), Version: res.Product.Version(), Created: res.Created, PackLevel: toPackLevel(res.Def)}
	verb := "declared"
	if !res.Created {
		verb = "re-declared"
	}
	out.summary = fmt.Sprintf("%s %s level (GTIN %s) on %s, version %d", verb, out.PackLevel.Level, out.PackLevel.GTIN, out.SKU, out.Version)
	return out
}

// problemFromUseCase maps a use case error to a problem answer, using the
// SAME classification as the REST adapter so a delegating agent sees the
// outcome a REST caller would. Domain rejections are EXPECTED business
// outcomes and are answered as RFC 7807-shaped problems; anything
// unrecognised is an internal failure, still answered as a problem -- status
// 500 -- so the calling agent always receives a structured result. The
// shared domain error texts name the same camelCase arguments this surface
// offers (unitsPerPack, qtyOfChild, ...), so no alias needs rewording here.
func problemFromUseCase(err error) *problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return &problem{Type: problemTypeBase + entry.p.slug, Title: entry.p.title, Detail: err.Error(), Status: entry.p.status}
		}
	}
	return problemInternal(err)
}

// problemInternal answers an unrecognised error as the REST adapter's
// internal-error problem. The error itself is logged, never shown: it may
// carry infrastructure detail (DSNs, SQL).
func problemInternal(err error) *problem {
	slog.Error("a2a skill failed", "error", err)
	return &problem{Type: problemTypeBase + "internal-error", Title: "Internal server error", Detail: "the skill failed; see server logs (trace id in the response metadata)", Status: http.StatusInternalServerError}
}
