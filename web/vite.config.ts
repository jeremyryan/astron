import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Build a self-contained SPA that is embedded into and served by the Astron
// operator binary. Relative base so assets resolve under any mount path.
export default defineConfig({
  plugins: [react()],
  base: "./",
  // Pre-bundle the large @tabler/icons-react barrel so the dev server stays
  // fast and doesn't spawn a module per icon on cold start.
  optimizeDeps: {
    include: ["@tabler/icons-react"],
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    // During local development, proxy API calls to the operator's API server.
    proxy: {
      "/api": "http://localhost:8082",
    },
  },
});
