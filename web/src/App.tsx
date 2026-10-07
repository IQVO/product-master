import { NavLink, Outlet, Route, Routes } from "react-router-dom";
import { ProductDetailRoute } from "./screens/ProductDetailScreen";
import { ProductListScreen } from "./screens/ProductListScreen";
import { RegisterProductScreen } from "./screens/RegisterProductScreen";

const SUB_NAV = [
  { to: ".", label: "Products", end: true },
  { to: "register", label: "Register product" },
];

const linkStyle = ({ isActive }: { isActive: boolean }) => ({
  display: "inline-flex",
  padding: "6px 12px",
  borderRadius: "var(--wh-radius-pill)",
  fontSize: "var(--wh-font-size-sm)",
  fontWeight: isActive ? 600 : 500,
  color: isActive ? "var(--wh-color-text)" : "var(--wh-color-text-muted)",
  background: isActive ? "var(--wh-color-accent-muted)" : "transparent",
  textDecoration: "none",
});

/** The sub-nav, rendered by a layout route with the path "/".
 *
 *  The console mounts this component inside its own `<Route
 *  path="/product-master/*">`. A relative `<NavLink>` rendered straight in that
 *  splat route resolves against the FULL current URL (react-router 7 uses the
 *  last path-contributing match's pathname, splat included), so from
 *  /product-master/register the link `register` would point at
 *  /product-master/register/register. A pathless layout route does not help
 *  (react-router drops pathless routes from relative resolution); a layout
 *  route WITH path "/" does: its own match is the mount point, under any
 *  prefix and standalone at `/`. (Same fix as warehouse-planning's capacity
 *  remote; App.test.tsx mounts under the host's splat route to prove it.) */
function ProductMasterLayout() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <nav aria-label="Product master sections" style={{ display: "flex", gap: "var(--wh-space-2)" }}>
        {SUB_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} end={item.end} style={linkStyle}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}

/** Exposed as productmaster_mfe/App via Module Federation. Takes NO props:
 *  the console mounts it under `/product-master/*` and provides the
 *  BrowserRouter, the design tokens and `window.__WAREHOUSE_CONFIG__`. Routes
 *  here are RELATIVE, so the component works identically under that prefix (in
 *  the shell) or at / (standalone dev, see main.tsx).
 *
 *   - Products (index): list with handling-tag / classified filters and paging.
 *   - products/:sku: classification, physical profile, version, and the
 *     classify / declare-dimensions / record-measurement forms.
 *   - register: register a SKU or change its description. */
export default function App() {
  return (
    <Routes>
      <Route path="/" element={<ProductMasterLayout />}>
        <Route index element={<ProductListScreen />} />
        <Route path="register" element={<RegisterProductScreen />} />
        <Route path="products/:sku" element={<ProductDetailRoute />} />
      </Route>
    </Routes>
  );
}
