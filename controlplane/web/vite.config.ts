/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The editor is a single-page app that talks to the control plane's cookie-gated
// API. In dev, /api is proxied to the running control plane so the session
// cookie is same-origin; in production the built assets are served by the
// control plane itself, so the same relative /api paths resolve without CORS.
const CONTROL_PLANE = process.env.FORGE_CONTROL_PLANE ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": { target: CONTROL_PLANE, changeOrigin: true },
    },
  },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: false,
  },
});
