// 探测点页(UC-12):节点页第三个页签,上半是自动剔除开关与周期,下半是
// 探测点名册。探测点本身不真跑 —— e2e 断言的是名册、接入命令与剔除设置
// 的读写,探测主循环由 Go 侧测试覆盖。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

test("探测点页签:剔除开关落设置,名册能添加、轮换、删除", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes/probes");
  await expect(page.locator("h1")).toHaveText("节点");
  await expect(page.locator(".tabs a.on")).toHaveText("探测点");

  // 默认关:只有一行说明,开关是关的,周期选择器不出现。
  const swi = page.locator("[data-act=exclude-toggle]");
  await expect(swi).toHaveAttribute("aria-checked", "false");
  await expect(page.locator("[data-act=exclude-interval]")).toHaveCount(0);

  // 空名册给空态文案与添加入口。
  await expect(page.locator(".empty")).toContainText("还没有探测点");

  // 开剔除:开关变开、周期选择器出现,设置已写入控制面。
  await swi.click();
  await expect(swi).toHaveAttribute("aria-checked", "true");
  const sel = page.locator("[data-act=exclude-interval]");
  await expect(sel).toBeVisible();
  await expect.poll(async () => (await (await cpFetch(env.cpURL + "/api/settings", { headers: { Authorization: "Bearer " + env.token } })).json()).reach_exclude).toBe(true);

  // 改周期:选 30 分钟,落库后再读回。
  await sel.selectOption("30");
  await expect.poll(async () => (await (await cpFetch(env.cpURL + "/api/settings", { headers: { Authorization: "Bearer " + env.token } })).json()).reach_interval_minutes).toBe(30);

  // 添加探测点:名字 → 一次性接入命令;命令只能用一次,页面上不该有凭据明文之外的东西。
  await page.click("[data-act=probe-add]");
  const drawer = page.locator(".drawer");
  await expect(drawer).toContainText("添加探测点");
  await drawer.locator("#probe-name").fill("家 · 测试机");
  await drawer.locator("[data-act=probe-create]").click();
  await expect(drawer).toContainText("在这台主机上执行");
  const cmd = drawer.locator(".probe-cmd");
  await expect(cmd).toContainText("mpx probe run --server http://");
  await expect(cmd).toContainText("--join ");
  await drawer.locator("[data-act=close]").click();

  // 名册出现新探测点:未接入算离线一侧;轮换与删除走确认框。
  const row = page.locator("tr", { hasText: "家 · 测试机" });
  await expect(row).toBeVisible();
  await expect(row).toContainText("未接入");

  await row.hover();
  await row.locator("[data-act=probe-rotate]").click();
  await page.locator(".modal #okb").click();
  await expect(page.locator(".drawer")).toContainText("接入 家 · 测试机");
  await page.locator(".drawer [data-act=close]").click();

  await row.hover();
  await row.locator("[data-act=probe-del]").click();
  await page.locator(".modal #okb").click();
  await expect(page.locator("tr", { hasText: "家 · 测试机" })).toHaveCount(0);
});

test("剔除开着时节点列表挂已剔除标,保留可撤销", async ({ page }) => {
  // 这条用例独立置位剔除开关,不靠前一条的状态。
  const st = await (await cpFetch(env.cpURL + "/api/settings", { headers: { Authorization: "Bearer " + env.token } })).json();
  st.reach_exclude = true;
  await cpFetch(env.cpURL + "/api/settings", { method: "PUT", headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json" }, body: JSON.stringify(st) });

  // 先造判定:reach-down 两个来源都不通;剔除开着它该出现在剔除集里。
  const listener = await import("node:net").then(net => new Promise((res, rej) => {
    const srv = net.createServer(s => s.resume());
    srv.on("error", rej);
    srv.listen(0, "127.0.0.1", () => res(srv));
  }));
  const port = listener.address().port;
  await env.seed("node", "add", "excl-ok", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("node", "add", "excl-down", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("inbound", "add", "--node", "excl-ok", "--protocol", "vless", "--port", String(port),
    "--security", "reality", "--sni", "example.com", "--reality-dest", "example.com:443");
  await env.seed("inbound", "add", "--node", "excl-down", "--protocol", "vless", "--port", String(port + 1),
    "--security", "reality", "--sni", "example.com", "--reality-dest", "example.com:443");

  // UC-26:触达检查已迁到 CLI(mpx node check),页面只展示存档。这里直接
  // POST /api/reach/results 造判定:excl-ok 两侧可达、excl-down 两侧不通。
  const get = async p => (await cpFetch(env.cpURL + p, { headers: { Authorization: "Bearer " + env.token } })).json();
  const post = (p, body) => cpFetch(env.cpURL + p, { method: "POST", headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json" }, body: JSON.stringify(body) });
  const nodes = await get("/api/nodes");
  const ids = Object.fromEntries(nodes.map(n => [n.name, n.id]));
  const inbounds = await get("/api/inbounds");
  const keyOf = name => "inbound:" + inbounds.find(i => i.node_id === ids[name]).id;
  const subject = (name, status) => ({ subject_key: "node:" + ids[name], results: { [keyOf(name)]: { tcp: { status, latency_ms: 40 } } } });
  for (const scope of ["domestic", "cp"]) {
    const r = await post("/api/reach/results", { checked_at: new Date().toISOString(), scope, source_id: `e2e-${scope}`,
      subjects: [subject("excl-ok", "reachable"), subject("excl-down", "refused")] });
    expect(r.status).toBeLessThan(300);
  }

  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator(".callout")).toContainText("自动剔除已开启");
  await expect(page.locator("th", { hasText: "控制面 → 节点" })).toBeVisible({ timeout: 30000 });

  const rowDown = page.locator("tr", { hasText: "excl-down" });
  await expect(rowDown.locator(".x-tag").first()).toHaveText("已剔除");
  const rowOK = page.locator("tr", { hasText: "excl-ok" });
  await expect(rowOK.locator(".x-tag")).toHaveCount(0);

  // 保留:已剔除标换成保留中,按钮变撤销。
  await rowDown.hover();
  await rowDown.locator("[data-act=reach-keep]").click();
  await expect(rowDown.locator(".x-tag")).toHaveCount(0);
  await expect(rowDown.locator(".k-tag").first()).toHaveText("保留中");

  // 撤销保留:已剔除标回来。
  await rowDown.hover();
  await rowDown.locator("[data-act=reach-keep]").click();
  await expect(rowDown.locator(".x-tag").first()).toHaveText("已剔除");

  listener.close();
});
