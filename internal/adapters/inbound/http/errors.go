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

// malformedRequestProblem is the shared problem of every typed error the
// adapters and the domain reject as a malformed request.
var malformedRequestProblem = problem{http.StatusBadRequest, "malformed-request", "Malformed request"}

// replayClass is what the Idempotency-Key middleware does with the answer of
// a problem type (ADR 0015). It is declared on the entry of the problem
// catalogue below; writeError tells the capturing writer, the middleware
// reads it and holds no list of slugs and no status ranges beyond 2xx and
// 5xx. A catalogue entry without a class fails
// TestEveryProblemTypeDeclaresAReplayClass.
type replayClass string

const (
	// replayUnset is the zero class: an answer nothing declared a class for.
	// It is released like RETRYABLE — the fail-safe direction.
	replayUnset replayClass = ""
	// replayFinal: the answer is a function of the request alone (or of
	// state that only ever moves one way); stored and replayed.
	replayFinal replayClass = "FINAL"
	// replayOutcome: the command committed state and then refused; the
	// writes commit with the stored answer. No problem type of this service
	// is one (ADR 0015): every write use case refuses before it persists
	// (usecases/writer.go change: apply's error returns before Save).
	replayOutcome replayClass = "OUTCOME"
	// replayRetryable: nothing committed and the answer depends on state
	// that can change; not stored, the key is released, a retry executes
	// again.
	replayRetryable replayClass = "RETRYABLE"
)

// declared reports whether c is one of the three classes.
func (c replayClass) declared() bool {
	return c == replayFinal || c == replayOutcome || c == replayRetryable
}

// stored reports whether an error answer of this class is remembered under
// its key.
func (c replayClass) stored() bool { return c == replayFinal || c == replayOutcome }

func (c replayClass) String() string {
	if c == replayUnset {
		return "UNSET"
	}
	return string(c)
}

// catalogueEntry is one typed error, its problem and the replay class of
// that problem.
type catalogueEntry struct {
	err    error
	p      problem
	replay replayClass
}

