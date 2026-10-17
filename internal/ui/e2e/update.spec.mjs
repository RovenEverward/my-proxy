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
  await expect(modal).toContainText("本机程序");
  await expect(modal).toContainText("控制面");
  await expect(modal).toContainText("la-1");
  await expect(modal).toContainText("xray");
  await expect(modal).toContainText("26.3.27");
  await expect(modal).toContainText("26.4.1");
  await expect(modal).toContainText("已是推荐版本"); // hk-2 在推荐版本上
  await expect(modal).toContainText("将依次升级控制面、节点、本机程序");
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

test("更新弹窗只列这一版的要点:下载表、安装说明、文件清单都不出现", async ({ page }) => {
  const notes = [
    "## 更新要点",
    "",
    "- 安装向导不再问 **节点名**",
    "- 总览的用时改成 `秒`",
    "- <script>alert(1)</script>",
    "",
    "### 修复",
    "",
    "- 这一条是详细说明,不该出现",
    "",
    "## 我该下哪个",
    "",
    "| 你的电脑 | 下载 | 怎么装 |",
    "| --- | --- | --- |",
    "| Windows | [MPX.exe](https://example.com/a.exe) | 双击 |",
    "",
    "**第一次打开被系统拦住?** 点「仍要运行」。",
    "",
    "<details>",
    "<summary>其他文件</summary>",
    "",
    "- `mpx_linux_amd64.tar.gz`",
    "",
    "</details>",
  ].join("\n");
  await mockUpdate(page, { ...available, notes });
  await login(page, env);
  await page.locator("#upd-pill").click();
  const box = page.locator(".upd-notes");
  await expect(box.locator("li")).toHaveCount(3);
  await expect(box.locator("li").first()).toContainText("安装向导不再问");
  await expect(box.locator("b")).toHaveText("节点名");
  await expect(box.locator("code")).toHaveText("秒");
  await expect(box.locator("script")).toHaveCount(0);
  await expect(box).toContainText("<script>alert(1)</script>");
  for (const bad of ["| ---", "我该下哪个", "仍要运行", "mpx_linux", "详细说明", "<details>"]) await expect(box).not.toContainText(bad);
  await expect(page.locator("#modal a.u-link")).toHaveText("查看发布说明");
});

test("老版本的发布说明(没有「更新要点」):只取正文里的列表项", async ({ page }) => {
  const notes = ["## 我该下哪个", "", "| a | b |", "| --- | --- |", "", "<details>", "<summary>x</summary>", "", "</details>", "", "### 修复", "", "- 第一项", "- 第二项", "", "## 下载", "", "- 不该出现"].join("\n");
  await mockUpdate(page, { ...available, notes });
  await login(page, env);
  await page.locator("#upd-pill").click();
  const box = page.locator(".upd-notes");
  await expect(box.locator("li")).toHaveText(["第一项", "第二项"]);
  await expect(box).not.toContainText("| ---");
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
    { seq: 2, step: "nodes", status: "ok", msg: "la-1 xray:26.3.27 → 26.4.1 完成" },
    { seq: 3, step: "cli", status: "ok", msg: "本机程序 v1.0.0 → v1.1.0" },
    { seq: 4, step: "restart", status: "start", msg: "重启本机界面启用新版本" },
  ];
  const state = { v: "running" };
  const calls = await mockUpdateAll(page, events, state);
  await login(page, env);
  await page.locator("#upd-pill").click();
  await page.click("[data-act=update-apply]");
  await expect(page.locator("#modal")).toContainText("正在更新");
  await expect(page.locator("#modal")).toContainText("控制面 v1.0.0 → v1.1.0");
  await expect(page.locator("#modal")).toContainText("la-1 xray:26.3.27 → 26.4.1 完成");
  await expect(page.locator(".upd-step").first()).toHaveText("控制面");
  await expect(page.locator(".upd-step").nth(1)).toHaveText("节点");
  // 转入 restarting:显示重启中,/ui/meta 问通(测试里本机界面活着,第一次就通)后整页刷新。
  state.v = "restarting";
  await expect(page.locator("#modal")).toContainText("本机界面 正在重启");
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
    { seq: 2, step: "nodes", status: "failed", msg: "la-1 xray:升级失败(下载超时)" },
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

// 桌面端后台预下载:prefetch.state 决定主按钮的文字与可点性。三种状态都用
// page.route 改 /local/update 的响应,不需要真的新版本。
const withPrefetch = pf => ({ ...available, desktop: true, prefetch: pf });

test("预下载中:按钮显示百分比且不可点,弹窗开着每 2 秒刷新", async ({ page }) => {
  let calls = 0;
  await page.route("**/local/update", r => {
    calls++;
    // 第一次 45%,之后 60%:证明弹窗真的在轮询。
    const bytes = calls === 1 ? 45 : 60;
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(withPrefetch({ version: "v1.1.0", state: "downloading", bytes, total: 100 })) });
  });
  await login(page, env);
  await page.locator("#upd-pill").click();
  const ok = page.locator("#upd-ok");
  await expect(ok).toHaveText("正在后台下载… 45%");
  await expect(ok).toBeDisabled();
  // 2 秒后自动刷新:进度变成 60%,按钮仍不可点。
  await expect(ok).toHaveText("正在后台下载… 60%", { timeout: 6000 });
  await expect(ok).toBeDisabled();
  expect(calls).toBeGreaterThan(1);
});

