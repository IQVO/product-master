import { useState } from "react";
import { Link } from "react-router-dom";
import { StatusPill } from "@warehouse/ui-kit";
import { resolveGtin, toApiError } from "../api";
import type { ApiError } from "../api";
import { Form, FormRow, InlineError, SubmitButton, TextField } from "../components/formkit";
import { Facts } from "../components/layout";
import { ProblemAlert } from "../components/ProblemAlert";
import { gtinProblem, packLabel, utcLabel } from "../lib";
import type { GtinResolution } from "../types";
import { PACK_PROBLEM_LEADS } from "./packagingCopy";

/** GET /gtins/{gtin}: which SKU and pack level a GTIN identifies. The service
 *  normalises 8/12/13/14 digit spellings to 14 digits; a retired GTIN still
 *  resolves (inactive), so an in-flight ASN naming it can be read. */
export function GtinLookupCard() {
  const [gtin, setGtin] = useState("");
  const [invalid, setInvalid] = useState<string | null>(null);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [result, setResult] = useState<GtinResolution | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    setProblem(null);
    setResult(null);
    const g = gtinProblem(gtin);
    if (g) return setInvalid(g);
    setInvalid(null);
    setBusy(true);
    try {
      setResult(await resolveGtin(gtin.trim()));
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Form label="Look up GTIN" onSubmit={submit}>
      <FormRow>
        <TextField label="GTIN to look up" value={gtin} onChange={setGtin} inputMode="numeric" hint="Scan or type 8, 12, 13 or 14 digits" />
        <SubmitButton disabled={busy}>{busy ? "Looking up…" : "Look up"}</SubmitButton>
      </FormRow>
      <InlineError message={invalid} />
      {problem && <ProblemAlert error={problem} lead={PACK_PROBLEM_LEADS[problem.slug]} />}
      {result && (
        <Facts
          items={[
            { label: "GTIN", value: <code>{result.gtin}</code> },
            {
              label: "SKU",
              value: <Link to={`../products/${encodeURIComponent(result.sku)}/packaging`}>{result.sku}</Link>,
            },
            { label: "Level", value: result.level },
            { label: "Pack", value: packLabel(result.level, result.unitsPerPack) },
            { label: "Child GTIN", value: result.childGtin ? <code>{result.childGtin}</code> : "—" },
            {
              label: "Status",
              value: result.active ? (
                <StatusPill status="Active" tone="success" size="sm" />
              ) : (
                <StatusPill status="Retired" tone="neutral" size="sm" />
              ),
            },
            { label: "Effective from", value: utcLabel(result.effectiveFrom) },
            ...(result.effectiveTo ? [{ label: "Effective to", value: utcLabel(result.effectiveTo) }] : []),
          ]}
        />
      )}
    </Form>
  );
}
