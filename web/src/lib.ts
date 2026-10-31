import { NO_VALUE, formatNumber } from "@warehouse/ui-kit";
import { HANDLING_TAGS, PACK_LEVELS } from "./types";
import type { HandlingTag, PackLevel, PackLevelDefinition, UnitDimensions } from "./types";

/** "200 × 120 × 80 mm" (length × width × height). */
export function formatDims(d: UnitDimensions | undefined): string {
  if (!d) return NO_VALUE;
  return `${formatNumber(d.lengthMm)} × ${formatNumber(d.widthMm)} × ${formatNumber(d.heightMm)} mm`;
}

export function formatWeight(grams: number | undefined): string {
  return grams === undefined ? NO_VALUE : `${formatNumber(grams)} g`;
}

export function formatVolume(mm3: number | undefined): string {
  return mm3 === undefined ? NO_VALUE : `${formatNumber(mm3)} mm³`;
}

/** Tags in the service's stable enum order (Hazmat, Fragile, TemperatureSensitive, Oversized, HighValue). */
export function sortTags(tags: readonly HandlingTag[]): HandlingTag[] {
  return [...tags].sort((a, b) => HANDLING_TAGS.indexOf(a) - HANDLING_TAGS.indexOf(b));
}

/** Tone of a handling tag's pill: the tags are not lifecycle statuses, so
 *  StatusPill's `tone` override (not a hand-rolled colour) carries them. */
export const TAG_TONE: Record<HandlingTag, "danger" | "warning" | "progress" | "neutral"> = {
  Hazmat: "danger",
  Fragile: "warning",
  TemperatureSensitive: "progress",
  Oversized: "neutral",
  HighValue: "warning",
};

/**
 * A strictly whole, non-negative number typed into a field, or null when
 * the text is empty or not a whole number ("12.5", "1e3", "abc", "-1").
 */
export function wholeNumber(raw: string): number | null {
  const s = raw.trim();
  if (!/^\d+$/.test(s)) return null;
  const n = Number(s);
  return Number.isSafeInteger(n) ? n : null;
}

/**
 * A `datetime-local` value ("2026-10-06T14:05" or with seconds) read as UTC
 * and written as RFC 3339 ("2026-10-06T14:05:00Z"); null when not a valid
 * instant. The picker is labelled "(UTC)", so the operator's browser time
 * zone never shifts the instant that is sent.
 */
export function utcInputToIso(value: string): string | null {
  const m = /^(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(value.trim());
  if (!m) return null;
  const iso = `${m[1]}T${m[2]}:${m[3]}:${m[4] ?? "00"}Z`;
  return Number.isNaN(Date.parse(iso)) ? null : iso;
}

/** "2026-10-06 14:05Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 16)}Z`;
}

/** SKU rule of the API: 1..64 characters, no whitespace, control characters or "/". */
export function skuProblem(sku: string): string | null {
  if (sku.length === 0) return "Enter a SKU.";
  if (sku.length > 64) return "A SKU is at most 64 characters.";
  // eslint-disable-next-line no-control-regex
  if (/[\s/\u0000-\u001f\u007f]/.test(sku)) return "A SKU cannot contain spaces, control characters or '/'.";
  return null;
}

const LEVEL_NOUN: Record<PackLevel, string> = { EACH: "Each", INNER: "Inner", CASE: "Case", PALLET: "Pallet" };

/** "Case of 12 eaches". Quantities are ALWAYS eaches, so a pack is named by
 *  how many eaches it holds ("Each" for the single unit, "Case of 1 each" never occurs). */
export function packLabel(level: PackLevel, unitsPerPack: number): string {
  if (level === "EACH") return LEVEL_NOUN.EACH;
  return `${LEVEL_NOUN[level]} of ${formatNumber(unitsPerPack)} ${unitsPerPack === 1 ? "each" : "eaches"}`;
}

/** A GTIN as typed: only digits, 8, 12, 13 or 14 of them (the service checks the mod-10 digit). */
export function gtinProblem(raw: string, what = "GTIN"): string | null {
  const s = raw.trim();
  if (s.length === 0) return `Enter the ${what}.`;
  if (!/^\d+$/.test(s)) return `The ${what} is digits only.`;
  if (![8, 12, 13, 14].includes(s.length)) return `The ${what} must have 8, 12, 13 or 14 digits.`;
  return null;
}

/** Levels in hierarchy order (EACH, INNER, CASE, PALLET); within a level the
 *  active definition first, then the retired ones, newest first. */
export function orderLevels(items: readonly PackLevelDefinition[]): PackLevelDefinition[] {
  const rank = (l: PackLevel) => PACK_LEVELS.indexOf(l);
  return [...items].sort(
    (a, b) =>
      rank(a.level) - rank(b.level) ||
      Number(b.active) - Number(a.active) ||
      b.effectiveFrom.localeCompare(a.effectiveFrom),
  );
}
