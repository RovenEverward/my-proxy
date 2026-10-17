#!/usr/bin/env node
// 给用户文档截 mpx ui 的界面图:浅色存 docs/public/ui/<name>.png,深色存 <name>-dark.png。
// 用法:node scripts/docs-shots.mjs [--only a,b]
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { startEnv, startSetupEnv, login, cpFetch } from "../internal/ui/e2e/env.mjs";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const OUT = path.join(ROOT, "docs", "public", "ui");

// 灌示例数据:两个套餐、五个用户、四台节点各一条入站与入口,设置页填上订阅
// 前缀等,让页面不是空列表。都走控制面 API,不碰 SSH(auto-locate 在测试
// 环境里是关的,建节点不会出网)。
async function seed(env) {
  const req = async (method, p, body) => {
    const r = await cpFetch(env.cpURL + p, {
      method,
      headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!r.ok) throw new Error(`${method} ${p} -> ${r.status}: ${(await r.text()).slice(0, 200)}`);
    return r.status === 204 ? null : r.json();
  };
  const post = (p, body) => req("POST", p, body);

  await post("/api/plans", { name: "标准", note: "全部节点", all_nodes: true });
  await post("/api/plans", { name: "入门", note: "轻量使用" });
  const r = await cpFetch(env.cpURL + "/api/plans", { headers: { Authorization: "Bearer " + env.token } });
  if (!r.ok) throw new Error("GET /api/plans -> " + r.status);
  const data = await r.json();
  const plans = Array.isArray(data) ? data : data.plans;
  const planID = name => plans.find(p => p.name === name)?.id;

  const GiB = 1024 ** 3;
  const in2Days = new Date(Date.now() + 2 * 86400e3).toISOString();
  const users = [
    { username: "alice", plan_id: planID("标准") },
    { username: "bob", plan_id: planID("标准"), data_limit: 200 * GiB },
    { username: "carol", plan_id: planID("标准"), expire_at: in2Days },
    { username: "dave", plan_id: planID("入门"), data_limit: 50 * GiB },
    { username: "erin", plan_id: planID("入门") },
  ];
  for (const u of users) await post("/api/users", { enabled: true, ...u });

  // 节点与入站:只建记录,不连 SSH(不带 ssh_password 就不会去碰节点)。
  const CERT = ["/etc/letsencrypt/live/node.example.com/fullchain.pem", "/etc/letsencrypt/live/node.example.com/privkey.pem"];
  const nodes = [
    { name: "tokyo-a", address: "203.0.113.10", inbound: { protocol: "vless", port: 443, network: "ws", security: "tls", ws_path: "/ws", cert_file: CERT[0], key_file: CERT[1] }, host: "东京 A" },
    { name: "tokyo-b", address: "203.0.113.11", inbound: { protocol: "vmess", port: 8443, network: "ws", security: "tls", ws_path: "/vm", cert_file: CERT[0], key_file: CERT[1] }, host: "东京 B" },
    { name: "frankfurt-a", address: "203.0.113.20", inbound: { protocol: "trojan", port: 443, network: "tcp", security: "tls", cert_file: CERT[0], key_file: CERT[1] }, host: "法兰克福 A" },
    { name: "sanjose-a", address: "198.51.100.30", inbound: { protocol: "vless", port: 443, network: "tcp", security: "reality", reality_dest: "www.microsoft.com:443", sni: "www.microsoft.com" }, host: "圣何塞 A" },
  ];
  for (const n of nodes) {
    const created = await post("/api/nodes", { name: n.name, address: n.address });
    const inb = await post("/api/inbounds", { node_id: created.id, ...n.inbound });
    await post(`/api/inbounds/${inb.id}/hosts`, { remark: n.host });
  }

  // 分流规则页:灌三条示例(域名与网段都用文档专用段,不指真实站点)。
  await req("PUT", "/api/rules", {
    rules: [
      { type: "suffix", value: "example.com", target: "DIRECT", enabled: true, note: "文档示例:直连" },
      { type: "suffix", value: "ads.example.com", target: "REJECT", enabled: true, note: "文档示例:拦截" },
      { type: "cidr", value: "192.0.2.0/24", target: "DIRECT", enabled: true, note: "文档示例:测试网段" },
    ],
  });

  // 设置是整体替换:先取回再合并,免得把 templates 抹掉;识别规则给三条示例,
  // 设置·识别规则页不是空表(平时没配过会回落内置默认,但页面上按 rules 原样显示)。
  const settings = await req("GET", "/api/settings");
  await req("PUT", "/api/settings", {
    ...settings,
    sub_url_prefix: "https://sub.example.com",
    profile_title: "{USERNAME} 的订阅",
    update_interval: 12,
    support_link: "https://example.com/support",
    rules: [
      { pattern: "(?i)karing", result: "clash-meta" },
      { pattern: "(?i)sing-box|sfi/|sfa/|sfm/|hiddify|nekobox", result: "sing-box" },
      { pattern: "(?i)shadowrocket", result: "base64-links" },
    ],
  });

  // 设置·设备:设一个设备密码,用它让 laptop / phone 两台各接入一次(回环上
  // 明文可受理),再用设备 token 打一次 /api/status,让「上次使用」有值。
  const devPW = "Docs-Device-2026";
  await req("PUT", "/api/device-password", { password: devPW });
  for (const name of ["laptop", "phone"]) {
    const r = await cpFetch(env.cpURL + "/api/connect", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: devPW, device_name: name }),
    });
    if (!r.ok) throw new Error(`POST /api/connect(${name}) -> ${r.status}: ${(await r.text()).slice(0, 200)}`);
    const tok = (await r.json()).token;
    const s = await cpFetch(env.cpURL + "/api/status", { headers: { Authorization: "Bearer " + tok } });
    if (!s.ok) throw new Error(`GET /api/status(${name}) -> ${s.status}`);
  }
}

