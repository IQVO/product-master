package ports

import (
	"context"

	"github.com/claudioed/product-master/internal/application/idempotency"
)

// IdempotencyStore serialises requests by Idempotency-Key.
//
// Execute runs run at most once per key. On a fresh key it opens ONE unit of
// work, hands run a ctx carrying it (so the aggregate save and the outbox
// rows join it), stores the returned response in the same transaction as the
// key row, and commits. A key seen before with the same fingerprint returns
// the stored response with replayed=true and does not call run; with a
// different fingerprint it returns idempotency.ErrKeyReused. If run returns
// an error (idempotency.ErrNotStored included) or panics, nothing is committed. Concurrent
// calls with the same key are serialised by the key's unique index; the loser
// replays the winner's answer.
type IdempotencyStore interface {
	Execute(ctx context.Context, key, fingerprint string,
		run func(ctx context.Context) (idempotency.StoredResponse, error)) (resp idempotency.StoredResponse, replayed bool, err error)
}
