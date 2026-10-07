-- 0001_products_outbox.up.sql: product-master's OLTP schema.

-- products: one row per Product aggregate (ADR 0001/0002). The SKU uses the
-- "C" collation so ORDER BY / sku > $cursor follow byte order, the same
-- order the in-memory adapter and the opaque list cursor assume.
-- handling_tags is NULL for an unclassified product; when set it holds the
-- tags in the stable order Hazmat, Fragile, TemperatureSensitive, Oversized,
-- HighValue. version starts at 1 and guards every UPDATE (optimistic
-- concurrency: UPDATE ... WHERE version = <loaded>).
CREATE TABLE products (
    sku                    TEXT COLLATE "C" PRIMARY KEY,
    description            TEXT        NOT NULL DEFAULT '',
    version                BIGINT      NOT NULL CHECK (version >= 1),
    handling_tags          TEXT[],
    temperature_class      TEXT,
    dot_hazard_class       SMALLINT    CHECK (dot_hazard_class BETWEEN 1 AND 9),
    classification_source  TEXT        CHECK (classification_source IN ('native', 'legacy-import')),
    declared_length_mm     BIGINT,
    declared_width_mm      BIGINT,
    declared_height_mm     BIGINT,
    declared_weight_g      BIGINT,
    measured_length_mm     BIGINT,
    measured_width_mm      BIGINT,
    measured_height_mm     BIGINT,
    measured_weight_g      BIGINT,
    measured_at            TIMESTAMPTZ,
    measured_device_id     TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((handling_tags IS NULL) = (classification_source IS NULL)),
    CHECK ((measured_at IS NULL) = (measured_length_mm IS NULL))
);

-- GET /products?handlingTag= filters on array membership.
CREATE INDEX idx_products_handling_tags ON products USING GIN (handling_tags);

-- outbox_events: the transactional outbox (mirrors warehouse-planning's,
-- including the (event_id, topic) identity of migration 0007 there). One row
-- per already-encoded Kafka message, inserted in the SAME transaction as the
-- product row; the relay drains unpublished rows to Kafka.
--   event_id    the CloudEvents `id`, minted once at encode time and
--               persisted: a relay retry republishes the same id.
--   event_type  the FULL CloudEvents `type`
--               (com.warehouse.wms.product-master.product.<EventName>).
--   subject     the CloudEvents `subject` (the SKU).
--   key         the Kafka message key (the SKU).
--   dataschema  the CloudEvents `dataschema` URN.
--   value       the structured-mode CloudEvents JSON bytes.
--   headers     [{"key":..,"value":..}] Kafka headers (content-type).
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);

-- The relay only ever scans the unpublished tail; keep that scan tiny.
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- processed_events: idempotency guard of the inbound consumers (the legacy
-- importer, ADR 0003). (consumer, event_id) is claimed with INSERT ... ON
-- CONFLICT DO NOTHING in the SAME transaction as the effect, so a rollback
-- un-claims it.
CREATE TABLE processed_events (
    consumer     TEXT        NOT NULL,
    event_id     TEXT        NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
