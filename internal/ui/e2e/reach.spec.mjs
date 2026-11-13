// 触达检查(UC-06):节点列表长出「本机 → 节点」「控制面 → 节点」两列、语义筛选与
// 行明细抽屉。检查本身走 /local/reach/check(本机编排:控制面探一轮、本机探一轮、
// 结果回存),所以 e2e 不 mock 探测 —— 两个确定性目标就够断言判定:
//   reach-ok   地址 127.0.0.1,入站监听在测试开的 TCP 端口上,两边都该「可达」
//   reach-down 地址 127.0.0.1,入站端口没人听,两边都该「拒绝」→ 都不通
// 还没查过时(GET /api/reach/last 404)两列与筛选不该出现。
import net from "node:net";
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env, listener;
test.beforeAll(async () => {
  env = await startEnv();
  listener = net.createServer(s => s.resume());
  const port = await new Promise((res, rej) => { listener.on("error", rej); listener.listen(0, "127.0.0.1", () => res(listener.address().port)); });
  await env.seed("node", "add", "reach-ok", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("node", "add", "reach-down", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("inbound", "add", "--node", "reach-ok", "--protocol", "vless", "--port", String(port),
    "--security", "reality", "--sni", "example.com", "--reality-dest", "example.com:443");
  await env.seed("inbound", "add", "--node", "reach-down", "--protocol", "vless", "--port", String(port + 1),
    "--security", "reality", "--sni", "example.com", "--reality-dest", "example.com:443");
});
test.afterAll(async () => { listener?.close(); await env?.stop(); });

test("没查过时没有触达列,点了检查才出现两列与判定", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("h1")).toHaveText("节点");
  // 还没查过:表格只有状态那一套列,分段筛选也不出现。
  await expect(page.locator("th", { hasText: "本机 → 节点" })).toHaveCount(0);
  await expect(page.locator(".seg")).toHaveCount(0);

  // 发起检查:先看到正在检查提示,完成后两列与计数出现,进行态消退。
  await page.click("[data-act=reach-check]");
  await expect(page.locator(".callout")).toContainText("正在检查");
  await expect(page.locator("th", { hasText: "本机 → 节点" })).toBeVisible({ timeout: 30000 });
  await expect(page.locator("th", { hasText: "控制面 → 节点" })).toBeVisible();
  await expect(page.locator(".callout")).toHaveCount(0);
  await expect(page.locator("[data-act=reach-check]")).not.toHaveClass(/busy/);

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
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("th", { hasText: "本机 → 节点" })).toBeVisible();
  const rowOK = page.locator("tr", { hasText: "reach-ok" });
  await rowOK.hover();
  await rowOK.locator("[data-act=reach-view]").click();
  const drawer = page.locator(".drawer");
  await expect(drawer).toContainText("双通");
  await expect(drawer).toContainText("本机和控制面都能触达");
  await expect(drawer.locator(".reach-row")).toHaveCount(2); // 每个来源各一组入站行
  await expect(drawer.locator(".reach-row").first().locator(".reach-r").first()).toContainText("可达");
  await drawer.locator("[data-act=close]").click();
  await expect(drawer).toHaveCount(0);

  const rowDown = page.locator("tr", { hasText: "reach-down" });
  await rowDown.hover();
  await rowDown.locator("[data-act=reach-view]").click();
  await expect(page.locator(".drawer")).toContainText("都不通");
  await expect(page.locator(".drawer .reach-r").first()).toContainText("拒绝");
});
