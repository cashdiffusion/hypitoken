import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "node:path";

// packageOf extracts the npm package name from a module id, so chunk
// assignment can match on the package itself rather than on a substring of
// the whole path (which matched far too much).
function packageOf(id: string): string {
  const m = id.match(/[\\/]node_modules[\\/]((?:@[^\\/]+[\\/])?[^\\/]+)/);
  return m ? m[1].replace(/\\/g, "/") : "";
}

// react-vendor also absorbs the tiny styling utilities every component uses.
// Left unassigned, Rollup folds a shared module like `clsx` into whichever
// manual chunk it happens to co-occur with — it landed in `charts`, which put
// 115 kB of recharts on the landing page's critical path for one 300-byte
// helper. Pinning them to the chunk that is always loaded prevents that.
const REACT_VENDOR = new Set([
  "react",
  "react-dom",
  "scheduler",
  "react-router",
  "react-router-dom",
  "clsx",
  "tailwind-merge",
  "class-variance-authority",
]);
const THREE_DEPS = new Set(["three-stdlib", "maath", "zustand", "three-mesh-bvh", "meshline"]);
const MOTION_VENDOR = new Set(["motion", "motion-dom", "motion-utils", "framer-motion"]);
// Matched as either the exact name or a `<prefix>-…` package, which covers the
// unified/remark/rehype ecosystem's dozens of tiny packages.
const MARKDOWN_PREFIXES = [
  "react-markdown",
  "highlight.js",
  "lowlight",
  "unified",
  "unist",
  "hast",
  "mdast",
  "micromark",
  "remark",
  "rehype",
  "vfile",
  "bail",
  "trough",
  "zwitch",
  "devlop",
  "ccount",
  "estree",
  "property-information",
  "space-separated-tokens",
  "comma-separated-tokens",
  "html-void-elements",
  "character-entities",
  "decode-named-character-reference",
  "longest-streak",
  "markdown-table",
  "parse-entities",
  "stringify-entities",
];

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
    // Routes are code-split in App.tsx; this splits the *vendors* so a chunk
    // stays cached across deploys instead of being invalidated by any app
    // edit, and so a heavy library only downloads on the route that uses it.
    rollupOptions: {
      output: {
        manualChunks(id: string) {
          const pkg = packageOf(id);
          if (!pkg) return;
          // React and its renderer/router must stay in ONE chunk — splitting
          // them apart reorders module init and breaks at runtime.
          if (REACT_VENDOR.has(pkg)) return "react-vendor";
          if (pkg === "three" || pkg.startsWith("@react-three/") || THREE_DEPS.has(pkg)) {
            return "three";
          }
          if (pkg === "recharts" || pkg === "victory-vendor" || pkg.startsWith("d3-")) {
            return "charts";
          }
          // The markdown pipeline (react-markdown + unified + highlight.js) is
          // docs-only and is by far the biggest non-3D dependency.
          if (MARKDOWN_PREFIXES.some((p) => pkg === p || pkg.startsWith(`${p}-`))) {
            return "markdown";
          }
          if (pkg.startsWith("@stripe/")) return "stripe";
          if (pkg === "hls.js") return "hls";
          if (pkg === "gsap") return "gsap";
          if (MOTION_VENDOR.has(pkg)) return "motion";
          if (pkg.startsWith("i18next") || pkg === "react-i18next") return "i18n";
          // Everything else (clsx, tailwind-merge, lucide icons, Radix, …) is
          // left to Rollup so it lands next to whichever route actually uses
          // it. Grouping by a loose substring match here used to drag `clsx`
          // into the charts chunk and put 115 kB of recharts on the landing
          // page's critical path.
        },
      },
    },
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
