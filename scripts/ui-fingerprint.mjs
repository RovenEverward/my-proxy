// UI 源码指纹:前端源码树的 sha256 清单与其总 digest。
// package-ui.mjs 把它写进 assets/build/UI-SOURCES 一起嵌进二进制;这里
// 对外再算一次,用于"这份 mpx 里的页面是不是当前源码"这个核对。
// 输入集合与摘要算法必须与 package-ui.mjs 保持一致——改一边必须同步另一边。
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

export function uiSourceEntries(rootDir = root) {
  const roots = [
    path.join(rootDir, "internal/ui/frontend"),
    path.join(rootDir, "internal/ui/assets/styles"),
  ];
  const extra = [path.join(rootDir, "internal/ui/icons.json")];
  const files = roots.flatMap(dir => (existsSync(dir) ? walk(dir, []) : []));
  files.push(...extra.filter(existsSync));
  files.sort();
  return files.map(file => {
    const rel = path.relative(rootDir, file).split(path.sep).join("/");
    const hash = createHash("sha256").update(readFileSync(file)).digest("hex").slice(0, 16);
    return `${hash}  ${rel}`;
  });
}

export function uiSourceDigest(entries) {
  return createHash("sha256").update(entries.join("\n") + "\n").digest("hex");
}

export function manifestText(entries) {
  return `ui-src ${uiSourceDigest(entries)}\n` + entries.join("\n") + "\n";
}

function walk(dir, out) {
  for (const name of readdirSync(dir).sort()) {
    const full = path.join(dir, name);
    const st = statSync(full);
    if (st.isDirectory()) {
      if (name !== "node_modules") walk(full, out);
    } else if (!name.endsWith(".map")) out.push(full);
  }
  return out;
}