// 截图前统一改写页面拿到的元数据,让图里是文档想展示的样子而不是测试环境的
// 随机值:版本号去掉 +dirty、地址与指纹换成示例、节点状态显示运行中。拦的是
// 页面自己的请求,r.fetch() 不走路由,不会递归。
async function polish(page) {
  await page.route("**/ui/meta", async r => {
    const res = await r.fetch();
    const j = await res.json();
    j.cli_version = "v0.9.1";
    j.server = "127.0.0.1:18080";
    j.fingerprint = "SHA256:Q2xhdWRlRXhhbXBsZUZpbmdlcnByaW50MDAwMDAwMDA";
    await r.fulfill({ response: res, json: j });
  });
  await page.route("**/api/status", async r => {
    const res = await r.fetch();
    const j = await res.json();
    j.version = "v0.9.1";
    await r.fulfill({ response: res, json: j });
  });
  // 测试环境没真的 SSH 上节点巡检过,列表里会全是「未巡检」;给每台补一份
  // 正常的状态,总览与节点页都显示「运行中」。页面可能接连请求几次(登录后
  // 一次、goto 再一次),控制面有限流:取一次成功就缓存着用,429 就等等重试,
  // 实在不行原样放行。
  let nodesCache = null;
  await page.route("**/api/nodes", async r => {
    if (r.request().method() !== "GET") return r.continue();
    if (!nodesCache) {
      let res;
      for (let i = 0; i < 4; i++) {
        res = await r.fetch();
        if (res.ok()) break;
        await page.waitForTimeout(400);
      }
      if (!res.ok()) return r.fulfill({ response: res });
      nodesCache = await res.json();
    }
    const j = nodesCache;
    const now = new Date().toISOString();
    for (const n of j) n.status = { node_id: n.id, checked_at: now, reachable: true, active: true, version: "26.3.27", certificates: [] };
    await r.fulfill({ json: j });
  });
}