test("预下载中不知道总大小时不显示百分比", async ({ page }) => {
  await mockUpdate(page, withPrefetch({ version: "v1.1.0", state: "downloading", bytes: 0, total: 0 }));
  await login(page, env);
  await page.locator("#upd-pill").click();
  await expect(page.locator("#upd-ok")).toHaveText("正在后台下载…");
  await expect(page.locator("#upd-ok")).toBeDisabled();
});

test("下载失败:提示稍后自动重试,小链接立即重试会发 POST", async ({ page }) => {
  const state = { retried: 0 };
  await mockUpdate(page, withPrefetch({ version: "v1.1.0", state: "failed", bytes: 0, total: 100, error: "网络断了" }));
  await page.route("**/local/update/prefetch", r => {
    state.retried++;
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: true }) });
  });
  // 点「立即重试」之后后台回到下载中,轮询会看到新状态。
  await page.route("**/local/update", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(withPrefetch(state.retried ? { version: "v1.1.0", state: "downloading", bytes: 10, total: 100 } : { version: "v1.1.0", state: "failed", bytes: 0, total: 100, error: "网络断了" })) }));
  await login(page, env);
  await page.locator("#upd-pill").click();
  const modal = page.locator("#modal.open");
  await expect(modal).toContainText("下载失败，稍后自动重试");
  const retry = page.locator("#upd-retry");
  await expect(retry).toHaveText("立即重试");
  await expect(page.locator("#upd-ok")).toBeDisabled();
  await retry.click();
  await expect.poll(() => state.retried).toBe(1);
  await expect(page.locator("#upd-ok")).toHaveText("正在后台下载… 10%", { timeout: 6000 });
});

test("预下载就绪:按钮恢复为可点的「重启以更新」", async ({ page }) => {
  await mockUpdate(page, withPrefetch({ version: "v1.1.0", state: "ready", bytes: 100, total: 100 }));
  await login(page, env);
  await page.locator("#upd-pill").click();
  const ok = page.locator("#upd-ok");
  await expect(ok).toHaveText("重启以更新");
  await expect(ok).toBeEnabled();
});

test("没有 prefetch 字段:保持原来的按钮逻辑", async ({ page }) => {
  await mockUpdate(page, { ...available, desktop: true });
  await login(page, env);
  await page.locator("#upd-pill").click();
  await expect(page.locator("#upd-ok")).toHaveText("重启以更新");
  await expect(page.locator("#upd-ok")).toBeEnabled();
});

// 开发版(v0.0.0-…、dev)没有可比的发布号:顶栏不出现「更新」,总览也不写「有新版本」。
test("开发版:v0.0.0 与 dev 不显示「更新」入口", async ({ page }) => {
  for (const cur of ["v0.0.0-20261017-abcdef", "dev", "v0.0.0"]) {
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await mockUpdate(page, {
      cli: part({ current: cur, latest: "v0.9.7", available: true }),
      control: part({ current: "dev", latest: "v0.9.7", available: true }),
      nodes: [node({ current: "26.4.1" })],
      notes: "",
    });
    await login(page, env);
    await page.waitForResponse(r => r.url().endsWith("/local/update"));
    await expect(page.locator("#upd-pill"), cur).toBeHidden();
  }
});

async function mockStatus(page, version, update) {
  await page.route("**/api/status", async r => {
    const res = await r.fetch();
    const j = await res.json();
    await r.fulfill({ response: res, json: { ...j, version, update } });
  });
}
const cpStat = page => page.locator(".card.stat").filter({ has: page.locator(".l", { hasText: "控制面" }) });

test("总览控制面:有新版本是黑白灰底标签;开发版不比较", async ({ page }) => {
  await mockStatus(page, "v0.9.5", { latest: "v0.9.7", behind: 2 });
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  const card = cpStat(page);
  await expect(card.locator(".tag.lab")).toHaveText("有新版本 v0.9.7");
  await expect(card.locator(".c-warn")).toHaveCount(0);
  await expect(card.locator(".l .dot")).toHaveClass(/\bok\b/);
  // 灰底标签:背景和 .tag 一样是 --stone,不是橙色。
  const bg = await card.locator(".tag.lab").evaluate(el => getComputedStyle(el).backgroundColor);
  const stone = await page.evaluate(() => { const d = document.createElement("span"); d.className = "tag"; document.body.append(d); const c = getComputedStyle(d).backgroundColor; d.remove(); return c; });
  expect(bg).toBe(stone);

  await page.unrouteAll({ behavior: "ignoreErrors" });
  await mockStatus(page, "v0.0.0-20261017-abcdef", { latest: "v0.9.7", behind: 9 });
  await page.reload();
  await expect(cpStat(page)).toContainText("开发版");
  await expect(cpStat(page)).not.toContainText("有新版本");
});
