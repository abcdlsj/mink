import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/sumi.": {
        target: "http://127.0.0.1:8080",
        changeOrigin: false,
      },
      "/auth": {
        target: "http://127.0.0.1:8080",
        changeOrigin: false,
      },
    },
  },
});
