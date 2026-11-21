// 界面骨架第 1 步:新侧栏(按使用顺序)、旧地址跳到新位置、用户页的「用户 | 套餐」页签、
// 顶栏控制面胶囊的浮层与指纹详情、节点详情里的「连接方式」卡片、订阅 / 系统两页各自保存不丢对方的字段。
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
  await expect(page.locator(".side .nav")).toHaveText(["总览", "节点1", "用户", "订阅", "分流", "体检", "系统"]);
  await expect(page.locator(".side .nsep")).toHaveCount(1);
  await expect(page.locator(".side .navg")).toHaveCount(0);
  await expect(page.locator(".side")).not.toContainText("token 不出本进程");
  await expect(page.locator(".side")).not.toContainText("套餐");
  await expect(page.locator(".side")).not.toContainText("入站与入口");
});

for (const [from, to] of [
  ["plans", "users/plans"], ["sources", "nodes/sources"], ["settings", "sub"], ["settings/general", "sub"], ["settings/rules", "sub/clients"],
  ["settings/templates", "sub/templates"], ["settings/devices", "system/acme"], ["settings/backup", "system/backup"],
]) {
  test(`旧地址 #${from} 跳到 #${to}`, async ({ page }) => {
    await login(page, env);
    await page.goto(env.origin + "/#" + from);
    await expect(page).toHaveURL(new RegExp("#" + to.replace("/", "\\/") + "$"));
    if (to.startsWith("sub")) {
      await expect(page.locator(".tabs")).toHaveCount(0);
      await expect(page.locator(to.endsWith("clients") ? "#sec-clients" : to.endsWith("templates") ? "#sec-adv.open" : "#sec-link")).toBeInViewport();
    } else await expect(page.locator(".tabs a.on")).toHaveCount(1);
  });
}

test("节点页两个页签;外部订阅页签亮着「节点」", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator(".tabs a")).toHaveText(["自建节点", "外部订阅", "探测点"]);
  await page.click(".tabs a:has-text('外部订阅')");
  await expect(page).toHaveURL(/#nodes\/sources$/);
  await expect(page.locator(".tabs a.on")).toHaveText("外部订阅");
  await expect(page.locator(".side .nav.on")).toHaveAccessibleName(/^节点/);
  await expect(page.locator("[data-act=source-new]").first()).toBeVisible();
});

test("用户页两个页签;套餐页签亮着「用户」", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#users");
  await expect(page.locator(".tabs a")).toHaveText(["用户", "套餐"]);
  await page.click(".tabs a:has-text('套餐')");
  await expect(page).toHaveURL(/#users\/plans$/);
  await expect(page.locator(".tabs a.on")).toHaveText("套餐");
  await expect(page.locator(".side .nav.on")).toHaveAccessibleName(/^用户/);
  await expect(page.locator("[data-act=pl-edit]").first()).toBeVisible();
});

test("控制面胶囊:顶栏点开是地址、版本、4 位一组的指纹、复制与退出登录,再点收起", async ({ page }) => {
  const FP = "SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg";
  await page.route("**/api/admin/meta", async r => {
    const res = await r.fetch(); const j = await res.json();
    await r.fulfill({ response: res, json: { ...j, fingerprint: FP } });
  });
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  // 侧栏不放控制面信息,胶囊在顶栏。
  await expect(page.locator(".side .profile")).toHaveCount(0);
  const pill = page.locator(".top .profile[data-act=cp-toggle]");
  await expect(pill).toBeVisible();
  await expect(pill).toContainText(env.admin.username);
  await expect(page.locator(".cp-pop")).toHaveCount(0);
  // 连上是墨色点,不是绿色。
  const dot = page.locator(".cp-menu .dot.ok");
  await expect(dot).toHaveCount(1);
  const [bg, ink] = await dot.evaluate(e => [getComputedStyle(e).backgroundColor, getComputedStyle(e.closest(".app")).color]);
  expect(bg).toBe(ink);
  await pill.click();
  const pop = page.locator(".cp-pop");
  await expect(pop).toContainText("地址");
  await expect(pop).toContainText("版本");
  await expect(pop).toContainText("证书指纹");
  await expect(pop.locator(".fp i").first()).toHaveText("DU4S");
  await expect(pop.locator("[data-act=copy]")).toBeVisible();
  await expect(pop.locator("[data-act=logout]")).toHaveText("退出登录");
  await expect(pop).toContainText("mpx admin password");
  await pill.click();
  await expect(page.locator(".cp-pop")).toHaveCount(0);
});

test("节点详情:连接方式卡片用连接组件,有「添加连接」", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes/1");
  const card = page.locator(".conn-card");
  await expect(card.locator(".ch")).toContainText("连接方式");
  await expect(card.locator(".cx-row")).toHaveCount(1);
  await expect(card.locator("[data-act=conn-new]")).toHaveText("添加连接");
  await expect(page.locator(".side .nav.on")).toHaveAccessibleName(/^节点/);
});

