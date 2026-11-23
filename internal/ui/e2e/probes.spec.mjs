// 探测点页(UC-30 后):节点页第三个页签,只有探测点名册 —— 探测点常驻执行
// CDN IP 优选。e2e 断言名册、接入命令与轮换删除;优选主循环由 Go 侧覆盖。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

test("探测点页签:名册能添加、轮换、删除", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes/probes");
  await expect(page.locator("h1")).toHaveText("节点");
  await expect(page.locator(".tabs a.on")).toHaveText("探测点");

  // 空名册给空态文案与添加入口。
  await expect(page.locator(".empty")).toContainText("还没有探测点");

  // 添加探测点:名字 → 一次性接入命令。
  await page.click("[data-act=probe-add]");
  const drawer = page.locator(".drawer");
  await expect(drawer).toContainText("添加探测点");
  await drawer.locator("#probe-name").fill("家 · 测试机");
  await drawer.locator("[data-act=probe-create]").click();
  await expect(page.locator(".drawer")).toContainText("mpx probe run --server");
  await page.click(".drawer [data-act=probe-copy]");
  await page.keyboard.press("Escape");

  // 名册里出现这台,状态为「未接入」。
  const row = page.locator("tr", { hasText: "家 · 测试机" });
  await expect(row).toContainText("未接入");

  // 轮换:确认后出新接入命令。
  await row.hover();
  await row.locator("[data-act=probe-rotate]").click();
  await page.click("#okb");
  await expect(page.locator(".drawer")).toContainText("mpx probe run --server");
  await page.locator(".drawer [data-act=close]").click();

  // 删除:确认后名册清空。
  await row.hover();
  await row.locator("[data-act=probe-del]").click();
  await page.click("#okb");
  await expect(page.locator("tr", { hasText: "家 · 测试机" })).toHaveCount(0);
});
