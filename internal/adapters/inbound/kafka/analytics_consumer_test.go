package kafka

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/product-master/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/product-master/internal/adapters/outbound/kafka"
	"github.com/claudioed/product-master/internal/analytics/report"
	"github.com/claudioed/product-master/internal/domain/product"
)

const anTypePre = "com.warehouse.wms.product-master.product."

var anAt = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)

// analyticsWire encodes ev exactly as the OLTP service writes it to the
// analytics topic (the real AnalyticsEncoder), under the given id.
func analyticsWire(t *testing.T, id string, ev product.Event) []byte {
	t.Helper()
	msgs, err := (&outboundkafka.AnalyticsEncoder{NewID: func() string { return id }}).Encode(ev)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("encode: %d msgs, %v", len(msgs), err)
	}
	return msgs[0].Value
}

func anHeader(sku string, version int64, at time.Time) product.Header {
	return product.Header{SKU: product.SKU(sku), Version: version, At: at}
}

func anDeclared() product.UnitDimensions { return product.RehydrateUnitDimensions(200, 120, 80, 1500) }

func anMeasurement(lengthMm int64) product.Measurement {
	return product.RehydrateMeasurement(product.RehydrateUnitDimensions(lengthMm, 120, 80, 1500), anAt.Add(-time.Hour), "CUBISCAN-03")
}

// anWire builds a hand-made CloudEvent (payload variants the real encoder
// never produces).
func anWire(t *testing.T, id, typ, subject string, at time.Time, data any) []byte {
	t.Helper()
	e := ce.New(ce.CloudEventsVersionV1)
	e.SetID(id)
	e.SetSource("/warehouse/product-master")
	e.SetType(typ)
	e.SetSubject(subject)
	if !at.IsZero() {
		e.SetTime(at)
	}
	if err := e.SetData("application/json", data); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lockedBuffer is a goroutine-safe log sink.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) lines(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range strings.Split(l.b.String(), "\n") {
		if line != "" && strings.Contains(line, substr) {
			n++
		}
	}
	return n
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type fakeDLQ struct {
	mu     sync.Mutex
	msgs   []kafkago.Message
	fail   int // fail the next n writes
	closed bool
}

func (f *fakeDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("dlq broker down")
	}
	f.msgs = append(f.msgs, msgs...)
	return nil
}

func (f *fakeDLQ) Close() error { f.closed = true; return nil }

func (f *fakeDLQ) written() []kafkago.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]kafkago.Message(nil), f.msgs...)
}

// recordingProjection records every event and can fail on demand.
type recordingProjection struct {
	mu     sync.Mutex
	events []report.ProductEvent
	errs   []error // returned in order, one per call, then nil
	calls  int
}

func (r *recordingProjection) Apply(_ context.Context, e report.ProductEvent) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if len(r.errs) > 0 {
		err := r.errs[0]
		r.errs = r.errs[1:]
		if err != nil {
			return false, err
		}
	}
	r.events = append(r.events, e)
	return true, nil
}

func (r *recordingProjection) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type anHarness struct {
	consumer *AnalyticsConsumer
	dlq      *fakeDLQ
	logs     *lockedBuffer
	clockMu  sync.Mutex
	clock    time.Time
}

