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
  const serve = spawn(bin, ["serve", "--addr", `127.0.0.1:${cpPort}`, "--db", path.join(dir, "mpx.db"), "--auto-locate=false"] /* 不出网:自动识别另有 Go 测试 */, { env, stdio: ["ignore", "pipe", "pipe"] });
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
  let last = null;
  const rec = http.createServer((req, res) => {
    const chunks = [];
    req.on("data", c => chunks.push(c));
    req.on("end", () => {
      const body = Buffer.concat(chunks);
      const up = http.request(cpURL + req.url, { method: req.method, headers: req.headers }, r => {
        // 被限流(429)的那次会被重发,两边重发次数不定,不算进对照。
        if (req.method !== "GET" && r.statusCode !== 429) {
          const e = { method: req.method, path: req.url, body: body.length ? body.toString() : "", status: r.statusCode, response: "" };
          log.push(e); last = e;
          r.on("data", c => { e.response += c; });
        }
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
    // 最近一条写请求的控制面响应(状态码与原文),用来核对页面报的是不是原话。
    lastResponse: () => last && { status: last.status, body: last.response },
    async stop() { for (const p of procs) p.kill(); rec.close(); await sleep(200); rmSync(dir, { recursive: true, force: true }); },
  };
}

// 把一条写请求摆成可比的样子:twin 的名字换成 <u>;各人各异的 id、订阅 token、
// 创建时间去掉;时间统一成毫秒数(CLI 带本地时区偏移,浏览器给 Z,是同一刻)。
// opt.ids:twin 的数字 id(路径段与 id / node_id / inbound_id 字段)换成同一个占位;
// opt.drop:PUT 里两边各自生成、本就不同的字段(如 REALITY 公钥)。
export function normalize(reqs, names, opt = {}) {
  const ids = opt.ids || [];
  const idOf = v => ids.includes(String(v)) ? "<id>" : v;
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
    p = p.split("?")[0].split("/").map(idOf).join("/") + (p.includes("?") ? "?" + p.split("?")[1] : "");
    let body = null;
    if (r.body) { try { body = JSON.parse(r.body); } catch { body = r.body; } } // 模板正文不是 JSON
    if (body && typeof body === "object") {
      for (const k of ["username", "name", "tag", "remark"]) if (names.includes(body[k])) body[k] = "<u>";
      for (const k of ["id", "node_id", "inbound_id"]) if (k in body) body[k] = idOf(body[k]);
      if (r.method === "PUT") for (const k of opt.drop || []) delete body[k];
      if (r.method === "PUT" && "username" in body) { delete body.id; delete body.sub_token; delete body.created_at; }
      body = fixTimes(body);
    }
    return { method: r.method, path: p, body };
  });
}

// startSetupEnv:给「首次安装向导」用的独立环境。没有控制面,只有 mpx ui 自己。
// 编译一次 mpx,HOME/配置目录指到临时目录,清掉 MPX_SERVER/MPX_TOKEN/MPX_PROFILE,
// 于是页面进入 setup 模式、显示向导。从 stdout 抓带一次性 key 的登录链接。
export async function startSetupEnv() {
  const dir = mkdtempSync(path.join(tmpdir(), "mpx-e2e-setup-"));
  const bin = path.join(dir, process.platform === "win32" ? "mpx-e2e.exe" : "mpx-e2e");
  await run("go", ["build", "-o", bin, "./cmd/mpx"], { cwd: ROOT });
  const env = {
    ...process.env, HOME: dir, USERPROFILE: dir, XDG_CONFIG_HOME: dir,
    MPX_SERVER: "", MPX_TOKEN: "", MPX_PROFILE: "",
  };
  const cp = spawn(bin, ["ui", "--no-open", "--addr", "127.0.0.1:0"], { env, stdio: ["ignore", "pipe", "pipe"] });
  const link = await new Promise((res, rej) => {
    let out = "";
    cp.stdout.on("data", d => { out += d; const m = /http:\/\/\S+\?k=\S+/.exec(out); if (m) res(m[0]); });
    cp.stderr.on("data", d => { out += d; });
    cp.on("exit", c => rej(new Error("mpx ui 退出了: " + c + "\n" + out)));
  });
  return {
    link, origin: new URL(link).origin,
    async stop() { cp.kill(); await sleep(200); rmSync(dir, { recursive: true, force: true }); },
  };
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

// cpFetch 直接问控制面,遇到 429 按 Retry-After 等一下再试 —— 和 CLI、页面的做法一样。
// 整套用例在同一个 IP 上连续写,限流桶偶尔会见底;那是限流在正常工作,不是被测的东西坏了。
export async function cpFetch(url, init) {
  for (let i = 0; ; i++) {
    const r = await fetch(url, init);
    if (r.status !== 429 || i >= 5) return r;
    await new Promise(ok => setTimeout(ok, 1000 * (+r.headers.get("Retry-After") || 2)));
  }
}
