package memory

import (
	"context"
	"sync"

	"github.com/claudioed/product-master/internal/application/idempotency"
)

type memoryKey struct {
	fingerprint string
	resp        idempotency.StoredResponse
}

// IdempotencyStore is the in-memory ports.IdempotencyStore (a test double):
// Execute is serialised by one mutex, a failed run stores nothing.
type IdempotencyStore struct {
	mu   sync.Mutex
	keys map[string]memoryKey
}

// NewIdempotencyStore returns an empty store.
func NewIdempotencyStore() *IdempotencyStore {
	return &IdempotencyStore{keys: map[string]memoryKey{}}
}

// Execute implements ports.IdempotencyStore.
func (s *IdempotencyStore) Execute(ctx context.Context, key, fingerprint string,
	run func(ctx context.Context) (idempotency.StoredResponse, error),
) (idempotency.StoredResponse, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[key]; ok {
		if k.fingerprint != fingerprint {
			return idempotency.StoredResponse{}, false, idempotency.ErrKeyReused
		}
		return k.resp, true, nil
	}
	resp, err := run(ctx)
	if err != nil {
		return idempotency.StoredResponse{}, false, err
	}
	s.keys[key] = memoryKey{fingerprint: fingerprint, resp: resp}
	return resp, false, nil
}

// Len is the number of stored keys (for tests).
func (s *IdempotencyStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}
