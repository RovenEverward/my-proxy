import { defineConfig } from "cf/config";

export default defineConfig({
  worker: {
    name: "mpx-cdn-speedtest",
    entrypoint: "./index.js",
    compatibilityDate: "2025-01-01",
    domains: ["cdn-speedtest.shiziyi.top"],
  },
});
