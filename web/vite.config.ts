import { defineConfig, loadEnv } from "vite";
import vue from "@vitejs/plugin-vue";
import { fileURLToPath, URL } from "node:url";

export default defineConfig(({ mode }) => {
  // loadEnv reads .env / .env.local from this directory into a plain
  // object. process.env is NOT populated from .env files at config-eval
  // time, so reading process.env.VITE_HOST directly silently ignores
  // .env.local — loadEnv is the supported way to surface it here.
  const env = loadEnv(mode, fileURLToPath(new URL(".", import.meta.url)), "VITE_");

  return {
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
      // Set VITE_HOST=0.0.0.0 (or a specific interface) in web/.env.local
      // to opt-in to LAN access — e.g. for testing the UI from a mobile
      // device on the same network.
      host: env.VITE_HOST || process.env.VITE_HOST || "127.0.0.1",
      proxy: {
        "/api": {
          target: "http://localhost:8080",
          changeOrigin: true,
        },
      },
    },
  };
});
