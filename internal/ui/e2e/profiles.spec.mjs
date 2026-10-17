// 左上角配置档卡片:用 page.route 把 /local/profiles 换成假列表,验证
// 当前档打勾、每项显示指纹前 16 位、点别的会 POST /local/profiles/use 并整页刷新;
// 弹窗里有「添加控制面…」入口,只有一个档时也弹(加新控制面就从这进)。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const FP = "SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg";
const list = [
  { name: "default", server: "http://127.0.0.1:9001", current: true, fingerprint: FP },
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
  // 指纹只摆前 16 位;地址里的 scheme 不显示,token 不在弹窗里。
  await expect(page.locator(".pfitem.on")).toContainText(FP.slice(0, 16));
  await expect(page.locator("#modal")).not.toContainText(FP);
  await expect(page.locator("#modal")).not.toContainText("http://");
  await expect(page.locator("#modal")).not.toContainText("token");

  await page.click(".pfitem:not(.on)");
  await expect.poll(() => useCalls.length).toBe(1);
  expect(useCalls[0]).toEqual({ name: "prod" });
  // 切换成功后整页 reload:模态会被冲掉。
  await expect(page.locator("#modal.open")).toBeHidden();
});

test("弹窗里有「添加控制面…」:打开接入表单", async ({ page }) => {
  await mockProfiles(page, list, null);
  // 接入表单第一步要取指纹,把 probe 挡在路由层即可(不点接入就不用真连)。
  await page.route("**/local/profiles/connect/probe", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ server: "https://203.0.113.10:8080", fingerprint: FP }) }));
  await login(page, env);

  await page.click(".profile[data-act=profile-open]");
  await page.click("[data-act=profile-add]");
  // 模态关掉,换成全屏的接入表单;配置档名是正常模式多出来的必填项。
  await expect(page.locator("#modal.open")).toBeHidden();
  await expect(page.locator(".setup")).toBeVisible();
  await expect(page.locator("#s-cp-addr")).toBeVisible();
  await expect(page.locator("#s-cp-profile")).toBeVisible();
  await page.fill("#s-cp-addr", "203.0.113.10");
  await page.click("#s-cp-probe");
  // 取到指纹进入下一步:指纹显示出来,设备密码输入框出现;界面里没有命令文案。
  await expect(page.locator(".setup")).toContainText(FP);
  await expect(page.locator("#s-cp-pw")).toBeVisible();
  await expect(page.locator(".setup")).not.toContainText("mpx ");
});

test("只有一个配置档:也弹窗,好从这加新的", async ({ page }) => {
  await mockProfiles(page, [list[0]], null);
  await login(page, env);
  await page.click(".profile[data-act=profile-open]");
  await expect(page.locator("#modal.open")).toBeVisible();
  await expect(page.locator(".pfitem")).toHaveCount(1);
  await expect(page.locator("[data-act=profile-add]")).toBeVisible();
});