func newAnHarness(p report.Projection) *anHarness {
	h := &anHarness{dlq: &fakeDLQ{}, logs: &lockedBuffer{}, clock: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	h.consumer = &AnalyticsConsumer{
		Projection: p, DLQ: h.dlq, DLQTopic: "warehouse.product-master.analytics.dlq",
		Logger: slog.New(slog.NewJSONHandler(h.logs, nil)),
		Retry:  RetryPolicy{Initial: time.Millisecond, Max: 2 * time.Millisecond},
		Now:    h.now,
	}
	return h
}

func (h *anHarness) now() time.Time {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	return h.clock
}

func (h *anHarness) advance(d time.Duration) {
	h.clockMu.Lock()
	defer h.clockMu.Unlock()
	h.clock = h.clock.Add(d)
}

// run feeds values (offsets 0..n-1) through the REAL Run loop and returns
// once every one was committed.
func (h *anHarness) run(t *testing.T, values ...[]byte) *fakeReader {
	t.Helper()
	reader := &fakeReader{}
	for i, v := range values {
		reader.queue = append(reader.queue, kafkago.Message{Topic: "warehouse.product-master.analytics", Partition: 2, Offset: int64(i), Key: []byte("SKU-1"), Value: v})
	}
	h.consumer.Reader = reader
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.consumer.Run(ctx) }()
	deadline := time.After(10 * time.Second)
	for len(reader.commits()) < len(values) {
		select {
		case err := <-done:
			cancel()
			t.Fatalf("Run returned early: %v (commits %v)", err, reader.commits())
		case <-deadline:
			cancel()
			t.Fatalf("timed out: %d of %d commits", len(reader.commits()), len(values))
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	return reader
}

func TestAnalyticsConsumer_ValidEventsUpdateTheModelAndCommitInOrder(t *testing.T) {
	store := analyticsstore.NewMemory()
	h := newAnHarness(store)
	d, m := anDeclared(), anMeasurement(300)
	reader := h.run(t,
		analyticsWire(t, "e1", product.ProductRegistered{Header: anHeader("SKU-1", 1, anAt), Description: "d"}),
		analyticsWire(t, "e2", product.ProductDimensionsDeclared{Header: anHeader("SKU-1", 2, anAt.Add(time.Minute)), Profile: product.RehydratePhysicalProfile(&d, nil)}),
		analyticsWire(t, "e3", product.ProductMeasured{Header: anHeader("SKU-1", 3, anAt.Add(2*time.Minute)), Profile: product.RehydratePhysicalProfile(&d, &m)}),
	)
	days, _ := store.QualityDays(context.Background(), report.Range{From: anAt.Truncate(24 * time.Hour), To: anAt.Truncate(24 * time.Hour).Add(24 * time.Hour)})
	if len(days) != 1 || days[0].Registered != 1 || days[0].DimensionsDeclared != 1 || days[0].Measured != 1 || days[0].OpenDiscrepancies != 1 {
		t.Fatalf("days = %+v, want one registered, declared and measured product with an open discrepancy", days)
	}
	if got := reader.commits(); !reflect.DeepEqual(got, []int64{0, 1, 2}) {
		t.Fatalf("commits = %v, want each offset once, in order", got)
	}
	if n := len(h.dlq.written()); n != 0 {
		t.Fatalf("%d messages dead-lettered, want 0", n)
	}
}

func TestAnalyticsConsumer_ReplayOfTheSameIDIsANoOp(t *testing.T) {
	store := analyticsstore.NewMemory()
	h := newAnHarness(store)
	reg := analyticsWire(t, "e1", product.ProductRegistered{Header: anHeader("SKU-1", 1, anAt)})
	reader := h.run(t, reg, reg, reg)
	if c, _ := store.Coverage(context.Background()); c.Products != 1 {
		t.Fatalf("coverage = %+v", c)
	}
	if len(reader.commits()) != 3 {
		t.Fatalf("commits = %v: every duplicate must still be committed", reader.commits())
	}
}

func TestAnalyticsConsumer_UnknownTypeIsIgnoredNotDeadLettered(t *testing.T) {
	proj := &recordingProjection{}
	h := newAnHarness(proj)
	other := anWire(t, "e-other", anTypePre+"ProductDeleted", "SKU-1", anAt, map[string]any{"sku": "SKU-1", "version": 2})
	foreign := anWire(t, "e-foreign", "com.warehouse.wms.inventory-storage.product.ProductClassified", "SKU-1", anAt, map[string]any{"sku": "SKU-1"})
	h.run(t, other, foreign)
	if proj.callCount() != 0 || len(h.dlq.written()) != 0 {
		t.Fatalf("projection calls = %d, dlq = %d; unknown types are acknowledged untouched", proj.callCount(), len(h.dlq.written()))
	}
}

func TestAnalyticsConsumer_LegacyAndGarbageAreSkippedWithoutFloodingTheLog(t *testing.T) {
	proj := &recordingProjection{}
	h := newAnHarness(proj)
	legacy := []byte(`{"event_id":"e-1","event_type":"ProductClassified","occurred_at":"2026-10-05T08:00:00Z","payload":{"sku":"SKU-1"}}`)
	garbage := []byte("not json at all \x00\x01")
	values := make([][]byte, 0, 1000)
	for i := 0; i < 500; i++ {
		values = append(values, legacy, garbage)
	}
	reader := h.run(t, values...)
	if proj.callCount() != 0 || len(h.dlq.written()) != 0 {
		t.Fatalf("projection calls = %d, dlq = %d; legacy/garbage are skipped", proj.callCount(), len(h.dlq.written()))
	}
	if len(reader.commits()) != 1000 {
		t.Fatalf("committed %d of 1000 skipped messages", len(reader.commits()))
	}
	if warns := h.logs.lines("not a CloudEvents"); warns != 1 {
		t.Fatalf("%d WARN lines for 1000 skips within one interval, want exactly 1", warns)
	}
	h.advance(61 * time.Second)
	h.run(t, legacy)
	if warns := h.logs.lines("not a CloudEvents"); warns != 2 {
		t.Fatalf("%d WARN lines after the interval elapsed, want 2", warns)
	}
	if !strings.Contains(h.logs.String(), `"suppressed_since_last_warning":999`) {
		t.Fatalf("second warning does not report the 999 suppressed skips: %s", h.logs.String())
	}
}

func TestAnalyticsConsumer_TransientFailuresAreRetriedOnTheSameMessageAndNeverDeadLettered(t *testing.T) {
	proj := &recordingProjection{errs: []error{errors.New("connection reset"), errors.New("timeout"), errors.New("timeout")}}
	h := newAnHarness(proj)
	reader := h.run(t, analyticsWire(t, "e1", product.ProductRegistered{Header: anHeader("SKU-1", 1, anAt)}))
	if proj.callCount() != 4 {
		t.Fatalf("Apply called %d times, want 4 (three transient failures then success)", proj.callCount())
	}
	if got := reader.commits(); !reflect.DeepEqual(got, []int64{0}) {
		t.Fatalf("commits = %v: committed once, after success", got)
	}
	if len(h.dlq.written()) != 0 {
		t.Fatal("a transient failure must never reach the DLQ")
	}
}

func TestAnalyticsConsumer_ATransientFailureIsReturned(t *testing.T) {
	h := newAnHarness(&recordingProjection{errs: []error{errors.New("db down")}})
	err := h.consumer.HandleMessage(context.Background(), kafkago.Message{Value: analyticsWire(t, "e1", product.ProductRegistered{Header: anHeader("SKU-1", 1, anAt)})})
	if err == nil || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("HandleMessage = %v, want the transient error", err)
	}
}

func TestAnalyticsConsumer_DeterministicPoisonGoesToTheDLQWithTheRawBytes(t *testing.T) {
	profile := func(extra map[string]any) map[string]any {
		m := map[string]any{"sku": "SKU-1", "version": 3, "effective_source": "measured"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	dims := map[string]any{"length_mm": 1, "width_mm": 1, "height_mm": 1, "weight_g": 1, "volume_mm3": 1}
	cases := []struct {
		name string
		raw  []byte
	}{
		{"sku is not the subject", anWire(t, "p1", anTypePre+"ProductRegistered", "SKU-2", anAt, map[string]any{"sku": "SKU-1", "version": 1})},
		{"no sku", anWire(t, "p2", anTypePre+"ProductClassified", "SKU-1", anAt, map[string]any{"version": 1})},
		{"no version", anWire(t, "p3", anTypePre+"ProductClassified", "SKU-1", anAt, map[string]any{"sku": "SKU-1"})},
		{"version 0", anWire(t, "p4", anTypePre+"ProductRegistered", "SKU-1", anAt, map[string]any{"sku": "SKU-1", "version": 0})},
		{"no time", anWire(t, "p5", anTypePre+"ProductRegistered", "SKU-1", time.Time{}, map[string]any{"sku": "SKU-1", "version": 1})},
		{"wrong shape", anWire(t, "p6", anTypePre+"ProductRegistered", "SKU-1", anAt, map[string]any{"sku": 42, "version": 1})},
		{"profile without discrepancy", anWire(t, "p7", anTypePre+"ProductMeasured", "SKU-1", anAt, profile(map[string]any{"measured": dims}))},
		{"measured without measured", anWire(t, "p8", anTypePre+"ProductMeasured", "SKU-1", anAt, profile(map[string]any{"declared": dims, "discrepancy": false}))},
		{"declared without declared", anWire(t, "p9", anTypePre+"ProductDimensionsDeclared", "SKU-1", anAt, profile(map[string]any{"declared": nil, "discrepancy": false}))},
		{"discrepancy without declared", anWire(t, "p10", anTypePre+"ProductMeasured", "SKU-1", anAt, profile(map[string]any{"measured": dims, "discrepancy": true}))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			proj := &recordingProjection{}
			h := newAnHarness(proj)
			reader := h.run(t, tc.raw)
			if proj.callCount() != 0 {
				t.Fatal("Apply called for poison")
			}
			assertDeadLettered(t, h, reader, tc.raw)
		})
	}
}

// assertDeadLettered checks the DLQ holds exactly raw (bytes and key) with
// the x-dlq-* context of offset 0, and that the offset was committed.
func assertDeadLettered(t *testing.T, h *anHarness, reader *fakeReader, raw []byte) {
	t.Helper()
	got := h.dlq.written()
	if len(got) != 1 || !bytes.Equal(got[0].Value, raw) || string(got[0].Key) != "SKU-1" {
		t.Fatalf("dlq = %d messages; want exactly the raw poison bytes and key", len(got))
	}
	headers := map[string]string{}
	for _, hd := range got[0].Headers {
		headers[hd.Key] = string(hd.Value)
	}
	want := map[string]string{
		"x-dlq-source-topic": "warehouse.product-master.analytics", "x-dlq-source-partition": "2",
		"x-dlq-source-offset": "0", "x-dlq-failed-at": "2026-10-05T12:00:00Z",
	}
	for k, v := range want {
		if headers[k] != v {
			t.Errorf("header %s = %q, want %q", k, headers[k], v)
		}
	}
	if headers["x-dlq-error"] == "" || !reflect.DeepEqual(reader.commits(), []int64{0}) {
		t.Fatalf("dlq error = %q, commits = %v; want error context and the offset committed", headers["x-dlq-error"], reader.commits())
	}
}

func TestAnalyticsConsumer_ARejectionByTheStoreIsDeadLetteredAfterASingleAttempt(t *testing.T) {
	proj := &recordingProjection{errs: []error{report.ErrRejected}}
	h := newAnHarness(proj)
	raw := analyticsWire(t, "e1", product.ProductRegistered{Header: anHeader("SKU-1", 1, anAt)})
	h.run(t, raw)
	if proj.callCount() != 1 {
		t.Fatalf("Apply called %d times, want 1", proj.callCount())
	}
	if got := h.dlq.written(); len(got) != 1 || !bytes.Equal(got[0].Value, raw) {
		t.Fatalf("dlq = %+v", got)
	}
}

func TestAnalyticsConsumer_ADLQWriteFailureRetriesTheMessageInsteadOfLosingIt(t *testing.T) {
	h := newAnHarness(&recordingProjection{})
	h.dlq.fail = 2
	bad := anWire(t, "bad", anTypePre+"ProductRegistered", "SKU-1", anAt, map[string]any{"sku": "SKU-1"})
	reader := h.run(t, bad)
	if len(h.dlq.written()) != 1 {
		t.Fatalf("dlq = %d, want the poison written once the DLQ recovered", len(h.dlq.written()))
	}
	if got := reader.commits(); !reflect.DeepEqual(got, []int64{0}) {
		t.Fatalf("commits = %v: commit only after the DLQ write succeeded", got)
	}
}

// Every kind maps exactly as documented in ADR 0006.
func TestAnalyticsConsumer_EventMapping(t *testing.T) {
	proj := &recordingProjection{}
	h := newAnHarness(proj)
	d, m := anDeclared(), anMeasurement(205)
	cls, err := product.NewClassification([]product.HandlingTag{product.Fragile}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	h.run(t,
		analyticsWire(t, "e-reg", product.ProductRegistered{Header: anHeader("SKU-1", 1, anAt), Description: "d"}),
		analyticsWire(t, "e-desc", product.ProductDescriptionChanged{Header: anHeader("SKU-1", 2, anAt), Description: "e"}),
		analyticsWire(t, "e-cls", product.ProductClassified{Header: anHeader("SKU-1", 3, anAt), Classification: cls, Source: product.SourceNative}),
		analyticsWire(t, "e-dec", product.ProductDimensionsDeclared{Header: anHeader("SKU-1", 4, anAt), Profile: product.RehydratePhysicalProfile(&d, nil)}),
		analyticsWire(t, "e-mea", product.ProductMeasured{Header: anHeader("SKU-1", 5, anAt), Profile: product.RehydratePhysicalProfile(nil, &m)}),
	)
	base := func(kind report.Kind, id string, v int64) report.ProductEvent {
		return report.ProductEvent{Kind: kind, EventID: id, At: anAt, SKU: "SKU-1", Version: v}
	}
	dec, mea := base(report.KindDimensionsDeclared, "e-dec", 4), base(report.KindMeasured, "e-mea", 5)
	dec.Profile = &report.ProfileState{HasDeclared: true}
	mea.Profile = &report.ProfileState{HasMeasured: true}
	want := []report.ProductEvent{
		base(report.KindRegistered, "e-reg", 1), base(report.KindDescriptionChanged, "e-desc", 2),
		base(report.KindClassified, "e-cls", 3), dec, mea,
	}
	if !reflect.DeepEqual(proj.events, want) {
		t.Fatalf("events =\n%+v\nwant\n%+v", proj.events, want)
	}
}

func TestAnalyticsConsumer_CloseReleasesReaderAndDLQ(t *testing.T) {
	reader, dlq := &fakeReader{}, &fakeDLQ{}
	c := &AnalyticsConsumer{Reader: reader, DLQ: dlq}
	if err := c.Close(); err != nil || !reader.closed || !dlq.closed {
		t.Fatalf("close = %v, reader closed %v, dlq closed %v", err, reader.closed, dlq.closed)
	}
	if err := (&AnalyticsConsumer{}).Close(); err != nil {
		t.Fatalf("closing an unwired consumer = %v", err)
	}
}

func TestNewAnalyticsConsumer_DerivesTheDLQTopicAndNeverDials(t *testing.T) {
	c := NewAnalyticsConsumer([]string{"127.0.0.1:1"}, "warehouse.product-master.analytics", "g", &recordingProjection{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer func() { _ = c.Close() }()
	if c.DLQTopic != "warehouse.product-master.analytics.dlq" {
		t.Fatalf("dlq topic = %q", c.DLQTopic)
	}
	w, ok := c.DLQ.(*kafkago.Writer)
	if !ok || w.Topic != c.DLQTopic || w.RequiredAcks != kafkago.RequireAll || !w.AllowAutoTopicCreation || w.BatchTimeout != dlqBatchTimeout {
		t.Fatalf("dlq writer = %+v", c.DLQ)
	}
	if _, ok := w.Balancer.(*kafkago.Hash); !ok {
		t.Fatalf("dlq balancer = %T, want *kafkago.Hash", w.Balancer)
	}
}

func TestWriteDLQ_RetriesOnlyWhileTheTopicIsNotReady(t *testing.T) {
	notReady := &scriptedDLQ{errs: []error{kafkago.UnknownTopicOrPartition, kafkago.WriteErrors{kafkago.LeaderNotAvailable}}}
	if err := writeDLQ(context.Background(), notReady, kafkago.Message{}); err != nil || notReady.calls != 3 {
		t.Fatalf("err = %v after %d calls, want success on the 3rd", err, notReady.calls)
	}
	other := &scriptedDLQ{errs: []error{errors.New("auth failed")}}
	if err := writeDLQ(context.Background(), other, kafkago.Message{}); err == nil || other.calls != 1 {
		t.Fatalf("err = %v after %d calls, want the first non-readiness error returned at once", err, other.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stuck := &scriptedDLQ{errs: []error{kafkago.UnknownTopicOrPartition}}
	if err := writeDLQ(ctx, stuck, kafkago.Message{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
}

func TestIsTopicNotReady(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"unknown topic":     {kafkago.UnknownTopicOrPartition, true},
		"no leader":         {kafkago.LeaderNotAvailable, true},
		"write errors, all": {kafkago.WriteErrors{kafkago.LeaderNotAvailable, nil}, true},
		"write errors, mix": {kafkago.WriteErrors{kafkago.LeaderNotAvailable, errors.New("x")}, false},
		"write errors, nil": {kafkago.WriteErrors{nil}, false},
		"other":             {errors.New("x"), false},
	} {
		if got := isTopicNotReady(tc.err); got != tc.want {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// scriptedDLQ returns errs in order, then succeeds.
type scriptedDLQ struct {
	errs  []error
	calls int
}

func (s *scriptedDLQ) WriteMessages(context.Context, ...kafkago.Message) error {
	s.calls++
	if len(s.errs) > 0 {
		err := s.errs[0]
		s.errs = s.errs[1:]
		return err
	}
	return nil
}

func (s *scriptedDLQ) Close() error { return nil }
