#!/usr/bin/env node
// ui:build 的类型检查步骤:tsc --noEmit。独立成脚本让 ui-build.mjs 的编排里
// 不用 fork npm,也保持 npm run ui:typecheck 与这里执行的是同一条命令。
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
execFileSync(process.platform === "win32" ? "npx.cmd" : "npx",
  ["tsc", "--noEmit", "-p", "internal/ui/frontend/tsconfig.json"],
  { cwd: root, stdio: "inherit" });
