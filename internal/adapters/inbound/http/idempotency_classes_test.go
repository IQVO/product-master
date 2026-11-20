package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/claudioed/product-master/internal/adapters/outbound/memory"
	"github.com/claudioed/product-master/internal/application/idempotency"
)

// The replay classes (ADR 0015): every problem type declares what a retry
// under the same Idempotency-Key gets, and the middleware acts on the class
// and on nothing else. These tests need no database: the middleware's choice
// is driven over the in-memory store; the Postgres half is
// idempotency_replay_classes_integration_test.go and
// retryable_replay_integration_test.go.

// replayRoute is the one keyed route of the service.
const replayRoute = "/products/SKU-1/pack-levels/INNER/retirements"

func TestEveryProblemTypeDeclaresAReplayClass(t *testing.T) {
	if len(problemCatalogue) == 0 {
		t.Fatal("the catalogue is empty: the test would pass on nothing")
	}
	classOf := map[string]replayClass{}
	for _, e := range problemCatalogue {
		t.Run(fmt.Sprintf("%s/%d", e.p.slug, e.p.status), func(t *testing.T) {
			if !e.replay.declared() {
				t.Errorf("problem type %q (%d) declares no replay class (%s): set FINAL, OUTCOME or RETRYABLE on its catalogue entry (.claude/rules/fleet/idempotency-and-outbox.md)", e.p.slug, e.p.status, e.replay)
			}
			if e.p.status >= http.StatusInternalServerError && e.replay != replayRetryable {
				t.Errorf("problem type %q answers %d: a 5xx is never stored, so its class is RETRYABLE, got %s", e.p.slug, e.p.status, e.replay)
			}
			if prev, seen := classOf[e.p.slug]; seen && prev != e.replay {
				t.Errorf("slug %q is declared %s and %s: one problem type has exactly one class", e.p.slug, prev, e.replay)
			}
			classOf[e.p.slug] = e.replay
		})
	}
}

func TestTheUnclassifiedEntryCheckBites(t *testing.T) {
	for _, c := range []replayClass{replayUnset, "final", "STORED", "RETRYABLE "} {
		if c.declared() {
			t.Errorf("class %q is accepted; only FINAL, OUTCOME and RETRYABLE are classes", c)
		}
	}
	for c, name := range map[replayClass]string{replayFinal: "FINAL", replayOutcome: "OUTCOME", replayRetryable: "RETRYABLE"} {
		if !c.declared() || c.String() != name {
			t.Errorf("class %q = %s declared=%v, want %s declared", c, c, c.declared(), name)
		}
	}
	if replayUnset.String() != "UNSET" {
		t.Errorf("the zero class reads %q, want UNSET", replayUnset)
	}
}

// writeError is the one production error writer: it must tell the capturing
// writer the class of the entry it answers with, and nothing for a 5xx.
func TestWriteErrorMarksTheCaptureWithTheClassOfItsEntry(t *testing.T) {
	for _, e := range problemCatalogue {
		rec := newCapture()
		writeError(rec, httptest.NewRequest(http.MethodPost, replayRoute, nil), fmt.Errorf("%w: detail", e.err))
		if rec.class != e.replay || rec.status != e.p.status {
			t.Errorf("%s: capture holds %s/%d, want %s/%d", e.p.slug, rec.class, rec.status, e.replay, e.p.status)
		}
	}
	rec := newCapture()
	writeError(rec, httptest.NewRequest(http.MethodPost, replayRoute, nil), errors.New("disk on fire"))
	if rec.class != replayUnset || rec.status != http.StatusInternalServerError {
		t.Errorf("an unmapped error is %s/%d, want UNSET/500", rec.class, rec.status)
	}
	plain := httptest.NewRecorder() // a writer that is not the capture: no marking, no panic
	writeError(plain, httptest.NewRequest(http.MethodGet, "/products/SKU-1", nil), errInvalidLimit)
	if plain.Code != http.StatusBadRequest {
		t.Errorf("a plain writer answered %d, want 400", plain.Code)
	}
}

// The middleware's choice reads only the status family and the class: a 2xx
// is stored with its writes, a 5xx never is, a 4xx exactly when its class is
// FINAL or OUTCOME, and anything unmarked is released (the fail-safe
// direction).
func TestTheCaptureReleasesByStatusFamilyAndClass(t *testing.T) {
	for name, c := range map[string]struct {
		status  int
		class   replayClass
		release bool
	}{
		"a 200 is stored":                      {http.StatusOK, replayUnset, false},
		"a 201 is stored":                      {http.StatusCreated, replayUnset, false},
		"a FINAL 400 is stored":                {http.StatusBadRequest, replayFinal, false},
		"a FINAL 404 is stored":                {http.StatusNotFound, replayFinal, false},
		"an OUTCOME 409 is stored":             {http.StatusConflict, replayOutcome, false},
		"a RETRYABLE 409 is released":          {http.StatusConflict, replayRetryable, true},
		"a RETRYABLE 404 is released":          {http.StatusNotFound, replayRetryable, true},
		"an unclassified 4xx is released":      {http.StatusConflict, replayUnset, true},
		"a 500 is never stored":                {http.StatusInternalServerError, replayUnset, true},
		"a 503 is never stored":                {http.StatusServiceUnavailable, replayUnset, true},
		"a 5xx marked FINAL is never stored":   {http.StatusInternalServerError, replayFinal, true},
		"a 5xx marked OUTCOME is never stored": {http.StatusBadGateway, replayOutcome, true},
		"a 3xx is released":                    {http.StatusFound, replayUnset, true},
	} {
		rec := newCapture()
		rec.status = c.status
		rec.class = c.class
		if got := rec.releasesKey(); got != c.release {
			t.Errorf("%s: releases = %v, want %v", name, got, c.release)
		}
	}
}

