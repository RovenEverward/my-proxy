#!/usr/bin/env node
// Package the reviewed Vite entries for the existing Go static file handler.
// Vite writes to a per-run staged directory (MPX_UI_STAGED, default
// build.tmp/): entry html at its root, hashed chunks under build.tmp/build/assets/.
// This script adds the UI-SOURCES fingerprint manifest, copies the entry html
// next to the other assets/ files, then atomically swaps build.tmp/build/ into
// place as assets/build/. A run that loses the race fails cleanly instead of
// leaving mixed old/new chunks.
//
// 布局(UC-26):入口 html 里的引用一律相对(./build/assets/x.js、boot.js、
// favicon.svg),所以同一份文件既能在 mpx serve 的 /admin/ 挂载下用,也能在
// mpx ui 的根挂载下用 —— 两个挂载点都把 assets/ 树原样伺服。
import { copyFile, readFile, rename, rm, writeFile } from "node:fs/promises";
import { createHash, randomBytes } from "node:crypto";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { transform } from "esbuild";
import { manifestText, root, uiSourceEntries } from "./ui-fingerprint.mjs";

const assets = path.join(root, "internal/ui/assets");
const staged = process.env.MPX_UI_STAGED ? path.resolve(root, process.env.MPX_UI_STAGED) : path.join(assets, "build.tmp");
const target = path.join(assets, "build");

if (!existsSync(path.join(staged, "index.html"))) {
  console.error(`暂存产物不在 ${path.relative(root, staged)}/:vite build 先跑(package-ui.mjs 只负责收尾)。`);
  process.exit(1);
}
if (!existsSync(path.join(staged, "build", "assets"))) {
  console.error(`暂存产物缺 ${path.relative(root, staged)}/build/assets/:vite output 配置被改过?`);
  process.exit(1);
}

// Everything that can change what the bundle looks like: the React tree, the
// shared token stylesheet directory, and the icon registry. Content hash, not
// mtime, so a checkout at an older commit fingerprints the same bundle.
const entries = uiSourceEntries();
const digest = createHash("sha256").update(entries.join("\n") + "\n").digest("hex");
await writeFile(path.join(staged, "build", "UI-SOURCES"), manifestText(entries));

for (const name of ["index.html", "components.html"]) {
  await copyFile(path.join(staged, name), path.join(assets, name));
}
const source = await readFile(path.join(root, "internal/ui/frontend/boot.ts"), "utf8");
const output = await transform(source, { loader: "ts", format: "iife", target: "es2022", minify: true });
await writeFile(path.join(assets, "boot.js"), "// Generated from internal/ui/frontend/boot.ts.\n" + output.code);

// Atomic swap: rename the old build aside (same volume keeps rename cheap),
// move the staged build/ tree into place, then drop both backups. A reader can
// only ever see a complete old tree or a complete new one, never a mixed write.
const backup = path.join(assets, "build.old-" + randomBytes(6).toString("hex"));
if (existsSync(target)) await rename(target, backup);
await rename(path.join(staged, "build"), target);
await rm(backup, { recursive: true, force: true });
await rm(staged, { recursive: true, force: true });
console.log(`Packaged React entries, fingerprint ${digest.slice(0, 16)}, and bootstrap for Go embed.`);
