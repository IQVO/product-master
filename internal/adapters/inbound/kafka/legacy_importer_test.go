package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/application/usecases"
	"github.com/claudioed/product-master/internal/domain/product"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeReader hands out queued messages, then blocks until ctx is done.
type fakeReader struct {
	mu        sync.Mutex
	queue     []kafkago.Message
	committed []int64
	commitErr []error
	fetchErr  error
	closed    bool
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	r.mu.Lock()
	if r.fetchErr != nil {
		r.mu.Unlock()
		return kafkago.Message{}, r.fetchErr
	}
	if len(r.queue) > 0 {
		m := r.queue[0]
		r.queue = r.queue[1:]
		r.mu.Unlock()
		return m, nil
	}
	r.mu.Unlock()
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (r *fakeReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.commitErr) > 0 {
		err := r.commitErr[0]
		r.commitErr = r.commitErr[1:]
		return err
	}
	for _, m := range msgs {
		r.committed = append(r.committed, m.Offset)
	}
	return nil
}

func (r *fakeReader) Close() error { r.closed = true; return nil }

func (r *fakeReader) commits() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]int64(nil), r.committed...)
}

type importCall struct {
	id, sku string
	tags    []string
	tc      string
	dot     int
}

// fakeImporter records calls and fails the first failures calls.
type fakeImporter struct {
	mu       sync.Mutex
	calls    []importCall
	failures int
	err      error
}

func (f *fakeImporter) Handle(_ context.Context, id, sku string, tags []string, tc string, dot int) (usecases.ImportOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, importCall{id, sku, tags, tc, dot})
	if f.err != nil {
		return "", f.err
	}
	if f.failures > 0 {
		f.failures--
		return "", errors.New("db down")
	}
	return usecases.ImportApplied, nil
}

func cloudEvent(t *testing.T, id, typ string, data any) []byte {
	t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource("/warehouse/inventory-storage")
	e.SetType(typ)
	e.SetSubject("SKU-9")
	e.SetTime(time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC))
	e.SetDataSchema("urn:warehouse:inventory-storage:events:ProductClassified:v1")
	if err := e.SetData("application/json", data); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func legacy(t *testing.T, id string, data any) []byte {
	return cloudEvent(t, id, TypeLegacyProductClassified, data)
}