// problemCatalogue maps every typed error to its problem and its replay
// class, in match order. Slugs are the catalogue of .claude/rules/rest-api.md.
// ADR 0015 holds the reason of every class and a test keeps its table equal
// to this one; a new entry without a class fails
// TestEveryProblemTypeDeclaresAReplayClass. When unsure the class is
// RETRYABLE: a released refusal costs one more execution, a wrongly FINAL
// one pins a stale refusal for the 7-day retention.
var problemCatalogue = []catalogueEntry{
	// The body is not valid JSON for the operation: a function of the request alone (decodeJSON, response.go).
	{errMalformedRequest, malformedRequestProblem, replayFinal},
	// GET only (a collection query parameter); classified for completeness.
	{errInvalidLimit, problem{http.StatusBadRequest, "invalid-limit", "Invalid limit"}, replayFinal},
	// GET only.
	{errInvalidCursor, problem{http.StatusBadRequest, "invalid-cursor", "Invalid cursor"}, replayFinal},
	// GET only.
	{errUnknownQueryParameter, problem{http.StatusBadRequest, "unknown-query-parameter", "Unknown query parameter"}, replayFinal},
	// The list query is not valid: the query string alone (queries.go).
	{usecases.ErrInvalidListQuery, malformedRequestProblem, replayFinal},
	// The measurement body alone (physical.go NewMeasurement).
	{product.ErrMissingMeasuredAt, malformedRequestProblem, replayFinal},
	// The measurement body alone.
	{product.ErrInvalidDeviceID, malformedRequestProblem, replayFinal},
	// The SKU of the path alone (usecases parseSKU).
	{product.ErrInvalidSKU, problem{http.StatusBadRequest, "invalid-sku", "SKU is invalid"}, replayFinal},
	// The body alone (RegisterProduct).
	{product.ErrInvalidDescription, problem{http.StatusBadRequest, "invalid-description", "Description is invalid"}, replayFinal},
	// The classification body alone.
	{product.ErrNoHandlingTags, problem{http.StatusBadRequest, "no-handling-tags", "Classification requires at least one handling tag"}, replayFinal},
	// The classification body alone.
	{product.ErrUnknownHandlingTag, problem{http.StatusBadRequest, "unknown-handling-tag", "Unknown handling tag"}, replayFinal},
	// The classification body alone.
	{product.ErrDuplicateHandlingTag, problem{http.StatusBadRequest, "duplicate-handling-tag", "Duplicate handling tag"}, replayFinal},
	// The classification body alone.
	{product.ErrTemperatureClassRequired, problem{http.StatusBadRequest, "temperature-class-required", "Temperature-sensitive classification requires a temperature class"}, replayFinal},
	// The classification body alone.
	{product.ErrTemperatureClassNotApplicable, problem{http.StatusBadRequest, "temperature-class-not-applicable", "Temperature class is only meaningful when the temperature-sensitive tag is present"}, replayFinal},
	// The classification body alone.
	{product.ErrUnknownTemperatureClass, problem{http.StatusBadRequest, "unknown-temperature-class", "Unknown temperature class"}, replayFinal},
	// The classification body alone.
	{product.ErrInvalidDOTHazardClass, problem{http.StatusBadRequest, "invalid-dot-hazard-class", "DOT hazard class must be between 1 and 9"}, replayFinal},
	// The classification body alone.
	{product.ErrDOTHazardClassNotApplicable, problem{http.StatusBadRequest, "dot-hazard-class-not-applicable", "DOT hazard class is only meaningful when the hazmat tag is present"}, replayFinal},
	// The dimensions of the body alone.
	{product.ErrInvalidDimension, problem{http.StatusBadRequest, "invalid-dimension", "Dimension out of range"}, replayFinal},
	// The weight of the body alone.
	{product.ErrInvalidWeight, problem{http.StatusBadRequest, "invalid-weight", "Weight out of range"}, replayFinal},
	// measuredAt is compared with the service clock (physical.go:88), which moves: a reading
	// dated seconds in the future is valid on retry; when unsure, RETRYABLE.
	{product.ErrMeasuredAtInFuture, problem{http.StatusBadRequest, "measured-at-in-future", "Measurement time is in the future"}, replayRetryable},
	// The level of the path alone (ParsePackLevel).
	{product.ErrInvalidPackLevel, problem{http.StatusBadRequest, "invalid-pack-level", "Pack level must be EACH, INNER, CASE or PALLET"}, replayFinal},
	// The GTIN of the body alone (its indicator digit).
	{product.ErrVariableMeasureUnsupported, problem{http.StatusBadRequest, "variable-measure-unsupported", "Variable measure GTINs (indicator digit 9) are not supported"}, replayFinal},
	// The GTIN of the body alone (its check digit).
	{product.ErrInvalidGTIN, problem{http.StatusBadRequest, "invalid-gtin", "GTIN is invalid"}, replayFinal},
	// The pack quantity fields of the body alone.
	{product.ErrInvalidPackQuantity, problem{http.StatusBadRequest, "invalid-pack-quantity", "Pack quantity is invalid"}, replayFinal},
	// The reason of the retirement body alone.
	{product.ErrInvalidReason, problem{http.StatusBadRequest, "invalid-reason", "Reason must be 1 to 200 characters without control characters"}, replayFinal},
	// The product of the path is registered by another request (RegisterProduct), so it can
	// exist a moment later.
	{repository.ErrProductNotFound, problem{http.StatusNotFound, "product-not-found", "Product not found"}, replayRetryable},
	// The GTIN's pack level is declared by another request or the declare_pack_level tool,
	// so it can exist a moment later.
	{repository.ErrGtinNotFound, problem{http.StatusNotFound, "gtin-not-found", "GTIN not found"}, replayRetryable},
	// The level is declared by another request or the declare_pack_level tool; the BDD
	// scenario of the keyed retirement POST proves the retry under the same key.
	{product.ErrPackLevelNotFound, problem{http.StatusNotFound, "pack-level-not-found", "Pack level not found"}, replayRetryable},
	// The classification arrives by another request (ClassifyProduct) or event.
	{product.ErrNotClassified, problem{http.StatusNotFound, "product-classification-not-found", "Product classification not found"}, replayRetryable},
	// One-way state: the current reading's measuredAt only moves forward (RecordMeasurement
	// refuses older, corrections keep measuredAt; product.go:130-137), so the same request
	// can never stop being stale.
	{product.ErrStaleMeasurement, problem{http.StatusConflict, "stale-measurement", "A newer measurement is already recorded"}, replayFinal},
	// One-way state: a GTIN is never freed — retirement keeps the definition resolvable by
	// its GTIN ("stays resolvable", usecases/pack_levels.go:150-152) and redeclaring a
	// retired definition is refused as assigned too (packaging_declare.go:63-70); the
	// cross-SKU unique index says the same (pack_level_repository.go:254).
	{product.ErrGtinAlreadyAssigned, problem{http.StatusConflict, "gtin-already-assigned", "The GTIN already identifies a pack level"}, replayFinal},
	// One-way state: the child/quantity of an active definition never changes (that is this
	// very guard, packaging_declare.go:71-72); retiring the level turns a redeclare into
	// gtin-already-assigned, never into success.
	{product.ErrPackQuantityImmutable, problem{http.StatusConflict, "pack-quantity-immutable", "The content quantity of an active pack level cannot change; declare a new GTIN at that level"}, replayFinal},
	// Mixed slug: a missing childGtin is the request alone (packaging_declare.go:59-60) but
	// a child level not active yet is state another request clears (104-105); one class per
	// slug, so RETRYABLE; splitting is a recorded follow-up (ADR 0015).
	{product.ErrPackLevelOrder, problem{http.StatusConflict, "pack-level-order", "A pack level needs an active child level that is lower than it"}, replayRetryable},
	// The pack weight and the child's weight both move (declared dimensions, newer
	// measurements), so the check's verdict can change (packaging_declare.go:159).
	{product.ErrPackDimensionsBelowContents, problem{http.StatusConflict, "pack-dimensions-below-contents", "The pack weighs less than its contents"}, replayRetryable},
	// The optimistic-lock guard of the version-guarded Save (writer.go persist).
	{repository.ErrConcurrentModification, problem{http.StatusConflict, "concurrent-modification", "The resource was modified by another request; re-fetch the latest version and retry"}, replayRetryable},
}

// entryFor returns the first catalogue entry err matches; an unmatched error
// is the internal-error problem with no class (released like RETRYABLE).
func entryFor(err error) catalogueEntry {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry
		}
	}
	return catalogueEntry{p: internalProblem}
}
