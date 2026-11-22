package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/claudioed/product-master/internal/application/idempotency"
	"github.com/claudioed/product-master/internal/application/ports"
)

const (
	// IdempotencyKeyHeader is the request header of every keyed POST/PATCH.
	IdempotencyKeyHeader = "Idempotency-Key"
	// IdempotencyReplayedHeader is set to "true" on a replayed answer only.
	IdempotencyReplayedHeader = "Idempotency-Replayed"

	maxIdempotencyKeyLength = 128
)

var (
	problemKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	problemKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid"}
	problemKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
)

// validIdempotencyKey reports whether key is 1-128 printable ASCII characters.
func validIdempotencyKey(key string) bool {
	if key == "" || len(key) > maxIdempotencyKeyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// fingerprint is method + path + SHA-256 of the body, itself hashed.
func fingerprint(r *http.Request, body []byte) string {
	sum := sha256.Sum256(body)
	all := sha256.Sum256([]byte(r.Method + "\n" + r.URL.EscapedPath() + "\n" + hex.EncodeToString(sum[:])))
	return hex.EncodeToString(all[:])
}

// requireIdempotencyKey is the route-scoped middleware of every POST/PATCH.
//
//   - a missing key is 400 idempotency-key-required, a key that is not 1-128
//     printable ASCII characters is 400 idempotency-key-invalid;
//   - the handler runs inside store.Execute: the key row commits in the same
//     transaction as the aggregate change and its outbox rows;
//   - the same key with the same method, path and body replays the stored
//     answer (status, headers, body) with Idempotency-Replayed: true, the
//     same key with another request is 409 idempotency-key-reused;
//   - whether an answer is stored is its replay class (ADR 0015): a 2xx is
//     stored; a FINAL or OUTCOME refusal is stored and replayed; a RETRYABLE
//     or unclassified refusal, a 5xx and a panic roll everything back, key
//     row included, and are never stored, so the key can retry the real work.
//     The class is read from the capturing writer (markReplay); the
//     middleware holds no slug list and no status ranges beyond 2xx and 5xx.
func requireIdempotencyKey(store ports.IdempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := readIdempotencyKey(w, r)
			if !ok {
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
			if err != nil || len(body) > maxBodyBytes {
				writeError(w, r, errMalformedRequest)
				return
			}
			_ = r.Body.Close()

			var failed idempotency.StoredResponse
			resp, replayed, err := store.Execute(r.Context(), key, fingerprint(r, body),
				func(ctx context.Context) (idempotency.StoredResponse, error) {
					rec := newCapture()
					runHandler(rec, next, r.Clone(ctx), body)
					out := rec.storedResponse()
					if rec.releasesKey() {
						// A 5xx, a panic's partial answer, or a refusal whose
						// problem is RETRYABLE (or unclassified): nothing is
						// stored, the store's rollback drops the key row and
						// the handler's writes, and the retry re-executes.
						failed = out
						return out, idempotency.ErrNotStored
					}
					return out, nil
				})
			switch {
			case errors.Is(err, idempotency.ErrNotStored):
				resp = failed
			case errors.Is(err, idempotency.ErrKeyReused):
				writeProblem(w, r, problemKeyReused, "the "+IdempotencyKeyHeader+" was first used with a different method, path or body; use a new key for a different request")
				return
			case err != nil:
				writeError(w, r, err)
				return
			}
			writeStored(w, resp, replayed)
		})
	}
}

// readIdempotencyKey validates the header; it answers the 400 itself.
func readIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	keys := r.Header.Values(IdempotencyKeyHeader)
	if len(keys) == 0 {
		writeProblem(w, r, problemKeyRequired, "send an "+IdempotencyKeyHeader+" header of 1 to 128 printable ASCII characters (a client-generated UUID per logical submit)")
		return "", false
	}
	if len(keys) > 1 || !validIdempotencyKey(keys[0]) {
		writeProblem(w, r, problemKeyInvalid, "the "+IdempotencyKeyHeader+" header must be 1 to 128 printable ASCII characters")
		return "", false
	}
	return keys[0], true
}

// capture records the handler's answer so it can be stored before anything
// reaches the client. writeError tells it the replay class of the problem it
// answered with (markReplay); the middleware reads it (releasesKey) and
// nothing else does.
type capture struct {
	header http.Header
	body   bytes.Buffer
	status int
	class  replayClass
}

// newCapture returns an empty capturing writer.
func newCapture() *capture { return &capture{header: http.Header{}} }

// replayMarker is what writeError tells the capturing writer: the replay
// class of the problem it answers with. Any other writer ignores it.
type replayMarker interface{ markReplay(replayClass) }

// markReplay tells w the replay class of the problem being answered; a
// writer that is not the middleware's capture ignores it.
func markReplay(w http.ResponseWriter, class replayClass) {
	if m, ok := w.(replayMarker); ok {
		m.markReplay(class)
	}
}

func (c *capture) markReplay(class replayClass) { c.class = class }

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(b)
}

// releasesKey reports whether the answer must NOT be remembered under its
// key: a 5xx never is; a 2xx always is; anything else only when the class of
// its problem is FINAL or OUTCOME. An unclassified answer (a handler that
// wrote a bare status) is released — the fail-safe direction.
func (c *capture) releasesKey() bool {
	switch {
	case c.status >= http.StatusInternalServerError:
		return true
	case c.status >= http.StatusOK && c.status < http.StatusMultipleChoices:
		return false
	default:
		return !c.class.stored()
	}
}

// storedResponse is the answer as the store keeps it.
func (c *capture) storedResponse() idempotency.StoredResponse {
	return idempotency.StoredResponse{Status: c.status, Header: c.header.Clone(), Body: c.body.Bytes()}
}

// runHandler runs next against the capturing writer rec, replaying body as
// the request body.
func runHandler(rec *capture, next http.Handler, req *http.Request, body []byte) {
	req.Body = io.NopCloser(bytes.NewReader(body))
	next.ServeHTTP(rec, req)
}

// writeStored writes a first answer or a replay of one.
func writeStored(w http.ResponseWriter, resp idempotency.StoredResponse, replayed bool) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if replayed {
		w.Header().Set(IdempotencyReplayedHeader, "true")
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write(resp.Body)
}
