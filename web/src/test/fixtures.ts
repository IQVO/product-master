import type { GtinResolution, PackLevelDefinition, PackLevelList, PhysicalProfile, Product } from "../types";

/** A classified product with declared and measured values that disagree
 *  (the openapi.yaml recordMeasurement example). */
export const MEASURED_PROFILE: PhysicalProfile = {
  declared: { lengthMm: 200, widthMm: 120, heightMm: 80, weightG: 1500, volumeMm3: 1920000 },
  measured: {
    lengthMm: 205,
    widthMm: 121,
    heightMm: 82,
    weightG: 1720,
    volumeMm3: 2034010,
    measuredAt: "2026-10-06T14:05:00Z",
    deviceId: "CUBISCAN-03",
  },
  effective: { lengthMm: 205, widthMm: 121, heightMm: 82, weightG: 1720, volumeMm3: 2034010 },
  effectiveSource: "measured",
  discrepancy: true,
};

export function product(over: Partial<Product> = {}): Product {
  return {
    sku: "SKU-1",
    description: "Lithium battery pack 12V",
    version: 4,
    classification: {
      handlingTags: ["Hazmat", "TemperatureSensitive"],
      temperatureClass: "Frozen",
      dotHazardClass: 3,
      classificationSource: "native",
    },
    physicalProfile: MEASURED_PROFILE,
    ...over,
  };
}

/** Registered only: no classification, nothing known physically. */
export const BARE: Product = {
  sku: "SKU-2",
  description: "",
  version: 1,
  physicalProfile: { effectiveSource: "none", discrepancy: false },
};

/** The openapi.yaml example chain: EACH -> CASE of 12, plus a retired earlier CASE of 6. */
export const EACH_LEVEL: PackLevelDefinition = {
  level: "EACH",
  gtin: "04006381333931",
  unitsPerPack: 1,
  dims: { lengthMm: 100, widthMm: 80, heightMm: 60, weightG: 500, volumeMm3: 480000 },
  isOrderable: false,
  isDespatchUnit: false,
  active: true,
  effectiveFrom: "2026-10-06T19:00:00Z",
};

export const CASE_LEVEL: PackLevelDefinition = {
  level: "CASE",
  gtin: "24006381333935",
  childGtin: "04006381333931",
  qtyOfChild: 12,
  unitsPerPack: 12,
  dims: { lengthMm: 400, widthMm: 300, heightMm: 200, weightG: 6500, volumeMm3: 24000000 },
  isOrderable: true,
  isDespatchUnit: true,
  active: true,
  effectiveFrom: "2026-10-06T20:00:00Z",
};

export const OLD_CASE_LEVEL: PackLevelDefinition = {
  level: "CASE",
  gtin: "14006381333938",
  childGtin: "04006381333931",
  qtyOfChild: 6,
  unitsPerPack: 6,
  isOrderable: true,
  isDespatchUnit: false,
  active: false,
  effectiveFrom: "2026-09-01T08:00:00Z",
  effectiveTo: "2026-10-06T20:00:00Z",
};

export function packLevels(items: PackLevelDefinition[] = [CASE_LEVEL, OLD_CASE_LEVEL, EACH_LEVEL]): PackLevelList {
  return { sku: "SKU-1", version: 3, items };
}

export const GTIN_CASE: GtinResolution = {
  gtin: "24006381333935",
  sku: "SKU-1",
  level: "CASE",
  unitsPerPack: 12,
  childGtin: "04006381333931",
  active: true,
  effectiveFrom: "2026-10-06T20:00:00Z",
};
