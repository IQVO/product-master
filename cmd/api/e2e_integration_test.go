//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The end-to-end proof that the shipped binary is really wired: the test boots
// the REAL entry point (run, in-process) against testcontainers Postgres and
// Kafka with nothing but the environment a deployment sets, and drives it only
// through REST. It reads the effects from Postgres and from the produced
// topics (the integration topic and the analytics topic), and finally takes
// the dependencies away to watch /readyz follow them.

const (
	topicEvents    = "warehouse.product-master.events"
	topicAnalytics = "warehouse.product-master.analytics"

	source     = "/warehouse/product-master"
	typePrefix = "com.warehouse.wms.product-master.product."
)

// containerStartTimeout bounds a testcontainers start.
const containerStartTimeout = 120 * time.Second

func startPostgres(t *testing.T) (*tcpostgres.PostgresContainer, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("product_master_e2e"), tcpostgres.WithUsername("e2e"), tcpostgres.WithPassword("e2e"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres dsn: %v", err)
	}
	return c, url
}

func startKafka(t *testing.T) (*tckafka.KafkaContainer, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), containerStartTimeout)
	defer cancel()
	c, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.6.1", tckafka.WithClusterID("product-master-e2e"))
	if err != nil {
		t.Fatalf("start kafka: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	brokers, err := c.Brokers(ctx)
	if err != nil {
		t.Fatalf("kafka brokers: %v", err)
	}
	return c, brokers
}

// topicExists asks the broker for the topic through a metadata request that
// cannot create it (kafka-go's Client.Metadata never auto-creates).
func topicExists(t *testing.T, brokers []string, topic string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := (&kafkago.Client{Addr: kafkago.TCP(brokers...)}).Metadata(ctx, &kafkago.MetadataRequest{Topics: []string{topic}})
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	for _, tp := range resp.Topics {
		if tp.Name == topic && tp.Error == nil {
			return true
		}
	}
	return false
}

// createTopic creates a one-partition topic and waits for its leader.
func createTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()
	conn, err := kafkago.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	if err := conn.CreateTopics(kafkago.TopicConfig{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if parts, err := conn.ReadPartitions(topic); err == nil && len(parts) == 1 && parts[0].Leader.ID != 0 {
			return
		}
	}
	t.Fatalf("topic %s never got a leader", topic)
}

// consume reads exactly n messages of the one-partition topic from the start,
// bounded by a deadline.
func consume(t *testing.T, brokers []string, topic string, n int) []kafkago.Message {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer r.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	var out []kafkago.Message
	for len(out) < n {
		m, err := r.ReadMessage(ctx)
		if err != nil {
			t.Fatalf("read %s: got %d of %d messages: %v", topic, len(out), n, err)
		}
		out = append(out, m)
	}
	return out
}

// noMoreMessages fails if the topic holds more than want messages after a
// settling period (nothing was duplicated).
func noMoreMessages(t *testing.T, brokers []string, topic string, want int) {
	t.Helper()
	r := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: 200 * time.Millisecond})
	defer r.Close()
	if err := r.SetOffset(kafkago.FirstOffset); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	got := 0
	for {
		if _, err := r.ReadMessage(ctx); err != nil {
			break
		}
		got++
	}
	if got != want {
		t.Fatalf("%s holds %d messages, want exactly %d", topic, got, want)
	}
}

// service is one running instance of the real entry point.
type service struct {
	t      *testing.T
	base   string
	cancel context.CancelFunc
	done   chan error
}

// env is the environment a deployment sets.
type env struct {
	dbURL   string
	brokers []string
	addr    string
}

func newEnv(t *testing.T, dbURL string, brokers []string) env {
	t.Helper()
	return env{dbURL: dbURL, brokers: brokers, addr: freeAddr(t)}
}

func (e env) apply(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", e.dbURL)
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", strings.Join(e.brokers, ","))
	t.Setenv("EVENT_PUBLISHER", "kafka")
	t.Setenv("HTTP_ADDR", e.addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "1s")
	t.Setenv("LOG_LEVEL", "warn")
}

