// 主页工作区验收：真实创建用户，保留入口、主题、移动导航和键盘操作。
// 只检查用户可观察的行为；颜色和具体布局由现有测试覆盖。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

test.use({ video: process.env.MPX_HOME_EVIDENCE ? { mode: "on", size: { width: 1440, height: 900 } } : "off" });

const NODE_NAME = "workspace-node";
const EXPIRED_USER = "workspace-expired";
const NEW_USER = "workspace-created";
let env, node;

async function control(path) {
  const response = await cpFetch(env.cpURL + path, { headers: { Authorization: "Bearer " + env.token } });
  expect(response.ok, `控制面读取 ${path}`).toBe(true);
  return response.json();
}

const settled = page => expect(page.locator("html[data-loading]")).toHaveCount(0);
const addUser = page => page.locator(".ph [data-act=user-new]");
const nodeLink = page => page.locator(`#page a[href="#nodes/${node.id}"]`).filter({ hasText: NODE_NAME });

async function overview(page) {
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  await expect(page.locator(".ph h1")).toHaveText("总览");
  await settled(page);
  await expect(page.locator(".app.home-workspace")).toBeVisible();
}

// 用实际 Tab 查找动作，不依赖固定顺序，也不用脚本代替键盘聚焦。
async function tabTo(page, target) {
  await expect(target).toBeVisible();
  for (let i = 0; i < 64; i++) {
    await page.keyboard.press("Tab");
    if (await target.evaluate(el => el === document.activeElement)) {
      await expect(target).toBeFocused();
      await expect(target).toBeInViewport();
      return;
    }
  }
  await expect(target, "Tab 应能访问这个主要动作").toBeFocused();
}

async function noHorizontalOverflow(page) {
  const overflow = await page.evaluate(() => {
    const containers = [document.documentElement, document.body, document.querySelector("main")];
    const result = containers.filter(el => el.scrollWidth > el.clientWidth).map(el => el.tagName.toLowerCase());
    // 检查内容本身，避免外层裁切把溢出隐藏。
    for (const el of document.querySelectorAll(".ph [data-act=user-new], .ov-t, .ov-n, #ov-notify, .mnav")) {
      if (!el.getClientRects().length) continue;
      const rect = el.getBoundingClientRect();
      if (rect.left < 0 || rect.right > innerWidth) result.push(el.className || el.id);
    }
    return result;
  });
  expect(overflow, "主页内容应在手机宽度内显示").toEqual([]);
}

test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", NODE_NAME, "--address", "127.0.0.1", "--ssh-port", "1");
  node = (await control("/api/nodes")).find(n => n.name === NODE_NAME);
  expect(node).toBeTruthy();
  await env.seed("inbound", "add", "--node", String(node.id), "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.example.com", "--reality-dest", "www.example.com:443");
  const expired = new Date(Date.now() - 3 * 864e5).toISOString().slice(0, 10);
  await env.seed("user", "add", EXPIRED_USER, "--expire", expired, "--limit", "50GB");
});
test.afterAll(async () => { await env?.stop(); });

test("主页页头添加用户：真写请求成功，并打开新用户详情", async ({ page }) => {
  await overview(page);
  await expect(addUser(page)).toHaveText("添加用户");
  await tabTo(page, addUser(page));
  await page.keyboard.press("Enter");
  await expect(page.locator("#drawer.open #user-form")).toBeVisible();
  await page.getByLabel("用户名", { exact: true }).fill(NEW_USER);
  await page.getByLabel("流量上限", { exact: true }).fill("10GB");
  await expect(page.locator("#f-save")).toBeEnabled();

  // 不拦截写请求；同时核对浏览器响应和控制面持久化结果。
  const saved = page.waitForResponse(response => response.request().method() === "POST"
    && new URL(response.url()).pathname === "/api/users" && response.status() !== 429);
  await page.locator("#f-save").click();
  expect((await saved).ok()).toBe(true);
  await expect(page).toHaveURL(new RegExp(`#users/${NEW_USER}$`));
  await expect(page.locator("#drawer.open h2")).toHaveText(NEW_USER);
  await expect(page.locator("#drawer.open #send")).toBeVisible();
  await expect(page.locator("#drawer.open #send h3")).toHaveText("发给他");
  await expect(page.locator("#drawer.open .ud-use")).toContainText("本期剩余");
  await expect(page.locator("#drawer.open .ud-v")).toHaveText(/^10(?:\.0+)?\s*GB$/);
  const user = await control(`/api/users/${NEW_USER}`);
  expect(user).toMatchObject({ username: NEW_USER, enabled: true, data_limit: 10 * 2 ** 30 });
  await expect(page.locator(".home-workspace")).toHaveCount(0);
});

