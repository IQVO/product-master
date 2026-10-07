/**
 * Wire shapes of product-master's REST API (apis/openapi.yaml). JSON is
 * camelCase; optional parts of a product are OMITTED, never null (except
 * dotHazardClass, which the spec marks nullable).
 */

/** Closed set, in the stable order the service emits them. */
export const HANDLING_TAGS = ["Hazmat", "Fragile", "TemperatureSensitive", "Oversized", "HighValue"] as const;
export type HandlingTag = (typeof HANDLING_TAGS)[number];

export const TEMPERATURE_CLASSES = ["Ambient", "Chilled", "Frozen"] as const;
export type TemperatureClass = (typeof TEMPERATURE_CLASSES)[number];

export type ClassificationSource = "native" | "legacy-import";
export type EffectiveSource = "measured" | "declared" | "none";

export interface UnitDimensions {
  lengthMm: number;
  widthMm: number;
  heightMm: number;
  weightG: number;
  volumeMm3: number;
}

export interface Measurement extends UnitDimensions {
  measuredAt: string;
  deviceId?: string;
}

export interface PhysicalProfile {
  declared?: UnitDimensions;
  measured?: Measurement;
  effective?: UnitDimensions;
  effectiveSource: EffectiveSource;
  /** Declared and measured both exist and volume or weight differ by more than 10 %. */
  discrepancy: boolean;
  /** Present on write responses: the product version after the change. */
  version?: number;
}

export interface Classification {
  handlingTags: HandlingTag[];
  temperatureClass?: TemperatureClass;
  dotHazardClass?: number | null;
  classificationSource: ClassificationSource;
}

export interface ProductClassification extends Classification {
  sku: string;
  version: number;
}

export interface Product {
  sku: string;
  description: string;
  version: number;
  classification?: Classification;
  physicalProfile: PhysicalProfile;
}

export interface ProductPage {
  items: Product[];
  /** Absent on the last page. */
  nextCursor?: string;
}

export interface ListProductsQuery {
  limit?: number;
  cursor?: string;
  handlingTag?: HandlingTag;
  classified?: boolean;
}

export interface RegisterProductInput {
  description: string;
}

export interface ClassifyProductInput {
  handlingTags: HandlingTag[];
  temperatureClass?: TemperatureClass;
  dotHazardClass?: number;
}

export interface UnitDimensionsInput {
  lengthMm: number;
  widthMm: number;
  heightMm: number;
  weightG: number;
}

export interface MeasurementInput extends UnitDimensionsInput {
  measuredAt: string;
  deviceId?: string;
}
