import { useState } from "react";
import { declarePackLevel, toApiError } from "../api";
import type { ApiError } from "../api";
import {
  CheckboxField,
  Form,
  FormRow,
  InlineError,
  InlineSuccess,
  SelectField,
  SubmitButton,
  TextField,
} from "../components/formkit";
import { ProblemAlert } from "../components/ProblemAlert";
import { gtinProblem, packLabel, wholeNumber } from "../lib";
import { PACK_LEVELS } from "../types";
import type { PackLevel, PackLevelDefinition, PackLevelInput } from "../types";
import { PACK_PROBLEM_LEADS } from "./packagingCopy";

const MAX_MM = 20000;
const MAX_G = 2000000;
const MAX_QTY = 1000000;

/** Parses the optional declared dimensions: all four empty (none) or all four valid. */
function parseDims(length: string, width: string, height: string, weight: string): PackLevelInput["dims"] | null | string {
  const raws = [length, width, height, weight];
  if (raws.every((r) => r.trim() === "")) return null;
  const named: [string, string, number][] = [
    ["Length", length, MAX_MM],
    ["Width", width, MAX_MM],
    ["Height", height, MAX_MM],
    ["Weight", weight, MAX_G],
  ];
  const out: number[] = [];
  for (const [name, raw, max] of named) {
    const n = wholeNumber(raw);
    if (n === null || n < 1 || n > max)
      return `${name} must be a whole number from 1 to ${max.toLocaleString("en-US")}${name === "Weight" ? " g" : " mm"} (or leave all four dimension fields empty).`;
    out.push(n);
  }
  return { lengthMm: out[0], widthMm: out[1], heightMm: out[2], weightG: out[3] };
}

/**
 * PUT /products/{sku}/pack-levels/{level}: declares a level with its GTIN. EACH
 * has no child; every other level names the child GTIN (an ACTIVE lower level)
 * and how many of it make one pack. The eaches per pack are derived by the
 * service from the chain, so the form never asks for them. Declaring the same
 * GTIN again changes only orderable / despatch / dimensions; a NEW GTIN at a
 * level that has an active one supersedes it.
 */
export function DeclarePackLevelForm({
  sku,
  items,
  onSaved,
}: {
  sku: string;
  items: readonly PackLevelDefinition[];
  onSaved: () => void;
}) {
  const [level, setLevel] = useState<PackLevel | "">("");
  const [gtin, setGtin] = useState("");
  const [childGtin, setChildGtin] = useState("");
  const [qty, setQty] = useState("");
  const [dim, setDim] = useState({ length: "", width: "", height: "", weight: "" });
  const [orderable, setOrderable] = useState(false);
  const [despatch, setDespatch] = useState(false);
  const [invalid, setInvalid] = useState<string | null>(null);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const hasChild = level !== "" && level !== "EACH";
  const activeChildren = items.filter((i) => i.active && level !== "" && PACK_LEVELS.indexOf(i.level) < PACK_LEVELS.indexOf(level));

  const submit = async () => {
    setProblem(null);
    setSaved(null);
    if (level === "") return setInvalid("Choose the pack level.");
    const g = gtinProblem(gtin);
    if (g) return setInvalid(g);
    const input: PackLevelInput = { gtin: gtin.trim() };
    if (hasChild) {
      const c = gtinProblem(childGtin, "child GTIN");
      if (c) return setInvalid(c);
      const q = wholeNumber(qty);
      if (q === null || q < 2 || q > MAX_QTY)
        return setInvalid(`Quantity of the child level must be a whole number from 2 to ${MAX_QTY.toLocaleString("en-US")}.`);
      input.childGtin = childGtin.trim();
      input.qtyOfChild = q;
    }
    const dims = parseDims(dim.length, dim.width, dim.height, dim.weight);
    if (typeof dims === "string") return setInvalid(dims);
    if (dims) input.dims = dims;
    input.isOrderable = orderable;
    input.isDespatchUnit = despatch;
    setInvalid(null);
    setSaving(true);
    try {
      const res = await declarePackLevel(sku, level, input);
      setSaved(
        `${packLabel(res.data.level, res.data.unitsPerPack)} (${res.data.gtin}) ${res.status === 201 ? "declared" : "saved"} (version ${res.data.version}).`,
      );
      onSaved();
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setSaving(false);
    }
  };

  const setD = (k: keyof typeof dim) => (v: string) => setDim({ ...dim, [k]: v });
  return (
    <Form label="Declare pack level" onSubmit={submit}>
      <FormRow>
        <SelectField
          label="Pack level"
          value={level}
          onChange={(v) => setLevel(v as PackLevel)}
          options={PACK_LEVELS.map((l) => ({ value: l, label: l }))}
          required
        />
        <TextField label="GTIN" value={gtin} onChange={setGtin} inputMode="numeric" required hint="8, 12, 13 or 14 digits" />
        {hasChild && (
          <>
            <TextField
              label="Child GTIN"
              value={childGtin}
              onChange={setChildGtin}
              inputMode="numeric"
              required
              hint={
                activeChildren.length
                  ? `Active lower levels: ${activeChildren.map((c) => `${c.level} ${c.gtin}`).join(", ")}`
                  : "An ACTIVE lower level of this product; declare it first"
              }
            />
            <TextField
              label="Quantity of child level"
              value={qty}
              onChange={setQty}
              inputMode="numeric"
              required
              hint="How many child packs make one of this level (2 or more)"
            />
          </>
        )}
      </FormRow>
      <FormRow>
        <TextField label="Length (mm)" value={dim.length} onChange={setD("length")} inputMode="numeric" />
        <TextField label="Width (mm)" value={dim.width} onChange={setD("width")} inputMode="numeric" />
        <TextField label="Height (mm)" value={dim.height} onChange={setD("height")} inputMode="numeric" />
        <TextField label="Weight (g)" value={dim.weight} onChange={setD("weight")} inputMode="numeric" hint="Dimensions are optional, of ONE pack" />
      </FormRow>
      <FormRow>
        <CheckboxField label="Orderable from the supplier" checked={orderable} onChange={setOrderable} />
        <CheckboxField label="Ships as a unit" checked={despatch} onChange={setDespatch} />
        <SubmitButton disabled={saving}>{saving ? "Saving…" : "Declare pack level"}</SubmitButton>
      </FormRow>
      <InlineError message={invalid} />
      {problem && <ProblemAlert error={problem} lead={PACK_PROBLEM_LEADS[problem.slug]} />}
      <InlineSuccess message={saved} />
    </Form>
  );
}
