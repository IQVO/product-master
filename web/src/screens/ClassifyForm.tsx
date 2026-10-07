import { useState } from "react";
import { classifyProduct, toApiError } from "../api";
import type { ApiError } from "../api";
import { CheckboxField, Form, FormRow, InlineError, InlineSuccess, SelectField, SubmitButton, TextField } from "../components/formkit";
import { ProblemAlert } from "../components/ProblemAlert";
import { sortTags, wholeNumber } from "../lib";
import { HANDLING_TAGS, TEMPERATURE_CLASSES } from "../types";
import type { Classification, ClassifyProductInput, HandlingTag, TemperatureClass } from "../types";

const TEMPERATURE_OPTIONS = TEMPERATURE_CLASSES.map((t) => ({ value: t, label: t }));

/**
 * PUT /products/{sku}/classification. The rules are the API's (closed tag
 * set, non-empty, temperature class iff TemperatureSensitive, DOT hazard class
 * 1..9 only with Hazmat); the form checks them before sending so the operator
 * gets a sentence instead of a round trip, and still shows the service's RFC
 * 7807 answer (title and detail) for anything it rejects.
 */
export function ClassifyForm({
  sku,
  current,
  onSaved,
}: {
  sku: string;
  current?: Classification;
  onSaved: () => void;
}) {
  const [tags, setTags] = useState<HandlingTag[]>(current?.handlingTags ?? []);
  const [temperature, setTemperature] = useState<"" | TemperatureClass>(current?.temperatureClass ?? "");
  const [dot, setDot] = useState(current?.dotHazardClass ? String(current.dotHazardClass) : "");
  const [invalid, setInvalid] = useState<string | null>(null);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const hazmat = tags.includes("Hazmat");
  const tempSensitive = tags.includes("TemperatureSensitive");

  const toggle = (tag: HandlingTag, on: boolean) =>
    setTags(sortTags(on ? [...tags, tag] : tags.filter((t) => t !== tag)));

  const submit = async () => {
    setProblem(null);
    setSaved(null);
    if (tags.length === 0) return setInvalid("Choose at least one handling tag.");
    if (tempSensitive && !temperature) return setInvalid("A TemperatureSensitive product needs a temperature class.");
    const dotClass = dot.trim() === "" ? undefined : wholeNumber(dot);
    if (dotClass === null || (dotClass !== undefined && (dotClass < 1 || dotClass > 9)))
      return setInvalid("The DOT hazard class is a whole number from 1 to 9, or left empty.");
    setInvalid(null);

    const input: ClassifyProductInput = {
      handlingTags: tags,
      // Sent only where the API allows them; a stale value of a hidden field is dropped.
      ...(tempSensitive && temperature && { temperatureClass: temperature }),
      ...(hazmat && dotClass !== undefined && { dotHazardClass: dotClass }),
    };
    setSaving(true);
    try {
      const res = await classifyProduct(sku, input);
      setSaved(
        res.status === 201
          ? `Classification set (version ${res.data.version}).`
          : `Classification saved (version ${res.data.version}).`,
      );
      onSaved();
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Form label="Classify product" onSubmit={submit}>
      <fieldset style={{ border: "none", padding: 0, margin: 0 }}>
        <legend style={{ fontSize: "var(--wh-font-size-xs)", color: "var(--wh-color-text-muted)", fontWeight: 600 }}>
          Handling tags *
        </legend>
        <FormRow>
          {HANDLING_TAGS.map((tag) => (
            <CheckboxField key={tag} label={tag} checked={tags.includes(tag)} onChange={(on) => toggle(tag, on)} />
          ))}
        </FormRow>
      </fieldset>
      <FormRow>
        {tempSensitive && (
          <SelectField
            label="Temperature class"
            value={temperature}
            onChange={(v) => setTemperature(v as TemperatureClass)}
            options={TEMPERATURE_OPTIONS}
            required
          />
        )}
        {hazmat && (
          <TextField
            label="DOT hazard class"
            value={dot}
            onChange={setDot}
            inputMode="numeric"
            hint="Optional, 1 to 9"
          />
        )}
        <SubmitButton disabled={saving}>{saving ? "Saving…" : "Save classification"}</SubmitButton>
      </FormRow>
      <InlineError message={invalid} />
      {problem && <ProblemAlert error={problem} />}
      <InlineSuccess message={saved} />
    </Form>
  );
}
