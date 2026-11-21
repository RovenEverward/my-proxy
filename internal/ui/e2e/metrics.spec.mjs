// 境内指标(UC-20):节点列表「境内」列 = 境内稳定度 + 带宽。
// 数据不走按钮流程(真实测速要出网,不可 e2e),而是直接往控制面 API 写:
//   /api/reach/results 报境内探测摘要 → 稳定度聚合
//   /api/bandwidth/results 报一次测量 → 带宽展示值
// 单元格状态:正常值 / 样本不足 / 未测。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
let fullKey, thinKey; // subject_key,beforeAll 里定下来
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "metric-full", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("node", "add", "metric-thin", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("inbound", "add", "--node", "metric-full", "--protocol", "vless", "--port", "24443",
    "--security", "reality", "--sni", "example.com", "--reality-dest", "example.com:443");
  // 探测按(节点,入站)出目标:没入站的节点样本落地即被孤儿清理,不算指标对象。
  await env.seed("inbound", "add", "--node", "metric-thin", "--protocol", "vless", "--port", "24444",
    "--security", "reality", "--sni", "example.com", "--reality-dest", "example.com:443");

  fullKey = await subject("metric-full");
  thinKey = await subject("metric-thin");
  // full:4 轮(3 通 1 失败)→ 75% + 中位 42ms;一次带宽 ok → 200 Mbps。
  for (let i = 0; i < 4; i++) {
    await domRound(fullKey, { reachable: i !== 2 }, new Date(Date.now() - (4 - i) * 3600e3));
  }
  await post("/api/bandwidth/results", {
    subject_key: fullKey, status: "ok", bytes_per_sec: 25e6, duration_ms: 5000, bytes: 15e6, test_url: "t",
  });
  // thin:只 1 轮 → 样本不足。
  await domRound(thinKey);
});
test.afterAll(async () => { await env?.stop(); });

// 直接对控制面写一条记录(带 token,不经转发层)。
const post = async (path, body) => {
  const r = await fetch(env.cpURL + path, {
    method: "POST",
    headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!r.ok) throw new Error(path + " → " + r.status + " " + (await r.text()));
};

const subject = async name => {
  // 节点 id 从 GET /api/nodes 拿(名字→id 的解析走页面,不走这里)。
  const r = await fetch(env.cpURL + "/api/nodes", { headers: { Authorization: "Bearer " + env.token } });
  const node = (await r.json()).find(n => n.name === name);
  return "node:" + node.id;
};

// 报一轮境内样本:摘要走 SourceSummary 形状(三个布尔 + 最快时延)。
const domRound = (key, { reachable = true, latency = 42 } = {}, at) => post("/api/reach/results", {
  checked_at: (at || new Date()).toISOString(), scope: "domestic", source_id: "probe:e2e",
  subjects: [{
    subject_key: key,
    summary: { probed: true, reachable, failed: !reachable, latency_ms: reachable ? latency : 0 },
  }],
});

test("境内列:正常值与样本不足两种展示", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("h1")).toHaveText("节点");

  await expect(page.locator("th", { hasText: /^境内$/ })).toBeVisible();

  const cellFull = page.locator("tr", { hasText: "metric-full" }).locator("[data-act=metric-cell]");
  await expect(cellFull).toContainText("75%");
  await expect(cellFull).toContainText("42ms");
  await expect(cellFull).toContainText("200 Mbps");

  // thin 只有 1 轮样本:稳定度说样本不足,带宽说未测。
  const cellThin = page.locator("tr", { hasText: "metric-thin" }).locator("[data-act=metric-cell]");
  await expect(cellThin).toContainText("样本不足");
  await expect(cellThin).toContainText("未测");
});