const SHOTS = [
  {
    name: "overview",
    run: async (page, env) => { await page.goto(env.origin + "/#overview"); },
  },
  {
    name: "settings-general",
    run: async (page, env) => { await page.goto(env.origin + "/#settings/general"); },
  },
  { name: "users", run: async (page, env) => { await page.goto(env.origin + "/#users"); } },
  {
    // 添加用户弹窗:填好 frank / 标准,停在提交前。
    name: "user-new",
    run: async (page, env) => {
      await page.goto(env.origin + "/#users");
      await page.click('button[data-act="user-new"]');
      await page.waitForSelector("#user-form");
      await page.fill("#f-name", "frank");
      await page.selectOption("#f-plan", { label: "标准" });
      await page.waitForTimeout(350); // 等抽屉滑入
    },
  },
  { name: "user-detail", run: async (page, env) => { await page.goto(env.origin + "/#users/alice"); await page.waitForSelector("#drawer.open"); await page.waitForTimeout(350); } },
  { name: "plans", run: async (page, env) => { await page.goto(env.origin + "/#plans"); } },
  {
    // 编辑套餐「标准」:点那一行打开抽窗。
    name: "plan-detail",
    run: async (page, env) => {
      await page.goto(env.origin + "/#plans");
      const r = await cpFetch(env.cpURL + "/api/plans", { headers: { Authorization: "Bearer " + env.token } });
      if (!r.ok) throw new Error("GET /api/plans -> " + r.status);
      const data = await r.json();
      const plans = Array.isArray(data) ? data : data.plans;
      const id = plans.find(p => p.name === "标准")?.id;
      if (!id) throw new Error("没找到套餐「标准」");
      await page.click(`.pl-row[data-id="${id}"] .pl-name`);
      await page.waitForSelector("#pl-form");
      await page.waitForTimeout(350);
    },
  },
  { name: "rules", run: async (page, env) => { await page.goto(env.origin + "/#rules"); } },
  { name: "settings-devices", run: async (page, env) => { await page.goto(env.origin + "/#settings/devices"); } },
  { name: "settings-backup", run: async (page, env) => { await page.goto(env.origin + "/#settings/backup"); } },
  { name: "settings-rules", run: async (page, env) => { await page.goto(env.origin + "/#settings/rules"); } },
];

// 一张图 = 浅色、深色各截一次;每次新 context,主题在首个文档脚本前写进 localStorage。
async function takeShot(browser, shot, env) {
  for (const theme of ["light", "dark"]) {
    const context = await browser.newContext({
      viewport: { width: 1280, height: 820 },
      deviceScaleFactor: 2,
      locale: "zh-CN",
      timezoneId: "Asia/Shanghai",
    });
    try {
      await context.addInitScript(t => localStorage.setItem("mpx-theme", t), theme);
      const page = await context.newPage();
      await polish(page);
      await login(page, env);
      await shot.run(page, env);
      // 页面有周期轮询时 networkidle 等不到,超时就接着截,再留 300ms 收尾。
      await page.waitForLoadState("networkidle", { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(300);
      const file = path.join(OUT, theme === "dark" ? `${shot.name}-dark.png` : `${shot.name}.png`);
      await page.screenshot({ path: file });
      console.log("已生成 " + path.relative(ROOT, file));
    } finally {
      await context.close();
    }
  }
}

const argv = process.argv.slice(2);
const only = argv[0] === "--only" ? argv[1]?.split(",").map(s => s.trim()).filter(Boolean) : null;
const shots = only ? SHOTS.filter(s => only.includes(s.name)) : SHOTS;
if (!shots.length) {
  console.error(`没有匹配的截图:${only ? only.join(", ") : "(SHOTS 为空)"}`);
  process.exit(1);
}

const browser = await chromium.launch();
try {
  mkdirSync(OUT, { recursive: true });
  const withEnv = shots.filter(s => !s.setup);
  const withSetup = shots.filter(s => s.setup);
  if (withEnv.length) {
    const env = await startEnv();
    try {
      await seed(env);
      for (const shot of withEnv) await takeShot(browser, shot, env);
    } finally {
      await env.stop();
    }
  }
  if (withSetup.length) {
    const env = await startSetupEnv();
    try {
      for (const shot of withSetup) await takeShot(browser, shot, env);
    } finally {
      await env.stop();
    }
  }
} finally {
  await browser.close();
}
