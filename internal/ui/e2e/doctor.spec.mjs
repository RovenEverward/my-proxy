// 自检页:GET /api/doctor 的结果渲染成 mpx doctor 那张表(名称 / 结论 / 说明 + 汇总)。
// 自检会逐台节点现场查,所以进页面不许自动发请求;点了「开始自检」才发。接口报错时
// 要显示控制面的原话,而不是笼统的"失败了"。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const REPORT = { checks: [
  { name: "数据库", status: "ok", detail: "能读能写" },
  { name: "节点 hk", status: "warn", detail: "配置和下发的不一致(节点上被改过)", hint: "到节点页点「下发配置」覆盖回去" },
  { name: "订阅 xray", status: "fail", detail: "渲染失败:模板第 3 行不是合法 JSON", hint: "改掉模板的语法错误" },
] };

test("进页面不自动跑,点了才查;三行与汇总都按报告渲染", async ({ page }) => {
  await login(page, env);
  const hits = [];
  await page.route("**/api/doctor", r => { hits.push(r.request().method()); return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(REPORT) }); });
  await page.goto(env.origin + "/#doctor");
  await expect(page.locator("h1")).toHaveText("体检");
  await expect(page.locator(".ph p")).not.toContainText("mpx ");
  await expect(page.locator("[data-act=doc-run]")).toBeVisible();
  // 页面停一会儿也不许自己发:自检不是只读的便宜请求。
  await page.waitForTimeout(500);
  expect(hits).toEqual([]);
  await expect(page.locator("table tbody tr")).toHaveCount(0);

  await page.click("[data-act=doc-run]");
  await expect(page.locator("table tbody tr")).toHaveCount(3);
  expect(hits).toEqual(["GET"]);
  // 每行的名称、结论、说明(带 hint)都在,且顺序与报告一致。
  const rows = page.locator("table tbody tr");
  await expect(rows.nth(0)).toContainText("数据库");
  await expect(rows.nth(0).locator(".st")).toHaveText("正常");
  await expect(rows.nth(0)).toContainText("能读能写");
  await expect(rows.nth(1)).toContainText("节点 hk");
  await expect(rows.nth(1).locator(".st")).toHaveText("警告");
  await expect(rows.nth(1).locator(".dot")).toHaveClass(/warn/);
  await expect(rows.nth(1)).toContainText("配置和下发的不一致");
  await expect(rows.nth(1)).toContainText("到节点页点「应用到节点」覆盖回去");
  await expect(rows.nth(2)).toContainText("订阅 xray");
  await expect(rows.nth(2).locator(".st")).toHaveText("失败");
  await expect(rows.nth(2).locator(".dot")).toHaveClass(/bad/);
  // 顶部汇总:ok/warn/fail 各一项。颜色沿用状态点(绿/黄/红)。
  await expect(page.locator("[data-sum=ok]")).toHaveText("1");
  await expect(page.locator("[data-sum=warn]")).toHaveText("1");
  await expect(page.locator("[data-sum=fail]")).toHaveText("1");
  await expect(page.locator("[data-sum=ok]")).toHaveClass(/c-ok/);
  await expect(page.locator("[data-sum=warn]")).toHaveClass(/c-warn/);
  await expect(page.locator("[data-sum=fail]")).toHaveClass(/c-bad/);
});

test("接口报错时把控制面的原话显示出来", async ({ page }) => {
  await login(page, env);
  await page.route("**/api/doctor", r => r.fulfill({ status: 502, contentType: "application/json", body: JSON.stringify({ error: "连不上控制面 https://cp.example.net: dial tcp: 超时" }) }));
  await page.goto(env.origin + "/#doctor");
  await page.click("[data-act=doc-run]");
  await expect(page.locator("[data-doc-err]")).toHaveText("连不上控制面 https://cp.example.net: dial tcp: 超时");
  await expect(page.locator("table tbody tr")).toHaveCount(0);
});

// 控制面同时给了 hint(命令行用,带命令)和 ui_hint(界面说法)时,页面只显示 ui_hint;
// 接口报错带 ui_hint 时,报错卡片也用它。
test("优先显示 ui_hint,不把命令摆进窗口", async ({ page }) => {
  await login(page, env);
  await page.route("**/api/doctor", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ checks: [
    { name: "节点 hk", status: "warn", detail: "配置和下发的不一致", hint: "确认后 mpx node sync hk 覆盖回去", ui_hint: "确认后到「节点」页打开 hk,点「下发配置」覆盖回去" },
  ] }) }));
  await page.goto(env.origin + "/#doctor");
  await page.click("[data-act=doc-run]");
  const row = page.locator("table tbody tr").first();
  await expect(row).toContainText("点「应用到节点」覆盖回去");
  await expect(row).not.toContainText("mpx ");
  await page.unroute("**/api/doctor");
  await page.route("**/api/doctor", r => r.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "token 不正确或这台设备已被吊销;重新 mpx connect", ui_hint: "这台设备的登录已失效或被吊销:请在 MPX 里重新「接入已有控制面」" }) }));
  await page.click("[data-act=doc-run]");
  await expect(page.locator("#content, main, body").first()).toContainText("重新「接入已有控制面」");
  await expect(page.locator("body")).not.toContainText("重新 mpx connect");
});
