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
// 页面自己的请求,r.fetch() 不走路由,不会递归。setup 模式没有控制面:地址、
// 指纹与 /api/* 都是控制面相关的改写,一概不挂,只留版本号。
async function polish(page, setup = false) {
  await page.route("**/ui/meta", async r => {
    const res = await r.fetch();
    const j = await res.json();
    j.cli_version = "v0.9.1";
    if (!setup) {
      j.server = "127.0.0.1:18080";
      j.fingerprint = "SHA256:Q2xhdWRlRXhhbXBsZUZpbmdlcnByaW50MDAwMDAwMDA";
    }
    await r.fulfill({ response: res, json: j });
  });
  if (setup) return;
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

// 自检报告的示例:13 项,12 过 1 警,文案对齐 internal/server/doctor.go 的拼法,
// 数字与路径都是示例。采样一项带时间戳,按安装路由那一刻算。
const doctorReport = () => {
  const ok = (name, detail) => ({ name, status: "ok", detail });
  const latest = new Date(Date.now() - 35e3).toISOString();
  return { checks: [
    ok("数据库", "能读能写"),
    ok("后台任务", "上一轮 1.4s,距现在 6s"),
    ok("节点 tokyo-a", "可达,服务在跑(v26.3.27),配置没有漂移,1 张证书都不临期"),
    ok("节点 tokyo-b", "可达,服务在跑(v26.3.27),配置没有漂移,1 张证书都不临期"),
    { name: "节点 frankfurt-a", status: "warn", detail: "证书 /etc/letsencrypt/live/node.example.com/fullchain.pem 还有 14 天到期", hint: "这一批不做自动续期,提前安排换证书" },
    ok("节点 sanjose-a", "可达,服务在跑(v26.3.27),配置没有漂移"),
    ok("订阅 links", "渲染出 412 字节"),
    ok("订阅 base64-links", "渲染出 556 字节"),
    ok("订阅 clash", "渲染出 1284 字节"),
    ok("订阅 clash-meta", "渲染出 1316 字节"),
    ok("订阅 sing-box", "渲染出 2410 字节"),
    ok("订阅 xray", "渲染出 1876 字节"),
    ok("流量采样", `9137 行,最近一条在 ${latest}(35s 前)`),
  ] };
};

// 更新弹窗的示例结论:桌面程序与控制面 v0.9.0 → v0.9.1 可升级,四个节点的
// xray 都已在推荐版本上;notes 是 v0.9.1 的发版说明,弹窗里按 Markdown 渲染。
const UPDATE_INFO = {
  desktop: true,
  cli: { current: "v0.9.0", latest: "v0.9.1", available: true, release_url: "" },
  control: { current: "v0.9.0", latest: "v0.9.1", available: true, release_url: "" },
  nodes: [
    { name: "tokyo-a", core: "xray", current: "26.3.27", target: "26.3.27" },
    { name: "tokyo-b", core: "xray", current: "26.3.27", target: "26.3.27" },
    { name: "frankfurt-a", core: "xray", current: "26.3.27", target: "26.3.27" },
    { name: "sanjose-a", core: "xray", current: "26.3.27", target: "26.3.27" },
  ],
  notes: "### 新增\n- 设置 → 通用 显示**当前控制面**与证书指纹\n- 配置档列表可「添加控制面…」\n\n### 修复\n- 预发布版不再误显示为「已是最新」\n- 安装脚本补跑时沿用 `mpx.service` 的监听地址",
};

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
  {
    // 添加节点向导第 1 步「连接」:填好地址与节点名,停在提交前。
    name: "node-wizard",
    run: async (page, env) => {
      await page.goto(env.origin + "/#nodes");
      await page.click('button[data-act="node-wizard"]');
      await page.waitForSelector("#w-address");
      await page.fill("#w-address", "203.0.113.40");
      await page.fill("#w-name", "singapore-a");
      await page.waitForTimeout(350); // 等抽屉滑入
    },
  },
  {
    // 节点详情 tokyo-a:巡检状态和在线用户都要现场连节点,测试环境拿不到,
    // 路由补示例(核心 26.3.27、配置一致、证书剩 47 天、在线用户 2)。只拍
    // 详情页本身,不开在线用户抽屉。
    name: "node-detail",
    routes: async (page, env) => {
      const r = await cpFetch(env.cpURL + "/api/nodes", { headers: { Authorization: "Bearer " + env.token } });
      if (!r.ok) throw new Error("GET /api/nodes -> " + r.status);
      const id = (await r.json()).find(n => n.name === "tokyo-a")?.id;
      if (!id) throw new Error("没找到节点 tokyo-a");
      env.tokyoID = id;
      const hash = "9f2c7a51e8b4d06c3a1f5e9728b4d06c3a1f5e97";
      await page.route(`**/api/nodes/${id}`, async r => {
        const res = await r.fetch();
        const j = await res.json();
        const now = new Date().toISOString();
        j.status = {
          node_id: id, checked_at: now, reachable: true, active: true, version: "26.3.27",
          observed_hash: hash,
          certificates: [{ inbound_name: "vless-443", days_left: 47, path: "/etc/letsencrypt/live/node.example.com/fullchain.pem" }],
        };
        j.deployed_hash = hash; // 与 observed 一致,详情页显示「一致」
        j.deployed_at = now;
        await r.fulfill({ response: res, json: j });
      });
      await page.route(`**/api/nodes/${id}/online`, r => r.fulfill({ json: { node: "tokyo-a", online: [
        { user: "alice", count: 2, ips: ["198.51.100.7", "198.51.100.9"] },
        { user: "bob", count: 1, ips: ["203.0.113.99"] },
      ] } }));
    },
    run: async (page, env) => {
      await page.goto(env.origin + "/#nodes/" + env.tokyoID);
      await page.waitForSelector('[data-act="node-online"]');
      await page.waitForTimeout(350);
    },
  },
  {
    // 自检页:点「开始自检」后展示示例报告(12 过 1 警)。
    name: "doctor",
    routes: async page => { await page.route("**/api/doctor", r => r.fulfill({ json: doctorReport() })); },
    run: async (page, env) => {
      await page.goto(env.origin + "/#doctor");
      await page.click('[data-act="doc-run"]');
      await page.waitForSelector('tr[data-check]');
      await page.waitForTimeout(300);
    },
  },
  {
    // 更新弹窗:/local/update 摆成示例结论,等右上角出现「更新」pill 后点开
    // 弹窗。结论要在登录前就拦上:刷新 pill 有 30 秒节流,登录那一次 load
    // 拿到什么就定形了。
    name: "update",
    routes: async page => { await page.route("**/local/update", r => r.fulfill({ json: UPDATE_INFO })); },
    run: async page => {
      await page.locator("#upd-pill").waitFor({ state: "visible" });
      await page.click("#upd-pill");
      await page.waitForSelector("#modal.open");
      await page.waitForTimeout(300); // 等 Markdown 渲染与按钮聚焦
    },
  },
  { name: "settings-devices", run: async (page, env) => { await page.goto(env.origin + "/#settings/devices"); } },
  { name: "settings-backup", run: async (page, env) => { await page.goto(env.origin + "/#settings/backup"); } },
  { name: "settings-rules", run: async (page, env) => { await page.goto(env.origin + "/#settings/rules"); } },
  // 首次安装(setup 模式:配置为空的 mpx ui)。
  { name: "setup-welcome", setup: true, run: async page => { await page.waitForSelector("#s-start"); } },
  {
    name: "setup-host", setup: true,
    run: async page => {
      await page.click("#s-start");
      await page.waitForSelector("#s-host");
      await page.fill("#s-host", "203.0.113.10");
      await page.fill("#s-port", "22");
    },
  },
  {
    // 接入已有控制面:取指纹的结果用路由摆成示例,不连任何真控制面。
    name: "setup-connect", setup: true,
    routes: async page => {
      await page.route("**/local/setup/connect/probe", r => r.fulfill({ json: {
        server: "https://203.0.113.10:8080", fingerprint: "SHA256:Q2xhdWRlRXhhbXBsZUZpbmdlcnByaW50MDAwMDAwMDA",
      } }));
    },
    run: async page => {
      await page.click("#s-connect");
      await page.waitForSelector("#s-cp-addr");
      await page.fill("#s-cp-addr", "203.0.113.10:8080");
      await page.fill("#s-cp-name", "laptop");
      await page.click("#s-cp-probe");
      await page.waitForSelector("#s-cp-ok");
    },
  },
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
      await polish(page, !!shot.setup);
      if (shot.routes) await shot.routes(page, env);
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
