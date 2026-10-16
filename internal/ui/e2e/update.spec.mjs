// 顶部「更新」pill:在页面里把 /local/update 换成假结论,免得依赖控制面真的查到
// 新版本。断言:有更新才出现、能跳过、重启以更新会按控制面→本机的顺序打接口。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const part = (over = {}) => ({ current: "v1.0.0", latest: "v1.1.0", behind: 1, available: true, release_url: "https://example.com/releases/v1.1.0", error: "", ...over });

// 把 /local/update 固定成一份假结论;返回 route 便于用例里再改。
async function mockUpdate(page, body) {
  await page.route("**/local/update", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }));
}
const available = { cli: part(), control: part({ current: "v1.0.0" }), notes: "修了几个小问题" };

test("有更新:顶栏出现 pill,弹窗有稍后与重启以更新;稍后本会话不再显示", async ({ page }) => {
  await mockUpdate(page, available);
  await login(page, env);
  const pill = page.locator("#upd-pill");
  await expect(pill).toBeVisible();
  await pill.click();
  await expect(page.locator("#modal.open")).toBeVisible();
  await expect(page.locator("#modal")).toContainText("更新到");
  await expect(page.locator("[data-act=update-skip]")).toBeVisible();
  await expect(page.locator("[data-act=update-apply]")).toBeVisible();
  await expect(page.locator(".upd-notes")).toContainText("修了几个小问题");
  // 「稍后」:关框、pill 消失,刷新后本会话仍不再出现。
  await page.click("[data-act=update-skip]");
  await expect(pill).toBeHidden();
  await page.reload();
  await expect(page.locator("#upd-pill")).toBeHidden();
});

test("无更新:顶栏不出现 pill", async ({ page }) => {
  await mockUpdate(page, { cli: part({ available: false }), control: part({ available: false }), notes: "" });
  await login(page, env);
  await expect(page.locator("#upd-pill")).toBeHidden();
});

test("重启以更新:先控制面后本机,本机升级后改成「知道了」", async ({ page }) => {
  await mockUpdate(page, available);
  const calls = [];
  await page.route("**/local/update/control", r => { calls.push("control"); return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: true, message: "控制面已升级" }) }); });
  await page.route("**/local/update/cli", r => { calls.push("cli"); return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: true, from: "v1.0.0", to: "v1.1.0", message: "已升级到 v1.1.0" }) }); });
  await login(page, env);
  await page.locator("#upd-pill").click();
  await page.click("[data-act=update-apply]");
  await expect(page.locator("#modal")).toContainText("本机二进制已更新");
  await expect(page.locator("#modal")).toContainText("重新运行一次");
  expect(calls).toEqual(["control", "cli"]);
});