func keyedRequest(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, replayRoute, strings.NewReader(`{"reason":"discontinued"}`))
	req.Header.Set(IdempotencyKeyHeader, key)
	return req
}

func middlewareOverMemory(next http.Handler) http.Handler {
	return requireIdempotencyKey(memory.NewIdempotencyStore())(next)
}

// Every catalogue entry is stored and replayed, or released and re-executed,
// exactly as its class says — driven through the production writer.
func TestEveryEntryIsReleasedOrStoredAsItsClassSays(t *testing.T) {
	for _, e := range problemCatalogue {
		calls := 0
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			writeError(w, r, fmt.Errorf("%w: replay-class test", e.err))
		})
		key := fmt.Sprintf("k-%s-%d", e.p.slug, e.p.status)
		mw := middlewareOverMemory(h)
		first := httptest.NewRecorder()
		mw.ServeHTTP(first, keyedRequest(key))
		wantStored := e.replay.stored() && e.p.status < http.StatusInternalServerError
		if first.Code != e.p.status {
			t.Errorf("%s: answered %d, want %d", e.p.slug, first.Code, e.p.status)
			continue
		}
		second := httptest.NewRecorder()
		mw.ServeHTTP(second, keyedRequest(key))
		checkSecondAnswer(t, e.p.slug, e.replay, wantStored, first, second, calls)
	}
}

func checkSecondAnswer(t *testing.T, slug string, class replayClass, stored bool, first, second *httptest.ResponseRecorder, calls int) {
	t.Helper()
	replayHeader := second.Header().Get(IdempotencyReplayedHeader)
	if stored {
		if calls != 1 || replayHeader != "true" || second.Body.String() != first.Body.String() {
			t.Errorf("%s (%s): second answer calls=%d replayed=%q bodies-equal=%v, want the stored answer replayed",
				slug, class, calls, replayHeader, second.Body.String() == first.Body.String())
		}
		return
	}
	if calls != 2 || replayHeader != "" || second.Code != first.Code {
		t.Errorf("%s (%s): second answer calls=%d replayed=%q code=%d, want a fresh execution (calls 2, no replay header, %d)",
			slug, class, calls, replayHeader, second.Code, first.Code)
	}
}

// A RETRYABLE release leaves the key free: a DIFFERENT body under the same
// key is a new request, not a reuse conflict; what that new request stored
// then replays like any stored answer.
func TestReleasedKeyIsFreeForTheSameAndForAnotherRequest(t *testing.T) {
	e := firstCatalogueEntryOf(func(e catalogueEntry) bool { return e.replay == replayRetryable })
	script := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		script++
		if script == 1 {
			writeError(w, r, fmt.Errorf("%w: replay-class test", e.err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": "ok"})
	})
	mw := middlewareOverMemory(h)

	first := httptest.NewRecorder()
	mw.ServeHTTP(first, keyedRequest("k-free"))
	if first.Code != e.p.status {
		t.Fatalf("first answer = %d, want %d", first.Code, e.p.status)
	}
	other := httptest.NewRecorder()
	otherReq := httptest.NewRequest(http.MethodPost, replayRoute, strings.NewReader(`{"reason":"other"}`))
	otherReq.Header.Set(IdempotencyKeyHeader, "k-free")
	mw.ServeHTTP(other, otherReq)
	if other.Code != http.StatusOK || strings.Contains(other.Body.String(), problemKeyReused.slug) || script != 2 {
		t.Fatalf("another body under the released key = %d %q calls=%d, want a new request (200, 2 calls)", other.Code, other.Body.String(), script)
	}
	again := httptest.NewRecorder()
	againReq := httptest.NewRequest(http.MethodPost, replayRoute, strings.NewReader(`{"reason":"other"}`))
	againReq.Header.Set(IdempotencyKeyHeader, "k-free")
	mw.ServeHTTP(again, againReq)
	if again.Code != http.StatusOK || again.Header().Get(IdempotencyReplayedHeader) != "true" || script != 2 {
		t.Fatalf("replay of the new request's answer = %d replayed=%q calls=%d, want the stored 200 replayed (2 calls)", again.Code, again.Header().Get(IdempotencyReplayedHeader), script)
	}
}

// An OUTCOME refusal (the mechanism, with a synthetic entry: no problem type
// of this service is one today, ADR 0015) is stored and replayed like a
// FINAL one; only its class says its writes commit.
func TestAnOutcomeMarkedRefusalIsStoredAndReplayed(t *testing.T) {
	calls := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		markReplay(w, replayOutcome)
		writeProblem(w, r, problem{http.StatusConflict, "recorded-then-refused", "Recorded then refused"}, "the command committed and then refused")
	})
	first := httptest.NewRecorder()
	mw := middlewareOverMemory(h)
	mw.ServeHTTP(first, keyedRequest("k-outcome"))
	second := httptest.NewRecorder()
	mw.ServeHTTP(second, keyedRequest("k-outcome"))
	if calls != 1 || second.Code != http.StatusConflict ||
		second.Header().Get(IdempotencyReplayedHeader) != "true" || second.Body.String() != first.Body.String() {
		t.Fatalf("OUTCOME: calls=%d second=%d replayed=%q, want stored and replayed once", calls, second.Code, second.Header().Get(IdempotencyReplayedHeader))
	}
}

