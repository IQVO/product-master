import { describe, expect, it } from "vitest";
import { ApiError, classifyProduct, declarePackLevel, getProduct, listPackLevels, listProducts, resolveGtin, retirePackLevel, recordMeasurement, registerProduct, toApiError } from "./api";
import { json, mockApi, problem } from "./test/fetchMock";
import { BARE, CASE_LEVEL, GTIN_CASE, MEASURED_PROFILE, packLevels, product } from "./test/fixtures";

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

describe("pack levels", () => {
  it("lists a product's levels, treating a missing items array as empty", async () => {
    mockApi({ "GET /products/SKU-1/pack-levels": json(packLevels()), "GET /products/SKU-2/pack-levels": json({ sku: "SKU-2", version: 1 }) });
    expect((await listPackLevels("SKU-1")).items).toHaveLength(3);
    expect((await listPackLevels("SKU-2")).items).toEqual([]);
  });

  it("declares with PUT, the level in the path and the JSON body, keeping the 201/200 status", async () => {
    const api = mockApi({ "PUT /products/SKU-1/pack-levels/CASE": json({ sku: "SKU-1", version: 3, ...CASE_LEVEL }, 201) });
    const res = await declarePackLevel("SKU-1", "CASE", { gtin: "24006381333935", childGtin: "4006381333931", qtyOfChild: 12 });
    expect(res.status).toBe(201);
    expect(res.data.unitsPerPack).toBe(12);
    const [call] = api.to("PUT /products/SKU-1/pack-levels/CASE");
    expect(call.headers).toEqual({ "Content-Type": "application/json" });
    expect(call.body).toEqual({ gtin: "24006381333935", childGtin: "4006381333931", qtyOfChild: 12 });
  });

  it("retires with a keyed POST to the /retirements noun; reason is optional", async () => {
    const api = mockApi({ "POST /products/SKU-1/pack-levels/CASE/retirements": json({ sku: "SKU-1", version: 4, ...CASE_LEVEL, active: false }) });
    await retirePackLevel("SKU-1", "CASE");
    await retirePackLevel("SKU-1", "CASE", "discontinued");
    const calls = api.to("POST /products/SKU-1/pack-levels/CASE/retirements");
    expect(calls[0].body).toBeUndefined();
    expect(calls[1].body).toEqual({ reason: "discontinued" });
    expect(calls[0].headers).toMatchObject({ "Idempotency-Key": expect.stringMatching(/^[\x20-\x7e]{1,128}$/) });
    expect(calls[0].headers?.["Idempotency-Key"]).not.toEqual(calls[1].headers?.["Idempotency-Key"]);
  });

  it("a retry of the same submit reuses the caller's Idempotency-Key", async () => {
    const api = mockApi({ "POST /products/SKU-1/pack-levels/CASE/retirements": json({ sku: "SKU-1", version: 4, ...CASE_LEVEL, active: false }) });
    await retirePackLevel("SKU-1", "CASE", "x", "key-1");
    await retirePackLevel("SKU-1", "CASE", "x", "key-1");
    const calls = api.to("POST /products/SKU-1/pack-levels/CASE/retirements");
    expect(calls.map((c) => c.headers?.["Idempotency-Key"])).toEqual(["key-1", "key-1"]);
  });

  it("lists pack levels by following nextCursor until it is absent", async () => {
    const api = mockApi({
      "GET /products/SKU-1/pack-levels": (call) =>
        call.query.get("cursor")
          ? json({ sku: "SKU-1", version: 4, items: [CASE_LEVEL] })
          : json({ sku: "SKU-1", version: 4, items: [CASE_LEVEL], nextCursor: "abc" }),
    });
    const list = await listPackLevels("SKU-1");
    expect(list.items).toHaveLength(2);
    expect(api.to("GET /products/SKU-1/pack-levels")).toHaveLength(2);
    expect(api.to("GET /products/SKU-1/pack-levels")[1].query.get("cursor")).toBe("abc");
  });

  it("resolves a GTIN, and a failure carries the problem code", async () => {
    mockApi({
      "GET /gtins/24006381333935": json(GTIN_CASE),
      "GET /gtins/5901234123457": problem(404, "gtin-not-found", "GTIN not found", "GTIN not found"),
    });
    expect((await resolveGtin("24006381333935")).level).toBe("CASE");
    await expect(resolveGtin("5901234123457")).rejects.toMatchObject({ status: 404, slug: "gtin-not-found" });
  });
});
