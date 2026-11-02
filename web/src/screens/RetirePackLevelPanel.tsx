import { useRef, useState } from "react";
import { retirePackLevel, toApiError } from "../api";
import type { ApiError } from "../api";
import { Form, FormRow, InlineSuccess, SubmitButton, TextField } from "../components/formkit";
import { ProblemAlert } from "../components/ProblemAlert";
import { packLabel } from "../lib";
import type { PackLevelDefinition } from "../types";
import { PACK_PROBLEM_LEADS } from "./packagingCopy";

/**
 * Confirms and sends POST /products/{sku}/pack-levels/{level}/retirements. Retiring
 * end-dates the level: its GTIN stays resolvable (inactive) and is never
 * assigned again, and the levels ABOVE it are not retired by this call. The
 * panel stays on screen after success so the confirmation is readable.
 */
export function RetirePackLevelPanel({
  sku,
  def,
  onClose,
  onRetired,
}: {
  sku: string;
  def: PackLevelDefinition;
  onClose: () => void;
  onRetired: () => void;
}) {
  const [reason, setReason] = useState("");
  // One Idempotency-Key per logical submit: a retry reuses it, new input or a success mints a new one.
  const keyRef = useRef(crypto.randomUUID());
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    setProblem(null);
    setSaving(true);
    try {
      const res = await retirePackLevel(sku, def.level, reason.trim() || undefined, keyRef.current);
      keyRef.current = crypto.randomUUID();
      setDone(`${packLabel(res.level, res.unitsPerPack)} (${res.gtin}) retired (version ${res.version}).`);
      onRetired();
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Form label={`Retire ${def.level}`} onSubmit={submit}>
      <div style={{ fontSize: "var(--wh-font-size-sm)" }}>
        Retire <strong>{packLabel(def.level, def.unitsPerPack)}</strong> (GTIN <code>{def.gtin}</code>)? The GTIN keeps
        resolving as retired and is never reused. Levels above it are not retired; retire them yourself if they
        should go too.
      </div>
      <FormRow>
        <TextField
          label="Reason"
          value={reason}
          onChange={(v) => {
            keyRef.current = crypto.randomUUID();
            setReason(v);
          }}
          hint="Optional, up to 200 characters; defaults to “retired”"
        />
        {!done && <SubmitButton disabled={saving}>{saving ? "Retiring…" : `Retire ${def.level}`}</SubmitButton>}
        <SubmitButton type="button" secondary onClick={onClose}>
          {done ? "Close" : "Cancel"}
        </SubmitButton>
      </FormRow>
      {problem && <ProblemAlert error={problem} lead={PACK_PROBLEM_LEADS[problem.slug]} />}
      <InlineSuccess message={done} />
    </Form>
  );
}
