// 界面骨架第 1 步:新侧栏(按使用顺序)、旧地址跳到新位置、用户页的「用户 | 套餐」页签、
// 侧栏控制面卡片的展开面板、节点详情里的「连接方式」卡片、订阅 / 系统两页各自保存不丢对方的字段。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "tokyo-a", "--address", "203.0.113.21", "--ssh-port", "1");
  await env.seed("inbound", "add", "--node", "1", "--tag", "jp-reality", "--protocol", "vless", "--port", "8443", "--security", "reality", "--sni", "www.example.com", "--reality-dest", "www.example.com:443");
});
test.afterAll(async () => { await env?.stop(); });

test("侧栏:总览 节点 用户 订阅 分流,分隔线,体检 系统;没有分组标题和页脚术语", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  await expect(page.locator(".side .nav")).toHaveText(["总览", "节点1", "用户", "订阅", "分流", "体检", "系统"].map(t => new RegExp("^" + t)));
  await expect(page.locator(".side .nsep")).toHaveCount(1);
  await expect(page.locator(".side .navg")).toHaveCount(0);
  await expect(page.locator(".side")).not.toContainText("token 不出本进程");
  await expect(page.locator(".side")).not.toContainText("套餐");
  await expect(page.locator(".side")).not.toContainText("入站与入口");
});

for (const [from, to] of [
  ["plans", "users/plans"], ["settings", "sub"], ["settings/general", "sub"], ["settings/rules", "sub/clients"],
  ["settings/templates", "sub/templates"], ["settings/devices", "system/devices"], ["settings/backup", "system/backup"],
]) {
  test(`旧地址 #${from} 跳到 #${to}`, async ({ page }) => {
    await login(page, env);
    await page.goto(env.origin + "/#" + from);
    await expect(page).toHaveURL(new RegExp("#" + to.replace("/", "\\/") + "$"));
    await expect(page.locator(".tabs a.on")).toHaveCount(1);
  });
}

test("用户页两个页签;套餐页签亮着「用户」", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#users");
  await expect(page.locator(".tabs a")).toHaveText(["用户", "套餐"]);
  await page.click(".tabs a:has-text('套餐')");
  await expect(page).toHaveURL(/#users\/plans$/);
  await expect(page.locator(".tabs a.on")).toHaveText("套餐");
  await expect(page.locator(".side .nav.on")).toHaveText(/^用户/);
  await expect(page.locator("[data-act=pl-edit]").first()).toBeVisible();
});

test("控制面卡片:点开是地址、4 位一组的指纹、复制和切换,再点收起", async ({ page }) => {
  const FP = "SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg";
  await page.route("**/ui/meta", async r => {
    const res = await r.fetch(); const j = await res.json();
    await r.fulfill({ response: res, json: { ...j, fingerprint: FP, profiles: true } });
  });
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  await expect(page.locator(".cp-pop")).toHaveCount(0);
  // 连上是墨色点,不是绿色。
  const dot = page.locator(".cpw .dot.ok");
  await expect(dot).toHaveCount(1);
  const [bg, ink] = await dot.evaluate(e => [getComputedStyle(e).backgroundColor, getComputedStyle(document.body).color]);
  expect(bg).toBe(ink);
  await page.click(".profile[data-act=cp-toggle]");
  const pop = page.locator(".cp-pop");
  await expect(pop).toContainText("地址");
  await expect(pop.locator(".fp i").first()).toHaveText("DU4S");
  await expect(pop.locator("[data-act=copy]")).toHaveAttribute("data-text", FP);
  await expect(pop.locator("[data-act=cp-switch]")).toHaveText("切换控制面");
  await expect(pop).toContainText("只在这台电脑上能打开");
  await page.click(".profile[data-act=cp-toggle]");
  await expect(page.locator(".cp-pop")).toHaveCount(0);
});

test("节点详情:连接方式卡片用连接组件,有「添加连接」", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes/1");
  const card = page.locator(".conn-card");
  await expect(card.locator(".ch")).toContainText("连接方式");
  await expect(card.locator(".cx-row")).toHaveCount(1);
  await expect(card.locator("[data-act=conn-new]")).toHaveText("添加连接");
  await expect(page.locator(".side .nav.on")).toHaveText(/^节点/);
});

test("订阅页只改订阅那几项,通知字段原样带回;系统页反过来", async ({ page }) => {
  // 先直接在控制面上填两项通知,好看出订阅页保存时没把它们冲掉。
  const cp = (p, init = {}) => fetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
  const seeded = { ...(await (await cp("/api/settings")).json()), webhook_url: "https://hooks.example.com/x", notify_days_left: 4 };
  expect((await cp("/api/settings", { method: "PUT", body: JSON.stringify(seeded) })).ok).toBe(true);
  await login(page, env);
  const puts = [];
  page.on("request", r => { if (r.method() === "PUT" && r.url().endsWith("/api/settings")) puts.push(r.postDataJSON()); });
  const before = seeded;
  await page.goto(env.origin + "/#sub");
  await expect(page.locator("#s-webhook_url")).toHaveCount(0);
  await page.fill("#s-sub_url_prefix", "https://sub.example.com");
  await page.click("#s-save");
  await expect(page.locator(".toast")).toContainText("已保存订阅设置");
  expect(puts).toHaveLength(1);
  expect(puts[0].sub_url_prefix).toBe("https://sub.example.com");
  expect(puts[0].webhook_url).toBe(before.webhook_url);
  expect(puts[0].notify_days_left).toBe(before.notify_days_left);
  await page.goto(env.origin + "/#system");
  await expect(page.locator("#s-sub_url_prefix")).toHaveCount(0);
  await page.fill("#s-notify_usage_percent", "85");
  await page.click("#s-save");
  await expect(page.locator(".toast")).toContainText("已保存通知设置");
  expect(puts).toHaveLength(2);
  expect(puts[1].sub_url_prefix).toBe("https://sub.example.com");
  expect(puts[1].notify_usage_percent).toBe(85);
});

test("系统 · 更新与版本:显示当前版本,按钮打开更新弹窗", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#system/update");
  await expect(page.locator(".tabs a.on")).toHaveText("更新与版本");
  await expect(page.locator("[data-act=update-open]").last()).toBeVisible();
});