test("节点名和需处理用户保留详情入口及列表入口", async ({ page }) => {
  await overview(page);
  await expect(page.getByRole("link", { name: /全部用户/ })).toHaveAttribute("href", "#users");
  await expect(page.locator('.ov-main a[href="#nodes"]')).toBeVisible();
  await nodeLink(page).click();
  await expect(page).toHaveURL(new RegExp(`#nodes/${node.id}$`));
  await settled(page);
  await expect(page.locator(".ph h1")).toContainText(NODE_NAME);
  await expect(page.locator(".conn-card")).toContainText("连接方式");
  await expect(page.locator(".home-workspace")).toHaveCount(0);

  await page.locator('.side a[href="#overview"]').click();
  await settled(page);
  const attention = page.locator(`tr[data-href="#users/${EXPIRED_USER}"]`);
  await expect(attention).toContainText("已过期");
  await attention.getByText(EXPIRED_USER, { exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`#users/${EXPIRED_USER}$`));
  await expect(page.locator("#drawer.open h2")).toHaveText(EXPIRED_USER);
});

test("主页主题可切换，并在跨页及重新加载后保留", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await overview(page);
  const html = page.locator("html");
  const initial = await html.getAttribute("data-theme");
  expect(["light", "dark"]).toContain(initial);
  const changed = initial === "light" ? "dark" : "light";
  await page.getByRole("button", { name: "切换主题", exact: true }).click();
  await expect(html).toHaveAttribute("data-theme", changed);
  await page.locator('.side a[href="#nodes"]').click();
  await settled(page);
  await expect(html).toHaveAttribute("data-theme", changed);
  await page.locator('.side a[href="#overview"]').click();
  await settled(page);
  await page.reload();
  await settled(page);
  await expect(page.locator(".app.home-workspace")).toBeVisible();
  await expect(html).toHaveAttribute("data-theme", changed);
  await page.getByRole("button", { name: "切换主题", exact: true }).click();
  await expect(html).toHaveAttribute("data-theme", initial);
  await page.reload();
  await settled(page);
  await expect(html).toHaveAttribute("data-theme", initial);
});

test("390×844 手机无水平溢出，底部导航可访问用户", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await overview(page);
  await expect(addUser(page)).toBeInViewport();
  await noHorizontalOverflow(page);
  await page.locator("main").evaluate(main => { main.scrollTop = main.scrollHeight; });
  await noHorizontalOverflow(page);
  const users = page.locator(".mnav").getByRole("link", { name: "用户", exact: true });
  await expect(users).toBeInViewport();
  await users.click();
  await expect(page).toHaveURL(/#users$/);
  await settled(page);
  await expect(page.locator(".ph h1")).toHaveText("用户");
  await expect(page.locator(`tr[data-href="#users/${EXPIRED_USER}"]`)).toBeVisible();
  await expect(page.locator(".home-workspace")).toHaveCount(0);
});

test("主页风格范围只在总览，切换其他主页面后清除", async ({ page }) => {
  await overview(page);
  for (const route of ["nodes", "users", "sub", "rules", "doctor", "system"]) {
    await page.locator(`.side a[href="#${route}"]`).click();
    await expect(page).toHaveURL(new RegExp(`#${route}$`));
    await settled(page);
    await expect(page.locator(".app")).toBeVisible();
    await expect(page.locator(".home-workspace")).toHaveCount(0);
    await page.locator('.side a[href="#overview"]').click();
    await settled(page);
    await expect(page.locator(".app.home-workspace")).toBeVisible();
    await expect(page.locator(".home-workspace")).toHaveCount(1);
  }
});

test("键盘可操作主题、节点详情和全部用户入口", async ({ page }) => {
  await overview(page);
  const theme = page.getByRole("button", { name: "切换主题", exact: true });
  const before = await page.locator("html").getAttribute("data-theme");
  await tabTo(page, theme);
  await page.keyboard.press("Enter");
  await expect(page.locator("html")).toHaveAttribute("data-theme", before === "dark" ? "light" : "dark");
  await tabTo(page, nodeLink(page));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`#nodes/${node.id}$`));
  await settled(page);
  await expect(page.locator(".ph h1")).toContainText(NODE_NAME);

  await page.goto(env.origin + "/#overview");
  await settled(page);
  await tabTo(page, page.getByRole("link", { name: /全部用户/ }));
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/#users$/);
  await settled(page);
  await expect(page.locator(".ph h1")).toHaveText("用户");
});
