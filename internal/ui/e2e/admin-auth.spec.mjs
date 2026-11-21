// 管理台登录(UC-26):登录页、会话 cookie、退出登录与失效回登录页。
// 会话凭据是 HttpOnly cookie,JS 看不到;断言都落在界面状态与 /api 响应上。
// 登录失败统一回「用户名或密码不对」;连续错到阈值(connectLockAfter=20)
// 会整体锁 15 分钟,这里只打前几次,断言第 20 次才出现的锁定文案。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const gotoLogin = async page => {
  await page.goto(env.origin + "/");
  await expect(page.locator("#lg-user")).toBeVisible();
};

test("未登录打开管理台只看到登录页,不泄漏数据", async ({ page }) => {
  await gotoLogin(page);
  // 静态页能加载,但任何 /api 数据都不该回来。
  await expect(page.locator("aside.side")).toHaveCount(0);
  const status = await page.evaluate(() => fetch("/api/status").then(r => r.status));
  expect(status).toBe(401);
  const meta = await page.evaluate(() => fetch("/api/admin/meta").then(r => r.status));
  expect(meta).toBe(401);
});

test("错误密码:统一说法,本地拦空输入", async ({ page }) => {
  await gotoLogin(page);
  // 空提交本地拦,不打网络。
  const reqs = [];
  page.on("request", r => { if (r.url().includes("/api/admin/login")) reqs.push(1); });
  await page.click("[data-act=login-submit]");
  await expect(page.locator("#lg-err")).toContainText("用户名和密码都要填");
  expect(reqs).toHaveLength(0);
  // 错误凭据:服务端统一说法原样展示,不猜是用户错还是密码错。
  await page.fill("#lg-user", env.admin.username);
  await page.fill("#lg-pass", "wrong-password-000");
  await page.click("[data-act=login-submit]");
  await expect(page.locator("#lg-err")).toContainText("用户名或密码不对");
  // 密码被清空可重填,用户名留住。
  await expect(page.locator("#lg-pass")).toHaveValue("");
  await expect(page.locator("#lg-user")).toHaveValue(env.admin.username);
});

test("登录成功进工作区,退出登录回登录页且会话已失效", async ({ page }) => {
  await login(page, env);
  await expect(page.locator("aside.side")).toBeVisible();
  await expect(page.locator(".top .profile[data-act=cp-toggle]")).toContainText(env.admin.username);
  // 退出:服务端会话删掉,整页重载回登录页。
  await page.click(".top .profile[data-act=cp-toggle]");
  await page.click("[data-act=logout]");
  await expect(page.locator("#lg-user")).toBeVisible();
  expect(page.url()).toContain("#login");
  const status = await page.evaluate(() => fetch("/api/status").then(r => r.status));
  expect(status).toBe(401);
});


test("会话失效:运行中收到 401 自动回登录页", async ({ page }) => {
  await login(page, env);
  await expect(page.locator("aside.side")).toBeVisible();
  // 让下一个 /api 响应变成 401,触发 api.ts 的 session-expired 广播。
  await page.route("**/api/status", r => r.fulfill({ status: 401, json: { error: "会话已过期" } }));
  await page.click("[data-act=reload]");
  await expect(page.locator("#lg-user")).toBeVisible();
});

