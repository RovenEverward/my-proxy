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
    outDir: "../assets/build",
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: { input: {
      index: fileURLToPath(new URL("index.html", import.meta.url)),
      components: fileURLToPath(new URL("components.html", import.meta.url)),
    } },
  },
});
