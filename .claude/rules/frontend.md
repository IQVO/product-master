---
paths:
  - "web/**"
  - "charts/product-master/templates/frontend-*.yaml"
  - "charts/product-master/templates/_frontend.tpl"
---

# Frontend micro-frontend remote (`web/`)

This repo also owns `web/`: `productmaster-mfe`, a Vite + React + TypeScript
**Module Federation remote** consumed by the separate `warehouse-console` shell
repo. It is a plain browser client of this service's own REST API
(`apis/openapi.yaml`): nothing in `web/` talks to any other bounded context, and
nothing in `internal/` knows `web/` exists.

## Pinned host/remote contract (change both repos together or not at all)

| Fact | Value |
|---|---|
| Federation container | `productmaster_mfe` (unique among the console's remotes) |
| Exposed module | `./App` (`src/App.tsx`, default export, **no props**) |
| Console route | `/product-master/*` (splat; the shell provides `BrowserRouter`, tokens, `window.__WAREHOUSE_CONFIG__`) |
| Production base / gateway path | `/mfes/product-master/` |
| Dev/preview port | **5191** (`vite --port 5191`, strictPort) |
| API base | `${apiOrigin}/api/product-master` (`src/config.ts`); dev fallback `http://localhost:8080` |

Routes are relative: `/` (product list), `register`, `products/:sku` (detail +
classify / declare dimensions / record measurement forms).

- Own `package.json`, build and tests. Not part of the Go module: `make check`
  never touches it. CI's `web` job runs `npm run lint`, `npx tsc -b`, `npm test`,
  `npm run build` after building the sibling `warehouse-ui-kit`.
- Standalone dev against a local `cmd/api` needs
  `CORS_ALLOWED_ORIGINS=http://localhost:5173,http://localhost:5191` (the
  binary's default allows only the console's :5173). In the cluster Kong's
  global CORS plugin answers browsers; every write here is a `PUT` with a JSON
  `Content-Type`, so Kong must allow both for the console origin.

## Rules that each cost real time elsewhere in the fleet

- **`web/vite.config.ts` stays in OBJECT form**; `base` comes from
  `process.argv.includes("build")`. `vitest.config.ts` is standalone (it does not
  import or `mergeConfig` the Vite config).
- **A relative `dist/remoteEntry.js` is correct** (chunks resolve relative to
  where it was fetched; `dist/index.html` carries the absolute prefixed URLs).
  Do not force an absolute public path; verify through a prefix-stripping
  gateway instead.
- **The Docker build cannot use the committed lockfile or a host
  `node_modules`** (missing linux native bindings, npm/cli#4828): see the
  comments in `web/Dockerfile`. Build with
  `docker build --build-context uikit=../../warehouse-ui-kit -t warehouse/product-master-frontend:local web`.
- **nginx cache headers**: one `Cache-Control` per response; never combine
  `expires` with `add_header Cache-Control`. `remoteEntry.js` and `index.html`
  are `no-cache`.
- **Use only `@warehouse/ui-kit`** components and tokens (Card, DataTable,
  StatusPill). Handling tags and the discrepancy flag are not lifecycle
  statuses, so they use StatusPill's `tone` override (`src/lib.ts` `TAG_TONE`).
- **No Dependabot `npm` entry for `/web`**: it depends on
  `file:../../warehouse-ui-kit`, which Dependabot cannot fetch (repo_lint R3).
- **Relative links come from a layout route WITH a path** (`<Route path="/">`),
  and `App.test.tsx` mounts the remote under the host's
  `<Route path="/product-master/*">` to prove every href; a root-mounted test
  cannot see the compounded-URL bug.
- Tests mock `fetch` (`src/test/fetchMock.ts`: an unmocked request REJECTS).
  Every screen has loading, success, empty and error tests; an RFC 7807 failure
  must surface BOTH `title` and `detail`.
- Chart: `frontend.enabled` (default `false`) renders
  `templates/frontend-{deployment,service}.yaml` (component `frontend`,
  ClusterIP, no HPA, no Ingress/HTTPRoute). `tests/test_service_selectors.py`
  asserts each Service selects exactly one Deployment with it enabled.
