import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import viteTsconfigPaths from "vite-tsconfig-paths";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    port: 5173,
    // Default to loopback-only to match the Go server's posture
    // (internal/cli/serve.go:50-51 binds 127.0.0.1 by default). LAN
    // exposure was the previous default and is a security-posture
    // regression for users running `npm run dev` on shared networks.
    // Set VITE_HOST=0.0.0.0 (or a specific interface) to opt-in to LAN
    // access — e.g. for testing the UI from a mobile device on the
    // same network.
    host: process.env.VITE_HOST || "127.0.0.1",
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
});
