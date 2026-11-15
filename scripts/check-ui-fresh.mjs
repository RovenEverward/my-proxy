#!/usr/bin/env node
// Stale-UI guard: compare the current frontend source fingerprint with the
// one embedded in the last build or a chosen mpx binary.
//
//   node scripts/check-ui-fresh.mjs           → assets/build/UI-SOURCES vs source tree
//   node scripts/check-ui-fresh.mjs /path/mpx → binary-embedded UI vs source tree
//
// Exit 0 when the UI artifact matches the source, exit 1 with a readable
// message when it is stale or missing.
import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { manifestText, root, uiSourceDigest, uiSourceEntries } from "./ui-fingerprint.mjs";

const arg = process.argv[2];
const entries = uiSourceEntries();
const want = uiSourceDigest(entries);
const hint = "先跑 npm run ui:build 再重新编译 mpx;或直接 npm run ui 一步到位";

let have = "", label = "";
if (arg) {
  // cobra 的 cmd.Println 走 stderr,stdout 是空的,两路都接住。
  const res = spawnSync(arg, ["ui", "fingerprint"], { cwd: root, encoding: "utf8" });
  have = res.status === 0 ? ((res.stdout || "") + (res.stderr || "")).trim() : "";
  label = res.status === 0 ? arg : arg + "（取不到指纹,可能是没这个命令的旧构建）";
} else {
  const file = path.join(root, "internal/ui/assets/build/UI-SOURCES");
  if (existsSync(file)) {
    const first = readFileSync(file, "utf8").split("\n", 1)[0];
    have = first.startsWith("ui-src ") ? first.slice(7).trim() : "";
  }
  label = "internal/ui/assets/build/";
}

if (have !== want) {
  const detail = have === "" ? "找不到 UI-SOURCES 指纹" : `内嵌 ${have.slice(0, 16)} ≠ 源码 ${want.slice(0, 16)}`;
  console.error(`UI 产物与源码不一致: ${detail}\n${hint}`);
  process.exit(1);
}
console.log(`UI 产物指纹 ${want.slice(0, 16)} 与源码一致(${label})`);
