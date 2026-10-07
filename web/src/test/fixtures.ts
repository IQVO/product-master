import type { PhysicalProfile, Product } from "../types";

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
