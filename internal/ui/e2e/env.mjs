// 测试环境:编出 mpx、起控制面、在它前面放一个记录请求的转发、起 mpx ui。
// CLI 与页面都经过这个转发,于是能把「同一件事,CLI 发了什么、页面发了什么」逐条对照。
import { execFile, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import http from "node:http";
import net from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const run = promisify(execFile);
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const TOKEN = "e2e-token";

const freePort = () => new Promise(res => { const s = net.createServer(); s.listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => res(p)); }); });
const sleep = ms => new Promise(r => setTimeout(r, ms));

export async function startEnv() {
  const dir = mkdtempSync(path.join(tmpdir(), "mpx-e2e-"));
  const bin = path.join(dir, process.platform === "win32" ? "mpx-e2e.exe" : "mpx-e2e");
  await run("go", ["build", "-o", bin, "./cmd/mpx"], { cwd: ROOT });
  const env = { ...process.env, HOME: dir, USERPROFILE: dir, XDG_CONFIG_HOME: dir, MPX_TOKEN: TOKEN, MPX_PROFILE: "" };
  const cpPort = await freePort();
  const procs = [];
  const serve = spawn(bin, ["serve", "--addr", `127.0.0.1:${cpPort}`, "--db", path.join(dir, "mpx.db")], { env, stdio: ["ignore", "pipe", "pipe"] });
  procs.push(serve);
  let serveOut = "";
  serve.stdout.on("data", d => { serveOut += d; }); serve.stderr.on("data", d => { serveOut += d; });
  const cpURL = `http://127.0.0.1:${cpPort}`;
  for (let i = 0; ; i++) {
    try { const r = await fetch(cpURL + "/api/status", { headers: { Authorization: "Bearer " + TOKEN } }); if (r.ok) break; } catch {}
    if (i > 300) throw new Error("控制面没起来:\n" + serveOut);
    await sleep(100);
  }

  // 记录转发:只记写请求(GET 是读,两边各自读多少次无所谓)。
  const log = [];
  const rec = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", c => chunks.push(c));
    req.on("end", () => {
      const body = Buffer.concat(chunks);
      const up = http.request(cpURL + req.url, { method: req.method, headers: req.headers }, r => {
        // 被限流(429)的那次会被重发,两边重发次数不定,不算进对照。
        if (req.method !== "GET" && r.statusCode !== 429) log.push({ method: req.method, path: req.url, body: body.length ? body.toString() : "" });
        res.writeHead(r.statusCode, r.headers); r.pipe(res);
      });
      up.on("error", e => { res.writeHead(502); res.end(String(e)); });
      up.end(body);
    });
  });
  const recPort = await freePort();
  await new Promise(r => rec.listen(recPort, "127.0.0.1", r));
  const recURL = `http://127.0.0.1:${recPort}`;

  const cliEnv = { ...env, MPX_SERVER: recURL };
  const cli = async (...args) => {
    const { stdout } = await run(bin, args, { env: cliEnv });
    return stdout;
  };
  // 直接打控制面的 CLI:造数据用,不进记录。
  const seed = async (...args) => (await run(bin, args, { env: { ...env, MPX_SERVER: cpURL } })).stdout;

  const ui = spawn(bin, ["ui", "--no-open", "--addr", "127.0.0.1:0"], { env: cliEnv });
  procs.push(ui);
  const link = await new Promise((res, rej) => {
    let out = "";
    ui.stdout.on("data", d => { out += d; const m = /http:\/\/\S+\?k=\S+/.exec(out); if (m) res(m[0]); });
    ui.on("exit", c => rej(new Error("mpx ui 退出了: " + c + "\n" + out)));
  });

  return {
    link, origin: new URL(link).origin, cli, seed, token: TOKEN, cpURL,
    takeLog: () => log.splice(0, log.length),
    async stop() { for (const p of procs) p.kill(); rec.close(); await sleep(200); rmSync(dir, { recursive: true, force: true }); },
  };
}

// 把一条写请求摆成可比的样子:twin 用户名换成 <u>;各人各异的 id、订阅 token、
// 创建时间去掉;时间统一成毫秒数(CLI 带本地时区偏移,浏览器给 Z,是同一刻)。
export function normalize(reqs, names) {
  const isoRe = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;
  const fixTimes = v => {
    if (typeof v === "string" && isoRe.test(v)) return Date.parse(v);
    if (Array.isArray(v)) return v.map(fixTimes);
    if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).sort(([a], [b]) => a.localeCompare(b)).map(([k, x]) => [k, fixTimes(x)]));
    return v;
  };
  return reqs.map(r => {
    let p = r.path;
    for (const n of names) p = p.split(encodeURIComponent(n)).join("<u>");
    let body = r.body ? JSON.parse(r.body) : null;
    if (body && typeof body === "object") {
      if (names.includes(body.username)) body.username = "<u>";
      if (r.method === "PUT") { delete body.id; delete body.sub_token; delete body.created_at; }
      body = fixTimes(body);
    }
    return { method: r.method, path: p, body };
  });
}

// 登录:用一次性链接换 cookie。
// 链接里的 key 只能换一次会话;之后的用例复用第一次拿到的 cookie。
export async function login(page, env) {
  if (env.cookies) {
    await page.context().addCookies(env.cookies);
    await page.goto(env.origin + "/");
  } else {
    await page.goto(env.link);
    await page.waitForURL(env.origin + "/");
    env.cookies = await page.context().cookies();
  }
}
