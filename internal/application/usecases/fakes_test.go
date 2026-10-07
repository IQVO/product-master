package usecases_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/repository"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

// fakeRepo is a version-guarded in-test ProductRepository. Products are
// stored as the pointers saved (the use cases never reuse them after a
// save), and Get hands out a rehydrated copy so a command applied to a
// loaded product never mutates the stored one.
type fakeRepo struct {
	mu       sync.Mutex
	products map[product.SKU]*product.Product
	saves    []savedCall
	getErr   error
	saveErr  error
	listErr  error

	listFilter repository.ListFilter
	listAfter  product.SKU
	listLimit  int
}

type savedCall struct {
	sku           product.SKU
	loadedVersion int64
	version       int64
}

func newFakeRepo() *fakeRepo { return &fakeRepo{products: map[product.SKU]*product.Product{}} }

func clone(p *product.Product) *product.Product {
	var c *product.Classification
	var src product.ClassificationSource
	if cl, s, err := p.Classification(); err == nil {
		cc := product.RehydrateClassification(cl.Tags(), cl.TemperatureClass(), cl.DOTHazardClass())
		c, src = &cc, s
	}
	return product.Rehydrate(p.SKU(), p.Description(), c, src, p.PhysicalProfile(), p.Version())
}

func (r *fakeRepo) Get(_ context.Context, sku product.SKU) (*product.Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getErr != nil {
		return nil, r.getErr
	}
	p, ok := r.products[sku]
	if !ok {
		return nil, repository.ErrProductNotFound
	}
	return clone(p), nil
}

func (r *fakeRepo) Save(_ context.Context, p *product.Product, loaded int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.saveErr != nil {
		return r.saveErr
	}
	cur, ok := r.products[p.SKU()]
	if (loaded == 0 && ok) || (loaded != 0 && (!ok || cur.Version() != loaded)) {
		return repository.ErrConcurrentModification
	}
	r.products[p.SKU()] = clone(p)
	r.saves = append(r.saves, savedCall{sku: p.SKU(), loadedVersion: loaded, version: p.Version()})
	return nil
}

func (r *fakeRepo) List(_ context.Context, f repository.ListFilter, after product.SKU, limit int) ([]*product.Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listFilter, r.listAfter, r.listLimit = f, after, limit
	if r.listErr != nil {
		return nil, r.listErr
	}
	var out []*product.Product
	for sku, p := range r.products {
		if sku > after {
			out = append(out, clone(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKU() < out[j].SKU() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// put stores p directly (test setup).
func (r *fakeRepo) put(p *product.Product) { r.products[p.SKU()] = clone(p) }

// fakeEncoder records the events and returns one message per event.
type fakeEncoder struct {
	events []product.Event
	err    error
}

func (e *fakeEncoder) Encode(events ...product.Event) ([]outbox.Message, error) {
	if e.err != nil {
		return nil, e.err
	}
	e.events = append(e.events, events...)
	out := make([]outbox.Message, len(events))
	for i, ev := range events {
		out[i] = outbox.Message{EventType: ev.EventName(), Subject: string(ev.ProductSKU())}
	}
	return out, nil
}

type fakeOutbox struct {
	msgs []outbox.Message
	err  error
}

func (o *fakeOutbox) Insert(_ context.Context, msgs ...outbox.Message) error {
	if o.err != nil {
		return o.err
	}
	o.msgs = append(o.msgs, msgs...)
	return nil
}

func (o *fakeOutbox) types() []string {
	out := make([]string, len(o.msgs))
	for i, m := range o.msgs {
		out[i] = m.EventType
	}
	return out
}

// fakeUoW runs fn and records how many units ran and how many failed.
type fakeUoW struct {
	runs, failures int
}

func (u *fakeUoW) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	u.runs++
	if err := fn(ctx); err != nil {
		u.failures++
		return err
	}
	return nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type fakeProcessed struct {
	seen map[string]bool
	err  error
}

func (f *fakeProcessed) Claim(_ context.Context, consumer, id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	k := consumer + "/" + id
	if f.seen[k] {
		return false, nil
	}
	f.seen[k] = true
	return true, nil
}

var (
	t0      = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	errBoom = errors.New("boom")
)

// harness wires every fake into a Writer.
type harness struct {
	repo    *fakeRepo
	enc     *fakeEncoder
	outbox  *fakeOutbox
	uow     *fakeUoW
	writer  usecases.Writer
	process *fakeProcessed
}

func newHarness() *harness {
	h := &harness{repo: newFakeRepo(), enc: &fakeEncoder{}, outbox: &fakeOutbox{}, uow: &fakeUoW{}, process: &fakeProcessed{}}
	h.writer = usecases.Writer{Products: h.repo, Outbox: h.outbox, Encoder: h.enc, UoW: h.uow, Clock: fixedClock{t0}}
	return h
}

// registered stores a registered product (version 1) and returns it.
func (h *harness) registered(sku string) *product.Product {
	p, _, err := product.Register(product.SKU(sku), "desc", t0)
	if err != nil {
		panic(err)
	}
	h.repo.put(p)
	return p
}

func mustClassification(tags []product.HandlingTag, tc product.TemperatureClass, dot product.DOTHazardClass) product.Classification {
	c, err := product.NewClassification(tags, tc, dot)
	if err != nil {
		panic(err)
	}
	return c
}
