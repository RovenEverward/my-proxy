// 设置页的写操作:和 mpx settings 对照;设置是整体替换,改一处
// 不能把别处(规则、模板、其他字段)丢掉。设置只有一份,所以 CLI 做完先把它恢复成原样,
// 再让页面做同一件事。
import { test, expect } from "@playwright/test";
import { writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { startEnv, normalize, login } from "./env.mjs";

let env, tpl;
// 直接问控制面(不经记录转发);它也吃限流,429 就等一下再来。
async function cp(p, init = {}) {
  for (;;) {
    const r = await fetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, ...(init.headers || {}) } });
    if (r.status !== 429) return r;
    await new Promise(res => setTimeout(res, 1000));
  }
}
const getSettings = async () => (await cp("/api/settings")).json();
const putSettings = s => cp("/api/settings", { method: "PUT", body: JSON.stringify(s), headers: { "Content-Type": "application/json" } });
const file = (name, text) => { const f = path.join(tmpdir(), `mpx-e2e-${process.pid}-${name}`); writeFileSync(f, text); return f; };

test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "hk", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("user", "add", "alice");
  // 起点:一条自定义规则、一份自定义模板、几项通知设置 —— 好看出「别处没丢」。
  await env.seed("settings", "rules", "add", "(?i)karing", "--result", "sing-box", "--index", "1");
  tpl = await (await cp("/api/settings/templates/xray")).text();
  await env.seed("settings", "template", "set", "xray", "-f", file("tpl.json", tpl));
  const s = await getSettings();
  await putSettings({ ...s, webhook_url: "https://hooks.example.net/mpx", notify_days_left: 3, notify_usage_percent: 80, profile_title: "{USERNAME}" });
});
test.afterAll(async () => { await env?.stop(); });

// CLI 做一次,记下请求,再把设置恢复成做之前的样子。
async function viaCLI(...args) {
  const before = await getSettings();
  env.takeLog();
  await env.cli(...args);
  const reqs = normalize(env.takeLog(), []);
  await putSettings(before);
  return { reqs, before };
}

test("通用设置:只改一项,其余字段、规则、模板都不丢;与 settings set 一致", async ({ page }) => {
  await login(page, env);
  const before = await getSettings();
  const edited = { ...JSON.parse(await env.cli("-o", "json", "settings", "show")), notify_days_left: 5 };
  const { reqs: fromCLI } = await viaCLI("settings", "set", "-f", file("s.json", JSON.stringify(edited)));
  // 通知在「系统」页(原设置 → 通用),只改这一项,订阅那几项和规则、底稿都要原样带回。
  await page.goto(env.origin + "/#system");
  await expect(page.locator("#s-sub_url_prefix")).toHaveCount(0);
  await page.fill("#s-notify_days_left", "5");
  await page.click("#s-save");
  await expect(page.locator(".toast")).toContainText("已保存通知设置");
  expect(normalize(env.takeLog(), [])).toEqual(fromCLI);
  const after = await getSettings();
  expect(after).toEqual({ ...before, notify_days_left: 5 });
  expect(after.templates.xray).toBe(before.templates.xray);
  expect(after.rules[0]).toEqual({ pattern: "(?i)karing", result: "sing-box" });
});

test("识别规则:add / move / rm / reset 与 CLI 一致,删除和恢复默认要确认", async ({ page }) => {
  await login(page, env);
  const add = await viaCLI("settings", "rules", "add", "(?i)hiddify", "--result", "clash-meta", "--index", "2");
  await page.goto(env.origin + "/#settings/rules");
  await page.click("[data-act=rule-add]");
  await page.fill("#r-pat", "(?i)hiddify");
  await page.selectOption("#r-res", "clash-meta");
  await page.fill("#r-idx", "2");
  await expect(page.locator("#modal.open")).not.toContainText("mpx ");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已加规则 2");
  expect(normalize(env.takeLog(), [])).toEqual(add.reqs);
  await putSettings(add.before);

  const move = await viaCLI("settings", "rules", "move", "1", "2");
  await page.reload();
  await page.click("[data-act=rule-move][data-i='0'][data-to='1']");
  await expect(page.locator(".toast")).toContainText("挪到第 2 位");
  expect(normalize(env.takeLog(), [])).toEqual(move.reqs);
  await putSettings(move.before);

  const rm = await viaCLI("settings", "rules", "rm", "1");
  await page.reload();
  await page.click("[data-act=rule-rm][data-i='0']");
  await page.click("[data-act=modal-cancel]");
  expect(env.takeLog()).toEqual([]);
  await page.click("[data-act=rule-rm][data-i='0']");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已删掉第 1 条");
  expect(normalize(env.takeLog(), [])).toEqual(rm.reqs);
  await putSettings(rm.before);

  const reset = await viaCLI("settings", "rules", "reset");
  await page.reload();
  await page.click("[data-act=rule-reset]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已恢复内置默认规则");
  expect(normalize(env.takeLog(), [])).toEqual(reset.reqs);
  const after = await getSettings();
  expect(after.templates.xray).toBe(reset.before.templates.xray); // 模板没丢
  expect(after.webhook_url).toBe(reset.before.webhook_url);
});

