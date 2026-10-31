import { describe, expect, it } from "vitest";
import { formatDims, formatVolume, formatWeight, gtinProblem, orderLevels, packLabel, skuProblem, sortTags, utcInputToIso, utcLabel, wholeNumber } from "./lib";

describe("formatters", () => {
  it("writes dimensions, weight and volume with units, and a dash when unknown", () => {
    expect(formatDims({ lengthMm: 1200, widthMm: 120, heightMm: 80, weightG: 1, volumeMm3: 1 })).toBe(
      "1,200 × 120 × 80 mm",
    );
    expect(formatDims(undefined)).toBe("—");
    expect(formatWeight(1720)).toBe("1,720 g");
    expect(formatWeight(undefined)).toBe("—");
    expect(formatVolume(2034010)).toBe("2,034,010 mm³");
    expect(formatVolume(undefined)).toBe("—");
  });

  it("sorts tags into the service's stable order", () => {
    expect(sortTags(["HighValue", "TemperatureSensitive", "Hazmat"])).toEqual([
      "Hazmat",
      "TemperatureSensitive",
      "HighValue",
    ]);
  });
});

describe("wholeNumber", () => {
  it.each([
    ["12", 12],
    [" 7 ", 7],
    ["0", 0],
  ])("accepts %j", (raw, n) => expect(wholeNumber(raw)).toBe(n));

  it.each(["", "12.5", "-1", "1e3", "abc", "99999999999999999999"])("rejects %j", (raw) =>
    expect(wholeNumber(raw)).toBeNull(),
  );
});

describe("UTC instants", () => {
  it("reads a datetime-local value as UTC", () => {
    expect(utcInputToIso("2026-10-06T14:05")).toBe("2026-10-06T14:05:00Z");
    expect(utcInputToIso("2026-10-06T14:05:30")).toBe("2026-10-06T14:05:30Z");
  });

  it("rejects an empty or malformed value", () => {
    expect(utcInputToIso("")).toBeNull();
    expect(utcInputToIso("2026-13-45T99:99")).toBeNull();
    expect(utcInputToIso("yesterday")).toBeNull();
  });

  it("labels an instant in UTC", () => {
    expect(utcLabel("2026-10-06T14:05:00Z")).toBe("2026-10-06 14:05Z");
    expect(utcLabel("2026-10-06T16:05:00+02:00")).toBe("2026-10-06 14:05Z");
    expect(utcLabel(undefined)).toBe("—");
    expect(utcLabel("not a date")).toBe("not a date");
  });
});

describe("skuProblem", () => {
  it("accepts a valid SKU", () => expect(skuProblem("SKU-1")).toBeNull());
  it.each([
    ["", "Enter a SKU."],
    ["a".repeat(65), "A SKU is at most 64 characters."],
    ["SKU 1", "A SKU cannot contain spaces, control characters or '/'."],
    ["SKU/1", "A SKU cannot contain spaces, control characters or '/'."],
  ])("rejects %j", (sku, message) => expect(skuProblem(sku)).toBe(message));
});

describe("packLabel", () => {
  it("names a pack by its eaches", () => {
    expect(packLabel("EACH", 1)).toBe("Each");
    expect(packLabel("INNER", 6)).toBe("Inner of 6 eaches");
    expect(packLabel("CASE", 12)).toBe("Case of 12 eaches");
    expect(packLabel("PALLET", 1200)).toBe("Pallet of 1,200 eaches");
  });
});

describe("gtinProblem", () => {
  it("accepts 8, 12, 13 and 14 digits only", () => {
    for (const ok of ["40170725", "036000291452", "4006381333931", "24006381333935"]) expect(gtinProblem(ok)).toBeNull();
    expect(gtinProblem("")).toBe("Enter the GTIN.");
    expect(gtinProblem("12ab")).toBe("The GTIN is digits only.");
    expect(gtinProblem("123456789")).toBe("The GTIN must have 8, 12, 13 or 14 digits.");
    expect(gtinProblem("", "child GTIN")).toBe("Enter the child GTIN.");
  });
});

describe("orderLevels", () => {
  const def = (level: "EACH" | "INNER" | "CASE" | "PALLET", active: boolean, from: string) => ({
    level, gtin: `${level}-${from}`, unitsPerPack: 1, isOrderable: false, isDespatchUnit: false, active, effectiveFrom: from,
  });
  it("orders by hierarchy, then active first, then newest first", () => {
    const out = orderLevels([
      def("PALLET", true, "2026-03"),
      def("CASE", false, "2026-01"),
      def("EACH", true, "2026-01"),
      def("CASE", false, "2026-02"),
      def("CASE", true, "2026-03"),
      def("INNER", true, "2026-01"),
    ]);
    expect(out.map((d) => `${d.level}:${d.active ? "A" : "R"}:${d.effectiveFrom}`)).toEqual([
      "EACH:A:2026-01", "INNER:A:2026-01", "CASE:A:2026-03", "CASE:R:2026-02", "CASE:R:2026-01", "PALLET:A:2026-03",
    ]);
  });
});
