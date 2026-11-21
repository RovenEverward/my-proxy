// 触达检查(UC-06 → UC-26):发起检查已迁到 CLI(mpx node check),页面只读
// /api/reach/last 的存档展示「境内 → 节点」「控制面 → 节点」两列、语义筛选
// 与行明细抽屉。结果不真探(探测实现另有 Go 测试覆盖),直接 POST
// /api/reach/results 造两份确定性存档:
//   reach-ok    境内/控制面两侧都可达 → 双通
//   reach-down  两侧都拒绝          → 都不通
// 还没查过时(GET /api/reach/last 404)两列与筛选不该出现。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "reach-ok", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("node", "add", "reach-down", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("inbound", "add", "--node", "reach-ok", "--protocol", "vless", "--port", "24443",
    "--security", "reality", "--sni", "example.com", "--reality-dest", "example.com:443");
  await env.seed("inbound", "add", "--node", "reach-down", "--protocol", "vless", "--port", "24444",
    "--security", "reality", "--sni", "example.com", "--reality-dest", "example.com:443");
});
test.afterAll(async () => { await env?.stop(); });

const get = async path => {
  const r = await fetch(env.cpURL + path, { headers: { Authorization: "Bearer " + env.token } });
  if (!r.ok) throw new Error(path + " → " + r.status);
  return r.json();
};
const post = async (path, body) => {
  const r = await fetch(env.cpURL + path, {
    method: "POST",
    headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!r.ok) throw new Error(path + " → " + r.status + " " + (await r.text()));
};

const seedReach = async () => {
  const nodes = await get("/api/nodes");
  const ids = Object.fromEntries(nodes.map(n => [n.name, n.id]));
  // results 按 "inbound:<id>" 归集(SummarizeKeyed 按目标表回译),先取真实入站 id。
  const inbounds = await get("/api/inbounds");
  const keyOf = name => "inbound:" + inbounds.find(i => i.node_id === ids[name]).id;
  const subject = (name, status) => ({
    subject_key: "node:" + ids[name],
    results: { [keyOf(name)]: { tcp: { status, latency_ms: status === "reachable" ? 40 : 0 } } },
  });
  // 一侧一批:境内(domestic)与控制面(cp)各自的原始探测结果。
  for (const scope of ["domestic", "cp"]) {
    await post("/api/reach/results", {
      checked_at: new Date().toISOString(), scope, source_id: `e2e-${scope}`,
      subjects: [subject("reach-ok", "reachable"), subject("reach-down", "refused")],
    });
  }
};

test("没查过存档时列表只有状态列,存档落地后出现两列与判定", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("h1")).toHaveText("节点");
  // GET /api/reach/last 404:表格只有状态列,分段筛选不出现。
  await expect(page.locator("th", { hasText: "境内 → 节点" })).toHaveCount(0);
  await expect(page.locator(".seg")).toHaveCount(0);

  await seedReach();
  await page.reload();
  await expect(page.locator("th", { hasText: "境内 → 节点" })).toBeVisible();
  await expect(page.locator("th", { hasText: "控制面 → 节点" })).toBeVisible();
  // 页面不再出现发起检查的入口。
  await expect(page.locator("[data-act=reach-check]")).toHaveCount(0);

  const rowOK = page.locator("tr", { hasText: "reach-ok" });
  const rowDown = page.locator("tr", { hasText: "reach-down" });
  await expect(rowOK.locator(".reach-col").nth(0)).toContainText("可达");
  await expect(rowOK.locator(".reach-col").nth(1)).toContainText("可达");
  await expect(rowDown.locator(".reach-col").nth(0)).toContainText("不通");
  await expect(rowDown.locator(".reach-col").nth(1)).toContainText("不通");

  // 语义筛选:计数来自节点列表,「都不通」只留 reach-down。
  const seg = page.locator(".seg");
  await expect(seg).toContainText("双通");
  await expect(seg).toContainText("都不通");
  await seg.locator("button[data-f=down]").click();
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await expect(rowDown).toBeVisible();
  await expect(rowOK).toHaveCount(0);
  await seg.locator("button[data-f=all]").click();
  await expect(page.locator("tbody tr")).toHaveCount(2);
});

test("行尾「查看」打开两来源×入站明细抽屉", async ({ page }) => {
  // 单跑这条时(重启 worker)先补同一批存档。
  await seedReach();
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("th", { hasText: "境内 → 节点" })).toBeVisible();
  const rowOK = page.locator("tr", { hasText: "reach-ok" });
  await rowOK.hover();
  await rowOK.locator("[data-act=reach-view]").click();
  const drawer = page.locator(".drawer");
  await expect(drawer).toContainText("双通");
  await expect(drawer).toContainText("境内");
  await expect(drawer).toContainText("控制面");
  // 两来源各一组入站行;MetricSection 不带 data-inbound,只数入站行。
  await expect(drawer.locator(".reach-row[data-inbound]")).toHaveCount(2);
  await expect(drawer.locator(".reach-row[data-inbound]").first().locator(".reach-r").first()).toContainText("可达");
  await drawer.locator("[data-act=close]").click();
  await expect(drawer).toHaveCount(0);

  const rowDown = page.locator("tr", { hasText: "reach-down" });
  await rowDown.hover();
  await rowDown.locator("[data-act=reach-view]").click();
  await expect(page.locator(".drawer")).toContainText("都不通");
  await expect(page.locator(".drawer .reach-r").first()).toContainText("拒绝");
});
