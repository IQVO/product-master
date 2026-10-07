import { describe, expect, it } from "vitest";
import { PRODUCT_MASTER_API_BASE, resolveProductMasterApiBase } from "./config";

describe("resolveProductMasterApiBase", () => {
  it("builds the production API base from the runtime API origin", () => {
    expect(resolveProductMasterApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/product-master",
    );
  });

  it("normalizes trailing slashes on the runtime API origin", () => {
    expect(resolveProductMasterApiBase({ apiOrigin: "https://warehouse.example/" }, true)).toBe(
      "https://warehouse.example/api/product-master",
    );
    expect(resolveProductMasterApiBase({ apiOrigin: "https://warehouse.example///" }, true)).toBe(
      "https://warehouse.example/api/product-master",
    );
  });

  it("fails loudly when production runtime configuration has no API origin", () => {
    expect(() => resolveProductMasterApiBase({}, true)).toThrow(
      "window.__WAREHOUSE_CONFIG__.apiOrigin is required in production",
    );
    expect(() => resolveProductMasterApiBase({ apiOrigin: "" }, true)).toThrow(
      "window.__WAREHOUSE_CONFIG__.apiOrigin is required in production",
    );
  });

  it("falls back to the service's own dev port outside production", () => {
    expect(resolveProductMasterApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("still prefers a runtime origin over the dev fallback", () => {
    expect(resolveProductMasterApiBase({ apiOrigin: "http://localhost:8000" }, false)).toBe(
      "http://localhost:8000/api/product-master",
    );
  });
});

describe("PRODUCT_MASTER_API_BASE", () => {
  it("loads without /config.json (window.__WAREHOUSE_CONFIG__ absent) in a non-production build", () => {
    expect(window.__WAREHOUSE_CONFIG__).toBeUndefined();
    expect(PRODUCT_MASTER_API_BASE).toBe("http://localhost:8080");
  });
});
