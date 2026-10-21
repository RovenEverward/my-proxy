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
    if (to.startsWith("sub")) {
      await expect(page.locator(".tabs")).toHaveCount(0);
      await expect(page.locator(to.endsWith("clients") ? "#sec-clients" : to.endsWith("templates") ? "#sec-adv.open" : "#sec-link")).toBeInViewport();
    } else await expect(page.locator(".tabs a.on")).toHaveCount(1);
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

// Windows 自绘标题栏:最小化/最大化/关闭画在窗口右上角 138×52(--mygo-titlebar-*)。顶栏要和它们同一行、
// 右边让出这块,内容卡片从标题栏下面开始,右上角不被压住。浏览器里用 CSS 变量模拟。
test("Windows 标题栏:顶栏让出右侧按钮区,卡片在标题栏下方", async ({ page }) => {
  // 桌面壳在页面加载时注入这两个变量;这里在 DOMContentLoaded 时设到 <html> 上(页面 CSP 不许内联 <style>)。
  await page.addInitScript(() => {
    const put = () => { const r = document.documentElement.style; r.setProperty("--mygo-titlebar-inset-right", "138px"); r.setProperty("--mygo-titlebar-height", "52px"); };
    document.addEventListener("DOMContentLoaded", put);
  });
  await page.setViewportSize({ width: 1280, height: 800 });
  await login(page, env);
  await expect(page.locator("html.tb-right")).toHaveCount(1);
  // 量尺寸前先等壳子画出来:登录后到数据回来之间会整页重画,那一刻取 boundingBox 是 null。
  await expect(page.locator(".top")).toBeVisible();
  await expect(page.locator(".card").first()).toBeVisible();
  const vw = 1280;
  const top = await page.locator(".top").boundingBox();
  expect(top.y).toBe(0);
  expect(top.height).toBe(52);
  // 顶栏里最右边的按钮(主题切换)离窗口右边至少 138px,且和三按钮垂直居中对齐(中线 26px)。
  const theme = await page.locator(".top [data-act=theme]").boundingBox();
  expect(theme.x + theme.width).toBeLessThanOrEqual(vw - 138);
  expect(Math.abs(theme.y + theme.height / 2 - 26)).toBeLessThanOrEqual(1);
  // 内容卡片从标题栏下面开始:右上角那块 138×52 里没有卡片。顶栏就是主内容区的第一行(.top 是
  // main 里的吸顶行),所以卡片要在顶栏底边以下,不是只在 52px 以下。
  const main = await page.locator("main").boundingBox();
  expect(main.y).toBeGreaterThanOrEqual(52);
  const card = await page.locator(".card").first().boundingBox();
  expect(card.y).toBeGreaterThanOrEqual(top.y + top.height);
});

// 外层(html/body)不滚:WebView2 在外层出滚动条时是整页高的系统浅色条,压在窗口按钮下面。
// 只让内容区 main 自己滚,长页面照样能滚到底;标题栏模式下也一样。
for (const tb of [false, true]) test(`外层不滚动,内容区自己滚${tb ? " · Windows 标题栏" : ""}`, async ({ page }) => {
  if (tb) await page.addInitScript(() => {
    document.addEventListener("DOMContentLoaded", () => { const r = document.documentElement.style; r.setProperty("--mygo-titlebar-inset-right", "138px"); r.setProperty("--mygo-titlebar-height", "52px"); });
  });
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "platform", { value: "Win32" });
    Object.defineProperty(navigator, "userAgentData", { value: { platform: "Windows" } });
  });
  await page.setViewportSize({ width: 1100, height: 420 });
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  await expect(page.locator("html[data-loading]")).toHaveCount(0, { timeout: 20000 });
  await expect(page.locator(".grid.g4 .card.stat").first()).toBeVisible();
  if (tb) await expect(page.locator("html.tb-right")).toHaveCount(1);
  const m = await page.evaluate(() => {
    const se = document.scrollingElement, main = document.querySelector("main");
    main.scrollTop = 120;
    return {
      htmlOv: getComputedStyle(document.documentElement).overflowY, bodyOv: getComputedStyle(document.body).overflowY,
      outer: se.scrollHeight - innerHeight, mainOv: getComputedStyle(main).overflowY,
      inner: main.scrollHeight - main.clientHeight, top: main.scrollTop, mainBottom: main.getBoundingClientRect().bottom,
    };
  });
  expect(m.htmlOv).toBe("hidden");
  expect(m.bodyOv).toBe("hidden");
  expect(m.outer).toBeLessThanOrEqual(0);
  expect(m.mainOv).toBe("auto");
  expect(m.inner).toBeGreaterThan(0);  // 420 高放不下总览:内容区里有得滚
  expect(m.top).toBeGreaterThan(0);
  expect(m.mainBottom).toBeLessThanOrEqual(420);
  // 滚过之后重画(自动刷新)不会跳回顶部。
  await page.click("[data-act=reload]");
  await expect(page.locator("html[data-loading]")).toHaveCount(0, { timeout: 20000 });
  expect(await page.evaluate(() => document.querySelector("main").scrollTop)).toBeGreaterThan(0);
});

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
  // 离开后旧 main 不得重新排出隐藏计时器。
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  expect(await page.evaluate(() => mainScrollTimer)).toBe(0);
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

test("Windows 窄屏收起的抽屉完全位于视口之外", async ({ page }) => {
  await page.setViewportSize({ width: 640, height: 900 });
  await page.addInitScript(() => {
    Object.defineProperty(navigator, "platform", { value: "Win32" });
    Object.defineProperty(navigator, "userAgentData", { value: { platform: "Windows" } });
    document.addEventListener("DOMContentLoaded", () => {
      document.documentElement.style.setProperty("--mygo-titlebar-inset-right", "138px");
      document.documentElement.style.setProperty("--mygo-titlebar-height", "52px");
    });
  });
  await login(page, env);
  await expect(page.locator("html")).toHaveClass(/tb-right/);
  await expect.poll(() => page.locator("#drawer").evaluate(e => e.getBoundingClientRect().top >= innerHeight)).toBe(true);
});
