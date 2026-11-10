#!/usr/bin/env node
// Package the reviewed Vite entries for the existing Go static file handler.
import { copyFile, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { transform } from "esbuild";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const assets = path.join(root, "internal/ui/assets");
for (const name of ["index.html", "components.html"]) {
  await copyFile(path.join(assets, "build", name), path.join(assets, name));
}
const source = await readFile(path.join(root, "internal/ui/frontend/boot.ts"), "utf8");
const output = await transform(source, { loader: "ts", format: "iife", target: "es2022", minify: true });
await writeFile(path.join(assets, "boot.js"), "// Generated from internal/ui/frontend/boot.ts.\n" + output.code);
console.log("Packaged React entries and bootstrap for Go embed.");
