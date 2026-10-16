import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The SPA builds into ui/dist, which the Bun server serves. Assets get
// content hashes so the server can cache them for a year.
export default defineConfig({
  root: "web",
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../dist",
    emptyOutDir: true,
    assetsDir: "assets",
    // Never inline an asset as a data: URI: the CSP has no data: source,
    // so an inlined font or icon would be blocked (DESIGN-0036). The
    // check:dist script fails the build if one slips through.
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      "/api": "http://localhost:3000",
      "/auth": "http://localhost:3000",
      "/ui": "http://localhost:3000",
    },
  },
});