func TestHandleMessage_ImportsTheLegacyPayload(t *testing.T) {
	imp := &fakeImporter{}
	c := &LegacyImporter{Import: imp, Logger: quiet()}
	err := c.HandleMessage(context.Background(), legacy(t, "ev-1", map[string]any{
		"sku": "SKU-9", "handling_tags": []string{"Hazmat", "TemperatureSensitive"}, "temperature_class": "Frozen", "dot_hazard_class": 9,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := importCall{"ev-1", "SKU-9", []string{"Hazmat", "TemperatureSensitive"}, "Frozen", 9}
	if len(imp.calls) != 1 || imp.calls[0].id != want.id || imp.calls[0].sku != want.sku || imp.calls[0].tc != want.tc ||
		imp.calls[0].dot != want.dot || len(imp.calls[0].tags) != 2 || imp.calls[0].tags[1] != "TemperatureSensitive" {
		t.Fatalf("calls = %+v", imp.calls)
	}
}

func TestHandleMessage_SkipsDeterministicProblems(t *testing.T) {
	cases := map[string][]byte{
		"flat legacy envelope": []byte(`{"event_id":"1","event_type":"ProductClassified","occurred_at":"2026-10-06T20:00:00Z","payload":{"sku":"SKU-9","handling_tags":["Hazmat"]}}`),
		"not json":             []byte(`not json`),
		"other type":           cloudEvent(t, "ev-2", "com.warehouse.wms.inventory-storage.stock.StockStowed", map[string]any{"sku": "SKU-9"}),
		"short type name":      cloudEvent(t, "ev-3", "ProductClassified", map[string]any{"sku": "SKU-9", "handling_tags": []string{"Hazmat"}}),
		"own product type":     cloudEvent(t, "ev-4", "com.warehouse.wms.product-master.product.ProductClassified", map[string]any{"sku": "SKU-9", "handling_tags": []string{"Hazmat"}}),
		"malformed data":       legacy(t, "ev-5", map[string]any{"sku": 7}),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			imp := &fakeImporter{}
			if err := (&LegacyImporter{Import: imp, Logger: quiet()}).HandleMessage(context.Background(), value); err != nil {
				t.Fatalf("err = %v, want nil (skip)", err)
			}
			if len(imp.calls) != 0 {
				t.Fatalf("importer called for a message it must ignore: %+v", imp.calls)
			}
		})
	}
}

// The real use case decides validity; its ErrInvalidLegacyImport is a skip.
func TestHandleMessage_InvalidClassificationsAreSkippedNotRetried(t *testing.T) {
	products, ob, processed := memory.NewProductRepo(), memory.NewOutboxRepo(), memory.NewProcessedEventRepo()
	uc := &usecases.ImportLegacyClassification{
		Writer: usecases.Writer{
			Products: products, Outbox: ob, Encoder: &stubEncoder{}, UoW: memory.NewUnitOfWork(products, ob, processed),
			Clock: fixedClock{},
		},
		ProcessedEvents: processed,
	}
	c := &LegacyImporter{Import: uc, Logger: quiet()}
	for name, data := range map[string]map[string]any{
		"bad sku":          {"sku": "bad sku", "handling_tags": []string{"Hazmat"}},
		"unknown tag":      {"sku": "SKU-9", "handling_tags": []string{"Sharp"}},
		"no tags":          {"sku": "SKU-9"},
		"broken invariant": {"sku": "SKU-9", "handling_tags": []string{"Fragile"}, "dot_hazard_class": 3},
	} {
		t.Run(name, func(t *testing.T) {
			if err := c.HandleMessage(context.Background(), legacy(t, "ev-"+name, data)); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
	if _, err := products.Get(context.Background(), "SKU-9"); err == nil {
		t.Fatal("an invalid legacy message must not register the product")
	}
	if err := c.HandleMessage(context.Background(), legacy(t, "ev-ok", map[string]any{"sku": "SKU-9", "handling_tags": []string{"Hazmat"}})); err != nil {
		t.Fatal(err)
	}
	p, err := products.Get(context.Background(), "SKU-9")
	if err != nil || !p.IsClassified() {
		t.Fatalf("valid import: %v %v", p, err)
	}
	if !processed.Has(usecases.LegacyImportConsumer, "ev-ok") {
		t.Fatal("the CloudEvents id must be claimed")
	}
}

func TestHandleMessage_TransientErrorIsReturned(t *testing.T) {
	imp := &fakeImporter{err: errors.New("db down")}
	err := (&LegacyImporter{Import: imp, Logger: quiet()}).HandleMessage(context.Background(), legacy(t, "ev-1", map[string]any{"sku": "SKU-9", "handling_tags": []string{"Hazmat"}}))
	if err == nil {
		t.Fatal("a transient failure must be returned so the loop retries")
	}
}

func TestRun_RetriesTheSameMessageThenCommitsInOrder(t *testing.T) {
	reader := &fakeReader{
		queue: []kafkago.Message{
			{Offset: 10, Value: []byte(`{"event_id":"flat"}`)},
			{Offset: 11, Value: legacy(t, "ev-1", map[string]any{"sku": "SKU-9", "handling_tags": []string{"Hazmat"}})},
		},
	}
	imp := &fakeImporter{failures: 2}
	var mu sync.Mutex
	var slept []time.Duration
	c := &LegacyImporter{Reader: reader, Import: imp, Logger: quiet(), sleep: func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		slept = append(slept, d)
		return nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitFor(t, func() bool { return len(reader.commits()) == 2 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if got := reader.commits(); got[0] != 10 || got[1] != 11 {
		t.Fatalf("commits = %v, want [10 11]", got)
	}
	if len(imp.calls) != 3 || imp.calls[0].id != "ev-1" || imp.calls[2].id != "ev-1" {
		t.Fatalf("the same message must be retried until it succeeds: %+v", imp.calls)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(slept) != 2 || slept[0] != DefaultRetryInitial || slept[1] != 2*DefaultRetryInitial {
		t.Fatalf("backoff = %v", slept)
	}
}

func TestRun_RetriesAFailedCommit(t *testing.T) {
	reader := &fakeReader{
		queue:     []kafkago.Message{{Offset: 3, Value: []byte(`x`)}},
		commitErr: []error{errors.New("coordinator moved")},
	}
	c := &LegacyImporter{Reader: reader, Import: &fakeImporter{}, Logger: quiet(), sleep: func(context.Context, time.Duration) error { return nil }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitFor(t, func() bool { return len(reader.commits()) == 1 })
	cancel()
	<-done
}

func TestRun_StopsOnFetchErrorAndOnCancelDuringBackoff(t *testing.T) {
	boom := errors.New("broker gone")
	if err := (&LegacyImporter{Reader: &fakeReader{fetchErr: boom}, Import: &fakeImporter{}}).Run(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Run = %v", err)
	}

	reader := &fakeReader{queue: []kafkago.Message{{Offset: 1, Value: legacy(t, "ev-1", map[string]any{"sku": "SKU-9", "handling_tags": []string{"Hazmat"}})}}}
	ctx, cancel := context.WithCancel(context.Background())
	c := &LegacyImporter{Reader: reader, Import: &fakeImporter{err: errors.New("db down")}, Logger: quiet(),
		sleep: func(context.Context, time.Duration) error { cancel(); return context.Canceled }}
	if err := c.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if len(reader.commits()) != 0 {
		t.Fatal("a message that never succeeded must never be committed")
	}
	if err := c.Close(); err != nil || !reader.closed {
		t.Fatal("Close must close the reader")
	}
}

func TestRetryPolicyAndSleep(t *testing.T) {
	p := RetryPolicy{}.withDefaults()
	if p.Initial != DefaultRetryInitial || p.Max != DefaultRetryMax {
		t.Fatalf("defaults = %+v", p)
	}
	if got := p.next(4 * time.Second); got != DefaultRetryMax {
		t.Fatalf("next(4s) = %v, want the cap", got)
	}
	if got := (RetryPolicy{Initial: time.Millisecond, Max: time.Hour}).withDefaults().next(time.Second); got != 2*time.Second {
		t.Fatalf("next(1s) = %v", got)
	}
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleep on a cancelled ctx = %v", err)
	}
}

func TestNewLegacyImporter_UsesTheGivenGroupAndTopic(t *testing.T) {
	c := NewLegacyImporter([]string{"127.0.0.1:1"}, "group-from-env", &fakeImporter{}, nil)
	r, ok := c.Reader.(*kafkago.Reader)
	if !ok {
		t.Fatalf("reader = %T", c.Reader)
	}
	cfg := r.Config()
	if cfg.Topic != LegacyTopic || cfg.GroupID != "group-from-env" || cfg.CommitInterval != 0 {
		t.Fatalf("config = topic %q group %q commitInterval %v", cfg.Topic, cfg.GroupID, cfg.CommitInterval)
	}
	_ = c.Close()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 5s")
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 10, 6, 21, 0, 0, 0, time.UTC) }

// stubEncoder is enough for the use case: the encoder's wire format is
// tested in the outbound adapter.
type stubEncoder struct{}

func (stubEncoder) Encode(events ...product.Event) ([]outbox.Message, error) {
	return make([]outbox.Message, len(events)), nil
}
