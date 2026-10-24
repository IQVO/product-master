-- 0002_product_pack_levels.up.sql: the packaging hierarchy of a Product
-- (ADR 0011). Additive: no existing table changes.

-- product_pack_levels: every pack level a SKU ever declared, retired ones
-- included (an end-dated level is kept so a GTIN on an in-flight ASN still
-- resolves). Part of the Product aggregate: the repository writes it in the
-- same transaction as the products row and its version guard.
--   gtin           the 14-digit zero-left-padded GTIN, validated by the domain
--                  and re-checked here. A GTIN identifies exactly one
--                  (sku, level), fleet-wide and for ever, so it is UNIQUE on its
--                  own: a retired GTIN is never reassigned.
--   child_gtin     the GTIN of the child level; NULL exactly for EACH.
--   qty_of_child   how many children make one of this level; NULL for EACH.
--   units_per_pack the direct factor to eaches, stored (1 for EACH).
--   declared_*     declared dimensions of ONE pack, all four or none.
--   measured_*     the latest measurement, all five or none (ADR 0002).
--   effective_to   NULL while the level is active. At most one active level
--                  per (sku, level).
CREATE TABLE product_pack_levels (
    sku                   TEXT COLLATE "C" NOT NULL REFERENCES products (sku),
    level                 TEXT        NOT NULL CHECK (level IN ('EACH', 'INNER', 'CASE', 'PALLET')),
    gtin                  TEXT        NOT NULL CHECK (gtin ~ '^[0-9]{14}$' AND gtin <> '00000000000000' AND left(gtin, 1) <> '9'),
    child_gtin            TEXT        CHECK (child_gtin ~ '^[0-9]{14}$'),
    qty_of_child          INTEGER     CHECK (qty_of_child BETWEEN 2 AND 1000000),
    units_per_pack        INTEGER     NOT NULL CHECK (units_per_pack BETWEEN 1 AND 1000000),
    declared_length_mm    BIGINT,
    declared_width_mm     BIGINT,
    declared_height_mm    BIGINT,
    declared_weight_g     BIGINT,
    measured_length_mm    BIGINT,
    measured_width_mm     BIGINT,
    measured_height_mm    BIGINT,
    measured_weight_g     BIGINT,
    measured_at           TIMESTAMPTZ,
    measured_device_id    TEXT,
    is_orderable          BOOLEAN     NOT NULL,
    is_despatch_unit      BOOLEAN     NOT NULL,
    effective_from        TIMESTAMPTZ NOT NULL,
    effective_to          TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (sku, gtin),
    CHECK ((level = 'EACH') = (child_gtin IS NULL)),
    CHECK ((level = 'EACH') = (qty_of_child IS NULL)),
    CHECK (level <> 'EACH' OR units_per_pack = 1),
    CHECK (num_nonnulls(declared_length_mm, declared_width_mm, declared_height_mm, declared_weight_g) IN (0, 4)),
    CHECK (num_nonnulls(measured_length_mm, measured_width_mm, measured_height_mm, measured_weight_g, measured_at) IN (0, 5)),
    CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

-- A GTIN identifies exactly one (sku, level), fleet-wide (ErrGtinAlreadyAssigned);
-- GET /gtins/{gtin} reads through this index.
CREATE UNIQUE INDEX ux_product_pack_levels_gtin ON product_pack_levels (gtin);

-- At most one active level per (sku, level): a new GTIN at a level end-dates
-- the old row first (the repository updates before it inserts).
CREATE UNIQUE INDEX ux_product_pack_levels_active ON product_pack_levels (sku, level) WHERE effective_to IS NULL;
