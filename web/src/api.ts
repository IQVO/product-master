import { PRODUCT_MASTER_API_BASE } from "./config";
import type {
  ClassifyProductInput,
  GtinResolution,
  ListProductsQuery,
  MeasurementInput,
  PackLevel,
  PackLevelInput,
  PackLevelList,
  PackLevelResource,
  PhysicalProfile,
  Product,
  ProductClassification,
  ProductPage,
  RegisterProductInput,
  UnitDimensionsInput,
} from "./types";

/** RFC 7807 problem+json body every error response from product-master
 *  returns (`type` = https://errors.product-master.warehouse-systems.dev/<slug>). */
export interface ProblemDetails {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
}

/**
 * Every failed call -- an HTTP error with a problem+json body, an HTTP error
 * without one, or a network failure -- surfaces as one ApiError, so a screen
 * has a single shape to render: `title` (the problem's category) and `detail`
 * (what exactly was wrong), plus `slug` to branch on a specific problem.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails | null;
  readonly title: string;
  readonly detail: string;
  /** Last path segment of problem.type (e.g. "stale-measurement"); "" when unknown. */
  readonly slug: string;

  constructor(status: number, problem: ProblemDetails | null, fallbackTitle: string) {
    const title = problem?.title || fallbackTitle;
    const detail = problem?.detail ?? "";
    super(detail || title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.title = title;
    this.detail = detail;
    this.slug = problem?.type ? (problem.type.split("/").pop() ?? "") : "";
  }
}

/** Normalizes anything thrown by a request into an ApiError. */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  const message = err instanceof Error ? err.message : String(err);
  return new ApiError(0, { detail: message }, "Network error");
}

type Query = Record<string, string | number | boolean | undefined>;

function withQuery(path: string, query?: Query): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "") params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

export interface ApiResult<T> {
  status: number;
  data: T;
}

async function request<T>(
  method: "GET" | "PUT" | "POST",
  path: string,
  body?: unknown,
  idempotencyKey?: string,
): Promise<ApiResult<T>> {
  let res: Response;
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (idempotencyKey !== undefined) headers["Idempotency-Key"] = idempotencyKey;
  try {
    res = await fetch(`${PRODUCT_MASTER_API_BASE}${path}`, {
      method,
      ...(Object.keys(headers).length === 0 ? {} : { headers }),
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
  } catch (err) {
    throw toApiError(err);
  }
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`.trim());
  }
  return { status: res.status, data: (await res.json()) as T };
}

const seg = encodeURIComponent;

/** GET /products?limit=&cursor=&handlingTag=&classified= */
export async function listProducts(q: ListProductsQuery): Promise<ProductPage> {
  const res = await request<ProductPage>(
    "GET",
    withQuery("/products", {
      limit: q.limit,
      cursor: q.cursor,
      handlingTag: q.handlingTag,
      classified: q.classified,
    }),
  );
  return { items: res.data.items ?? [], nextCursor: res.data.nextCursor || undefined };
}

/** GET /products/{sku} */
export async function getProduct(sku: string): Promise<Product> {
  return (await request<Product>("GET", `/products/${seg(sku)}`)).data;
}

/** PUT /products/{sku}: 201 registered, 200 existed (description now the requested one). */
export function registerProduct(sku: string, input: RegisterProductInput): Promise<ApiResult<Product>> {
  return request<Product>("PUT", `/products/${seg(sku)}`, input);
}

/** PUT /products/{sku}/classification: 201 first classification, 200 replaced/identical. */
export function classifyProduct(
  sku: string,
  input: ClassifyProductInput,
): Promise<ApiResult<ProductClassification>> {
  return request<ProductClassification>("PUT", `/products/${seg(sku)}/classification`, input);
}

/** PUT /products/{sku}/dimensions/declared -> the whole physical profile. */
export async function declareDimensions(sku: string, input: UnitDimensionsInput): Promise<PhysicalProfile> {
  return (await request<PhysicalProfile>("PUT", `/products/${seg(sku)}/dimensions/declared`, input)).data;
}

/** PUT /products/{sku}/dimensions/measured -> the whole physical profile (409 stale-measurement). */
export async function recordMeasurement(sku: string, input: MeasurementInput): Promise<PhysicalProfile> {
  return (await request<PhysicalProfile>("PUT", `/products/${seg(sku)}/dimensions/measured`, input)).data;
}

/** GET /products/{sku}/pack-levels -> every level ever declared, retired ones included (follows nextCursor). */
export async function listPackLevels(sku: string): Promise<PackLevelList> {
  let cursor: string | undefined;
  let out: PackLevelList | undefined;
  do {
    const data = (await request<PackLevelList>("GET", withQuery(`/products/${seg(sku)}/pack-levels`, { cursor }))).data;
    out = out ? { ...data, items: [...out.items, ...(data.items ?? [])] } : { ...data, items: data.items ?? [] };
    cursor = data.nextCursor || undefined;
  } while (cursor);
  return out;
}

/** PUT /products/{sku}/pack-levels/{level}: 201 declared, 200 identical / changed in place / superseded. */
export function declarePackLevel(
  sku: string,
  level: PackLevel,
  input: PackLevelInput,
): Promise<ApiResult<PackLevelResource>> {
  return request<PackLevelResource>("PUT", `/products/${seg(sku)}/pack-levels/${seg(level)}`, input);
}

/**
 * POST /products/{sku}/pack-levels/{level}/retirements -> the retired level (200, also when already
 * retired). `idempotencyKey` is minted per user submit; a retry of the same submit reuses it.
 */
export async function retirePackLevel(
  sku: string,
  level: PackLevel,
  reason?: string,
  idempotencyKey: string = crypto.randomUUID(),
): Promise<PackLevelResource> {
  return (
    await request<PackLevelResource>(
      "POST",
      `/products/${seg(sku)}/pack-levels/${seg(level)}/retirements`,
      reason ? { reason } : undefined,
      idempotencyKey,
    )
  ).data;
}

/** GET /gtins/{gtin}: which SKU and pack level a GTIN identifies (a retired GTIN still resolves). */
export async function resolveGtin(gtin: string): Promise<GtinResolution> {
  return (await request<GtinResolution>("GET", `/gtins/${seg(gtin)}`)).data;
}
