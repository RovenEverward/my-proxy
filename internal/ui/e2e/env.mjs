// 测试环境:编出 mpx、起控制面、在它前面放一个记录请求的转发。
// UC-26:浏览器直接开控制面自己的 /admin/(不再需要 mpx ui 进程),
// CLI 走同一个转发,于是「同一件事,CLI 发了什么、页面发了什么」逐条对照。
import { execFile, spawn } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import http from "node:http";
import https from "node:https";
import net from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const run = promisify(execFile);
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../../..");
const TOKEN = "e2e-token";
// 管理员账号:密码下限 12 位。
const ADMIN = { username: "admin", password: "e2e-admin-password" };

const freePort = () => new Promise(res => { const s = net.createServer(); s.listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => res(p)); }); });
const sleep = ms => new Promise(r => setTimeout(r, ms));

// binFor:整套测试共用的那份 mpx 由 global-setup.mjs 编一次,路径放在 MPX_E2E_BIN;
// 单独跑这个文件(没有 globalSetup)时自己编一份。binDir 只用来放自己编的那份。
const binFor = async binDir => {
  if (process.env.MPX_E2E_BIN) return process.env.MPX_E2E_BIN;
  const bin = path.join(binDir, process.platform === "win32" ? "mpx-e2e.exe" : "mpx-e2e");
  await run("go", ["build", "-o", bin, "./cmd/mpx"], { cwd: ROOT });
  return bin;
};

export async function startEnv() {
  const dir = mkdtempSync(path.join(tmpdir(), "mpx-e2e-"));
  const bin = await binFor(dir);
  // MPX_API_BURST:同一文件里几十条用例连着打同一个服务、同一个 IP,桶按产品默认的 60 不够用。
  const env = { ...process.env, HOME: dir, USERPROFILE: dir, XDG_CONFIG_HOME: dir, MPX_TOKEN: TOKEN, MPX_PROFILE: "", MPX_API_BURST: "1000" };
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

  // 管理台账号:页面登录走它;主 token(MPX_TOKEN)只留给 CLI 造数。
  await seed("admin", "create", "--username", ADMIN.username, "--password", ADMIN.password);

  return {
    bin, dir, cpURL, token: TOKEN, admin: { ...ADMIN }, seed, cli, log,
    takeLog: () => log.splice(0, log.length),
    // 最近一条写请求的控制面响应(状态码与原文),用来核对页面报的是不是原话。
    lastResponse: () => last && { status: last.status, body: last.response },
    // 转发上游地址:对照「页面按请求 Host 拼链接」的用例拿它当控制面视角。
    upstreamURL: cpURL,
    // origin 不带尾斜杠:各 spec 用 env.origin + "/#nodes" 拼页面地址,
    // /admin 会被 301 到 /admin/,Playwright 跟着跳过去。
    origin: recURL + "/admin",
    page: recURL + "/admin/",
    get last() { return last; },
    serveOut: () => serveOut,
    async stop() {
      for (const p of procs) p.kill("SIGTERM");
      rec.close();
      try { await Promise.all(procs.map(p => new Promise(res => { p.once("exit", res); setTimeout(res, 5000); }))); } catch { /* 进程已退出 */ }
      rmSync(dir, { recursive: true, force: true });
    },
  };
}

// 登录:第一个用例走表单(顺带把表单本身测了),同一文件后面的用例复用 cookie。
// 管理台的 mpx_admin cookie 30 天有效,一个文件跑下来不会过期。
export async function login(page, env) {
  if (process.env.MPX_INTERACTION_AUDIT) await (await import("./interaction-audit.mjs")).auditInteractions(page);
  if (env.cookies) {
    await page.context().addCookies(env.cookies);
    await page.goto(env.page);
    // 会话可能已被服务端的退出/改密吊销:落在登录页就退回表单重登。
    // 用 main 判已登录(登录页没有 main):aside.side 在窄屏是隐藏的,等它会卡死。
    await page.waitForSelector("#lg-user, main", { timeout: 15000 });
    if (await page.locator("#lg-user").count() === 0) return;
    env.cookies = null;
  }
  await page.goto(env.page);
  await page.waitForSelector("#lg-user", { timeout: 15000 });
  await page.fill("#lg-user", env.admin.username);
  await page.fill("#lg-pass", env.admin.password);
  await page.click("[data-act=login-submit]");
  // 登录成功是整页重载:等工作区落地(main 只在工作区出现)。
  await page.waitForSelector("main", { timeout: 15000 });
  env.cookies = await page.context().cookies();
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

// normalize 把记录下来的写请求整成可比对的快照:用户名/名字换成 <u>,
// 自增 id 换成 <id>,时间换毫秒,PUT body 可按 opt.drop 丢字段。
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

// httpsJSON 直连控制面的 HTTPS 侧(sniff 端口同时受理 HTTP/TLS,自签证书
// 跳过 CA 校验)。给「必须在 TLS 上说话」的接口用,如 POST /api/connect。
export function httpsJSON(port, method, p, body, headers = {}) {
  return new Promise((res, rej) => {
    const req = https.request({ host: "127.0.0.1", port, path: p, method, headers,
      agent: new https.Agent({ rejectUnauthorized: false }) }, r => {
      let b = "";
      r.on("data", c => { b += c; });
      r.on("end", () => { let j = null; try { j = JSON.parse(b); } catch {} res({ status: r.statusCode, json: j, text: b }); });
    });
    req.on("error", rej);
    req.end(body === undefined ? "" : JSON.stringify(body));
  });
}
