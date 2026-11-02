package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

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
//   - 2xx and 4xx answers are stored; a 5xx or a panic rolls back and is
//     never stored, so the retry re-executes.
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
					out := capture(next, r.Clone(ctx), body)
					if out.Status >= http.StatusInternalServerError {
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

// capture runs next against a recorder, replaying body as the request body.
func capture(next http.Handler, req *http.Request, body []byte) idempotency.StoredResponse {
	req.Body = io.NopCloser(bytes.NewReader(body))
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, req)
	return idempotency.StoredResponse{Status: rec.Code, Header: rec.Header().Clone(), Body: rec.Body.Bytes()}
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
