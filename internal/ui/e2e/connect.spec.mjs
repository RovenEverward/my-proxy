// 「接入已有控制面」浏览器测试:欢迎屏进接入表单,走真的取指纹与设备密码换
// token(POST /local/setup/connect/probe、/local/setup/connect 背后就是 mpx connect
// 的那条路)。控制面是起真的:指纹来自 GET /api/status 的 tls_fingerprint,
// 设备密码是 beforeAll 里用管理 token 设的;mpx ui 每个用例各起一台(配置为空),
// 谁接入成功切到正常模式都不影响别人。
import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import path from "node:path";
import { startConnectEnv, login } from "./env.mjs";

test.describe.configure({ mode: "serial" });

let env;

test.beforeAll(async () => {
  env = await startConnectEnv();
});
test.afterAll(async () => {
  await env?.stop();
});

// 开一台空配置的 mpx ui,登录后从欢迎屏进「接入已有控制面」表单。
async function openConnectForm(page) {
  const u = await env.startUI();
  await login(page, u);
  await expect(page.locator("#s-start")).toBeVisible();
  await page.locator("#s-connect").click();
  await expect(page.locator("#s-cp-addr")).toBeVisible();
  return u;
}

// 填地址、取指纹,等指纹摆出来。返回显示指纹的那个 .cmd。
async function probe(page) {
  await page.locator("#s-cp-addr").fill(`127.0.0.1:${env.cpPort}`);
  await page.locator("#s-cp-probe").click();
  const fpBox = page.locator(".field", { hasText: "证书指纹" }).locator(".cmd");
  await expect(fpBox).not.toBeEmpty();
  return fpBox;
}

// a. 全程:指纹与控制面一致 → 核对 → 接入 → 进主界面,控制面落在临时配置目录。
test("a. 接入成功:指纹一致,进主界面,配置写入 https 地址与指纹", async ({ page }) => {
  const u = await openConnectForm(page);
  const fpBox = await probe(page);
  await expect(fpBox).toHaveText(env.fp);
  await expect(page.locator("#s-cp-ok")).toBeVisible();
  await page.locator("#s-cp-ok").check();
  await page.locator("#s-cp-name").fill("e2e-box");
  await page.locator("#s-cp-pw").fill(env.pw);
  await page.locator("#s-cp-go").click();

  // 接入成功后整页刷新,setup 遮罩拆掉,出现侧栏(正常模式的主界面)。
  await expect(page.locator("aside.side")).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".setup")).toHaveCount(0);

  // 控制面:https 地址 + 指纹(token 在,值不断言)。
  const cfg = JSON.parse(readFileSync(path.join(u.dir, "mpx", "config.json"), "utf8"));
  const p = cfg.profiles[cfg.current || "default"];
  expect(p.server).toBe(`https://127.0.0.1:${env.cpPort}`);
  expect(p.fingerprint).toBe(env.fp);
  expect(p.token || "").not.toBe("");
});

// b. 设备密码错:显示后端原话,仍停在接入表单。
test("b. 设备密码错误:提示原话,停在表单", async ({ page }) => {
  await openConnectForm(page);
  await probe(page);
  await page.locator("#s-cp-ok").check();
  await page.locator("#s-cp-name").fill("e2e-box");
  await page.locator("#s-cp-pw").fill("Wrong-Password-1");
  await page.locator("#s-cp-go").click();
  await expect(page.locator(".callout.bad")).toContainText("接入失败");
  // .raw 是后端原话(控制面 401 的那句),不是前端编的。
  await expect(page.locator(".callout.bad .raw")).toContainText("设备密码不对");
  await expect(page.locator("#s-cp-addr")).toBeVisible();
  await expect(page.locator("aside.side")).toHaveCount(0);
});

// c. 没勾「指纹核对无误」时接入按钮不可用;勾上(并填了密码)才可用。
test("c. 未确认指纹时接入不可点,确认后可用", async ({ page }) => {
  await openConnectForm(page);
  await probe(page);
  await page.locator("#s-cp-pw").fill(env.pw);
  const go = page.locator("#s-cp-go");
  await expect(go).toBeVisible();
  await expect(go).toBeDisabled();
  await page.locator("#s-cp-ok").check();
  await expect(go).toBeEnabled();
});