// start boots run in-process and waits until the listener answers /healthz.
func (e env) start(t *testing.T) *service {
	t.Helper()
	e.apply(t)
	ctx, cancel := context.WithCancel(context.Background())
	s := &service{t: t, base: "http://" + e.addr, cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- run(ctx) }()
	t.Cleanup(s.stopQuietly)
	waitFor(t, 90*time.Second, "the service to listen on "+e.addr, func() bool {
		select {
		case err := <-s.done:
			t.Fatalf("run returned while booting: %v", err)
		default:
		}
		status, _ := s.try(http.MethodGet, "/healthz", "")
		return status == http.StatusOK
	})
	return s
}

// stop cancels run and requires a clean shutdown within a bound.
func (s *service) stop() {
	s.t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil {
			s.t.Fatalf("shutdown returned %v", err)
		}
	case <-time.After(60 * time.Second):
		s.t.Fatal("run did not return after the shutdown")
	}
	s.done <- nil // let the cleanup find it stopped
}

func (s *service) stopQuietly() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(60 * time.Second):
	}
}

// try performs a request and returns -1 when the connection fails.
func (s *service) try(method, path, body string) (int, string) {
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		return -1, err.Error()
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return -1, err.Error()
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (s *service) call(method, path, body string, want int) string {
	s.t.Helper()
	status, raw := s.try(method, path, body)
	if status != want {
		s.t.Fatalf("%s %s = %d %s, want %d", method, path, status, raw, want)
	}
	return raw
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// pgView reads the service's own database.
type pgView struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func newPGView(t *testing.T, url string) pgView {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pgView{t: t, pool: pool}
}

func (v pgView) count(query string, args ...any) int {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	if err := v.pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		v.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// The CloudEvents context attributes of a published message, plus its data.
type envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            string          `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	DataSchema      string          `json:"dataschema"`
	Data            json.RawMessage `json:"data"`
}

func decodeEnvelope(t *testing.T, m kafkago.Message) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(m.Value, &e); err != nil {
		t.Fatalf("not a CloudEvent: %v: %s", err, m.Value)
	}
	return e
}

// assertEnvelope checks every context attribute of a message of this service.
func assertEnvelope(t *testing.T, m kafkago.Message, eventName, stream, sku string) envelope {
	t.Helper()
	e := decodeEnvelope(t, m)
	schema := fmt.Sprintf("urn:warehouse:product-master:%s:%s:v1", stream, eventName)
	if e.SpecVersion != "1.0" || e.Type != typePrefix+eventName || e.Source != source ||
		e.DataSchema != schema || e.DataContentType != "application/json" || e.Subject != sku || e.ID == "" {
		t.Fatalf("envelope = %+v, want 1.0 %s%s from %s (%s) about %s", e, typePrefix, eventName, source, schema, sku)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
		t.Fatalf("time %q is not RFC 3339: %v", e.Time, err)
	}
	if string(m.Key) != sku {
		t.Fatalf("kafka key = %q, want the SKU %q", m.Key, sku)
	}
	var ct string
	for _, h := range m.Headers {
		if h.Key == "content-type" {
			ct = string(h.Value)
		}
	}
	if ct != "application/cloudevents+json; charset=UTF-8" {
		t.Fatalf("content-type header = %q", ct)
	}
	return e
}

// publishedNames lists the event names of the messages in order.
func publishedNames(t *testing.T, msgs []kafkago.Message) []string {
	t.Helper()
	names := make([]string, len(msgs))
	for i, m := range msgs {
		names[i] = strings.TrimPrefix(decodeEnvelope(t, m).Type, typePrefix)
	}
	return names
}

func readyz(s *service) (int, string) { return s.try(http.MethodGet, "/readyz", "") }

func waitReadyzStatus(t *testing.T, s *service, want int, what string) string {
	t.Helper()
	var body string
	waitFor(t, 60*time.Second, what, func() bool {
		var status int
		status, body = readyz(s)
		return status == want
	})
	return body
}

// Phase 1: the topics do not exist yet (a fresh cluster before anything
// published). Readiness needs Postgres plus a broker answering metadata, NOT
// topics, so the service is ready at once, and it creates no topic itself.
func phaseTopicsMissing(t *testing.T, s *service, brokers []string) {
	t.Helper()
	body := waitReadyzStatus(t, s, http.StatusOK, "/readyz to be 200 although no topic exists")
	if !strings.Contains(body, "ready") {
		t.Fatalf("readyz = %s", body)
	}
	for _, topic := range []string{topicEvents, topicAnalytics} {
		if topicExists(t, brokers, topic) {
			t.Fatalf("topic %s exists: the service must never create topics itself", topic)
		}
	}
	// The topics appear as the cluster broker would create them on first write.
	createTopic(t, brokers, topicEvents)
	createTopic(t, brokers, topicAnalytics)
	if status, body := readyz(s); status != http.StatusOK {
		t.Fatalf("readyz after the topics appeared = %d %s, want 200", status, body)
	}
}

// Phase 2: the write API, driven only through REST. Four occurrences.
func phaseWrites(t *testing.T, s *service) {
	t.Helper()
	measuredAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	s.call(http.MethodPut, "/products/SKU-1", `{"description":"Frozen peas"}`, http.StatusCreated)
	s.call(http.MethodPut, "/products/SKU-1/classification",
		`{"handlingTags":["TemperatureSensitive","Fragile"],"temperatureClass":"Frozen"}`, http.StatusCreated)
	s.call(http.MethodPut, "/products/SKU-1/dimensions/declared",
		`{"lengthMm":200,"widthMm":100,"heightMm":50,"weightG":500}`, http.StatusOK)
	s.call(http.MethodPut, "/products/SKU-1/dimensions/measured",
		fmt.Sprintf(`{"lengthMm":201,"widthMm":100,"heightMm":50,"weightG":500,"measuredAt":%q,"deviceId":"CUBISCAN-03"}`, measuredAt), http.StatusOK)

	var got struct {
		SKU            string `json:"sku"`
		Description    string `json:"description"`
		Version        int64  `json:"version"`
		Classification struct {
			TemperatureClass string `json:"temperatureClass"`
		} `json:"classification"`
		PhysicalProfile struct {
			EffectiveSource string `json:"effectiveSource"`
		} `json:"physicalProfile"`
	}
	if err := json.Unmarshal([]byte(s.call(http.MethodGet, "/products/SKU-1", "", http.StatusOK)), &got); err != nil {
		t.Fatal(err)
	}
	if got.SKU != "SKU-1" || got.Description != "Frozen peas" || got.Version != 4 ||
		got.Classification.TemperatureClass != "Frozen" || got.PhysicalProfile.EffectiveSource != "measured" {
		t.Fatalf("GET /products/SKU-1 = %+v", got)
	}
}

// Phase 3: the rows are in Postgres, and the relay drained every one of them.
func phaseRows(t *testing.T, db pgView) {
	t.Helper()
	if n := db.count(`SELECT count(*) FROM products WHERE sku = 'SKU-1' AND version = 4`); n != 1 {
		t.Fatalf("products rows for SKU-1 at version 4 = %d, want 1", n)
	}
	// Four occurrences, each written twice in the same transaction: the
	// integration topic and the analytics topic.
	if n := db.count(`SELECT count(*) FROM outbox_events`); n != 8 {
		t.Fatalf("outbox rows = %d, want 8", n)
	}
	waitFor(t, 60*time.Second, "the relay to publish every outbox row", func() bool {
		return db.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`) == 0
	})
	if n := db.count(`SELECT count(*) FROM outbox_events WHERE topic = $1`, topicEvents); n != 4 {
		t.Fatalf("outbox rows for %s = %d, want 4", topicEvents, n)
	}
}