test("订阅页只改订阅那几项,通知字段原样带回;系统页反过来", async ({ page }) => {
  // 先直接在控制面上填两项通知,好看出订阅页保存时没把它们冲掉。
  // 前面的用例刚耗过限流桶:429 就照 Retry-After 等一下再发。
  const cp = async (p, init = {}) => {
    for (let i = 0; ; i++) {
      const r = await fetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
      if (r.status !== 429 || i >= 4) return r;
      await new Promise(res => setTimeout(res, 1000 * (+r.headers.get("Retry-After") || 1)));
    }
  };
  const seeded = { ...(await (await cp("/api/settings")).json()), webhook_url: "https://hooks.example.com/x", notify_days_left: 4 };
  expect((await cp("/api/settings", { method: "PUT", body: JSON.stringify(seeded) })).ok).toBe(true);
  await login(page, env);
  const puts = [];
  // 只数控制面真收下的 PUT:被限流(429)后页面会自己重发一次。
  page.on("response", r => { const q = r.request(); if (q.method() === "PUT" && q.url().endsWith("/api/settings") && r.status() !== 429) puts.push(q.postDataJSON()); });
  const before = seeded;
  await page.goto(env.origin + "/#sub");
  await expect(page.locator("#s-webhook_url")).toHaveCount(0);
  await page.fill("#f-prefix", "https://sub.example.com");
  await page.click("[data-act=s5-save]");
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
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


// 外层(html/body)不滚:WebView2 在外层出滚动条时是整页高的系统浅色条,压在窗口按钮下面。
// 只让内容区 main 自己滚,长页面照样能滚到底;标题栏模式下也一样。

for (const theme of ["light", "dark"]) test(`内容区细滚动条：空闲和悬停内容时隐藏，滚动时显示，宽度不变 · ${theme}`, async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "platform", { value: "Win32" });
    Object.defineProperty(navigator, "userAgentData", { value: { platform: "Windows" } });
  });
  await page.setViewportSize({ width: 1100, height: 420 });
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.evaluate(t => document.documentElement.dataset.theme = t, theme);
  await page.mouse.move(1, 1);
  const main = page.locator("main");
  const measure = () => main.evaluate(m => {
    const thumb = getComputedStyle(m, "::-webkit-scrollbar-thumb");
    return { width: m.clientWidth, content: m.querySelector("#page").getBoundingClientRect().width,
      bar: getComputedStyle(m, "::-webkit-scrollbar").width, color: thumb.backgroundColor,
      track: getComputedStyle(m, "::-webkit-scrollbar-track").backgroundColor };
  });
  const idle = await measure();
  expect(idle.bar).toBe("8px");
  expect(idle.color).toBe("rgba(0, 0, 0, 0)");
  expect(idle.track).toBe("rgba(0, 0, 0, 0)");
  await main.hover({ position: { x: 80, y: 100 } });
  const hover = await measure();
  expect(hover.color).toBe(idle.color);
  expect([hover.width, hover.content]).toEqual([idle.width, idle.content]);
  await page.mouse.move(1, 1);
  await main.evaluate(m => { m.scrollTop = m.scrollHeight; });
  await expect(main).toHaveClass(/scrolling/);
  const scrolling = await measure();
  expect(scrolling.color).not.toBe(idle.color);
  expect([scrolling.width, scrolling.content]).toEqual([idle.width, idle.content]);
  expect(await main.evaluate(m => Math.abs(m.scrollHeight - m.clientHeight - m.scrollTop))).toBeLessThanOrEqual(1);
  await expect(main).not.toHaveClass(/scrolling/);
  expect((await measure()).color).toBe(idle.color);
  expect([(await measure()).width, (await measure()).content]).toEqual([idle.width, idle.content]);
  // Leave while the old page still shows its scrolling state.
  await page.clock.install();
  await main.evaluate(m => { m.scrollTop = 0; });
  await expect(main).toHaveClass(/scrolling/);
  await page.locator('.side a[href="#nodes"]').click();
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(main).not.toHaveClass(/scrolling/);
  const nextPage = await measure();
  await page.clock.runFor(1_000);
  await expect(main).not.toHaveClass(/scrolling/);
  expect((await measure()).color).toBe("rgba(0, 0, 0, 0)");
  expect([(await measure()).width, (await measure()).content]).toEqual([nextPage.width, nextPage.content]);
});

test("Mac 由 boot 选择系统滚动条，滚到底时内容宽度稳定", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "platform", { value: "MacIntel" });
    Object.defineProperty(navigator, "userAgentData", { value: { platform: "macOS" } });
  });
  await page.setViewportSize({ width: 1100, height: 420 });
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator("html")).toHaveClass(/os-mac/);
  const main = page.locator("main");
  const measure = () => main.evaluate(m => [m.clientWidth, m.querySelector("#page").getBoundingClientRect().width]);
  expect(await main.evaluate(m => getComputedStyle(m, "::-webkit-scrollbar").width)).not.toBe("8px");
  const idle = await measure();
  await main.evaluate(m => { m.scrollTop = m.scrollHeight; });
  expect(await main.evaluate(m => m.scrollTop)).toBeGreaterThan(0);
  expect(await main.evaluate(m => Math.abs(m.scrollHeight - m.clientHeight - m.scrollTop))).toBeLessThanOrEqual(1);
  expect(await measure()).toEqual(idle);
  await expect(main).not.toHaveClass(/scrolling/);
  expect(await measure()).toEqual(idle);
});

