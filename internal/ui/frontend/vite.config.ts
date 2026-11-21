import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  root: fileURLToPath(new URL(".", import.meta.url)),
  // 相对 base:入口 html 由 mpx serve 挂在 /admin/ 下伺服,引用一律相对
  // (./build/assets/x.js、boot.js、favicon.svg)。
  // chunks 由 output.*FileNames 放进 build.tmp/build/assets/ 下,package-ui
  // 把它原子换成 assets/build/(布局不变)。绝对引用只有 /flags、/fonts、
  // /styles,控制面把它们挂在根上。
  base: "./",
  publicDir: false,
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": fileURLToPath(new URL(".", import.meta.url)), "/styles": fileURLToPath(new URL("../assets/styles", import.meta.url)) } },
  build: {
    // 先写临时目录;package-ui.mjs 校验指纹、生成 MANIFEST 后,把
    // build.tmp/build/ 原子替换成正式的 assets/build/。
    outDir: "../assets/build.tmp",
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      input: {
        index: fileURLToPath(new URL("index.html", import.meta.url)),
        components: fileURLToPath(new URL("components.html", import.meta.url)),
      },
      output: {
        // chunks 进 build/assets/,与入口 html 的相对引用 ./build/assets/ 对齐。
        entryFileNames: "build/assets/[name]-[hash].js",
        chunkFileNames: "build/assets/[name]-[hash].js",
        assetFileNames: "build/assets/[name]-[hash][extname]",
      },
    },
  },
});
