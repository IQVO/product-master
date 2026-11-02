// Package idempotency holds the value types of the Idempotency-Key store port
// (ports.IdempotencyStore): the stored answer and the two sentinel errors.
package idempotency

import "errors"

// ErrKeyReused means the Idempotency-Key was already used with a
// different request fingerprint (method + path + body hash).
var ErrKeyReused = errors.New("idempotency key reused with a different request")

// ErrNotStored is returned by the run function of IdempotencyStore.Execute
// for an answer that must NOT be stored (a 5xx): the unit of work rolls
// back, the key row disappears with it and the retry re-executes.
var ErrNotStored = errors.New("response is not stored")

// StoredResponse is the deterministic answer of a keyed request, kept so a
// retry replays it byte for byte.
type StoredResponse struct {
	Status int
	Header map[string][]string
	Body   []byte
}
