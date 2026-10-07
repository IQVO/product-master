package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/domain/product"
)

// LegacyImportConsumer namespaces the legacy importer's claims in
// ports.ProcessedEvents.
const LegacyImportConsumer = "legacy-classification-importer"

// ErrInvalidLegacyImport marks a legacy message that can never be imported
// (bad SKU, unknown tag, broken classification invariant). It wraps the
// domain error. The consumer logs it and commits past the message.
var ErrInvalidLegacyImport = errors.New("invalid legacy classification")

// ImportOutcome reports what ImportLegacyClassification did.
type ImportOutcome string

// The import outcomes.
const (
	// ImportApplied: the classification (and, for an unknown SKU, the
	// registration) was saved and its events enqueued.
	ImportApplied ImportOutcome = "applied"
	// ImportUnchanged: nothing changed (a native classification is never
	// overwritten; an identical one is no change).
	ImportUnchanged ImportOutcome = "unchanged"
	// ImportDuplicate: this CloudEvents id was already processed.
	ImportDuplicate ImportOutcome = "duplicate"
)

// ImportLegacyClassification imports a classification authored in
// inventory-storage during the migration (ADR 0003). The claim of the
// CloudEvents id, the registration of an unknown SKU (empty description)
// and the import itself run in ONE unit of work. The aggregate decides the
// semantics: a native classification is never overwritten and an
// identical one is no change.
type ImportLegacyClassification struct {
	Writer
	ProcessedEvents ports.ProcessedEvents
}

// Handle runs the use case. A deterministic validation failure is returned
// wrapped in ErrInvalidLegacyImport before anything is claimed; any other
// error is transient (the unit of work rolled back, including the claim).
func (uc *ImportLegacyClassification) Handle(ctx context.Context, eventID, rawSKU string, tags []string, temperatureClass string, dotHazardClass int) (ImportOutcome, error) {
	sku, err := parseSKU(rawSKU)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidLegacyImport, err)
	}
	c, err := newClassification(tags, temperatureClass, dotHazardClass)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidLegacyImport, err)
	}
	var outcome ImportOutcome
	err = uc.UoW.Do(ctx, func(ctx context.Context) error {
		claimed, err := uc.ProcessedEvents.Claim(ctx, LegacyImportConsumer, eventID)
		if err != nil {
			return fmt.Errorf("claim processed event: %w", err)
		}
		if !claimed {
			outcome = ImportDuplicate
			return nil
		}
		events, p, loaded, err := uc.registerIfAbsent(ctx, sku)
		if err != nil {
			return err
		}
		events = append(events, p.ImportLegacyClassification(c, uc.now())...)
		outcome = ImportUnchanged
		if len(events) > 0 {
			outcome = ImportApplied
		}
		return uc.persist(ctx, p, loaded, events)
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}

// registerIfAbsent loads the product or registers it with an empty
// description, returning the registration events (if any) and the version
// the save must be guarded by (0 for a new product).
func (uc *ImportLegacyClassification) registerIfAbsent(ctx context.Context, sku product.SKU) ([]product.Event, *product.Product, int64, error) {
	p, loaded, err := uc.load(ctx, sku)
	if err == nil {
		return nil, p, loaded, nil
	}
	if !isNotFound(err) {
		return nil, nil, 0, err
	}
	p, events, err := product.Register(sku, "", uc.now())
	if err != nil {
		return nil, nil, 0, err
	}
	return events, p, 0, nil
}
