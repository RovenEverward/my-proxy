#!/usr/bin/env node
// ui:build 编排:typecheck → vite(写各自唯一的暂存目录) → package-ui(指纹+原子
// 替换到 assets/build)。每次跑独占一个 build.tmp-*/ 目录,两个并行构建各写各的,
// 替换阶段只有一方赢;赢的一方产出完整一致的树,输的一方干净报错,不会留混写。
import { execFileSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { existsSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const staged = path.join("internal/ui/assets/build.tmp-" + randomBytes(4).toString("hex"));
const env = { ...process.env, MPX_UI_STAGED: staged };

function run(cmd, args) {
  execFileSync(cmd, args, { cwd: root, env, stdio: "inherit" });
}

try {
  run("node", ["scripts/ui-typecheck.mjs"]);
  run(process.platform === "win32" ? "npx.cmd" : "npx", ["vite", "build", "--config", "internal/ui/frontend/vite.config.ts", "--outDir", "../assets/" + path.basename(staged)]);
  run("node", ["scripts/package-ui.mjs"]);
} finally {
  const abs = path.join(root, staged);
  if (existsSync(abs)) rmSync(abs, { recursive: true, force: true });
}
