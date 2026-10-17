// 写操作之后列表要立刻反映新状态,不能等 30 秒一轮的自动刷新。
// 每条用例都把断言超时压到 3 秒:比轮询短得多,靠轮询碰巧刷到的不算过。
import { test, expect } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { startEnv, login } from "./env.mjs";

const NOW = { timeout: 3000 };
let env;
const file = (name, text) => { const f = path.join(tmpdir(), `mpx-e2e-r-${process.pid}-${name}`); writeFileSync(f, text); return f; };
const cp = async p => { for (;;) { const r = await fetch(env.cpURL + p, { headers: { Authorization: "Bearer " + env.token } }); if (r.status !== 429) return r; await new Promise(s => setTimeout(s, 1000)); } };

test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "keep", "--address", "203.0.113.10");
  await env.seed("node", "add", "gone", "--address", "203.0.113.11");
  await env.seed("inbound", "add", "--node", "1", "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.cloudflare.com", "--reality-dest", "www.cloudflare.com:443");
  await env.seed("inbound", "add", "--node", "1", "--protocol", "vless", "--port", "8443", "--security", "reality", "--sni", "www.apple.com", "--reality-dest", "www.apple.com:443");
  await env.seed("host", "add", "--inbound", "1", "--remark", "待删入口", "--port", "9443");
  for (const u of ["r-rm", "r-toggle", "r-keep"]) await env.seed("user", "add", u);
  await env.seed("user", "add", "r-renew", "--expire", "2026-12-01");
  const tpl = await (await cp("/api/settings/templates/xray")).text();
  await env.seed("settings", "template", "set", "xray", "-f", file("tpl.json", tpl));
});
test.afterAll(async () => { await env?.stop(); });

const userRow = (page, n) => page.locator(`tr[data-href="#users/${n}"]`);

test("删除用户:列表里立刻没了这个人", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#users");
  await expect(userRow(page, "r-rm")).toHaveCount(1);
  await userRow(page, "r-rm").click();
  await page.click("[data-act=user-rm]");
  await expect(page.locator("#modal.open")).toContainText("删除 r-rm?");
  await page.fill("#typed", "r-rm");
  await page.click("#okb");
  await expect(page).toHaveURL(/#users$/);
  await expect(userRow(page, "r-rm")).toHaveCount(0, NOW);
  await expect(userRow(page, "r-keep")).toHaveCount(1);
});

test("停用 / 启用用户:关掉抽屉,列表里状态已经变了", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#users");
  await userRow(page, "r-toggle").click();
  await page.click("[data-act=user-toggle]");
  await page.click("#okb");
  await page.keyboard.press("Escape");
  await expect(userRow(page, "r-toggle")).toContainText("已停用", NOW);
  await userRow(page, "r-toggle").click();
  await page.click("[data-act=user-toggle]");
  if (await page.locator("#modal.open").count()) await page.click("#okb");
  await expect(page.locator(".toast")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(userRow(page, "r-toggle")).toContainText("正常", NOW);
});

test("续期用户:列表里的剩余天数立刻更新", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#users");
  const before = await userRow(page, "r-renew").textContent();
  await userRow(page, "r-renew").click();
  await page.click("[data-act=user-renew]");
  await expect(page.locator(".toast")).toContainText("到期日");
  await page.keyboard.press("Escape");
  await expect(userRow(page, "r-renew")).not.toHaveText(before, NOW);
});

test("删除入口与入站:入站页立刻更新", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#inbounds");
  await expect(page.locator("[data-act=host-edit][data-id='1']")).toHaveCount(1);
  await page.click("[data-act=host-edit][data-id='1']");
  await page.click("[data-act=host-rm]");
  await page.click("#okb");
  await expect(page.locator("[data-act=host-edit][data-id='1']")).toHaveCount(0, NOW);

  await page.click("[data-act=inb-edit][data-id='2']");
  const tag = await page.locator("[data-act=inb-rm]").getAttribute("data-tag");
  await page.click("[data-act=inb-rm]");
  await page.fill("#typed", tag);
  await page.click("#okb");
  await expect(page.locator("[data-act=inb-edit][data-id='2']")).toHaveCount(0, NOW);
  await expect(page.locator("[data-act=inb-edit][data-id='1']")).toHaveCount(1);
});

test("删除节点:节点列表立刻更新", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  const row = page.locator('tr[data-href="#nodes/2"]');
  await row.click();
  await page.click("[data-act=node-rm]");
  await page.fill("#typed", "gone");
  await page.click("#okb");
  await expect(page).toHaveURL(/#nodes$/);
  await expect(row).toHaveCount(0, NOW);
  await expect(page.locator('tr[data-href="#nodes/1"]')).toHaveCount(1);
});

test("恢复内置模板:模板列表立刻显示内置默认", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#settings/templates");
  await expect(page.locator("tr[data-f='xray']")).toContainText("自定义");
  await page.click("button[data-act=tpl-del][data-f='xray']");
  await page.fill("#typed", "xray");
  await page.click("#okb");
  await expect(page.locator("tr[data-f='xray']")).toContainText("内置默认", NOW);
});
