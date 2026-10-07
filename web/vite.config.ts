import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { federation } from "@module-federation/vite";

// productmaster-mfe: the product-master remote. Exposes ./App -- the
// warehouse-console shell lazy-loads it as `productmaster_mfe/App` and mounts
// it under its own `/product-master/*` splat route. Also runnable standalone
// on :5191 for local development without the shell (see main.tsx).
//
// PINNED host/remote contract (warehouse-console vite.config.ts carries the
// matching entry; change both together or not at all):
//   federation container  productmaster_mfe
//   exposed module        ./App (default export, no props)
//   production base       /mfes/product-master/   (Nginx web gateway path)
//   dev/preview port      5191
//
// `base` is the deployment-time asset namespace. In the kind cluster this
// remote is served by its own nginx pod behind the Nginx web gateway at
// http://localhost/mfes/product-master/, so every hashed chunk and the
// federation remoteEntry.js must resolve under that prefix -- otherwise a
// dynamically imported chunk would request /assets/... at the shell's origin
// root and collide with every other remote's assets.
//
// Kept as a plain object rather than the ({ command }) => ({...}) callback
// form on purpose: a callback export cannot be merged by Vite's mergeConfig
// ("Cannot merge config in form of callback"), which has killed whole test
// suites elsewhere in the fleet. Reading the command off process.argv keeps
// this a static object. (vitest.config.ts here is standalone and does not
// import this file at all.)
const IS_BUILD = process.argv.includes("build");
const PUBLIC_BASE = IS_BUILD ? "/mfes/product-master/" : "/";

export default defineConfig({
  base: PUBLIC_BASE,
  plugins: [
    react(),
    federation({
      name: "productmaster_mfe",
      filename: "remoteEntry.js",
      exposes: {
        "./App": "./src/App.tsx",
      },
      shared: {
        react: { singleton: true, requiredVersion: "^19.2.8" },
        "react-dom": { singleton: true, requiredVersion: "^19.2.8" },
        "react-router-dom": { singleton: true, requiredVersion: "^7.18.3" },
        "@warehouse/ui-kit": { singleton: true },
      },
    }),
  ],
  server: {
    port: 5191,
    strictPort: true,
    cors: true,
    origin: "http://localhost:5191",
  },
  preview: {
    port: 5191,
    strictPort: true,
    cors: true,
  },
  build: {
    target: "esnext",
    modulePreload: false,
  },
});
