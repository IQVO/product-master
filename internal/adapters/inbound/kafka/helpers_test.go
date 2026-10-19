package kafka

import (
	"context"
	"sync"

	kafkago "github.com/segmentio/kafka-go"
)

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
