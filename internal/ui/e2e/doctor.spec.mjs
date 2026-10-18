// 自检页:GET /api/doctor 的结果渲染成 mpx doctor 那张表(名称 / 结论 / 说明 + 汇总)。
// 自检会逐台节点现场查,所以进页面不许自动发请求;点了「开始自检」才发。接口报错时
// 要显示控制面的原话,而不是笼统的"失败了"。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env, driftNodeID;
test.beforeAll(async () => {
  env = await startEnv();
  // 漂移警告要跳到的节点:地址给 127.0.0.1:1(SSH 必被拒),用例里再把详情 mock 成可达。
  await env.seed("node", "add", "doc-drift", "--address", "127.0.0.1", "--ssh-port", "1");
  const r = await cpFetch(env.cpURL + "/api/nodes", { headers: { Authorization: "Bearer " + env.token } });
  driftNodeID = (await r.json()).find(n => n.name === "doc-drift").id;
});
test.afterAll(async () => { await env?.stop(); });

const REPORT = { checks: [
  { name: "数据库", status: "ok", detail: "能读能写" },
  { name: "节点 hk", status: "warn", detail: "配置和下发的不一致(节点上被改过)", hint: "到节点页点「下发配置」覆盖回去" },
  { name: "订阅 xray", status: "fail", detail: "渲染失败:模板第 3 行不是合法 JSON", hint: "改掉模板的语法错误" },
] };

test("进页面不自动跑,点了才查;三行与汇总都按报告渲染", async ({ page }) => {
  await login(page, env);
  const hits = [];
  // 第一次请求先扣住:等它的时候页面要说明这是同步的一次现场查、关掉就不查了。
  let release, held = false;
  await page.route("**/api/doctor", async r => {
    hits.push(r.request().method());
    if (!held) { held = true; await new Promise(ok => { release = ok; }); }
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(REPORT) });
  });
  await page.goto(env.origin + "/#doctor");
  await expect(page.locator("h1")).toHaveText("体检");
  await expect(page.locator(".ph p")).not.toContainText("mpx ");
  await expect(page.locator("[data-act=doc-run]")).toBeVisible();
  // 页面停一会儿也不许自己发:自检不是只读的便宜请求。
  await page.waitForTimeout(500);
  expect(hits).toEqual([]);
  await expect(page.locator("table tbody tr")).toHaveCount(0);

  await page.click("[data-act=doc-run]");
  await expect(page.locator(".callout")).toHaveText("正在体检：会逐台连上节点现场查一次，可能要几十秒。只读不改，关掉这个页面就不查了，回来再点一次就行。");
  release();
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
  // 失败恢复实心红点;警告仍是空心圈。
  await expect(rows.nth(2).locator(".dot")).toHaveClass(/bad/);
  // 顶部汇总:ok/warn/fail 各一项,只有失败数大于 0 时标红。
  await expect(page.locator("[data-sum=ok]")).toHaveText("1");
  await expect(page.locator("[data-sum=warn]")).toHaveText("1");
  await expect(page.locator("[data-sum=fail]")).toHaveText("1");
  await expect(page.locator("[data-sum=fail]")).toHaveClass(/c-bad/);
  for (const k of ["ok", "warn"]) await expect(page.locator(`[data-sum=${k}]`)).not.toHaveClass(/c-(ok|warn|bad)/);
});

// 体检页一直是同步的一次请求,不摆进度条、也不去问后台任务:等结果时只说「正在体检」。
test("体检页是同步的一次现场查:不摆进度条,也不发后台任务", async ({ page }) => {
  await login(page, env);
  const jobs = [];
  page.on("request", r => { if (/\/jobs(\?|$)/.test(new URL(r.url()).pathname)) jobs.push(r.url()); });
  let release, held = false;
  await page.route("**/api/doctor", async r => {
    if (!held) { held = true; await new Promise(ok => { release = ok; }); }
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ checks: [{ name: "数据库", status: "ok", detail: "能读能写" }] }) });
  });
  await page.goto(env.origin + "/#doctor");
  await page.click("[data-act=doc-run]");
  await expect(page.locator("main [role=progressbar], main progress")).toHaveCount(0);
  await expect(page.locator(".callout")).toContainText("正在体检");
  release();
  await expect(page.locator("table tbody tr")).toHaveCount(1);
  expect(jobs).toEqual([]);
});

