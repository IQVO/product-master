package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// problemBaseURI is the namespace of this service's RFC 7807 `type` URIs.
const problemBaseURI = "https://errors.product-master.warehouse-systems.dev/"

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, missing required field).
var errMalformedRequest = errors.New("request body is not valid JSON for this operation")

// problem is the fixed (status, slug, title) of one error category; the
// detail comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// problemCatalogue maps every typed error to its problem, in match order.
// Slugs are the catalogue of .claude/rules/rest-api.md.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{usecases.ErrInvalidListQuery, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{product.ErrMissingMeasuredAt, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{product.ErrInvalidDeviceID, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{product.ErrInvalidSKU, problem{http.StatusBadRequest, "invalid-sku", "SKU is invalid"}},
	{product.ErrInvalidDescription, problem{http.StatusBadRequest, "invalid-description", "Description is invalid"}},
	{product.ErrNoHandlingTags, problem{http.StatusBadRequest, "no-handling-tags", "Classification requires at least one handling tag"}},
	{product.ErrUnknownHandlingTag, problem{http.StatusBadRequest, "unknown-handling-tag", "Unknown handling tag"}},
	{product.ErrDuplicateHandlingTag, problem{http.StatusBadRequest, "duplicate-handling-tag", "Duplicate handling tag"}},
	{product.ErrTemperatureClassRequired, problem{http.StatusBadRequest, "temperature-class-required", "Temperature-sensitive classification requires a temperature class"}},
	{product.ErrTemperatureClassNotApplicable, problem{http.StatusBadRequest, "temperature-class-not-applicable", "Temperature class is only meaningful when the temperature-sensitive tag is present"}},
	{product.ErrUnknownTemperatureClass, problem{http.StatusBadRequest, "unknown-temperature-class", "Unknown temperature class"}},
	{product.ErrInvalidDOTHazardClass, problem{http.StatusBadRequest, "invalid-dot-hazard-class", "DOT hazard class must be between 1 and 9"}},
	{product.ErrDOTHazardClassNotApplicable, problem{http.StatusBadRequest, "dot-hazard-class-not-applicable", "DOT hazard class is only meaningful when the hazmat tag is present"}},
	{product.ErrInvalidDimension, problem{http.StatusBadRequest, "invalid-dimension", "Dimension out of range"}},
	{product.ErrInvalidWeight, problem{http.StatusBadRequest, "invalid-weight", "Weight out of range"}},
	{product.ErrMeasuredAtInFuture, problem{http.StatusBadRequest, "measured-at-in-future", "Measurement time is in the future"}},
	{product.ErrInvalidPackLevel, problem{http.StatusBadRequest, "invalid-pack-level", "Pack level must be EACH, INNER, CASE or PALLET"}},
	{product.ErrVariableMeasureUnsupported, problem{http.StatusBadRequest, "variable-measure-unsupported", "Variable measure GTINs (indicator digit 9) are not supported"}},
	{product.ErrInvalidGTIN, problem{http.StatusBadRequest, "invalid-gtin", "GTIN is invalid"}},
	{product.ErrInvalidPackQuantity, problem{http.StatusBadRequest, "invalid-pack-quantity", "Pack quantity is invalid"}},
	{product.ErrInvalidReason, problem{http.StatusBadRequest, "invalid-reason", "Reason must be 1 to 200 characters without control characters"}},
	{repository.ErrProductNotFound, problem{http.StatusNotFound, "product-not-found", "Product not found"}},
	{repository.ErrGtinNotFound, problem{http.StatusNotFound, "gtin-not-found", "GTIN not found"}},
	{product.ErrPackLevelNotFound, problem{http.StatusNotFound, "pack-level-not-found", "Pack level not found"}},
	{product.ErrNotClassified, problem{http.StatusNotFound, "product-classification-not-found", "Product classification not found"}},
	{product.ErrStaleMeasurement, problem{http.StatusConflict, "stale-measurement", "A newer measurement is already recorded"}},
	{product.ErrGtinAlreadyAssigned, problem{http.StatusConflict, "gtin-already-assigned", "The GTIN already identifies a pack level"}},
	{product.ErrPackQuantityImmutable, problem{http.StatusConflict, "pack-quantity-immutable", "The content quantity of an active pack level cannot change; declare a new GTIN at that level"}},
	{product.ErrPackLevelOrder, problem{http.StatusConflict, "pack-level-order", "A pack level needs an active child level that is lower than it"}},
	{product.ErrPackDimensionsBelowContents, problem{http.StatusConflict, "pack-dimensions-below-contents", "The pack weighs less than its contents"}},
	{repository.ErrConcurrentModification, problem{http.StatusConflict, "concurrent-modification", "The resource was modified by another request; re-fetch the latest version and retry"}},
}

// problemFor returns the problem of the first catalogue entry err matches,
// or the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
