package http

import "net/http"

// Test hooks for the replay-class tests of the Idempotency-Key middleware
// (idempotency_replay_classes_*_test.go, package http_test): the production
// middleware and the production error writer, so the tests drive exactly what
// a handler's answer goes through.

// RequireIdempotencyKey is requireIdempotencyKey for the tests of the
// middleware over a real store.
var RequireIdempotencyKey = requireIdempotencyKey

// WriteError is writeError: it answers err as its problem, so the replay
// class of that problem marks the capturing writer the way a handler's
// answer does.
func WriteError(w http.ResponseWriter, r *http.Request, err error) { writeError(w, r, err) }