test("报告里没有失败项时,汇总的失败是 0 且不上红", async ({ page }) => {
  await login(page, env);
  await page.route("**/api/doctor", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ checks: [
    { name: "数据库", status: "ok", detail: "能读能写" },
    { name: "节点 hk", status: "warn", detail: "配置和下发的不一致" },
  ] }) }));
  await page.goto(env.origin + "/#doctor");
  await page.click("[data-act=doc-run]");
  await expect(page.locator("[data-sum=fail]")).toHaveText("0");
  await expect(page.locator("[data-sum=fail]")).not.toHaveClass(/c-bad/);
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

// 漂移警告行:文案换成界面说法,后面跟「应用到<节点>」;点一下跳到节点详情并弹出
// 「应用到节点」的确认弹窗(用户确认才下发),取消后不再弹第二次。没有 ui_action
// 的普通警告行不带这个按钮。
test("漂移警告:一键跳到节点详情并弹出应用确认", async ({ page }) => {
  await login(page, env);
  await page.route("**/api/doctor", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ checks: [
    { name: "节点 doc-drift", status: "warn", detail: "配置和下发的不一致(节点上被改过)", ui_hint: "可能是你自己改的,也可能是别人改的;确认后到「节点」页打开 doc-drift,点「下发配置」覆盖回去", ui_action: "apply", node_id: driftNodeID },
    { name: "节点 plain", status: "warn", detail: "配置和下发的不一致(节点上被改过)" },
  ] }) }));
  // 详情要有一个可点的「应用到节点」:可达,且观测哈希与下发的对不上。
  await page.route(`**/api/nodes/${driftNodeID}`, async route => {
    const response = await route.fetch();
    const n = await response.json();
    n.deployed_hash = "deployed";
    n.status = { ...(n.status || {}), node_id: driftNodeID, checked_at: new Date().toISOString(), reachable: true, error: "", observed_hash: "changed" };
    await route.fulfill({ response, json: n });
  });
  await page.goto(env.origin + "/#doctor");
  await page.click("[data-act=doc-run]");
  const row = page.locator("table tbody tr").first();
  await expect(row).toContainText("节点上的配置和控制面不一致");
  await expect(row).not.toContainText("配置和下发的不一致");
  await expect(row).not.toContainText("建议：");
  const btn = row.locator("[data-act=doc-apply]");
  await expect(btn).toHaveText("应用到doc-drift");
  // 没有 ui_action 的行只是普通警告,没有行内按钮,原文案也不变。
  const plainRow = page.locator("table tbody tr").nth(1);
  await expect(plainRow).toContainText("配置和下发的不一致");
  await expect(plainRow.locator("[data-act=doc-apply]")).toHaveCount(0);
  await btn.click();
  await expect(page).toHaveURL(new RegExp("#nodes/" + driftNodeID + "$"));
  const modal = page.locator("#modal.open");
  await expect(modal).toContainText("把配置应用到");
  // 从体检过来的应用确认要带上「可能是你自己改的」这句提醒(建议行被省掉了)。
  await expect(modal).toContainText("可能是你自己改的，也可能是别人改的");
  // 取消:确认弹窗关掉,主动触发一次重绘(体检页 30 秒才轮询一次)也不再自己冒出来。
  await page.click("#modal [data-act=modal-cancel]");
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await page.evaluate(() => load(true, true));
  await expect(page.locator("#modal.open")).toHaveCount(0);
});

// 节点连不上时点体检的「应用到<节点>」:详情里没有可点的下发按钮,应该给出下一步
// 并聚焦「刷新状态」,而不是静默什么都不做。
test("漂移行一键应用:节点连不上时提示并聚焦刷新", async ({ page }) => {
  await login(page, env);
  await page.route("**/api/doctor", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ checks: [
    { name: "节点 doc-drift", status: "warn", detail: "配置和下发的不一致(节点上被改过)", ui_action: "apply", node_id: driftNodeID },
  ] }) }));
  await page.route(`**/api/nodes/${driftNodeID}`, async route => {
    const response = await route.fetch();
    const n = await response.json();
    n.deployed_hash = "deployed";
    n.status = { ...(n.status || {}), node_id: driftNodeID, checked_at: new Date().toISOString(), reachable: false, error: "dial tcp: i/o timeout" };
    await route.fulfill({ response, json: n });
  });
  await page.goto(env.origin + "/#doctor");
  await page.click("[data-act=doc-run]");
  await page.locator("table tbody tr").first().locator("[data-act=doc-apply]").click();
  await expect(page).toHaveURL(new RegExp("#nodes/" + driftNodeID + "$"));
  await expect(page.locator(".toast")).toContainText("先点「刷新状态」");
  await expect(page.locator("#modal.open")).toHaveCount(0);
  expect(await page.evaluate(() => document.activeElement?.dataset?.act)).toBe("node-check");
});