// A 2xx is stored and replayed byte for byte; a 5xx and a panic are not.
func TestSuccessServerErrorAndPanicStorage(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, map[string]string{"id": "created"})
	})
	first := httptest.NewRecorder()
	mw := middlewareOverMemory(ok)
	mw.ServeHTTP(first, keyedRequest("k-2xx"))
	again := httptest.NewRecorder()
	mw.ServeHTTP(again, keyedRequest("k-2xx"))
	if again.Code != http.StatusCreated || again.Header().Get(IdempotencyReplayedHeader) != "true" || again.Body.String() != first.Body.String() {
		t.Fatalf("2xx replay = %d replayed=%q, want the stored 201", again.Code, again.Header().Get(IdempotencyReplayedHeader))
	}

	boom := 0
	failing := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		boom++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	mw5xx := middlewareOverMemory(failing)
	mw5xx.ServeHTTP(httptest.NewRecorder(), keyedRequest("k-5xx"))
	retry := httptest.NewRecorder()
	mw5xx.ServeHTTP(retry, keyedRequest("k-5xx"))
	if boom != 2 || retry.Code != http.StatusInternalServerError || retry.Header().Get(IdempotencyReplayedHeader) != "" {
		t.Fatalf("5xx retry: calls=%d code=%d replayed=%q, want a fresh execution (calls 2)", boom, retry.Code, retry.Header().Get(IdempotencyReplayedHeader))
	}

	crashes := 0
	crashing := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { crashes++; panic("handler panic") })
	mwPanic := middlewareOverMemory(crashing)
	func() {
		defer func() { _ = recover() }()
		mwPanic.ServeHTTP(httptest.NewRecorder(), keyedRequest("k-panic"))
	}()
	recovered := httptest.NewRecorder()
	func() {
		defer func() { _ = recover() }()
		mwPanic.ServeHTTP(recovered, keyedRequest("k-panic"))
	}()
	if crashes != 2 {
		t.Fatalf("after a panic the retry ran %d times, want 2: a panic is never stored", crashes)
	}
}

func firstCatalogueEntryOf(match func(catalogueEntry) bool) catalogueEntry {
	for _, e := range problemCatalogue {
		if match(e) && e.p.status < http.StatusInternalServerError {
			return e
		}
	}
	panic("no matching catalogue entry")
}

// ---- the records that must say what the code does ----------------------------------------------

var adrRow = regexp.MustCompile(`(?m)^\| ` + "`([a-z0-9-]+)`" + ` \| (FINAL|OUTCOME|RETRYABLE) \|`)

func TestTheADRDeclaresTheClassOfEveryProblem(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "docs", "adr", "0015-*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("docs/adr/0015-*.md = %v (%v), want exactly one ADR", matches, err)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{}
	for _, m := range adrRow.FindAllStringSubmatch(string(raw), -1) {
		if prev, dup := declared[m[1]]; dup {
			t.Errorf("the ADR lists %q twice (%s and %s)", m[1], prev, m[2])
		}
		declared[m[1]] = m[2]
	}
	want := map[string]string{}
	for _, e := range problemCatalogue {
		want[e.p.slug] = e.replay.String()
	}
	for slug, class := range want {
		if declared[slug] != class {
			t.Errorf("ADR 0015 says %q is %q, the catalogue says %s", slug, declared[slug], class)
		}
	}
	for slug := range declared {
		if _, ok := want[slug]; !ok {
			t.Errorf("ADR 0015 classifies %q, which is not in the catalogue", slug)
		}
	}
	if len(raw) == 0 || !strings.Contains(string(raw), "0015") {
		t.Fatal("the ADR file is empty")
	}
	_ = idempotency.ErrNotStored // keep the sentinel referenced beside the middleware tests
}
