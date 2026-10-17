// 设置页「设备」里的设备密码一节:改密码(PUT /api/device-password)与关闭接入
// (DELETE /api/device-password)。前端校验拦在前端;真改完之后从 Node 直接打
// 控制面的 HTTPS 管理口(POST /api/connect)验证:新密码能换 token,旧密码不行。
import { test, expect } from "@playwright/test";
import { startEnv, login, httpsJSON } from "./env.mjs";

test.describe.configure({ mode: "serial" });

const OLDPW = "Old-Device-Pw-1122";
const NEWPW = "New-Device-Pw-7788";

let env;
const cpPort = () => +new URL(env.cpURL).port;

test.beforeAll(async () => {
  env = await startEnv();
  // 起点:控制面上已有一个设备密码,页面这次把它改掉。
  const r = await fetch(env.cpURL + "/api/device-password", {
    method: "PUT", headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json" },
    body: JSON.stringify({ password: OLDPW }),
  });
  expect(r.ok).toBeTruthy();
});
test.afterAll(async () => { await env?.stop(); });

// 设备页里设备密码表单的 PUT/DELETE,从记录转发里挑出来。
const dpwWrites = () => env.takeLog().filter(r => r.path === "/api/device-password");

test("校验:两次不一致、少于 12 位都拦在前端,不发请求", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#settings/devices");
  await expect(page.locator("#dpw-form")).toBeVisible();

  // 两次输入不一致。
  await page.fill("#dpw1", NEWPW);
  await page.fill("#dpw2", "New-Device-Pw-9999");
  await page.click("#dpw-save");
  await expect(page.locator("#dpw-err")).toContainText("两次输入不一致");

  // 少于 12 位(两次一致也过不了)。
  await page.fill("#dpw1", "short1234");
  await page.fill("#dpw2", "short1234");
  await page.click("#dpw-save");
  await expect(page.locator("#dpw-err")).toContainText("设备密码至少 12 个字符");

  expect(dpwWrites()).toEqual([]);
});

test("改密码并勾选吊销:请求体是 {password, revoke_all:true},出成功提示", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#settings/devices");
  await expect(page.locator("#dpw-form")).toBeVisible();
  await page.fill("#dpw1", NEWPW);
  await page.fill("#dpw2", NEWPW);
  await page.locator("#dpw-revoke").check();
  await page.click("#dpw-save");
  await expect(page.locator(".toast")).toContainText("设备密码已更新");

  const writes = dpwWrites();
  expect(writes.map(r => r.method)).toEqual(["PUT"]);
  expect(JSON.parse(writes[0].body)).toEqual({ password: NEWPW, revoke_all: true });
});

test("改完之后:新密码能在控制面换到 token,旧密码不行", async () => {
  // POST /api/connect 只在 HTTPS(或回环)上受理;自签证书跳过 CA 校验即可。
  const ok = await httpsJSON(cpPort(), "POST", "/api/connect", { password: NEWPW, device_name: "node-check" },
    { "Content-Type": "application/json" });
  expect(ok.status).toBe(201);
  expect(ok.json.token || "").not.toBe("");

  const bad = await httpsJSON(cpPort(), "POST", "/api/connect", { password: OLDPW, device_name: "node-check" },
    { "Content-Type": "application/json" });
  expect(bad.status).toBe(401);
  expect(bad.json.error).toContain("设备密码不对");
});

test("关闭设备密码接入:二次确认,确认后才发 DELETE", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#settings/devices");
  await page.click("[data-act=dpw-off]");
  await expect(page.locator("#modal.open")).toContainText("关闭设备密码接入");
  // 取消不发请求。
  await page.click("[data-act=modal-cancel]");
  expect(dpwWrites()).toEqual([]);
  // 确认后发出 DELETE,出成功提示。
  await page.click("[data-act=dpw-off]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已关闭设备密码接入");
  const writes = dpwWrites();
  expect(writes.map(r => r.method)).toEqual(["DELETE"]);
});
