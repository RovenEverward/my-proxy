// 总览「开始使用」清单:4 步的完成与否都从数据推出来;「先用 IP」和「不再显示」记在这台电脑上;
// 4 步都完成后收成一行。标题下的一句和「近 24 小时在用」按设计稿。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "tokyo-a", "--address", "203.0.113.10");
});
test.afterAll(async () => { await env?.stop(); });

// 测试环境里节点连不上;把第一台摆成在线。
const nodesOnline = page => page.route("**/api/nodes", async r => {
  const res = await r.fetch(); const j = await res.json();
  await r.fulfill({ response: res, json: Array.isArray(j) ? j.map(n => ({ ...n, status: { ...(n.status || {}), reachable: true } })) : j });
});

test("开始使用:一步步推进,跳过与隐藏记在本机,走完收成一行", async ({ page }) => {
  await nodesOnline(page);
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  const start = page.locator("#start");
  await expect(page.locator(".ph p")).toHaveText("欢迎用 MPX。跟着下面几步，把第一条订阅发出去。");
  await expect(page.locator("main")).not.toContainText("后台循环每");
  await expect(page.locator("main")).not.toContainText("入口决定订阅里发什么地址");
  await expect(start.locator(".cl-n")).toHaveText("1 / 4");
  await expect(start.locator(".cl-h p")).toHaveText("还差 3 步");
  const ck = start.locator(".cks > .ck:not(.opt)");
  await expect(ck).toHaveCount(4);
  await expect(ck.nth(0)).toHaveClass(/done/);
  await expect(ck.nth(0)).toContainText("tokyo-a 在线");
  // 当前那一步才有按钮;后面的只有编号。
  await expect(ck.nth(1)).toHaveClass(/cur/);
  await expect(ck.nth(1).locator(".ck-a .btn")).toHaveText(["先用 IP", "去设置"]);
  await expect(ck.nth(2).locator(".ck-a .btn")).toHaveCount(0);
  await expect(ck.nth(2).locator(".ck-i")).toHaveText("3");
  await expect(start.locator(".ck.opt")).toContainText("可选：让它更好用");
  await expect(start.locator(".cl-bar i.on")).toHaveCount(1);
  await expect(page.locator(".card", { hasText: "需要处理的用户" })).toContainText("还没有用户");

  await ck.nth(1).locator("[data-act=cl-skip]").click();
  await expect(ck.nth(1)).toHaveClass(/done/);
  await expect(ck.nth(1)).toContainText("先用服务器 IP");
  await page.reload();
  await expect(ck.nth(1)).toHaveClass(/done/); // 记在本机
  expect(await page.evaluate(() => Object.keys(localStorage).filter(k => k.startsWith("mpx-cl-skip-sub:")).length)).toBe(1);
  await expect(ck.nth(2)).toHaveClass(/cur/);
  await expect(ck.nth(2).locator("[data-act=user-new]")).toHaveText("添加用户");

  await env.seed("user", "add", "frank");
  await page.reload();
  await expect(ck.nth(2)).toHaveClass(/done/);
  await expect(ck.nth(3)).toHaveClass(/cur/);
  await expect(ck.nth(3)).toContainText("他的客户端第一次拉取订阅后，这一步自动完成");
  await expect(ck.nth(3).locator("a.btn")).toHaveText("发给 frank");
  await expect(ck.nth(3).locator("a.btn")).toHaveAttribute("href", "#users/frank");

  // 复制了链接不算;客户端真拉一次才算。
  const links = await (await cpFetch(env.cpURL + "/api/users/frank/links", { headers: { Authorization: "Bearer " + env.token } })).json();
  const r = await cpFetch(links.auto.replace(/^https?:\/\/[^/]+/, env.cpURL), { headers: { "User-Agent": "sing-box 1.12.0" } });
  expect(r.status).toBe(200);
  await page.reload();
  const row = page.locator("#start-done");
  await expect(row).toContainText("开始使用 · 4 步都完成了");
  await expect(start).toHaveCount(0);
  await expect(page.locator(".ph p")).toHaveText("1 个用户 · 1 个节点");
  await expect(page.locator(".card.stat", { hasText: "近 24 小时在用" })).toContainText("1人");
  await expect(page.locator(".card.stat", { hasText: "近 24 小时在用" })).toContainText("客户端拉取过订阅");
  await row.locator("[data-act=cl-open]").click();
  await expect(start).toBeVisible();
  await expect(ck.nth(3)).toContainText("sing-box 已经导入");
  await expect(row.locator("[data-act=cl-open]")).toHaveText("收起");
  await row.locator("[data-act=cl-open]").click();
  await expect(start).toHaveCount(0);
  await row.locator("[data-act=cl-hide]").click();
  await expect(row).toHaveCount(0);
  await page.reload();
  await expect(page.locator(".card.stat").first()).toBeVisible();
  await expect(page.locator("#start-done")).toHaveCount(0);
  await expect(page.locator("#start")).toHaveCount(0);
});
