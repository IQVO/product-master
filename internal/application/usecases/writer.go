// Package usecases holds the application's use cases. Every write follows
// the same shape inside ONE ports.UnitOfWork: load the Product, apply the
// aggregate command, save it guarded by the version that was loaded, and
// enqueue the raised events in the transactional outbox. A command that
// changes nothing raises no event, and then nothing is saved or enqueued.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/product-master/internal/application/ports"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/domain/product"
)

// Writer bundles the ports every write use case needs.
type Writer struct {
	Products ports.ProductRepository
	Outbox   ports.OutboxRepository
	Encoder  ports.EventEncoder
	UoW      ports.UnitOfWork
	Clock    ports.Clock
}

// now returns the clock's current time in UTC.
func (w Writer) now() time.Time { return w.Clock.Now().UTC() }

// persist saves p guarded by loadedVersion and enqueues events, both on the
// ctx of the surrounding unit of work. No events: nothing to do.
func (w Writer) persist(ctx context.Context, p *product.Product, loadedVersion int64, events []product.Event) error {
	if len(events) == 0 {
		return nil
	}
	if err := w.Products.Save(ctx, p, loadedVersion); err != nil {
		return err
	}
	msgs, err := w.Encoder.Encode(events...)
	if err != nil {
		return fmt.Errorf("encode events: %w", err)
	}
	if err := w.Outbox.Insert(ctx, msgs...); err != nil {
		return fmt.Errorf("enqueue events: %w", err)
	}
	return nil
}

// load returns the stored product (with its version) or a typed error.
func (w Writer) load(ctx context.Context, sku product.SKU) (*product.Product, int64, error) {
	p, err := w.Products.Get(ctx, sku)
	if err != nil {
		return nil, 0, err
	}
	return p, p.Version(), nil
}

// change runs the common "load, apply, save, enqueue" write for an
// existing product inside one unit of work and returns the product after
// the change.
func (w Writer) change(ctx context.Context, sku product.SKU, apply func(p *product.Product, now time.Time) ([]product.Event, error)) (*product.Product, error) {
	var out *product.Product
	err := w.UoW.Do(ctx, func(ctx context.Context) error {
		p, loaded, err := w.load(ctx, sku)
		if err != nil {
			return err
		}
		events, err := apply(p, w.now())
		if err != nil {
			return err
		}
		if err := w.persist(ctx, p, loaded, events); err != nil {
			return err
		}
		out = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// parseSKU validates a raw SKU.
func parseSKU(raw string) (product.SKU, error) {
	return product.NewSKU(raw)
}

// isNotFound reports whether err is the repository's not-found error.
func isNotFound(err error) bool { return errors.Is(err, repository.ErrProductNotFound) }