test("模板:set / del 与 CLI 一致(正文原样发出)", async ({ page }) => {
  await login(page, env);
  const builtin = await (await cp("/api/settings/templates/sing-box")).text();
  const body = builtin.replace(/\s+$/, "") + "\n\n"; // 有改动才能保存
  env.takeLog();
  await env.cli("settings", "template", "set", "sing-box", "-f", file("sb.json", body));
  const setCLI = normalize(env.takeLog(), []);
  await env.cli("settings", "template", "del", "sing-box");
  const delCLI = normalize(env.takeLog(), []);
  await page.goto(env.origin + "/#settings/templates");
  await expect(page.locator("tr[data-f='sing-box']")).toContainText("内置默认");
  await page.click("button[data-act=tpl-open][data-f='sing-box']");
  const ta = page.locator("#tpl-ed textarea");
  await expect(ta).toHaveValue(builtin); // 原文,不经 JSON 重排
  await expect(page.locator("#tpl-save")).toBeDisabled();
  await ta.fill(body);
  await expect(page.locator("#tpl-save")).toBeEnabled();
  await page.click("#tpl-save");
  await expect(page.locator(".toast")).toContainText("已保存 sing-box 订阅底稿");
  expect(normalize(env.takeLog(), [])).toEqual(setCLI);
  await expect(page.locator("tr[data-f='sing-box']")).toContainText("自定义");
  await page.click("button[data-act=tpl-del][data-f='sing-box']");
  await expect(page.locator("#okb")).toBeDisabled(); // 危险操作:要输入格式名
  await page.fill("#typed", "sing-box");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已恢复 sing-box 为内置订阅底稿");
  expect(normalize(env.takeLog(), [])).toEqual(delCLI);
});

test("模板:保存前校验并定位到出错行,坏模板不发请求", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#settings/templates");
  await page.click("button[data-act=tpl-open][data-f='sing-box']");
  const ta = page.locator("#tpl-ed textarea");
  await expect(ta).not.toHaveValue("");
  env.takeLog();
  await ta.fill('{\n  "log": {},\n  "outbounds": [\n}\n');
  await expect(page.locator("#tpl-ok")).toContainText("有错误");
  await expect(page.locator("#tpl-save")).toBeDisabled();
  await page.locator("#tpl-form").evaluate(f => f.requestSubmit());
  await expect(page.locator("#tpl-err")).toContainText("第 4 行");
  await expect(page.locator(".ed-g span.bad")).toHaveText("4");
  await page.click("#tpl-revert");
  await expect(page.locator("#tpl-ok")).toContainText("语法正确");
  expect(normalize(env.takeLog(), [])).toEqual([]);
});

test("备份:点一下就下载归档,内容是 gzip", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#settings/backup");
  const [dl] = await Promise.all([page.waitForEvent("download"), page.click("[data-act=backup]")]);
  expect(dl.suggestedFilename()).toMatch(/\.tar\.gz$|\.tgz$|\.gz$/);
  const buf = readFileSync(await dl.path());
  expect([buf[0], buf[1]]).toEqual([0x1f, 0x8b]);
});

test("设备:列出、吊销要确认、这台不给吊销", async ({ page }) => {
  await login(page, env);
  const devices = [
    { id: 1, name: "this-box", current: true, last_ip: "127.0.0.1", created_at: "2026-10-01T00:00:00Z", last_used_at: "2026-10-16T00:00:00Z" },
    { id: 2, name: "old-laptop", current: false, last_ip: "", created_at: "2026-10-01T00:00:00Z", last_used_at: null },
  ];
  const deletes = [];
  await page.route("**/api/devices", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(devices) }));
  await page.route("**/api/devices/2", r => { deletes.push(r.request().method()); return r.fulfill({ status: 204, body: "" }); });
  await page.goto(env.origin + "/#settings/devices");
  const thisRow = page.locator(".row", { hasText: "this-box" });
  const oldRow = page.locator(".row", { hasText: "old-laptop" });
  await expect(thisRow).toContainText("这台");
  await expect(oldRow.locator("[data-act=dev-revoke]")).toHaveCount(1);
  await expect(thisRow.locator("[data-act=dev-revoke]")).toHaveCount(0);
  // 吊销要走确认框,取消不发请求。
  await oldRow.locator("[data-act=dev-revoke]").click();
  await expect(page.locator("#modal.open")).toContainText("吊销设备 old-laptop?");
  await page.click("[data-act=modal-cancel]");
  expect(deletes).toEqual([]);
  await oldRow.locator("[data-act=dev-revoke]").click();
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已吊销设备 old-laptop");
  expect(deletes).toEqual(["DELETE"]);
});
