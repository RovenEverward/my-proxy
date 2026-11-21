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
  // acme_* 字段在设置 JSON 里带着,但改它们走 /api/acme/config,不是设置 PUT —— 比较时剥掉。
  const reqs = normalize(env.takeLog(), [], { drop: ["bandwidth_test_url", "cdn_optimize", "acme_domain", "acme_cf_token_set", "acme_staging"] });
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
  expect(normalize(env.takeLog(), [], { drop: ["bandwidth_test_url", "cdn_optimize", "acme_domain", "acme_cf_token_set", "acme_staging"] })).toEqual(fromCLI);
  const after = await getSettings();
  expect(after).toEqual({ ...before, notify_days_left: 5 });
  expect(after.templates.xray).toBe(before.templates.xray);
  expect(after.rules[0]).toEqual({ pattern: "(?i)karing", result: "sing-box" });
});

test("识别规则：抽屉里增删排序只改草稿，页面统一保存", async ({ page }) => {
  await login(page, env);
  const before = await getSettings();
  await page.goto(env.origin + "/#settings/rules");
  await page.click("#sec-clients [data-act=s5-rules]");
  env.takeLog();
  // 内置规则占 data-rp 0..9;新的一条排在最后(「其他所有客户端」之前),编号接着往下数。
  const n = await page.locator("[data-rp]").count();
  await page.click("[data-act=rd-add]");
  // 新的一条的标识框里已经是 (?i),只填后面的文字。
  const added = page.locator(`[data-rp="${n}"]`);
  await expect(added).toBeFocused();
  await added.fill("^myclient/");
  await added.press("Tab");
  // 框里显示的是标识正文;(?i) 是内置的大小写开关,不跟着显示。
  await expect(page.locator(`[data-rp="${n}"]`)).toHaveValue("^myclient/");
  // 新的一条在最下面,「下移」不可点;往上挪一位,把被挤到下面的那条删掉,草稿里留下一处改动。
  await page.locator(`[data-act=rd-mv][data-i="${n}"][aria-label="上移"]`).click();
  await expect(page.locator(`[data-rp="${n - 1}"]`)).toHaveValue("^myclient/");
  await page.locator(`[data-act=rd-rm][data-i="${n}"]`).click();
  await expect(page.locator(`[data-rp="${n - 1}"]`)).toHaveValue("^myclient/");
  await page.click("[data-act=rd-done]");
  await expect(page.locator("#sb")).toContainText("识别规则");
  expect(env.takeLog()).toEqual([]);
  await page.click("[data-act=s5-save]");
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  const after = await getSettings();
  expect(after.rules).not.toEqual(before.rules);
  expect(after.templates).toEqual(before.templates);
  expect(after.webhook_url).toBe(before.webhook_url);
  await page.click("#sec-clients [data-act=s5-rules]");
  await page.click("[data-act=rd-reset]");
  await page.click("[data-act=rd-done]");
  // 恢复内置也要经过页面保存条，完成抽屉时还未写库。
  expect((await getSettings()).rules).toEqual(after.rules);
  // 上一次保存的 toast 可能还在,等这次 PUT 真的回来再读库。
  const put = page.waitForResponse(r => r.request().method() === "PUT" && /\/api\/settings$/.test(r.url()));
  await page.click("[data-act=s5-save]");
  expect((await put).status()).toBe(200);
  await expect(page.locator("#sb")).toBeEmpty();
  const builtin = await (await cp("/api/settings/builtin-rules")).json();
  expect((await getSettings()).rules).toEqual(builtin);
});

test("模板:set / del 与 CLI 一致(正文原样发出)", async ({ page }) => {
  await login(page, env);
  const builtin = await (await cp("/api/settings/templates/sing-box")).text();
  const body = builtin.replace(/\s+$/, "") + "\n\n"; // 有改动才能保存
  env.takeLog();
  await env.cli("settings", "template", "set", "sing-box", "-f", file("sb.json", body));
  const setCLI = normalize(env.takeLog(), [], { drop: ["bandwidth_test_url", "cdn_optimize"] });
  await env.cli("settings", "template", "del", "sing-box");
  const delCLI = normalize(env.takeLog(), [], { drop: ["bandwidth_test_url", "cdn_optimize"] });
  await page.goto(env.origin + "/#settings/templates");
  await expect(page.locator(".zr-tpl[data-f='sing-box']")).toContainText("内置");
  await page.click("button[data-act=tpl-open][data-f='sing-box']");
  const ta = page.locator("#tpl-ed textarea");
  await expect(ta).toHaveValue(builtin); // 原文,不经 JSON 重排
  await expect(page.locator("#tpl-save")).toBeDisabled();
  await ta.fill(body);
  await expect(page.locator("#tpl-save")).toBeEnabled();
  await page.click("#tpl-save");
  await expect(page.locator(".toast")).toContainText("已保存 sing-box 订阅底稿");
  expect(normalize(env.takeLog(), [], { drop: ["bandwidth_test_url", "cdn_optimize"] })).toEqual(setCLI);
  await expect(page.locator(".zr-tpl[data-f='sing-box']")).toContainText("自定义");
  await page.click("button[data-act=tpl-open][data-f='sing-box']");
  await page.click("button[data-act=tpl-del][data-f='sing-box']");
  await expect(page.locator("#okb")).toBeDisabled(); // 危险操作:要输入格式名
  await page.fill("#typed", "sing-box");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已恢复 sing-box 为内置订阅底稿");
  expect(normalize(env.takeLog(), [], { drop: ["bandwidth_test_url", "cdn_optimize"] })).toEqual(delCLI);
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
  expect(normalize(env.takeLog(), [], { drop: ["bandwidth_test_url", "cdn_optimize"] })).toEqual([]);
});

test("备份:点一下就下载归档,内容是 gzip", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#settings/backup");
  const [dl] = await Promise.all([page.waitForEvent("download"), page.click("[data-act=backup]")]);
  expect(dl.suggestedFilename()).toMatch(/\.tar\.gz$|\.tgz$|\.gz$/);
  const buf = readFileSync(await dl.path());
  expect([buf[0], buf[1]]).toEqual([0x1f, 0x8b]);
});
