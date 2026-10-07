import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { product } from "./test/fixtures";

function mount(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );
}

/** Mounted the way the console mounts the remote: inside the host's own
 *  `<Route path="/product-master/*">` splat route. Relative links behave
 *  differently there than at the router root, which root-mounted tests cannot
 *  see. */
function mountUnderHost(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/product-master/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

function subNavHrefs(): string[] {
  const nav = screen.getByRole("navigation", { name: "Product master sections" });
  return within(nav)
    .getAllByRole("link")
    .map((a) => a.getAttribute("href") ?? "");
}

function routes() {
  return mockApi({
    "GET /products": json({ items: [product()] }),
    "GET /products/SKU-1": json(product()),
  });
}

describe("App (the exposed ./App)", () => {
  it("renders the product list at the index route with a sub-nav", async () => {
    routes();
    mount("/");
    expect(screen.getByRole("heading", { name: "Products" })).toBeInTheDocument();
    expect(await screen.findByRole("link", { name: "SKU-1" })).toBeInTheDocument();
    const nav = screen.getByRole("navigation", { name: "Product master sections" });
    expect(nav).toHaveTextContent("Products");
    expect(nav).toHaveTextContent("Register product");
  });

  it("routes relatively: /register and /products/:sku work at the root", async () => {
    routes();
    const { unmount } = mount("/register");
    expect(screen.getByRole("heading", { name: "Register product" })).toBeInTheDocument();
    unmount();
    mount("/products/SKU-1");
    expect(await screen.findByRole("heading", { name: "SKU-1" })).toBeInTheDocument();
  });

  describe("mounted under the console's /product-master/* splat route", () => {
    it.each(["/product-master", "/product-master/register", "/product-master/products/SKU-1"])(
      "sub-nav links resolve against the mount point, not the current URL (%s)",
      async (start) => {
        routes();
        mountUnderHost(start);
        await screen.findByRole("navigation", { name: "Product master sections" });
        expect(subNavHrefs()).toEqual(["/product-master", "/product-master/register"]);
      },
    );

    it.each([
      ["/product-master", "Products"],
      ["/product-master/register", "Register product"],
    ])("marks only the current section active (%s)", async (start, active) => {
      routes();
      mountUnderHost(start);
      const nav = await screen.findByRole("navigation", { name: "Product master sections" });
      for (const name of ["Products", "Register product"]) {
        const link = within(nav).getByRole("link", { name });
        if (name === active) expect(link).toHaveAttribute("aria-current", "page");
        else expect(link).not.toHaveAttribute("aria-current");
      }
    });

    it("links a product row to its page and back, under the prefix", async () => {
      routes();
      const user = userEvent.setup();
      mountUnderHost("/product-master");
      const row = await screen.findByRole("link", { name: "SKU-1" });
      expect(row).toHaveAttribute("href", "/product-master/products/SKU-1");

      await user.click(row);
      expect(await screen.findByRole("heading", { name: "SKU-1" })).toBeInTheDocument();
      const back = screen.getByRole("link", { name: "← All products" });
      expect(back).toHaveAttribute("href", "/product-master");

      await user.click(back);
      expect(await screen.findByRole("heading", { name: "Products" })).toBeInTheDocument();
    });

    it("navigates between all sections in any order, hop after hop", async () => {
      routes();
      const user = userEvent.setup();
      mountUnderHost("/product-master/register");
      expect(screen.getByRole("heading", { name: "Register product" })).toBeInTheDocument();

      await user.click(screen.getByRole("link", { name: "Products" }));
      expect(await screen.findByRole("heading", { name: "Products" })).toBeInTheDocument();

      await user.click(screen.getByRole("link", { name: "Register product" }));
      expect(await screen.findByRole("heading", { name: "Register product" })).toBeInTheDocument();
      expect(subNavHrefs()).toEqual(["/product-master", "/product-master/register"]);
    });

    it("links a freshly registered product to its page under the prefix", async () => {
      mockApi({ "PUT /products/SKU-9": json({ ...product({ sku: "SKU-9", version: 1 }) }, 201) });
      const user = userEvent.setup();
      mountUnderHost("/product-master/register");
      await user.type(screen.getByLabelText(/^SKU/), "SKU-9");
      await user.click(screen.getByRole("button", { name: "Register" }));
      expect(await screen.findByRole("link", { name: "Open SKU-9" })).toHaveAttribute(
        "href",
        "/product-master/products/SKU-9",
      );
    });
  });
});
