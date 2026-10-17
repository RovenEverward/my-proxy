// 顶部「更新」pill 与一键更新弹窗:用 page.route 把 /local/update 与
// /local/update/all 摆成假结论 / 假进度,免得依赖真的新版本。覆盖:三层列表与
// xray 版本、成功走到重启并整页刷新、失败停在失败消息、隐藏时不轮询。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const part = (over = {}) => ({ current: "v1.0.0", latest: "v1.1.0", behind: 1, available: true, release_url: "https://example.com/releases/v1.1.0", error: "", ...over });
const node = (over = {}) => ({ id: 1, name: "la-1", display: "", core: "xray", current: "26.3.27", target: "26.4.1", ...over });

// 把 /local/update 固定成一份假结论;返回 route 便于用例里再改。
async function mockUpdate(page, body) {
  await page.route("**/local/update", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }));
}
const available = {
  cli: part(), control: part({ current: "v1.0.0" }),
  nodes: [node(), node({ id: 2, name: "hk-2", core: "sing-box", current: "1.12.0", target: "1.12.0" })],
  notes: "修了几个小问题",
};

// 把 /local/update/all 接上假任务:POST 回 202;GET 按 since 过滤 events,
// state 用例里随时改。返回计数器。
async function mockUpdateAll(page, events, state) {
  const calls = { posts: 0, gets: 0 };
  await page.route("**/local/update/all*", r => {
    if (r.request().method() === "POST") { calls.posts++; return r.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ ok: true }) }); }
    calls.gets++;
    const since = +(new URL(r.request().url()).searchParams.get("since") || 0);
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ state: state.v, step: "", seq: events.length, events: events.filter(e => e.seq > since) }) });
  });
  return calls;
}

test("弹窗列出本机、控制面与节点核心三层;稍后本会话不再显示", async ({ page }) => {
  await mockUpdate(page, available);
  await login(page, env);
  const pill = page.locator("#upd-pill");
  await expect(pill).toBeVisible();
  await pill.click();
  const modal = page.locator("#modal.open");
  await expect(modal).toBeVisible();
  await expect(modal).toContainText("更新到");
  await expect(modal).toContainText("本机 CLI");
  await expect(modal).toContainText("控制面");
  await expect(modal).toContainText("la-1");
  await expect(modal).toContainText("xray");
  await expect(modal).toContainText("26.3.27");
  await expect(modal).toContainText("26.4.1");
  await expect(modal).toContainText("已是推荐版本"); // hk-2 在推荐版本上
  await expect(modal).toContainText("将依次升级控制面、节点、本机");
  await expect(modal).not.toContainText("mpx update apply");
  await expect(page.locator("[data-act=update-skip]")).toBeVisible();
  await expect(page.locator("[data-act=update-apply]")).toBeVisible();
  await expect(page.locator(".upd-notes")).toContainText("修了几个小问题");
  // 「稍后」:关框、pill 消失,刷新后本会话仍不再出现。
  await page.click("[data-act=update-skip]");
  await expect(pill).toBeHidden();
  await page.reload();
  await expect(page.locator("#upd-pill")).toBeHidden();
});

test("无更新:三层都不落后时顶栏不出现 pill", async ({ page }) => {
  await mockUpdate(page, {
    cli: part({ current: "v1.1.0", latest: "v1.1.0", available: false }),
    control: part({ current: "v1.1.0", latest: "v1.1.0", available: false }),
    nodes: [node({ current: "26.4.1" })],
    notes: "",
  });
  await login(page, env);
  await expect(page.locator("#upd-pill")).toBeHidden();
});

test("只有节点核心落后:也出现 pill,弹窗列节点行", async ({ page }) => {
  await mockUpdate(page, {
    cli: part({ current: "v1.1.0", latest: "v1.1.0", available: false }),
    control: part({ current: "v1.1.0", latest: "v1.1.0", available: false }),
    nodes: [node()],
    notes: "",
  });
  await login(page, env);
  await expect(page.locator("#upd-pill")).toBeVisible();
  await page.locator("#upd-pill").click();
  await expect(page.locator("#modal")).toContainText("la-1");
  await expect(page.locator("#modal")).toContainText("26.3.27");
  await expect(page.locator("#modal")).toContainText("已是最新"); // 本机 / 控制面两行
});

test("重启以更新:进度按层分组,走到重启后整页刷新", async ({ page }) => {
  await mockUpdate(page, available);
  const events = [
    { seq: 1, step: "control", status: "ok", msg: "控制面 v1.0.0 → v1.1.0" },
    { seq: 2, step: "nodes", status: "ok", msg: "la-1 xray 26.3.27 → 26.4.1 ok" },
    { seq: 3, step: "cli", status: "ok", msg: "本机 CLI v1.0.0 → v1.1.0" },
    { seq: 4, step: "restart", status: "start", msg: "重启 mpx ui 启用新版本" },
  ];
  const state = { v: "running" };
  const calls = await mockUpdateAll(page, events, state);
  await login(page, env);
  await page.locator("#upd-pill").click();
  await page.click("[data-act=update-apply]");
  await expect(page.locator("#modal")).toContainText("正在更新");
  await expect(page.locator("#modal")).toContainText("控制面 v1.0.0 → v1.1.0");
  await expect(page.locator("#modal")).toContainText("la-1 xray 26.3.27 → 26.4.1");
  await expect(page.locator(".upd-step").first()).toHaveText("控制面");
  await expect(page.locator(".upd-step").nth(1)).toHaveText("节点");
  // 转入 restarting:显示重启中,/ui/meta 问通(测试里 mpx ui 活着,第一次就通)后整页刷新。
  state.v = "restarting";
  await expect(page.locator("#modal")).toContainText("mpx ui 正在重启");
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
    { seq: 1, step: "control", status: "ok", msg: "控制面 v1.0.0 → v1.1.0" },
    { seq: 2, step: "nodes", status: "failed", msg: "la-1 xray 26.3.27 → 26.4.1 failed: 下载超时" },
  ];
  const state = { v: "failed" };
  const calls = await mockUpdateAll(page, events, state);
  await login(page, env);
  await page.locator("#upd-pill").click();
  await page.click("[data-act=update-apply]");
  await expect(page.locator("#modal")).toContainText("更新失败");
  await expect(page.locator("#modal")).toContainText("下载超时");
  await expect(page.locator("#modal [data-act=modal-cancel]")).toHaveText("关闭");
  // 停止轮询:有事件时下一拍本该 1 秒后就到。
  const n = calls.gets;
  await page.waitForTimeout(3000);
  expect(calls.gets).toBe(n);
  await page.click("#modal [data-act=modal-cancel]");
  await expect(page.locator("#modal.open")).toHaveCount(0);
});

test("页面隐藏时不发轮询,回到可见立刻恢复", async ({ page }) => {
  await mockUpdate(page, available);
  const state = { v: "running" };
  const calls = await mockUpdateAll(page, [], state);
  await login(page, env);
  await page.locator("#upd-pill").click();
  await page.click("[data-act=update-apply]");
  // 第一拍发过了;把页面置成 hidden(和 wizard-b 一样在页内改 document.hidden,
  // CDP 的 frozen 态在无头 Chromium 里不会让 document.hidden 变真),下一拍不该再发。
  await expect(page.locator("#modal")).toContainText("正在更新");
  await expect.poll(() => calls.gets).toBe(1);
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await page.waitForTimeout(3000);
  expect(calls.gets).toBe(1);
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => calls.gets, { timeout: 5000 }).toBeGreaterThan(1);
});
