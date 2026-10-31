import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Card, DataTable, StatusPill } from "@warehouse/ui-kit";
import { listPackLevels } from "../api";
import { Stack, PageHeader } from "../components/layout";
import { EmptyNote, LoadingNote, ProblemAlert } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { formatDims, formatWeight, orderLevels, packLabel, utcLabel } from "../lib";
import type { PackLevelDefinition } from "../types";
import { DeclarePackLevelForm } from "./DeclarePackLevelForm";
import { GtinLookupCard } from "./GtinLookupCard";
import { RetirePackLevelPanel } from "./RetirePackLevelPanel";
import { PACK_PROBLEM_LEADS } from "./packagingCopy";

/** Route element for `products/:sku/packaging`: a fresh screen (and fresh forms) per SKU. */
export function PackagingRoute() {
  const { sku = "" } = useParams();
  return <PackagingScreen key={sku} sku={sku} />;
}

/**
 * The packaging of one product: its pack-level hierarchy EACH -> INNER -> CASE
 * -> PALLET (GET /products/{sku}/pack-levels), a form to declare a level, a
 * Retire action per active level, and a GTIN lookup. Every quantity is in
 * EACHES; a case is "Case of N eaches". After a write the list is re-read, so
 * the form's own confirmation stays on screen while the table updates.
 */
export function PackagingScreen({ sku }: { sku: string }) {
  const initial = useRequest(() => listPackLevels(sku), `pack-levels|${sku}`);
  const [fresh, setFresh] = useState<PackLevelDefinition[] | null>(null);
  const items = fresh ?? (initial.status === "success" ? initial.data.items : null);

  const refresh = () => {
    listPackLevels(sku).then(
      (list) => setFresh(list.items),
      () => {
        // The write itself succeeded and said so; a failed re-read keeps the last known table.
      },
    );
  };

  const back = (
    <Link to=".." relative="path" style={{ fontSize: "var(--wh-font-size-sm)" }}>
      ← Product {sku}
    </Link>
  );

  return (
    <Stack gap={5}>
      {back}
      <PageHeader
        title={`Packaging · ${sku}`}
        subtitle="product-master · pack levels. Quantities are always eaches."
      />
      <Card title="Pack hierarchy">
        {items ? (
          <HierarchyTable sku={sku} items={items} onChanged={refresh} />
        ) : initial.status === "error" ? (
          <ProblemAlert error={initial.error} lead={PACK_PROBLEM_LEADS[initial.error.slug]} />
        ) : (
          <LoadingNote what="pack levels" />
        )}
      </Card>
      {items && (
        <>
          <Card title="Declare a pack level">
            <DeclarePackLevelForm sku={sku} items={items} onSaved={refresh} />
          </Card>
          <Card title="Look up a GTIN">
            <GtinLookupCard />
          </Card>
        </>
      )}
    </Stack>
  );
}

function HierarchyTable({
  sku,
  items,
  onChanged,
}: {
  sku: string;
  items: PackLevelDefinition[];
  onChanged: () => void;
}) {
  const [retiring, setRetiring] = useState<PackLevelDefinition | null>(null);
  if (items.length === 0) {
    return <EmptyNote>No pack levels declared yet. Start with the EACH level below.</EmptyNote>;
  }
  const rows = orderLevels(items);
  const levelOf = (gtin: string | undefined) => rows.find((r) => r.gtin === gtin)?.level;
  return (
    <Stack>
      <DataTable
        rowKey={(r) => `${r.level}|${r.gtin}`}
        rows={rows}
        columns={[
          { key: "level", header: "Level", render: (r) => r.level },
          { key: "pack", header: "Pack", render: (r) => packLabel(r.level, r.unitsPerPack) },
          { key: "gtin", header: "GTIN", render: (r) => <code>{r.gtin}</code> },
          {
            key: "contents",
            header: "Contains",
            render: (r) =>
              r.qtyOfChild === undefined
                ? "—"
                : `${r.qtyOfChild} × ${levelOf(r.childGtin) ?? "child"} (${r.childGtin ?? "—"})`,
          },
          { key: "units", header: "Eaches per pack", align: "right", render: (r) => r.unitsPerPack },
          { key: "dims", header: "L × W × H", render: (r) => formatDims(r.dims) },
          { key: "weight", header: "Weight", align: "right", render: (r) => formatWeight(r.dims?.weightG) },
          {
            key: "flags",
            header: "Used for",
            render: (r) => [r.isOrderable && "orderable", r.isDespatchUnit && "despatch unit"].filter(Boolean).join(", ") || "—",
          },
          {
            key: "status",
            header: "Status",
            render: (r) =>
              r.active ? (
                <StatusPill status="Active" tone="success" size="sm" />
              ) : (
                <span>
                  <StatusPill status="Retired" tone="neutral" size="sm" />{" "}
                  <span style={{ color: "var(--wh-color-text-muted)" }}>{utcLabel(r.effectiveTo)}</span>
                </span>
              ),
          },
          {
            key: "action",
            header: "",
            render: (r) =>
              r.active ? (
                <button type="button" aria-label={`Retire ${r.level} ${r.gtin}`} onClick={() => setRetiring(r)}>
                  Retire
                </button>
              ) : null,
          },
        ]}
      />
      {retiring && (
        <RetirePackLevelPanel
          key={retiring.gtin}
          sku={sku}
          def={retiring}
          onClose={() => setRetiring(null)}
          onRetired={onChanged}
        />
      )}
    </Stack>
  );
}
