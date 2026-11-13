import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  root: fileURLToPath(new URL(".", import.meta.url)),
  base: "/build/",
  publicDir: false,
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": fileURLToPath(new URL(".", import.meta.url)), "/styles": fileURLToPath(new URL("../assets/styles", import.meta.url)) } },
  build: {
    // 先写临时目录;package-ui.mjs 校验指纹、生成 MANIFEST 后,把 build.tmp 原子替换成
    // 正式的 assets/build。直接写正式目录会让并行的 ui:build 把新旧 chunk 混在一起。
    outDir: "../assets/build.tmp",
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: { input: {
      index: fileURLToPath(new URL("index.html", import.meta.url)),
      components: fileURLToPath(new URL("components.html", import.meta.url)),
    } },
  },
});
