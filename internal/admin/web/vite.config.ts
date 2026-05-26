import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// SaaS SPA mounted at root by the Go server. Use absolute base so asset URLs
// remain stable across deep links like /app/billing or /pricing.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/",
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
  },
  server: {
    port: 5173,
    proxy: {
      "/admin/api": {
        target: "http://localhost:8317",
        changeOrigin: false,
      },
      "/api": {
        target: "http://localhost:8317",
        changeOrigin: false,
      },
    },
  },
});
