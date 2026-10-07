import { useState } from "react";
import { Link } from "react-router-dom";
import { Card, DataTable, StatusPill } from "@warehouse/ui-kit";
import { listProducts } from "../api";
import { Form, FormRow, SelectField, SubmitButton } from "../components/formkit";
import { PageHeader, Stack, TagPills } from "../components/layout";
import { RequestView } from "../components/ProblemAlert";
import { useRequest } from "../hooks/useRequest";
import { formatDims, formatWeight } from "../lib";
import { HANDLING_TAGS } from "../types";
import type { HandlingTag, ListProductsQuery, Product } from "../types";

const PAGE_SIZES = ["25", "50", "100"] as const;

const TAG_OPTIONS = HANDLING_TAGS.map((t) => ({ value: t, label: t }));
const CLASSIFIED_OPTIONS = [
  { value: "true", label: "Classified only" },
  { value: "false", label: "Unclassified only" },
];
const SIZE_OPTIONS = PAGE_SIZES.map((s) => ({ value: s, label: `${s} per page` }));

interface Filters {
  handlingTag: "" | HandlingTag;
  classified: "" | "true" | "false";
  limit: string;
}

const INITIAL: Filters = { handlingTag: "", classified: "", limit: "25" };

function toQuery(f: Filters, cursor: string): ListProductsQuery {
  return {
    limit: Number(f.limit),
    cursor: cursor || undefined,
    handlingTag: f.handlingTag || undefined,
    classified: f.classified === "" ? undefined : f.classified === "true",
  };
}

/**
 * Product list: GET /products in ascending SKU order, a page at a time, with
 * the API's two filters (a handling tag; classified or not). Paging follows
 * the opaque `nextCursor`; the cursors already visited are kept so "Previous"
 * walks back without the API having to support it. Changing the filters starts
 * again at the first page.
 */
export function ProductListScreen() {
  const [draft, setDraft] = useState<Filters>(INITIAL);
  const [applied, setApplied] = useState<Filters>(INITIAL);
  // cursors[i] is the cursor of page i+1 ("" = the first page).
  const [cursors, setCursors] = useState<string[]>([""]);
  const cursor = cursors[cursors.length - 1];
  const page = useRequest(
    () => listProducts(toQuery(applied, cursor)),
    `products|${applied.handlingTag}|${applied.classified}|${applied.limit}|${cursor}`,
  );

  const apply = () => {
    setApplied(draft);
    setCursors([""]);
  };

  return (
    <Stack gap={5}>
      <PageHeader
        title="Products"
        subtitle="product-master · what each SKU is: handling classification and physical profile"
      />
      <Card title="Filters">
        <Form label="Product filters" onSubmit={apply}>
          <FormRow>
            <SelectField
              label="Handling tag"
              value={draft.handlingTag}
              onChange={(v) => setDraft({ ...draft, handlingTag: v as Filters["handlingTag"] })}
              options={TAG_OPTIONS}
              emptyLabel="Any tag"
            />
            <SelectField
              label="Classification"
              value={draft.classified}
              onChange={(v) => setDraft({ ...draft, classified: v as Filters["classified"] })}
              options={CLASSIFIED_OPTIONS}
              emptyLabel="Classified or not"
            />
            <SelectField
              label="Page size"
              value={draft.limit}
              onChange={(v) => setDraft({ ...draft, limit: v })}
              options={SIZE_OPTIONS}
            />
            <SubmitButton>Apply filters</SubmitButton>
          </FormRow>
        </Form>
      </Card>
      <Card
        title={`Page ${cursors.length}`}
        actions={
          <div style={{ display: "flex", gap: "var(--wh-space-2)" }}>
            <SubmitButton
              type="button"
              secondary
              disabled={cursors.length === 1 || page.status === "loading"}
              onClick={() => setCursors(cursors.slice(0, -1))}
            >
              Previous page
            </SubmitButton>
            <SubmitButton
              type="button"
              secondary
              disabled={page.status !== "success" || !page.data.nextCursor}
              onClick={() => {
                if (page.status === "success" && page.data.nextCursor) {
                  setCursors([...cursors, page.data.nextCursor]);
                }
              }}
            >
              Next page
            </SubmitButton>
          </div>
        }
      >
        <RequestView
          state={page}
          what="products"
          isEmpty={(p) => p.items.length === 0}
          empty={
            cursors.length === 1
              ? "No products match these filters."
              : "No more products on this page."
          }
        >
          {(p) => <ProductTable products={p.items} />}
        </RequestView>
      </Card>
    </Stack>
  );
}

function ProductTable({ products }: { products: Product[] }) {
  return (
    <DataTable
      rowKey={(p) => p.sku}
      rows={products}
      columns={[
        {
          key: "sku",
          header: "SKU",
          // Relative link: resolves against this remote's mount point, so it
          // works under the console's /product-master/* and standalone at /.
          render: (p) => <Link to={`products/${encodeURIComponent(p.sku)}`}>{p.sku}</Link>,
        },
        { key: "description", header: "Description", render: (p) => p.description || "—" },
        {
          key: "tags",
          header: "Handling tags",
          render: (p) =>
            p.classification ? (
              <TagPills tags={p.classification.handlingTags} />
            ) : (
              <span style={{ color: "var(--wh-color-text-muted)" }}>Unclassified</span>
            ),
        },
        {
          key: "dims",
          header: "Effective dimensions",
          render: (p) => formatDims(p.physicalProfile.effective),
        },
        {
          key: "weight",
          header: "Weight",
          align: "right",
          render: (p) => formatWeight(p.physicalProfile.effective?.weightG),
        },
        {
          key: "source",
          header: "Source",
          render: (p) => p.physicalProfile.effectiveSource,
        },
        {
          key: "discrepancy",
          header: "Discrepancy",
          render: (p) =>
            p.physicalProfile.discrepancy ? <StatusPill status="Discrepancy" tone="warning" size="sm" /> : "—",
        },
        { key: "version", header: "Version", align: "right", render: (p) => String(p.version) },
      ]}
    />
  );
}
