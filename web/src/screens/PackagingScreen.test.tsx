import { describe, expect, it } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { PackagingRoute } from "./PackagingScreen";
import { json, mockApi, pending, problem } from "../test/fetchMock";
import type { Handler } from "../test/fetchMock";
import { CASE_LEVEL, EACH_LEVEL, GTIN_CASE, packLevels } from "../test/fixtures";

function mount(sku = "SKU-1") {
  return render(
    <MemoryRouter initialEntries={[`/products/${sku}/packaging`]}>
      <Routes>
        <Route path="/products/:sku/packaging" element={<PackagingRoute />} />
        <Route path="/products/:sku" element={<div>product detail</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

function routes(over: Record<string, Handler> = {}) {
  return mockApi({ "GET /products/SKU-1/pack-levels": json(packLevels()), ...over });
}

function form(name: string) {
  return within(screen.getByRole("form", { name }));
}

function bodyRows() {
  return within(screen.getByRole("table")).getAllByRole("row").slice(1);
}

describe("PackagingScreen hierarchy", () => {
  it("shows a loading state", () => {
    routes({ "GET /products/SKU-1/pack-levels": pending() });
    mount();
    expect(screen.getByText("Loading pack levels…")).toBeInTheDocument();
  });

  it("orders EACH -> CASE, active before retired, and labels quantities as eaches", async () => {
    routes();
    mount();
    expect(await screen.findByRole("heading", { name: "Packaging · SKU-1" })).toBeInTheDocument();
    const rows = bodyRows();
    expect(rows).toHaveLength(3);
    expect(rows[0]).toHaveTextContent("EACH");
    expect(rows[0]).toHaveTextContent("04006381333931");
    expect(rows[0]).toHaveTextContent("100 × 80 × 60 mm");
    expect(rows[0]).toHaveTextContent("500 g");
    expect(rows[0]).toHaveTextContent("Active");
    expect(rows[1]).toHaveTextContent("Case of 12 eaches");
    expect(rows[1]).toHaveTextContent("24006381333935");
    expect(rows[1]).toHaveTextContent("12 × EACH (04006381333931)");
    expect(rows[1]).toHaveTextContent("400 × 300 × 200 mm");
    expect(rows[1]).toHaveTextContent("6,500 g");
    expect(rows[1]).toHaveTextContent("orderable, despatch unit");
    expect(rows[2]).toHaveTextContent("Case of 6 eaches");
    expect(rows[2]).toHaveTextContent("Retired");
    expect(rows[2]).toHaveTextContent("2026-10-06 20:00Z");
    // No dimensions known: a dash, never a made-up zero.
    expect(rows[2]).toHaveTextContent("—");
  });

  it("offers Retire only on active levels", async () => {
    routes();
    mount();
    await screen.findByRole("table");
    expect(screen.getAllByRole("button", { name: /^Retire / })).toHaveLength(2);
    expect(within(bodyRows()[2]).queryByRole("button")).toBeNull();
  });

  it("says so when no level is declared yet", async () => {
    routes({ "GET /products/SKU-1/pack-levels": json(packLevels([])) });
    mount();
    expect(await screen.findByText(/No pack levels declared yet/)).toBeInTheDocument();
  });

  it("explains a 404 product-not-found with what to do", async () => {
    mockApi({ "GET /products/NOPE/pack-levels": problem(404, "product-not-found", "Product not found", "product not found") });
    mount("NOPE");
    expect(await screen.findByText("No product is registered under this SKU. Register it first.")).toBeInTheDocument();
    expect(screen.getByText("Product not found")).toBeInTheDocument();
  });

  it("links back to the product", async () => {
    routes();
    mount();
    await screen.findByRole("table");
    expect(screen.getByRole("link", { name: "← Product SKU-1" })).toHaveAttribute("href", "/products/SKU-1");
  });
});

describe("declaring a pack level", () => {
  it("sends a CASE with its child, quantity, dimensions and flags, then re-reads the list", async () => {
    const api = routes({
      "PUT /products/SKU-1/pack-levels/PALLET": json({ sku: "SKU-1", version: 4, ...CASE_LEVEL, level: "PALLET", gtin: "10012345678902", unitsPerPack: 120, qtyOfChild: 10 }, 201),
    });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    const f = form("Declare pack level");
    await user.selectOptions(f.getByLabelText(/Pack level/), "PALLET");
    await user.type(f.getByLabelText(/^GTIN/), "10012345678902");
    await user.type(f.getByLabelText(/^Child GTIN/), "24006381333935");
    await user.type(f.getByLabelText(/Quantity of child level/), "10");
    await user.type(f.getByLabelText(/Length \(mm\)/), "1200");
    await user.type(f.getByLabelText(/Width \(mm\)/), "800");
    await user.type(f.getByLabelText(/Height \(mm\)/), "1500");
    await user.type(f.getByLabelText(/Weight \(g\)/), "70000");
    await user.click(f.getByLabelText("Ships as a unit"));
    await user.click(f.getByRole("button", { name: "Declare pack level" }));

    expect(await screen.findByText(/Pallet of 120 eaches \(10012345678902\) declared \(version 4\)/)).toBeInTheDocument();
    const [call] = api.to("PUT /products/SKU-1/pack-levels/PALLET");
    expect(call.body).toEqual({
      gtin: "10012345678902",
      childGtin: "24006381333935",
      qtyOfChild: 10,
      dims: { lengthMm: 1200, widthMm: 800, heightMm: 1500, weightG: 70000 },
      isOrderable: false,
      isDespatchUnit: true,
    });
    await waitFor(() => expect(api.to("GET /products/SKU-1/pack-levels")).toHaveLength(2));
  });

  it("sends an EACH with no child and no dimensions", async () => {
    const api = routes({ "PUT /products/SKU-1/pack-levels/EACH": json({ sku: "SKU-1", version: 2, ...EACH_LEVEL }, 200) });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    const f = form("Declare pack level");
    await user.selectOptions(f.getByLabelText(/Pack level/), "EACH");
    expect(f.queryByLabelText(/^Child GTIN/)).toBeNull();
    await user.type(f.getByLabelText(/^GTIN/), "4006381333931");
    await user.click(f.getByRole("button", { name: "Declare pack level" }));
    expect(await screen.findByText(/Each \(04006381333931\) saved \(version 2\)/)).toBeInTheDocument();
    expect(api.to("PUT /products/SKU-1/pack-levels/EACH")[0].body).toEqual({
      gtin: "4006381333931",
      isOrderable: false,
      isDespatchUnit: false,
    });
  });

  it.each([
    ["no level", {}, "Choose the pack level."],
    ["letters in the GTIN", { level: "EACH", gtin: "40063813abc31" }, "The GTIN is digits only."],
    ["a 10-digit GTIN", { level: "EACH", gtin: "4006381333" }, "The GTIN must have 8, 12, 13 or 14 digits."],
    ["a quantity of 1", { level: "CASE", gtin: "24006381333935", child: "04006381333931", qty: "1" }, "Quantity of the child level must be a whole number from 2 to 1,000,000."],
    ["partial dimensions", { level: "EACH", gtin: "4006381333931", length: "100" }, "Width must be a whole number from 1 to 20,000 mm"],
  ])("rejects %s before calling the service", async (_name, v: Record<string, string>, message) => {
    const api = routes();
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    const f = form("Declare pack level");
    if (v.level) await user.selectOptions(f.getByLabelText(/Pack level/), v.level);
    if (v.gtin) await user.type(f.getByLabelText(/^GTIN/), v.gtin);
    if (v.child) await user.type(f.getByLabelText(/^Child GTIN/), v.child);
    if (v.qty) await user.type(f.getByLabelText(/Quantity of child level/), v.qty);
    if (v.length) await user.type(f.getByLabelText(/Length \(mm\)/), v.length);
    await user.click(f.getByRole("button", { name: "Declare pack level" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(api.calls.filter((c) => c.method === "PUT")).toHaveLength(0);
  });

  it.each([
    ["pack-quantity-immutable", 409, "NEW GTIN at this level"],
    ["gtin-already-assigned", 409, "never reused"],
    ["pack-level-order", 409, "Declare the child level first"],
    ["pack-dimensions-below-contents", 409, "weighs less than the packs it contains"],
    ["invalid-pack-quantity", 400, "2 to 1,000,000"],
    ["invalid-gtin", 400, "check digit"],
  ])("shows the title, detail and what to do for %s", async (slug, status, lead) => {
    routes({ "PUT /products/SKU-1/pack-levels/CASE": problem(status, slug, `Title of ${slug}`, `detail of ${slug}`) });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    const f = form("Declare pack level");
    await user.selectOptions(f.getByLabelText(/Pack level/), "CASE");
    await user.type(f.getByLabelText(/^GTIN/), "24006381333935");
    await user.type(f.getByLabelText(/^Child GTIN/), "04006381333931");
    await user.type(f.getByLabelText(/Quantity of child level/), "12");
    await user.click(f.getByRole("button", { name: "Declare pack level" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(`Title of ${slug}`);
    expect(alert).toHaveTextContent(`detail of ${slug}`);
    expect(alert).toHaveTextContent(`(HTTP ${status})`);
    expect(alert).toHaveTextContent(lead);
  });
});

describe("retiring a pack level", () => {
  it("confirms, POSTs :retire with the reason, and refreshes", async () => {
    const api = routes({
      "POST /products/SKU-1/pack-levels/CASE:retire": json({ sku: "SKU-1", version: 4, ...CASE_LEVEL, active: false, effectiveTo: "2026-10-06T21:01:00Z" }),
    });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Retire CASE 24006381333935" }));
    const f = form("Retire CASE");
    expect(f.getByText(/Levels above it are not retired/)).toBeInTheDocument();
    await user.type(f.getByLabelText("Reason"), "discontinued");
    await user.click(f.getByRole("button", { name: "Retire CASE" }));
    expect(await screen.findByText(/Case of 12 eaches \(24006381333935\) retired \(version 4\)/)).toBeInTheDocument();
    expect(api.to("POST /products/SKU-1/pack-levels/CASE:retire")[0].body).toEqual({ reason: "discontinued" });
    await waitFor(() => expect(api.to("GET /products/SKU-1/pack-levels")).toHaveLength(2));
  });

  it("sends no body when no reason is given, and Cancel closes the panel", async () => {
    const api = routes({
      "POST /products/SKU-1/pack-levels/EACH:retire": json({ sku: "SKU-1", version: 4, ...EACH_LEVEL, active: false }),
    });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Retire EACH 04006381333931" }));
    await user.click(form("Retire EACH").getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("form", { name: "Retire EACH" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Retire EACH 04006381333931" }));
    await user.click(form("Retire EACH").getByRole("button", { name: "Retire EACH" }));
    await screen.findByText(/retired \(version 4\)/);
    expect(api.to("POST /products/SKU-1/pack-levels/EACH:retire")[0].body).toBeUndefined();
  });

  it("shows a concurrent-modification conflict with what to do", async () => {
    routes({
      "POST /products/SKU-1/pack-levels/CASE:retire": problem(409, "concurrent-modification", "The resource was modified by another request", "concurrent modification"),
    });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Retire CASE 24006381333935" }));
    await user.click(form("Retire CASE").getByRole("button", { name: "Retire CASE" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Nothing was saved: reload and try again.");
    expect(alert).toHaveTextContent("(HTTP 409)");
  });

  it("explains pack-level-not-found", async () => {
    routes({ "POST /products/SKU-1/pack-levels/CASE:retire": problem(404, "pack-level-not-found", "Pack level not found", "pack level not found: CASE") });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Retire CASE 24006381333935" }));
    await user.click(form("Retire CASE").getByRole("button", { name: "Retire CASE" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("nothing to retire");
  });
});

describe("GTIN lookup", () => {
  it("resolves a GTIN to its SKU and level", async () => {
    const api = routes({ "GET /gtins/24006381333935": json(GTIN_CASE) });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("GTIN to look up"), "24006381333935");
    await user.click(screen.getByRole("button", { name: "Look up" }));
    const f = form("Look up GTIN");
    expect(await f.findByRole("link", { name: "SKU-1" })).toBeInTheDocument();
    expect(f.getByText("Case of 12 eaches")).toBeInTheDocument();
    expect(f.getByText("Active")).toBeInTheDocument();
    expect(api.to("GET /gtins/24006381333935")).toHaveLength(1);
  });

  it("shows a retired GTIN as retired with its end date", async () => {
    routes({ "GET /gtins/14006381333938": json({ ...GTIN_CASE, gtin: "14006381333938", unitsPerPack: 6, active: false, effectiveTo: "2026-10-07T09:30:00Z" }) });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("GTIN to look up"), "14006381333938");
    await user.click(screen.getByRole("button", { name: "Look up" }));
    const f = form("Look up GTIN");
    expect(await f.findByText("Retired")).toBeInTheDocument();
    expect(f.getByText("Case of 6 eaches")).toBeInTheDocument();
    expect(f.getByText("2026-10-07 09:30Z")).toBeInTheDocument();
  });

  it("explains gtin-not-found and invalid-gtin, and validates locally first", async () => {
    const api = routes({ "GET /gtins/5901234123457": problem(404, "gtin-not-found", "GTIN not found", "GTIN not found") });
    mount();
    await screen.findByRole("table");
    const user = userEvent.setup();
    const box = screen.getByLabelText("GTIN to look up");
    await user.type(box, "123");
    await user.click(screen.getByRole("button", { name: "Look up" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The GTIN must have 8, 12, 13 or 14 digits.");
    expect(api.to("GET /gtins/123")).toHaveLength(0);
    await user.clear(box);
    await user.type(box, "5901234123457");
    await user.click(screen.getByRole("button", { name: "Look up" }));
    const alert = await screen.findByText("No pack level holds this GTIN. Check the digits, or declare the GTIN on a product's pack level.");
    expect(alert).toBeInTheDocument();
    expect(screen.getAllByText("GTIN not found").length).toBeGreaterThan(0);
  });
});