// 中途离开节点详情(哪怕标志刚写下)也要丢掉自动应用:LIST 页的 load 会清掉它,
// 再回详情页不许突然弹确认。
test("中途离开后自动应用标志不再弹确认", async ({ page }) => {
  await login(page, env);
  await page.route(`**/api/nodes/${driftNodeID}`, async route => {
    const response = await route.fetch();
    const n = await response.json();
    n.deployed_hash = "deployed";
    n.status = { ...(n.status || {}), node_id: driftNodeID, checked_at: new Date().toISOString(), reachable: true, error: "", observed_hash: "changed" };
    await route.fulfill({ response, json: n });
  });
  await page.goto(env.origin + "/#doctor");
  await page.evaluate(id => { window.AUTO_APPLY = { id, at: Date.now() }; location.hash = "#nodes"; }, driftNodeID);
  await expect(page.locator(".ph h1")).toHaveText("节点");
  await page.evaluate(id => { location.hash = "#nodes/" + id; }, driftNodeID);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  // mock 的详情是可点的:标志若没被丢掉,这里一定会弹。
  await expect(page.locator(".ph [data-act=node-sync]")).not.toHaveAttribute("aria-disabled", "true");
  await expect(page.locator("#modal.open")).toHaveCount(0);
});

// ui_action==="apply" 时 xray 的漂移一定是观测到的「不一致」;sing-box 那句两种
// 可能都有,只有 xray 不在场时才按不一致讲;两句同时出现时 sing-box 保持原句,
// 不把「没读到」说成「被改过」。
for (const [name, detail, wants, forbids] of [
  ["仅 sing-box 漂移", "sing-box 配置未读取或与下发内容不一致", ["节点上的 sing-box 配置和控制面不一致"], ["未读取或与下发内容不一致"]],
  ["xray 与 sing-box 同时出现", "配置和下发的不一致(节点上被改过);sing-box 配置未读取或与下发内容不一致", ["节点上的配置和控制面不一致", "sing-box 配置未读取或与下发内容不一致"], []],
]) {
  test(`漂移文案:${name}`, async ({ page }) => {
    await login(page, env);
    await page.route("**/api/doctor", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ checks: [
      { name: "节点 doc-drift", status: "warn", detail, ui_action: "apply", node_id: driftNodeID },
    ] }) }));
    await page.goto(env.origin + "/#doctor");
    await page.click("[data-act=doc-run]");
    const row = page.locator("table tbody tr").first();
    for (const w of wants) await expect(row).toContainText(w);
    await expect(row).not.toContainText("配置和下发的不一致");
    for (const f of forbids) await expect(row).not.toContainText(f);
    // 只留一个「应用到」按钮。
    await expect(row.locator("[data-act=doc-apply]")).toHaveCount(1);
  });
}

// 控制面的提示里还用旧叫法(入站、入口、下发、排水、巡检)时,界面换成新说法再显示。
test("控制面提示里的旧叫法换成界面用语", async ({ page }) => {
  await login(page, env);
  await page.route("**/api/doctor", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ checks: [
    { name: "订阅", status: "warn", detail: "还没有可发的节点", hint: "再加一个入站才会有订阅;入口、下发、排水、巡检" },
  ] }) }));
  await page.goto(env.origin + "/#doctor");
  await page.click("[data-act=doc-run]");
  const row = page.locator("main").getByText("再加一个连接方式才会有订阅");
  await expect(row).toBeVisible();
  await expect(row).toContainText("对外地址、应用到节点、暂停分配、体检");
  await expect(page.locator("main")).not.toContainText(/入站|入口|下发|排水|巡检/);
});
