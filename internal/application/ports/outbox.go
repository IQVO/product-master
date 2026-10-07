package ports

import (
	"context"

	"github.com/claudioed/product-master/internal/application/outbox"
	"github.com/claudioed/product-master/internal/domain/product"
)

// OutboxRepository is the write side of the transactional outbox. Called
// with the ctx handed to UnitOfWork.Do it joins the SAME database
// transaction as the aggregate's Save, so the product and its integration
// events commit or roll back together. A relay adapter later drains the
// stored rows to Kafka (internal/adapters/outbound/outbox).
type OutboxRepository interface {
	Insert(ctx context.Context, msgs ...outbox.Message) error
}

// EventEncoder turns Product domain events into wire-ready outbox messages
// (CloudEvents 1.0 structured mode, each with a freshly minted id). It is
// pure, so the use case can call it inside its UnitOfWork.
type EventEncoder interface {
	Encode(events ...product.Event) ([]outbox.Message, error)
}

// ProcessedEvents is the inbound-consumer idempotency guard. Claim records
// (consumer, CloudEvents id) INSIDE the same UnitOfWork as the event's
// effect and reports false when an earlier committed handling already
// claimed it (the caller then skips).
type ProcessedEvents interface {
	Claim(ctx context.Context, consumer, eventID string) (claimed bool, err error)
}
