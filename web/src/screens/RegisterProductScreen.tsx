import { useState } from "react";
import { Link } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { registerProduct, toApiError } from "../api";
import type { ApiError } from "../api";
import { Form, FormRow, InlineError, InlineSuccess, SubmitButton, TextField } from "../components/formkit";
import { PageHeader, Stack } from "../components/layout";
import { ProblemAlert } from "../components/ProblemAlert";
import { skuProblem } from "../lib";
import type { Product } from "../types";

const MAX_DESCRIPTION = 200;

/**
 * PUT /products/{sku}: registers a SKU (201) or, when it is already
 * registered, replaces its description (200). A product must exist before it
 * can be classified or dimensioned, so success links straight to its page.
 */
export function RegisterProductScreen() {
  const [sku, setSku] = useState("");
  const [description, setDescription] = useState("");
  const [invalid, setInvalid] = useState<string | null>(null);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [result, setResult] = useState<{ created: boolean; product: Product } | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    setProblem(null);
    setResult(null);
    const s = sku.trim();
    const why = skuProblem(s);
    if (why) return setInvalid(why);
    if (description.length > MAX_DESCRIPTION)
      return setInvalid(`A description is at most ${MAX_DESCRIPTION} characters.`);
    setInvalid(null);
    setSaving(true);
    try {
      const res = await registerProduct(s, { description });
      setResult({ created: res.status === 201, product: res.data });
    } catch (err) {
      setProblem(toApiError(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Stack gap={5}>
      <PageHeader
        title="Register product"
        subtitle="product-master · register a SKU, or change the description of one that exists"
      />
      <Card title="Product">
        <Form label="Register product" onSubmit={submit}>
          <FormRow>
            <TextField label="SKU" value={sku} onChange={setSku} required hint="1 to 64 characters, no spaces or '/'" />
            <TextField
              label="Description"
              value={description}
              onChange={setDescription}
              hint={`Optional, at most ${MAX_DESCRIPTION} characters`}
            />
            <SubmitButton disabled={saving}>{saving ? "Saving…" : "Register"}</SubmitButton>
          </FormRow>
          <InlineError message={invalid} />
          {problem && <ProblemAlert error={problem} />}
          {result && (
            <InlineSuccess
              message={
                <span>
                  {result.created
                    ? `Registered ${result.product.sku} (version ${result.product.version}).`
                    : `${result.product.sku} was already registered; its description is now the one entered (version ${result.product.version}).`}{" "}
                  <Link to={`../products/${encodeURIComponent(result.product.sku)}`}>Open {result.product.sku}</Link>
                </span>
              }
            />
          )}
        </Form>
      </Card>
    </Stack>
  );
}
