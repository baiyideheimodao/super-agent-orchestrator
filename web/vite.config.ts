import { defineConfig } from "vite";

const backend = process.env.BACKEND_URL ?? "http://127.0.0.1:8080";

export default defineConfig({
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": { target: backend, changeOrigin: true },
      "/health": { target: backend, changeOrigin: true }
    }
  },
  build: {
    outDir: "dist",
    sourcemap: true
  }
});
