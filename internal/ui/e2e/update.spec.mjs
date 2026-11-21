// 顶部「更新」pill 与一键更新弹窗(UC-26 二层版):用 page.route 把
// /api/update、/api/update/nodes、/api/update/control 摆成假结论/假任务,
// 免得依赖真的新版本。覆盖:控制面+节点两层列表、节点 job 事件流走完后
// 写控制面升级、失败后原地停住不再轮询、「稍后」本会话不再显示、检查
// 按钮带 ?refresh=1 重新取结论。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const part = (over = {}) => ({ current: "v1.0.0", latest: "v1.1.0", behind: 1, available: true, release_url: "https://example.com/releases/v1.1.0", error: "", ...over });
const node = (over = {}) => ({ id: 1, name: "la-1", display: "", core: "xray", current: "26.3.27", target: "26.4.1", ...over });

// 把 GET /api/update 固定成一份假结论;返回 route 便于用例里再改。
async function mockUpdate(page, body) {
  await page.route("**/api/update", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }));
}
const available = {
  control: part(),
  nodes: [node(), node({ id: 2, name: "hk-2", core: "sing-box", current: "1.12.0", target: "1.12.0" })],
};

// 把 /api/update/nodes 接上假任务:POST 回 202;GET 按 since 过滤 events,
// state 用例里随时改。返回计数器。
async function mockUpdateNodes(page, events, state) {
  const calls = { posts: 0, gets: 0 };
  await page.route("**/api/update/nodes*", r => {
    if (r.request().method() === "POST") { calls.posts++; return r.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ job: 7 }) }); }
    calls.gets++;
    const since = +(new URL(r.request().url()).searchParams.get("since") || 0);
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ job: 7, state: state.v, seq: events.length, events: events.filter(e => e.seq > since) }) });
  });
  return calls;
}

test("弹窗列控制面与节点核心两层;稍后本会话不再显示", async ({ page }) => {
  await mockUpdate(page, available);
  await login(page, env);
  const pill = page.locator("#upd-pill");
  await expect(pill).toBeVisible();
  await pill.click();
  const modal = page.locator("#modal.open");
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("更新到");
  await expect(modal).toContainText("控制面");
  await expect(modal).toContainText("la-1");
  await expect(modal).toContainText("xray");
  await expect(modal).toContainText("26.3.27");
  await expect(modal).toContainText("26.4.1");
  await expect(modal).toContainText("已是最新"); // hk-2 已在目标版本上
  // 没有本机层、没有桌面预下载。
  await expect(modal).not.toContainText("本机程序");
  await expect(modal).not.toContainText("mpx update apply");
  await expect(page.locator("#upd-skip")).toBeVisible();
  await expect(page.locator("[data-act=update-apply]")).toHaveText("开始更新");
  // 「稍后」:关框、pill 消失,刷新后本会话仍不再出现。
  await page.click("#upd-skip");
  await expect(pill).toBeHidden();
  await page.reload();
  await expect(page.locator("#upd-pill")).toBeHidden();
});

test("更新跑通:节点 job 事件逐条出现,done 后写控制面升级并整页刷新", async ({ page }) => {
  await mockUpdate(page, available);
  const events = [
    { seq: 1, node: "la-1", core: "xray", from: "26.3.27", to: "26.4.1", status: "start", msg: "" },
    { seq: 2, node: "la-1", core: "xray", from: "26.3.27", to: "26.4.1", status: "ok", msg: "" },
  ];
  const state = { v: "running" };
  const calls = await mockUpdateNodes(page, events, state);
  const controls = [];
  await page.route("**/api/update/control", r => { controls.push(r.request().postData()); return r.fulfill({ status: 202, contentType: "application/json", body: "{}" }); });
  await login(page, env);
  await page.locator("#upd-pill").click();
  await page.click("[data-act=update-apply]");
  const modal = page.locator("#modal.open");
  await expect(modal).toContainText("正在更新");
  await expect(modal).toContainText("节点");
  await expect(modal).toContainText("正在升级");
  // 节点 done:事件落定,然后写控制面升级、进入重启等待。
  state.v = "done";
  await expect(modal).toContainText("完成", { timeout: 10000 });
  await expect.poll(() => controls.length).toBe(1);
  expect(controls[0]).toContain("upgrade");
  // 重启等待到整页刷新是秒级竞态,直接证刷新发生(即走过了 meta 轮询)。

  await page.waitForFunction(() => {
    const nav = performance.getEntriesByType("navigation");
    return nav.length > 0 && nav[0].type === "reload";
  }, undefined, { timeout: 20000 });
  expect(calls.posts).toBe(1);
  expect(calls.gets).toBeGreaterThan(1);
});

