import { describe, expect, it } from "vitest";
import { ApiError, classifyProduct, getProduct, listProducts, recordMeasurement, registerProduct, toApiError } from "./api";
import { json, mockApi, problem } from "./test/fetchMock";
import { BARE, MEASURED_PROFILE, product } from "./test/fixtures";

describe("listProducts", () => {
  it("sends only the filters that are set, with the API's parameter names", async () => {
    const api = mockApi({ "GET /products": json({ items: [product()], nextCursor: "U0tVLTE" }) });
    const page = await listProducts({ limit: 25, handlingTag: "Hazmat", classified: false });
    expect(page.items).toHaveLength(1);
    expect(page.nextCursor).toBe("U0tVLTE");
    const q = api.to("GET /products")[0].query;
    expect(Object.fromEntries(q)).toEqual({ limit: "25", handlingTag: "Hazmat", classified: "false" });
  });

  it("passes the cursor through and treats a missing nextCursor as the last page", async () => {
    const api = mockApi({ "GET /products": json({ items: [BARE] }) });
    const page = await listProducts({ cursor: "abc" });
    expect(page.nextCursor).toBeUndefined();
    expect(api.to("GET /products")[0].query.get("cursor")).toBe("abc");
  });
});

describe("writes", () => {
  it("PUTs a JSON body and reports the status (201 vs 200) of a registration", async () => {
    const api = mockApi({ "PUT /products/SKU-9": json({ ...BARE, sku: "SKU-9" }, 201) });
    const res = await registerProduct("SKU-9", { description: "Box" });
    expect(res.status).toBe(201);
    const call = api.to("PUT /products/SKU-9")[0];
    expect(call.body).toEqual({ description: "Box" });
    expect(call.headers).toEqual({ "Content-Type": "application/json" });
  });

  it("encodes the SKU as one path segment", async () => {
    const api = mockApi({ "GET /products/A%23B": json(product({ sku: "A#B" })) });
    await getProduct("A#B");
    expect(api.calls[0].path).toBe("/products/A%23B");
  });

  it("returns the classification with its version", async () => {
    mockApi({
      "PUT /products/SKU-1/classification": json(
        { sku: "SKU-1", handlingTags: ["Fragile"], classificationSource: "native", version: 5 },
        200,
      ),
    });
    const res = await classifyProduct("SKU-1", { handlingTags: ["Fragile"] });
    expect(res.data.version).toBe(5);
  });

  it("returns the whole physical profile after a measurement", async () => {
    mockApi({ "PUT /products/SKU-1/dimensions/measured": json({ ...MEASURED_PROFILE, version: 6 }) });
    const profile = await recordMeasurement("SKU-1", {
      lengthMm: 1,
      widthMm: 1,
      heightMm: 1,
      weightG: 1,
      measuredAt: "2026-10-06T14:05:00Z",
    });
    expect(profile.version).toBe(6);
    expect(profile.discrepancy).toBe(true);
  });
});

describe("errors", () => {
  it("turns an RFC 7807 problem into an ApiError with title, detail and slug", async () => {
    mockApi({
      "PUT /products/SKU-1/dimensions/measured": problem(
        409,
        "stale-measurement",
        "A newer measurement is already recorded",
        "measurement is older than the current one",
      ),
    });
    const err = await recordMeasurement("SKU-1", {
      lengthMm: 1,
      widthMm: 1,
      heightMm: 1,
      weightG: 1,
      measuredAt: "2026-10-06T14:05:00Z",
    }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    const e = err as ApiError;
    expect(e.status).toBe(409);
    expect(e.title).toBe("A newer measurement is already recorded");
    expect(e.detail).toBe("measurement is older than the current one");
    expect(e.slug).toBe("stale-measurement");
  });

  it("falls back to the HTTP status line when the body is not JSON", async () => {
    mockApi({ "GET /products/SKU-1": new Response("boom", { status: 502, statusText: "Bad Gateway" }) });
    const err = (await getProduct("SKU-1").catch((e: unknown) => e)) as ApiError;
    expect(err.title).toBe("502 Bad Gateway");
    expect(err.slug).toBe("");
  });

  it("reports a network failure as an ApiError with status 0", async () => {
    mockApi({}); // every request rejects
    const err = (await getProduct("SKU-1").catch((e: unknown) => e)) as ApiError;
    expect(err.status).toBe(0);
    expect(err.title).toBe("Network error");
    expect(err.detail).toMatch(/unmocked request/);
  });

  it("toApiError passes an ApiError through and wraps anything else", () => {
    const original = new ApiError(400, { title: "t", detail: "d" }, "x");
    expect(toApiError(original)).toBe(original);
    expect(toApiError("plain").detail).toBe("plain");
  });
});
