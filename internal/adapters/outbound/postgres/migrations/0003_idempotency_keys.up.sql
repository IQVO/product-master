-- Idempotency-Key store (REST API guideline, ADR 0013). One row per key ever
-- seen on a POST/PATCH route. The row is inserted, completed with the stored
-- answer and committed in the SAME transaction as the aggregate change and
-- its outbox rows, so a committed row always carries its outcome.
CREATE TABLE idempotency_keys (
    key              TEXT PRIMARY KEY,
    fingerprint      TEXT NOT NULL,
    status_code      INTEGER NOT NULL,
    response_body    BYTEA NOT NULL,
    response_headers JSONB NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
