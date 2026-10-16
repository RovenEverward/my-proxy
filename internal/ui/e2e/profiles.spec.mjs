// 左上角配置档卡片:用 page.route 把 /local/profiles 换成假列表,验证
// 当前档打勾、点别的会 POST /local/profiles/use 并整页刷新;只有一个档时不弹窗,
// 提示去 mpx login --profile 添加。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const list = [
  { name: "default", server: "http://127.0.0.1:9001", current: true },
  { name: "prod", server: "https://cp.example.net", current: false },
];

// mockProfiles 固定列表;useCalls 收集切换请求。返回 route 便于断言。
async function mockProfiles(page, items, useCalls) {
  await page.route("**/local/profiles", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(items) }));
  await page.route("**/local/profiles/use", r => {
    if (useCalls) useCalls.push(r.request().postDataJSON());
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: true }) });
  });
}

test("点配置档卡片:列出全部,当前档打勾,点别的会切换并刷新", async ({ page }) => {
  const useCalls = [];
  await mockProfiles(page, list, useCalls);
  await login(page, env);

  await page.click(".profile[data-act=profile-open]");
  await expect(page.locator("#modal.open")).toBeVisible();
  await expect(page.locator(".pfitem")).toHaveCount(2);
  await expect(page.locator(".pfitem.on")).toContainText("default");
  await expect(page.locator(".pfitem.on")).toContainText("127.0.0.1:9001");
  // 地址里的 scheme 不显示,token 不在弹窗里。
  await expect(page.locator("#modal")).not.toContainText("http://");
  await expect(page.locator("#modal")).not.toContainText("token");

  await page.click(".pfitem:not(.on)");
  await expect.poll(() => useCalls.length).toBe(1);
  expect(useCalls[0]).toEqual({ name: "prod" });
  // 切换成功后整页 reload:模态会被冲掉。
  await expect(page.locator("#modal.open")).toBeHidden();
});

test("只有一个配置档:不弹窗,提示去登录添加", async ({ page }) => {
  await mockProfiles(page, [list[0]], null);
  await login(page, env);
  await page.click(".profile[data-act=profile-open]");
  await expect(page.locator("#modal.open")).toBeHidden();
  await expect(page.locator(".toast")).toContainText("mpx login --profile");
});
