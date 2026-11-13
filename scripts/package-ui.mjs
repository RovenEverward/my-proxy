#!/usr/bin/env node
// Package the reviewed Vite entries for the existing Go static file handler.
// Vite writes to a per-run staged directory (MPX_UI_STAGED, default
// build.tmp/), this script adds the UI-SOURCES fingerprint manifest, then
// atomically swaps the staged tree into place as assets/build/. A run that
// loses the race fails cleanly instead of leaving mixed old/new chunks.
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

// Everything that can change what the bundle looks like: the React tree, the
// shared token stylesheet directory, and the icon registry. Content hash, not
// mtime, so a checkout at an older commit fingerprints the same bundle.
const entries = uiSourceEntries();
const digest = createHash("sha256").update(entries.join("\n") + "\n").digest("hex");
await writeFile(path.join(staged, "UI-SOURCES"), manifestText(entries));

for (const name of ["index.html", "components.html"]) {
  await copyFile(path.join(staged, name), path.join(assets, name));
}
const source = await readFile(path.join(root, "internal/ui/frontend/boot.ts"), "utf8");
const output = await transform(source, { loader: "ts", format: "iife", target: "es2022", minify: true });
await writeFile(path.join(assets, "boot.js"), "// Generated from internal/ui/frontend/boot.ts.\n" + output.code);

// Atomic swap: rename the old build aside (same volume keeps rename cheap),
// move the staged tree into place, then drop the old copy. A reader can only
// ever see a complete old tree or a complete new one, never a mixed write.
const backup = path.join(assets, "build.old-" + randomBytes(6).toString("hex"));
if (existsSync(target)) await rename(target, backup);
await rename(staged, target);
await rm(backup, { recursive: true, force: true });
console.log(`Packaged React entries, fingerprint ${digest.slice(0, 16)}, and bootstrap for Go embed.`);
