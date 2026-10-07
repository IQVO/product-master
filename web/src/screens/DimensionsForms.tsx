import { useState } from "react";
import { declareDimensions, recordMeasurement, toApiError } from "../api";
import type { ApiError } from "../api";
import { Form, FormRow, InlineError, InlineSuccess, SubmitButton, TextField } from "../components/formkit";
import { ProblemAlert } from "../components/ProblemAlert";
import { utcInputToIso, wholeNumber } from "../lib";
import type { MeasurementInput, UnitDimensions, UnitDimensionsInput } from "../types";

const MAX_MM = 20000;
const MAX_G = 2000000;

interface DimFields {
  length: string;
  width: string;
  height: string;
  weight: string;
}

function fieldsFrom(d?: UnitDimensions): DimFields {
  return d
    ? { length: String(d.lengthMm), width: String(d.widthMm), height: String(d.heightMm), weight: String(d.weightG) }
    : { length: "", width: "", height: "", weight: "" };
}

/** The API's bounds (ADR 0002): each dimension 1..20000 mm, weight 1..2000000 g, whole numbers. */
function parseDims(f: DimFields): UnitDimensionsInput | string {
  const named: [string, string, number][] = [
    ["Length", f.length, MAX_MM],
    ["Width", f.width, MAX_MM],
    ["Height", f.height, MAX_MM],
  ];
  const out: number[] = [];
  for (const [name, raw, max] of named) {
    const n = wholeNumber(raw);
    if (n === null || n < 1 || n > max) return `${name} must be a whole number of millimetres from 1 to ${max.toLocaleString("en-US")}.`;
    out.push(n);
  }
  const weight = wholeNumber(f.weight);
  if (weight === null || weight < 1 || weight > MAX_G)
    return `Weight must be a whole number of grams from 1 to ${MAX_G.toLocaleString("en-US")}.`;
  return { lengthMm: out[0], widthMm: out[1], heightMm: out[2], weightG: weight };
}

function DimInputs({ f, set, prefix }: { f: DimFields; set: (f: DimFields) => void; prefix: string }) {
  return (
    <FormRow>
      <TextField label={`${prefix} length (mm)`} value={f.length} onChange={(v) => set({ ...f, length: v })} inputMode="numeric" required />
      <TextField label={`${prefix} width (mm)`} value={f.width} onChange={(v) => set({ ...f, width: v })} inputMode="numeric" required />
      <TextField label={`${prefix} height (mm)`} value={f.height} onChange={(v) => set({ ...f, height: v })} inputMode="numeric" required />
      <TextField label={`${prefix} weight (g)`} value={f.weight} onChange={(v) => set({ ...f, weight: v })} inputMode="numeric" required />
    </FormRow>
  );
}

/** PUT /products/{sku}/dimensions/declared: the vendor/steward supplied unit dimensions and weight. */
export function DeclareDimensionsForm({
  sku,
  current,
  onSaved,
}: {
  sku: string;
  current?: UnitDimensions;
  onSaved: () => void;
}) {
  const [f, setF] = useState<DimFields>(fieldsFrom(current));
  const [invalid, setInvalid] = useState<string | null>(null);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    setProblem(null);
    setSaved(null);
    const dims = parseDims(f);
    if (typeof dims === "string") return setInvalid(dims);
    setInvalid(null);
    setSaving(true);
    try {
      const profile = await declareDimensions(sku, dims);
      setSaved(`Declared dimensions saved${profile.version ? ` (version ${profile.version})` : ""}.`);
      onSaved();
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Form label="Declare dimensions" onSubmit={submit}>
      <DimInputs f={f} set={setF} prefix="Declared" />
      <FormRow>
        <SubmitButton disabled={saving}>{saving ? "Saving…" : "Declare dimensions"}</SubmitButton>
      </FormRow>
      <InlineError message={invalid} />
      {problem && <ProblemAlert error={problem} />}
      <InlineSuccess message={saved} />
    </Form>
  );
}

const MEASUREMENT_LEADS: Record<string, string> = {
  "stale-measurement":
    "A newer measurement is already recorded for this product, so this one was not applied. Check the measured time.",
};

/**
 * PUT /products/{sku}/dimensions/measured: the latest reading of one unit (a
 * cubiscan or a manual measurement). It becomes the effective profile. The
 * measured time is picked and sent as UTC; a reading older than the current
 * one is rejected by the service as 409 stale-measurement.
 */
export function RecordMeasurementForm({ sku, onSaved }: { sku: string; onSaved: () => void }) {
  const [f, setF] = useState<DimFields>(fieldsFrom());
  const [measuredAt, setMeasuredAt] = useState("");
  const [deviceId, setDeviceId] = useState("");
  const [invalid, setInvalid] = useState<string | null>(null);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    setProblem(null);
    setSaved(null);
    const dims = parseDims(f);
    if (typeof dims === "string") return setInvalid(dims);
    const at = utcInputToIso(measuredAt);
    if (!at) return setInvalid("Pick when the unit was measured (UTC).");
    if (deviceId.trim().length > 64) return setInvalid("A device id is at most 64 characters.");
    setInvalid(null);
    const input: MeasurementInput = {
      ...dims,
      measuredAt: at,
      // Omitted (not "") for a manual measurement.
      ...(deviceId.trim() && { deviceId: deviceId.trim() }),
    };
    setSaving(true);
    try {
      const profile = await recordMeasurement(sku, input);
      setSaved(
        `Measurement recorded${profile.version ? ` (version ${profile.version})` : ""}.` +
          (profile.discrepancy ? " It differs from the declared values by more than 10 %." : ""),
      );
      onSaved();
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Form label="Record measurement" onSubmit={submit}>
      <DimInputs f={f} set={setF} prefix="Measured" />
      <FormRow>
        <TextField label="Measured at (UTC)" type="datetime-local" value={measuredAt} onChange={setMeasuredAt} required />
        <TextField label="Device id" value={deviceId} onChange={setDeviceId} hint="Optional; leave empty for a manual measurement" />
        <SubmitButton disabled={saving}>{saving ? "Recording…" : "Record measurement"}</SubmitButton>
      </FormRow>
      <InlineError message={invalid} />
      {problem && <ProblemAlert error={problem} lead={MEASUREMENT_LEADS[problem.slug]} />}
      <InlineSuccess message={saved} />
    </Form>
  );
}