test("失败:停在失败消息与关闭按钮,不再轮询", async ({ page }) => {
  await mockUpdate(page, available);
  const events = [
    { seq: 1, node: "la-1", core: "xray", status: "failed", msg: "升级失败(下载超时)" },
  ];
  const state = { v: "failed" };
  const calls = await mockUpdateNodes(page, events, state);
  const controls = [];
  await page.route("**/api/update/control", r => { controls.push(1); return r.fulfill({ status: 202, json: {} }); });
  await login(page, env);
  await page.locator("#upd-pill").click();
  await page.click("[data-act=update-apply]");
  const modal = page.locator("#modal.open");
  await expect(modal).toContainText("升级失败(下载超时)");
  expect(controls).toHaveLength(0); // 节点侧失败不再动控制面
  // 停止轮询:有事件时下一拍本该 1.5 秒后就到。
  const n = calls.gets;
  await page.waitForTimeout(3500);
  expect(calls.gets).toBe(n);
  await modal.getByRole("button", { name: "关闭" }).click();
  await expect(page.locator("#modal.open")).toHaveCount(0);
});

test("无更新:控制面与节点都不落后时顶栏不出现 pill", async ({ page }) => {
  await mockUpdate(page, {
    control: part({ current: "v1.1.0", latest: "v1.1.0", available: false }),
    nodes: [node({ current: "26.4.1" }), node({ id: 2, name: "hk-2", core: "sing-box", current: "1.12.0", target: "1.12.0" })],
  });
  const updDone = page.waitForResponse(r => r.url().includes("/api/update") && !r.url().includes("/api/update/"), { timeout: 15000 });
  await login(page, env);
  await updDone;
  await expect(page.locator("#upd-pill")).toBeHidden();
});

test("弹窗「检查更新」重新取结论且带 refresh=1", async ({ page }) => {
  let body = { control: part({ current: "v1.1.0", latest: "v1.1.0", available: false }), nodes: [] };
  const refreshes = [];
  await page.route("**/api/update*", r => {
    if (new URL(r.request().url()).searchParams.has("refresh")) refreshes.push(1);
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });
  const updDone = page.waitForResponse(r => r.url().includes("/api/update") && !r.url().includes("/api/update/"), { timeout: 15000 });
  await login(page, env);
  await updDone;
  await page.goto(env.origin + "/#system/update");
  await page.click("#page [data-act=update-open]");
  const modal = page.locator("#modal.open");
  await expect(modal).toContainText("检查更新");
  // 服务端上新版,再点检查:按钮带 refresh=1,pill 随之出现。
  body = available;
  await page.click("[data-act=update-check]");
  await expect(modal).toContainText("更新到", { timeout: 10000 });
  await expect.poll(() => refreshes.length).toBe(1);
  await page.keyboard.press("Escape");
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await expect(page.locator("#upd-pill")).toBeVisible();
});

// 开发版(v0.0.0-…、dev)没有可比的发布号:顶栏不出现「更新」。
test("开发版:v0.0.0 与 dev 不显示「更新」入口", async ({ page }) => {
  for (const cur of ["v0.0.0-20261017-abcdef", "dev", "v0.0.0"]) {
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await mockUpdate(page, {
      control: part({ current: "dev", latest: "v0.9.7", available: true }),
      nodes: [node({ current: "26.4.1" }), node({ id: 2, name: "hk-2", current: "" })],
    });
    await page.route("**/api/admin/meta", async r => {
      const res = await r.fetch();
      await r.fulfill({ response: res, json: { ...(await res.json()), version: cur } });
    });
    const updDone = page.waitForResponse(r => r.url().includes("/api/update") && !r.url().includes("/api/update/"), { timeout: 15000 });
    await login(page, env);
    await updDone;
    await expect(page.locator("#upd-pill"), cur).toBeHidden();
  }
});
