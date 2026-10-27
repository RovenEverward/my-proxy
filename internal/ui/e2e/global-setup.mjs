// 整套浏览器测试只编一份 mpx:每个 spec 文件自己起服务,但二进制是同一份,
// 编一次放进临时目录,路径写进 MPX_E2E_BIN,env.mjs 直接拿去用。
// 不用 globalSetup 单独跑一个 spec 文件时,env.mjs 会自己编(见 binFor)。
import { execFile } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const run = promisify(execFile);
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");

export default async function globalSetup() {
  const dir = mkdtempSync(path.join(tmpdir(), "mpx-e2e-bin-"));
  const bin = path.join(dir, process.platform === "win32" ? "mpx-e2e.exe" : "mpx-e2e");
  await run("go", ["build", "-o", bin, "./cmd/mpx"], { cwd: ROOT });
  process.env.MPX_E2E_BIN = bin;
  return () => rmSync(dir, { recursive: true, force: true });
}
