import { describe, expect, it } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { ProductListScreen } from "./ProductListScreen";
import { json, mockApi, pending, problem } from "../test/fetchMock";
import { BARE, product } from "../test/fixtures";

function mount() {
  return render(
    <MemoryRouter>
      <ProductListScreen />
    </MemoryRouter>,
  );
}

describe("ProductListScreen", () => {
  it("shows a loading state", () => {
    mockApi({ "GET /products": pending() });
    mount();
    expect(screen.getByText("Loading products…")).toBeInTheDocument();
  });

  it("lists products with tags, effective dimensions, discrepancy and version", async () => {
    const api = mockApi({ "GET /products": json({ items: [product(), BARE] }) });
    mount();
    const table = await screen.findByRole("table");
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(2);

    const first = within(rows[0]);
    expect(first.getByRole("link", { name: "SKU-1" })).toHaveAttribute("href", "/products/SKU-1");
    expect(first.getByText("Lithium battery pack 12V")).toBeInTheDocument();
    expect(first.getByText("Hazmat")).toHaveAttribute("data-tone", "danger");
    expect(first.getByText("TemperatureSensitive")).toBeInTheDocument();
    expect(first.getByText("205 × 121 × 82 mm")).toBeInTheDocument();
    expect(first.getByText("1,720 g")).toBeInTheDocument();
    expect(first.getByText("measured")).toBeInTheDocument();
    expect(first.getByText("Discrepancy")).toHaveAttribute("data-tone", "warning");
    expect(first.getByText("4")).toBeInTheDocument();

    const second = within(rows[1]);
    expect(second.getByText("Unclassified")).toBeInTheDocument();
    expect(second.getByText("none")).toBeInTheDocument();
    expect(second.queryByText("Discrepancy")).not.toBeInTheDocument();

    // first page, default size, no filters
    expect(Object.fromEntries(api.to("GET /products")[0].query)).toEqual({ limit: "25" });
  });

  it("applies the handling-tag, classified and page-size filters", async () => {
    const api = mockApi({ "GET /products": json({ items: [product()] }) });
    const user = userEvent.setup();
    mount();
    await screen.findByRole("table");
    await user.selectOptions(screen.getByLabelText("Handling tag"), "Fragile");
    await user.selectOptions(screen.getByLabelText("Classification"), "Unclassified only");
    await user.selectOptions(screen.getByLabelText("Page size"), "100 per page");
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    await waitFor(() => expect(api.to("GET /products")).toHaveLength(2));
    expect(Object.fromEntries(api.to("GET /products")[1].query)).toEqual({
      limit: "100",
      handlingTag: "Fragile",
      classified: "false",
    });
  });

  it("pages forward with nextCursor and back to the cursors already visited", async () => {
    const api = mockApi({
      "GET /products": (call) => {
        const c = call.query.get("cursor");
        if (!c) return json({ items: [product()], nextCursor: "C2" });
        if (c === "C2") return json({ items: [BARE], nextCursor: "C3" });
        return json({ items: [product({ sku: "SKU-3" })] });
      },
    });
    const user = userEvent.setup();
    mount();
    await screen.findByRole("link", { name: "SKU-1" });
    expect(screen.getByRole("button", { name: "Previous page" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByRole("link", { name: "SKU-2" })).toBeInTheDocument();
    expect(screen.getByText("Page 2")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByRole("link", { name: "SKU-3" })).toBeInTheDocument();
    // last page: no nextCursor
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Previous page" }));
    expect(await screen.findByRole("link", { name: "SKU-2" })).toBeInTheDocument();
    expect(api.to("GET /products").map((c) => c.query.get("cursor"))).toEqual([null, "C2", "C3", "C2"]);
  });

  it("starts again at the first page when the filters change", async () => {
    const api = mockApi({
      "GET /products": (call) =>
        call.query.get("cursor") ? json({ items: [BARE] }) : json({ items: [product()], nextCursor: "C2" }),
    });
    const user = userEvent.setup();
    mount();
    await screen.findByRole("link", { name: "SKU-1" });
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findByRole("link", { name: "SKU-2" });
    await user.selectOptions(screen.getByLabelText("Handling tag"), "Hazmat");
    await user.click(screen.getByRole("button", { name: "Apply filters" }));
    await screen.findByRole("link", { name: "SKU-1" });
    expect(screen.getByText("Page 1")).toBeInTheDocument();
    expect(api.to("GET /products").at(-1)?.query.get("cursor")).toBeNull();
  });

  it("shows an empty state", async () => {
    mockApi({ "GET /products": json({ items: [] }) });
    mount();
    expect(await screen.findByText("No products match these filters.")).toBeInTheDocument();
  });

  it("surfaces the problem title and detail when the list fails", async () => {
    mockApi({
      "GET /products": problem(400, "invalid-query", "Invalid query parameter", "limit must be 1..500"),
    });
    mount();
    expect(await screen.findByText("Invalid query parameter")).toBeInTheDocument();
    expect(screen.getByText("limit must be 1..500")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("HTTP 400");
  });
});
