import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { Card, DataTable, StatusPill } from "@warehouse/ui-kit";
import { getProduct } from "../api";
import { Facts, PageHeader, Stack, TagPills } from "../components/layout";
import { LoadingNote, ProblemAlert } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { formatDims, formatVolume, formatWeight, utcLabel } from "../lib";
import type { PhysicalProfile, Product, UnitDimensions } from "../types";
import { ClassifyForm } from "./ClassifyForm";
import { DeclareDimensionsForm, RecordMeasurementForm } from "./DimensionsForms";

/** Route element for `products/:sku`: a fresh screen (and fresh forms) per SKU. */
export function ProductDetailRoute() {
  const { sku = "" } = useParams();
  return <ProductDetailScreen key={sku} sku={sku} />;
}

/**
 * One product (GET /products/{sku}): its version, classification and physical
 * profile (declared, measured, effective and the discrepancy flag), with the
 * three write forms beneath. After a successful write the product is re-read
 * in the background, so the form's own confirmation stays on screen while the
 * figures above it update.
 */
export function ProductDetailScreen({ sku }: { sku: string }) {
  const initial = useRequest(() => getProduct(sku), `product|${sku}`);
  const [fresh, setFresh] = useState<Product | null>(null);
  const product = fresh ?? (initial.status === "success" ? initial.data : null);

  const refresh = () => {
    getProduct(sku).then(setFresh, () => {
      // The write itself succeeded and said so; a failed re-read keeps the
      // last known figures rather than replacing the page with an error.
    });
  };

  const back = (
    <Link to="../.." relative="path" style={{ fontSize: "var(--wh-font-size-sm)" }}>
      ← All products
    </Link>
  );

  if (!product) {
    return (
      <Stack gap={5}>
        {back}
        <PageHeader title={sku} subtitle="product-master · product" />
        {initial.status === "error" ? (
          <ProblemAlert
            error={initial.error}
            lead={initial.error.slug === "product-not-found" ? "No product is registered under this SKU." : undefined}
          />
        ) : (
          <LoadingNote what="product" />
        )}
      </Stack>
    );
  }

  return (
    <Stack gap={5}>
      {back}
      <PageHeader
        title={product.sku}
        subtitle={
          <>
            {product.description || "(no description)"} · version <strong>{product.version}</strong>
          </>
        }
      />
      <Card title="Classification">
        {product.classification ? (
          <Facts
            items={[
              { label: "Handling tags", value: <TagPills tags={product.classification.handlingTags} /> },
              { label: "Temperature class", value: product.classification.temperatureClass ?? "—" },
              { label: "DOT hazard class", value: product.classification.dotHazardClass ?? "—" },
              { label: "Source", value: product.classification.classificationSource },
            ]}
          />
        ) : (
          <div style={{ color: "var(--wh-color-text-muted)" }}>Not classified yet.</div>
        )}
      </Card>
      <Card title="Physical profile">
        <PhysicalProfileView profile={product.physicalProfile} />
      </Card>
      <Card title="Classify">
        <ClassifyForm sku={product.sku} current={product.classification} onSaved={refresh} />
      </Card>
      <Card title="Declare dimensions">
        <DeclareDimensionsForm sku={product.sku} current={product.physicalProfile.declared} onSaved={refresh} />
      </Card>
      <Card title="Record a measurement">
        <RecordMeasurementForm sku={product.sku} onSaved={refresh} />
      </Card>
    </Stack>
  );
}

interface ProfileRow {
  part: string;
  dims?: UnitDimensions;
  measuredAt?: string;
  deviceId?: string;
}

function PhysicalProfileView({ profile }: { profile: PhysicalProfile }) {
  const both = Boolean(profile.declared && profile.measured);
  const rows: ProfileRow[] = [
    { part: "Declared", dims: profile.declared },
    {
      part: "Measured",
      dims: profile.measured,
      measuredAt: profile.measured?.measuredAt,
      deviceId: profile.measured?.deviceId,
    },
    { part: `Effective (${profile.effectiveSource})`, dims: profile.effective },
  ];
  return (
    <Stack>
      <Facts
        items={[
          { label: "Effective source", value: profile.effectiveSource },
          {
            label: "Discrepancy",
            value: profile.discrepancy ? (
              <span>
                <StatusPill status="Discrepancy" tone="warning" size="sm" />{" "}
                <span style={{ color: "var(--wh-color-text-muted)" }}>
                  declared and measured volume or weight differ by more than 10 %
                </span>
              </span>
            ) : both ? (
              <StatusPill status="Consistent" tone="success" size="sm" />
            ) : (
              <span style={{ color: "var(--wh-color-text-muted)" }}>
                needs both declared and measured values
              </span>
            ),
          },
        ]}
      />
      <DataTable
        rowKey={(r) => r.part}
        rows={rows}
        columns={[
          { key: "part", header: "Profile", render: (r) => r.part },
          { key: "dims", header: "L × W × H", render: (r) => formatDims(r.dims) },
          { key: "volume", header: "Volume", align: "right", render: (r) => formatVolume(r.dims?.volumeMm3) },
          { key: "weight", header: "Weight", align: "right", render: (r) => formatWeight(r.dims?.weightG) },
          { key: "at", header: "Measured at", render: (r) => (r.measuredAt ? utcLabel(r.measuredAt) : "—") },
          { key: "device", header: "Device", render: (r) => r.deviceId ?? "—" },
        ]}
      />
    </Stack>
  );
}