// Phase 4: the CloudEvents on both output topics, byte-exact envelope.
func phaseTopics(t *testing.T, brokers []string) {
	t.Helper()
	want := []string{"ProductRegistered", "ProductClassified", "ProductDimensionsDeclared", "ProductMeasured"}
	for _, tc := range []struct{ topic, stream string }{{topicEvents, "events"}, {topicAnalytics, "analytics"}} {
		msgs := consume(t, brokers, tc.topic, len(want))
		for i, m := range msgs {
			assertEnvelope(t, m, want[i], tc.stream, "SKU-1")
		}
		if got := publishedNames(t, msgs); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s carries %v, want %v", tc.topic, got, want)
		}
	}
	// One occurrence, one CloudEvents id on both topics.
	ev, an := consume(t, brokers, topicEvents, 4), consume(t, brokers, topicAnalytics, 4)
	for i := range ev {
		if decodeEnvelope(t, ev[i]).ID != decodeEnvelope(t, an[i]).ID {
			t.Fatalf("occurrence %d has different ids on the two topics", i)
		}
	}
	var reg struct {
		SKU         string `json:"sku"`
		Description string `json:"description"`
		Version     int64  `json:"version"`
	}
	if err := json.Unmarshal(decodeEnvelope(t, ev[0]).Data, &reg); err != nil || reg.SKU != "SKU-1" || reg.Description != "Frozen peas" || reg.Version != 1 {
		t.Fatalf("ProductRegistered data = %+v (%v)", reg, err)
	}
}

// Phase 5: repeating a write that changes nothing adds no row and no event.
func phaseReplay(t *testing.T, s *service, db pgView, brokers []string) {
	t.Helper()
	s.call(http.MethodPut, "/products/SKU-1", `{"description":"Frozen peas"}`, http.StatusOK)
	s.call(http.MethodPut, "/products/SKU-1/classification",
		`{"handlingTags":["TemperatureSensitive","Fragile"],"temperatureClass":"Frozen"}`, http.StatusOK)
	if n := db.count(`SELECT count(*) FROM outbox_events`); n != 8 {
		t.Fatalf("outbox rows after the replay = %d, want still 8", n)
	}
	noMoreMessages(t, brokers, topicEvents, 4)
}

// Phase 6: restart the real entry point in-process on the same database and
// broker: nothing is lost, nothing is published twice.
func phaseRestart(t *testing.T, e env, s *service, db pgView, brokers []string) *service {
	t.Helper()
	s.stop()
	s2 := e.start(t)
	var got struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal([]byte(s2.call(http.MethodGet, "/products/SKU-1", "", http.StatusOK)), &got); err != nil || got.Version != 4 {
		t.Fatalf("after the restart SKU-1 = %+v (%v), want version 4", got, err)
	}
	s2.call(http.MethodPut, "/products/SKU-2", `{"description":"Mug"}`, http.StatusCreated)
	waitFor(t, 60*time.Second, "the relay to publish SKU-2 after the restart", func() bool {
		return db.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`) == 0
	})
	msgs := consume(t, brokers, topicEvents, 5)
	ids := map[string]bool{}
	for _, m := range msgs {
		ids[decodeEnvelope(t, m).ID] = true
	}
	if len(ids) != 5 {
		t.Fatalf("%d distinct CloudEvents ids over 5 messages: an event was published twice", len(ids))
	}
	noMoreMessages(t, brokers, topicEvents, 5)
	assertEnvelope(t, msgs[4], "ProductRegistered", "events", "SKU-2")
	return s2
}

// Phase 7: /readyz follows the dependencies. Postgres goes first (Kafka still
// up): only postgres is named. Then Kafka goes: kafka is named too.
// /healthz stays liveness and answers 200 throughout.
func phaseDependenciesGoAway(t *testing.T, s *service, pg *tcpostgres.PostgresContainer, kc *tckafka.KafkaContainer) {
	t.Helper()
	stopTimeout := 10 * time.Second
	if err := pg.Stop(context.Background(), &stopTimeout); err != nil {
		t.Fatalf("stop postgres: %v", err)
	}
	body := waitReadyzStatus(t, s, http.StatusServiceUnavailable, "/readyz to turn 503 once Postgres is gone")
	if !strings.Contains(body, "dependency-unavailable") || !strings.Contains(body, "postgres") || strings.Contains(body, "kafka") {
		t.Fatalf("readyz with Postgres down = %s, want dependency-unavailable naming only postgres", body)
	}
	if err := kc.Stop(context.Background(), &stopTimeout); err != nil {
		t.Fatalf("stop kafka: %v", err)
	}
	waitFor(t, 60*time.Second, "/readyz to name kafka as well", func() bool {
		_, body := readyz(s)
		return strings.Contains(body, "postgres") && strings.Contains(body, "kafka")
	})
	if status, body := s.try(http.MethodGet, "/healthz", ""); status != http.StatusOK {
		t.Fatalf("healthz with every dependency down = %d %s, want 200 (liveness only)", status, body)
	}
}

func TestRunOnRealPostgresAndKafka(t *testing.T) {
	pg, dbURL := startPostgres(t)
	kc, brokers := startKafka(t)
	e := newEnv(t, dbURL, brokers)
	db := newPGView(t, dbURL)

	s := e.start(t)
	phaseTopicsMissing(t, s, brokers)
	phaseWrites(t, s)
	phaseRows(t, db)
	phaseTopics(t, brokers)
	phaseReplay(t, s, db, brokers)
	s = phaseRestart(t, e, s, db, brokers)
	phaseDependenciesGoAway(t, s, pg, kc)
}
