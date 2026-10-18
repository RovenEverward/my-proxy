import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env;
const cp = (p, init = {}) => cpFetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
const settings = async () => (await cp("/api/settings")).json();
const putSettings = async patch => {
  const r = await cp("/api/settings", { method: "PUT", body: JSON.stringify({ ...await settings(), ...patch }) });
  expect(r.ok).toBe(true);
};
async function open(page, hash = "sub") {
  await login(page, env);
  await page.goto(env.origin + "/#" + hash);
  await expect(page.locator("#f-title")).toBeVisible();
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
}
const save = page => page.locator("[data-act=s5-save]").click();
// 订阅名是 contenteditable 的编辑框,{USERNAME} 在里面是一块「用户名」标签;直接改写里面的文字。
const setTitle = (page, text) => page.locator("#f-title").evaluate((el, t) => { el.textContent = t; el.dispatchEvent(new Event("input")); }, text);
// 读编辑框的值按页面保存时用的那套算(页面自己的 b5TokVal:{USERNAME} ↔ 「用户名」标签),不看 textContent。
const titleValue = page => page.locator("#f-title").evaluate(el => b5TokVal(el));
const linkOf = (page, expected) => expect(page.locator(".lk-url")).toHaveText(expected);
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("user", "add", "alice", "--limit", "100GB", "--expire", "2027-01-01");
  await env.seed("user", "add", "bob", "--limit", "200GB", "--expire", "2028-01-01");
});
test.afterAll(async () => { await env?.stop(); });
test.beforeEach(async () => { await putSettings({ sub_url_prefix: "", profile_title: "{USERNAME}", update_interval: 24, support_link: "", rules: null }); });

test("一页三段，高级默认折叠，展开才取底稿正文", async ({ page }) => {
  const requests = [];
  page.on("request", r => { if (r.url().includes("/api/settings/templates/")) requests.push(r.url()); });
  await open(page);
  for (const id of ["sec-link", "sec-look", "sec-clients", "sec-adv"]) await expect(page.locator("#" + id)).toHaveCount(1);
  await expect(page.locator("[data-act=s5-adv]")).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator(".zr-tpl")).toHaveCount(0);
  expect(requests).toEqual([]);
  await expect(page.locator("#sec-clients .b5-tr").filter({ hasText: "Clash Verge" })).toContainText("Clash Verge、Mihomo Party、FlClash、Stash");
  await page.click("[data-act=s5-adv]");
  await expect(page.locator(".zr-tpl")).toHaveCount(3);
  await expect(page.locator(".zr-tpl").first()).toContainText(/\d+ 行/);
  // 偶尔撞上限流会重试同一个地址,只看取了哪三份。
  expect(new Set(requests).size).toBe(3);
  await page.goto(env.origin + "/#system");
  await page.goto(env.origin + "/#sub");
  await expect(page.locator("[data-act=s5-adv]")).toHaveAttribute("aria-expanded", "true");
});

test("改订阅名即时预览，输入焦点保留，保存条列出一处改动", async ({ page }) => {
  await open(page);
  await page.locator("#f-title").click();
  // 光标放到文字末尾(「用户名」标签后面),接着打;刷新只换预览,不重画编辑框。
  // 存进去的是「测试 {USERNAME}」,alice 看到的是「测试 alice」。
  await page.keyboard.press("Control+End");
  await page.keyboard.type("测试 ");
  await expect(page.locator("#f-title")).toBeFocused();
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
  await expect(page.locator("#sb")).toContainText("订阅名");
  await expect.poll(() => titleValue(page)).toBe("测试 {USERNAME}");
  await expect(page.locator(".pv-t")).toHaveText("测试 alice");
  await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  await expect(page.locator("#sb")).toBeEmpty();
});

test("链接开头缺协议由控制面返回字段红字，刷新间隔仍校验非负整数", async ({ page }) => {
  const puts = [];
  page.on("request", r => { if (r.method() === "PUT") puts.push(r); });
  await open(page);
  await page.fill("#f-prefix", "sub.example.com");
  await save(page);
  // 页面把服务端原话里的「订阅地址」换成「链接开头」,保存条只说要改哪一栏。
  await expect(page.locator("#pfx-err")).toContainText("链接开头");
  await expect(page.locator("#pfx-err")).not.toContainText("订阅地址");
  await expect(page.locator("#pfx-err")).toContainText("http://");
  await expect(page.locator("#sb [role=alert]")).toHaveText("没保存上：链接开头的格式不对，改好再保存。");
  await expect(page.locator("#f-prefix")).toBeFocused();
  // 红字用的是 --bad(浅色 #dc2626,深色 #f2555a),和输入框里正常文字的颜色不同。
  const [errColor, inputColor, bad] = await page.locator("#pfx-err").evaluate(e => [getComputedStyle(e).color, getComputedStyle(document.querySelector("#f-prefix")).color, getComputedStyle(document.documentElement).getPropertyValue("--bad").trim()]);
  expect(errColor).toBe("rgb(" + [1, 3, 5].map(i => parseInt(bad.slice(i, i + 2), 16)).join(", ") + ")");
  expect(inputColor).not.toBe(errColor);
  await page.fill("#f-prefix", "https://sub.example.com");
  for (const value of ["-1", "1.5", "abc", ""]) {
    await page.fill("#f-int", value); await save(page);
    await expect(page.locator("#int-err")).toContainText("非负整数");
  }
  expect(puts).toHaveLength(1);
  expect(puts[0].postDataJSON().sub_url_prefix).toBe("sub.example.com");
});

test("前缀尾斜杠原样发送，由服务端规范化后读回", async ({ page }) => {
  const puts = [];
  page.on("request", r => { if (r.method() === "PUT" && r.url().endsWith("/api/settings")) puts.push(r.postDataJSON()); });
  await open(page);
  await page.fill("#f-prefix", "https://sub.example.com/");
  await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  expect(puts[0].sub_url_prefix).toBe("https://sub.example.com/");
  expect((await settings()).sub_url_prefix).toBe("https://sub.example.com");
  await expect(page.locator("#f-prefix")).toHaveValue("https://sub.example.com");
});

test("服务端 400 的订阅地址原话显示在字段下，草稿保留并可重试", async ({ page }) => {
  const message = "链接开头要以 http:// 或 https:// 开头，例如 https://sub.example.com";
  await open(page);
  // 旧控制面不报字段名:页面按原话里的「订阅地址」认,显示成「链接开头」。
  await page.route("**/api/settings", r => r.request().method() === "PUT" ? r.fulfill({ status: 400, json: { error: message } }) : r.continue());
  await page.fill("#f-prefix", "https://sub.example.com"); await save(page);
  await expect(page.locator("#pfx-err")).toHaveText(message.replace("订阅地址", "链接开头"));
  // 按字段名认出来的错误:保存条说的是「链接开头」的格式不对(别的错误才说「改动还在，可以再点一次保存。」)。
  await expect(page.locator("#sb [role=alert]")).toHaveText("没保存上：链接开头的格式不对，改好再保存。");
  await expect(page.locator("#sb")).not.toContainText("改动还在，可以再点一次保存。");
  await expect(page.locator("#f-prefix")).toHaveValue("https://sub.example.com");
  await page.unroute("**/api/settings"); await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
});

test("最近拉过订阅的用户默认入选，下拉切换链接和用量到期", async ({ page }) => {
  const links = await (await cp("/api/users/bob/links")).json();
  const pulled = await cpFetch(links.auto.replace(/^https?:\/\/[^/]+/, env.cpURL), { headers: { "User-Agent": "clash-verge/v2.0.0" } });
  expect(pulled.ok).toBe(true);
  await expect.poll(async () => (await (await cp("/api/sub-pulls")).json()).some(p => p.username === "bob")).toBe(true);
  await open(page);
  await expect(page.locator("#pv-user")).toHaveValue("bob");
  await expect(page.locator(".pv-t")).toHaveText("bob");
  await expect(page.locator(".pv-m")).toContainText("2028-01-01");
  const uiBob = await (await page.request.get(env.origin + "/api/users/bob/links")).json();
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", uiBob.auto);
  await expect(page.locator("#sec-link .ch small")).toContainText("复制给 bob");
  await page.selectOption("#pv-user", "alice");
  const alice = await (await page.request.get(env.origin + "/api/users/alice/links")).json();
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", alice.auto);
  await expect(page.locator(".pv-t")).toHaveText("alice");
  await expect(page.locator(".pv-m")).toContainText("2027-01-01");
  await page.fill("#f-prefix", "https://new.example.com");
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", "https://new.example.com/sub/" + alice.auto.split("/sub/")[1]);
  await linkOf(page, "https://new.example.com/sub/" + alice.auto.split("/sub/")[1]);
});

test("没有用户时标记示例", async ({ page }) => {
  await page.route("**/api/users?*", r => r.fulfill({ json: { users: [], total: 0 } }));
  await open(page);
  await expect(page.locator(".b5-pick .tag")).toHaveText("示例");
  await expect(page.locator("#pv-user")).toHaveCount(0);
  await expect(page.locator(".lk-box [data-act=copy]")).toBeDisabled();
});

test("客户端和底稿旧路由滚到对应段，底稿路由自动展开", async ({ page }) => {
  await open(page, "sub/clients");
  await expect(page.locator("#sec-clients")).toBeInViewport();
  await page.goto(env.origin + "/#sub/templates");
  await expect(page.locator("#sec-adv")).toBeInViewport();
  await expect(page.locator("[data-act=s5-adv]")).toHaveAttribute("aria-expanded", "true");
});

test("未填前缀的提示为中性灰，使用信息图标", async ({ page }) => {
  await open(page);
  const callout = page.locator("#pfx-warn");
  await expect(callout).toHaveClass(/b5-note/);
  const colors = await callout.evaluate(e => {
    const c = getComputedStyle(e), svg = getComputedStyle(e.querySelector("svg"));
    const probe = document.createElement("span"); probe.style.color = "var(--muted)"; probe.style.backgroundColor = "var(--stone)"; e.append(probe);
    const p = getComputedStyle(probe), out = [c.backgroundColor, p.backgroundColor, svg.color, p.color]; probe.remove(); return out;
  });
  expect(colors[0]).toBe(colors[1]); expect(colors[2]).toBe(colors[3]);
});


test("只读角色的字段只读，不出现写入口和保存条", async ({ page }) => {
  await page.route("**/ui/meta", async r => {
    const response = await r.fetch(), meta = await response.json();
    await r.fulfill({ response, json: { ...meta, allow: ["GET /api"] } });
  });
  await open(page);
  for (const id of ["f-prefix", "f-int", "f-help"]) await expect(page.locator("#" + id)).toHaveAttribute("readonly", "");
  // 订阅名的编辑框也不能改,「+ 用户名」不出现。
  await expect(page.locator("#f-title")).toHaveAttribute("contenteditable", "false");
  await expect(page.getByRole("button", { name: "用户名" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "识别规则", exact: true })).toHaveCount(0);
  await expect(page.locator("#sec-clients .b5-tr").first()).toBeVisible();
  await expect(page.locator("#sb")).toBeEmpty();
});

test("保存先读最新设置，只覆盖改过的键和规则", async ({ page }) => {
  await open(page);
  await setTitle(page, "新名字 {USERNAME}");
  // 页面打开后，另一个操作者改前缀、刷新间隔、规则和通知。
  const rules = [{ pattern: "(?i)^other/", result: "sing-box" }];
  await putSettings({ sub_url_prefix: "https://other.example.com", update_interval: 48, rules, notify_days_left: 7 });
  await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  const stored = await settings();
  expect(stored.profile_title).toBe("新名字 {USERNAME}");
  expect(stored.sub_url_prefix).toBe("https://other.example.com");
  expect(stored.update_interval).toBe(48);
  expect(stored.rules).toEqual(rules);
  expect(stored.notify_days_left).toBe(7);
});

test("用户可用节点按套餐和额外授权合并，排除排水与满员节点", async ({ page }) => {
  const users = [
    { username: "alice", status: "active", plan_id: 1, inbound_ids: [3], used_traffic: 1024, data_limit: 2048 },
    { username: "bob", status: "active", plan_id: 2 },
  ];
  const nodes = [
    { id: 1, name: "full", display: "已满 A" }, { id: 2, name: "draining", display: "排水 A", draining: true },
    { id: 3, name: "extra", display: "🇯🇵 东京 A" }, { id: 4, name: "other", display: "其他 A" },
  ];
  const inbounds = [1, 2, 3, 4].map(id => ({ id, node_id: id, protocol: "vless", security: "reality", max_users: id === 1 ? 2 : 0 }));
  await page.route("**/api/users?*", r => r.fulfill({ json: { users, total: users.length } }));
  await page.route("**/api/sub-pulls", r => r.fulfill({ json: [] }));
  await page.route("**/api/nodes", r => r.fulfill({ json: nodes }));
  await page.route("**/api/inbounds", r => r.fulfill({ json: inbounds }));
  await page.route("**/api/plans", r => r.fulfill({ json: [{ id: 1, node_ids: [1, 2] }, { id: 2, node_ids: [1, 4] }] }));
  await open(page);
  await expect(page.locator(".pv-node")).toContainText("东京 A");
  await expect(page.locator(".pv-node")).toContainText("VLESS");
  // 节点名里的地区旗帜走 .flag-w/.flag 的矩形 SVG,节点名旁边没有自绘的地区点。
  await expect(page.locator(".pv-node .flag-w img.flag")).toHaveAttribute("src", "/flags/jp.svg");
  await expect(page.locator(".pv-node .dot")).toHaveCount(0);
  expect(await page.evaluate(() => document.querySelector(".pv-node").innerText)).not.toMatch(/[\u{1F1E6}-\u{1F1FF}]{2}/u);
  await expect(page.locator(".pv-m")).toContainText("1.0 KB / 2.0 KB");
  await page.selectOption("#pv-user", "bob");
  await expect(page.locator(".pv-node")).toContainText("其他 A");
});

test("放弃立即生效，toast 可撤销且改动项一致", async ({ page }) => {
  await open(page);
  await setTitle(page, "改过的名字 {USERNAME}");
  await page.fill("#f-int", "48");
  await expect(page.locator("#sb")).toContainText("有 2 处改动");
  const items = await page.locator("#sb .s5-bar-t .sub").textContent();
  expect(items).toContain("订阅名");
  expect(items).toContain("刷新间隔");
  await page.click("[data-act=s5-discard]");
  await expect(page.locator("#sb")).toBeEmpty();
  await expect(page.locator(".toast")).toContainText("已放弃改动");
  await page.locator(".toast .toast-act").click();
  await expect(page.locator("#sb")).toContainText("有 2 处改动");
  await expect(page.locator("#sb .s5-bar-t .sub")).toHaveText(items);
  await expect.poll(() => titleValue(page)).toBe("改过的名字 {USERNAME}");
  await expect(page.locator("#f-title .tk")).toHaveText("用户名");
  await expect(page.locator("#f-int")).toHaveValue("48");
});

test("有改动离开订阅页弹应用内对话框，可留下、不保存、保存并离开", async ({ page }) => {
  const native = [];
  page.on("dialog", d => { native.push(d.type()); d.dismiss().catch(() => {}); });
  const puts = [];
  page.on("request", r => { if (r.method() === "PUT" && r.url().endsWith("/api/settings")) puts.push(r.postDataJSON()); });
  await open(page);
  await setTitle(page, "别丢了 {USERNAME}");
  await expect(page.locator("#sb")).toContainText("有 1 处改动");

  // 点侧栏「节点」:地址被拦回 #sub,弹应用内对话框,不是原生弹窗。
  await page.click('.side a.nav[href="#nodes"]');
  await expect(page.locator("#modal.open h2")).toContainText("有 1 处改动还没保存");
  await expect(page.locator("#modal.open")).toContainText("订阅名");
  expect(native).toEqual([]);
  await expect(page).toHaveURL(/#sub$/);

  // 留下:关对话框,还在订阅页,草稿还在。
  await page.getByRole("button", { name: "留下" }).click();
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await expect(page).toHaveURL(/#sub$/);
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
  await expect.poll(() => titleValue(page)).toBe("别丢了 {USERNAME}");

  // 不保存:丢掉草稿到节点页;回到订阅页没有保存条。
  await page.click('.side a.nav[href="#nodes"]');
  await expect(page.locator("#modal.open h2")).toContainText("有 1 处改动还没保存");
  await page.getByRole("button", { name: "不保存" }).click();
  await expect(page).toHaveURL(/#nodes$/);
  await page.click('.side a.nav[href="#sub"]');
  await expect(page.locator("#f-title")).toBeVisible();
  await expect(page.locator("#sb")).toBeEmpty();

  // 保存并离开:发出 PUT,到达目标页,控制面已保存。
  await setTitle(page, "这次保存 {USERNAME}");
  await page.click('.side a.nav[href="#nodes"]');
  await expect(page.locator("#modal.open h2")).toContainText("有 1 处改动还没保存");
  await page.getByRole("button", { name: "保存并离开" }).click();
  await expect(page).toHaveURL(/#nodes$/);
  await expect.poll(async () => (await settings()).profile_title).toBe("这次保存 {USERNAME}");
  expect(puts.length).toBeGreaterThan(0);
  expect(puts[puts.length - 1].profile_title).toBe("这次保存 {USERNAME}");
  expect(native).toEqual([]);
});

test("同页锚点 #sub/clients 不算离开，不弹对话框", async ({ page }) => {
  const native = [];
  page.on("dialog", d => { native.push(d.type()); d.dismiss().catch(() => {}); });
  await open(page);
  await setTitle(page, "保持 {USERNAME}");
  await page.evaluate(() => { location.hash = "sub/clients"; });
  await expect(page).toHaveURL(/#sub\/clients$/);
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
  expect(native).toEqual([]);
});

test("规则抽屉 UA 试用提示与黑白焦点环", async ({ page }) => {
  await open(page);
  await page.click("[data-act=s5-rules]");
  await expect(page.locator("#rd-ua")).toBeVisible();
  await expect(page.locator("#rd-ruling")).toHaveCount(0);
  await page.fill("#rd-ua", "clash-verge/v2.0.3");
  await expect(page.locator("#rd-ruling")).toHaveText("实际以控制面判断为准");
  await page.focus("#rd-ua");
  // 抽屉里的试框是 2px 墨色描边(浅色 #111),不用 box-shadow 光圈。
  const ring = await page.locator("#rd-ua").evaluate(e => { const s = getComputedStyle(e); return [s.outlineColor, s.boxShadow, s.outlineWidth]; });
  expect(ring[0]).toBe("rgb(17, 17, 17)");
  expect(ring[1]).toBe("none");
  expect(ring[2]).toBe("2px");
});

test("底稿抽屉「语法正确」用墨色不是绿色", async ({ page }) => {
  await open(page, "sub/templates");
  await page.click(".zr-tpl");
  await expect(page.locator("#tpl-ok")).toContainText("语法正确", { timeout: 10000 });
  const color = await page.locator("#tpl-ok span").evaluate(e => getComputedStyle(e).color);
  const m = /rgb\((\d+), (\d+), (\d+)\)/.exec(color);
  expect(m).not.toBeNull();
  const [r, g, b] = m.slice(1).map(Number);
  expect(Math.max(r, g, b) - Math.min(r, g, b)).toBeLessThan(16);
  expect(Math.max(r, g, b)).toBeLessThan(64);
});

test("放弃后后台刷新，撤销只恢复原先改过的键", async ({ page }) => {
  await open(page);
  await setTitle(page, "撤销的标题");
  await page.click("[data-act=s5-discard]");
  await putSettings({ sub_url_prefix: "https://fresh.example.com", update_interval: 72 });
  await page.evaluate(() => load(true));
  await expect(page.locator("#f-prefix")).toHaveValue("https://fresh.example.com");
  await page.locator(".toast .toast-act").click();
  await expect.poll(() => titleValue(page)).toBe("撤销的标题");
  await expect(page.locator("#f-prefix")).toHaveValue("https://fresh.example.com");
  await expect(page.locator("#f-int")).toHaveValue("72");
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
});

test("旧前缀未改时可保存标题，PUT 保留原前缀", async ({ page }) => {
  let stored = { ...await settings(), sub_url_prefix: "sub.example.com" };
  const puts = [];
  await page.route("**/api/settings", r => {
    if (r.request().method() === "PUT") { stored = r.request().postDataJSON(); puts.push(stored); }
    return r.fulfill({ json: stored });
  });
  await open(page);
  await setTitle(page, "旧前缀标题");
  await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  expect(puts).toHaveLength(1);
  expect(puts[0].sub_url_prefix).toBe("sub.example.com");
  await expect(page.locator("#sb")).toBeEmpty();
});

test("未编辑前缀的预览和复制逐字保留控制面的旧链接", async ({ page }) => {
  const stored = { ...await settings(), sub_url_prefix: "https://old.example.com/sub///" };
  const auto = "https://old.example.com/sub///sub/token-old";
  await page.route("**/api/settings", r => r.fulfill({ json: stored }));
  await page.route("**/api/users/*/links", r => r.fulfill({ json: { auto } }));
  await open(page);
  // 完整链接按最后一个 /sub/ 切成「开头」和「每人自己的部分」两段显示。
  await linkOf(page, auto);
  await expect(page.locator(".lk-pre")).toHaveText("https://old.example.com/sub//");
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", auto);
  await setTitle(page, "只改标题");
  await linkOf(page, auto);
});

test("编辑前缀时取最后一段订阅令牌并去掉全部尾斜杠", async ({ page }) => {
  await page.route("**/api/users/*/links", r => r.fulfill({ json: { auto: "https://old.example.com/sub/path/sub/token-last" } }));
  await open(page);
  await page.fill("#f-prefix", "https://new.example.com/sub///");
  const expected = "https://new.example.com/sub/sub/token-last";
  await linkOf(page, expected);
  await expect(page.locator(".lk-pre")).toHaveText("https://new.example.com/sub");
  await expect(page.locator(".lk-tail")).toHaveText("/sub/token-last");
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", expected);
});

test("Go 正则允许完成，浏览器不支持的规则提示交由控制面检查", async ({ page }) => {
  await open(page);
  await page.click("[data-act=s5-rules]");
  // 前导 Go flags 可预览，作用域 flags 在支持它的浏览器里也能使用。
  for (const pattern of ["(?m)^Clash", "(?is)^clash"]) {
    await page.fill('[data-rp="0"]', pattern);
    await page.locator('[data-rp="0"]').press("Tab");
    await page.fill("#rd-ua", "CLASH");
    // 试框的说法是「会拿到 … 这条」,并对上的那行标 hit。
    if (pattern.includes("is")) {
      await expect(page.locator("#rd-hit")).toContainText("会拿到");
      await expect(page.locator('.rd-row[data-i="0"]')).toHaveClass(/hit/);
    }
    await expect(page.locator("#rd-done")).toBeEnabled();
  }
  await page.fill('[data-rp="0"]', "(?i:clash)");
  await page.locator('[data-rp="0"]').press("Tab");
  await expect(page.locator("#rd-done")).toBeEnabled();
  await page.click("#rd-done");
  await page.click("[data-act=s5-rules]");
  // Go 命名组在 JS 中无法编译，仍可完成并由控制面保存。
  await page.fill('[data-rp="0"]', "(?P<client>clash)");
  await page.locator('[data-rp="0"]').press("Tab");
  await expect(page.locator('.rd-row[data-i="0"] .h')).toHaveText("浏览器没法预览这条，保存时由控制面检查");
  await page.fill("#rd-ua", "clash");
  await expect(page.locator('.rd-row[data-i="0"]')).not.toHaveClass(/hit/);
  await expect(page.locator("#rd-done")).toBeEnabled();
  await page.click("#rd-done"); await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  await page.click("[data-act=s5-rules]");
  await page.fill('[data-rp="0"]', ""); await page.locator('[data-rp="0"]').press("Tab");
  await expect(page.locator("#rd-done")).toBeDisabled();
  await page.fill('[data-rp="0"]', "["); await page.locator('[data-rp="0"]').press("Tab");
  await expect(page.locator("#rd-done")).toBeEnabled();
  await page.click("#rd-done"); await save(page);
  await expect(page.locator("#sb")).toContainText("规则正则不合法");
});

test("切换控制面先确认草稿，留下取消，不保存或保存后才切换", async ({ page }) => {
  const calls = [];
  await page.route("**/local/profiles", r => r.fulfill({ json: [
    { name: "default", server: env.cpURL, current: true }, { name: "other", server: "https://other.example.com", current: false },
  ] }));
  await page.route("**/local/profiles/use", r => { calls.push(r.request().postDataJSON()); return r.fulfill({ json: { ok: true } }); });
  await open(page);
  const choose = async () => {
    if (await page.locator(".profile[data-act=cp-toggle]").getAttribute("aria-expanded") !== "true") await page.click(".profile[data-act=cp-toggle]");
    await page.click(".cp-pop [data-act=cp-switch]");
    await page.click('.pfitem[data-name="other"]');
    await expect(page.locator("#modal.open h2")).toContainText("还没保存");
  };
  await setTitle(page, "切换前草稿");
  await choose();
  await page.getByRole("button", { name: "留下", exact: true }).click();
  await expect(page.locator("#modal.open")).toHaveCount(0);
  expect(calls).toEqual([]);
  await expect.poll(() => titleValue(page)).toBe("切换前草稿");
  await choose(); await page.getByRole("button", { name: "不保存", exact: true }).click();
  await expect.poll(() => calls.length).toBe(1);
  // 默认标题就是一块「用户名」标签:存进去的是 {USERNAME}。
  await expect.poll(() => titleValue(page)).toBe("{USERNAME}");
  await setTitle(page, "切换前保存");
  await choose(); await page.getByRole("button", { name: "保存并离开", exact: true }).click();
  await expect.poll(() => calls.length).toBe(2);
  expect((await settings()).profile_title).toBe("切换前保存");
});

test("离页对话框声明名称，Tab 循环限制在内部并在关闭时还原焦点", async ({ page }) => {
  await open(page);
  await setTitle(page, "焦点测试");
  const nav = page.locator('.side a.nav[href="#nodes"]');
  await nav.click();
  const dialog = page.getByRole("dialog", { name: "有 1 处改动还没保存" });
  await expect(dialog).toHaveAttribute("aria-modal", "true");
  await expect(page.locator("#sub-leave-ok")).toBeFocused();
  const buttons = dialog.getByRole("button");
  await page.keyboard.press("Tab"); await expect(buttons.nth(0)).toBeFocused();
  await page.keyboard.press("Shift+Tab"); await expect(buttons.nth(2)).toBeFocused();
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press("Tab");
    expect(await dialog.evaluate(e => e.contains(document.activeElement))).toBe(true);
  }
  await page.getByRole("button", { name: "留下", exact: true }).click();
  await expect(nav).toBeFocused();
  await expect(page.locator("#modal")).not.toHaveAttribute("aria-labelledby", "sub-leave-title");
});

test("规则修改后 Tab 的目标格式控件保留焦点，格式修改也保留焦点", async ({ page }) => {
  await open(page); await page.click("[data-act=s5-rules]");
  await page.fill('[data-rp="0"]', "(?i)^focus");
  await page.locator('[data-rp="0"]').press("Tab");
  await expect(page.locator('[data-rf="0"]')).toBeFocused();
  await expect(page.locator('.rd-row[data-i="0"] .rd-main .h')).toHaveCount(0);
  await page.selectOption('[data-rf="0"]', "sing-box");
  await expect(page.locator('[data-rf="0"]')).toBeFocused();
});

// 保存了新地址、页面还没重载回来时,预览和复制已经是新地址的链接,不再沿用旧链接。
test("保存新订阅地址后，重载回来前预览就换成新链接", async ({ page }) => {
  await page.route("**/api/users/*/links", r => r.fulfill({ json: { auto: "http://127.0.0.1:1/sub/tok-1" } }));
  let saved = false, hold;
  const held = new Promise(r => { hold = r; });
  // PUT 发出后拖住重载要取的内置规则,好看重载回来前的预览。
  page.on("request", r => { if (r.method() === "PUT" && /\/api\/settings$/.test(r.url())) saved = true; });
  await page.route("**/api/settings/builtin-rules", async r => { if (saved) await held; await r.continue(); });
  await open(page);
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", "http://127.0.0.1:1/sub/tok-1");
  await page.fill("#f-prefix", "https://sub.example.com");
  await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", "https://sub.example.com/sub/tok-1");
  await linkOf(page, "https://sub.example.com/sub/tok-1");
  hold();
});

// 选「不保存」后先把页面画回已保存的值;切换控制面失败时,页面不留着已丢掉的草稿和保存条。
test("切换控制面选不保存但切换失败：页面已画回保存的值", async ({ page }) => {
  await page.route("**/local/profiles", r => r.fulfill({ json: [
    { name: "default", server: env.cpURL, current: true }, { name: "other", server: "https://other.example.com", current: false },
  ] }));
  await page.route("**/local/profiles/use", r => r.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "连不上" }) }));
  await open(page);
  await setTitle(page, "要丢掉的草稿");
  await page.click(".profile[data-act=cp-toggle]");
  await page.click(".cp-pop [data-act=cp-switch]");
  await page.click('.pfitem[data-name="other"]');
  await page.getByRole("button", { name: "不保存", exact: true }).click();
  await expect.poll(() => titleValue(page)).toBe("{USERNAME}");
  await expect(page.locator("#sb")).toBeEmpty();
});

// 离页对话框开着时,慢回来的「切换控制面」弹窗换掉它,要先收掉它的焦点陷阱;再点「添加控制面」后 Tab 不被拦回弹窗。
test("弹窗被换掉时先收掉离页对话框的焦点陷阱", async ({ page }) => {
  let release;
  const held = new Promise(r => { release = r; });
  await page.route("**/local/profiles", async r => { await held; await r.fulfill({ json: [
    { name: "default", server: env.cpURL, current: true }, { name: "other", server: "https://other.example.com", current: false },
  ] }); });
  await open(page);
  await setTitle(page, "陷阱测试");
  await page.click(".profile[data-act=cp-toggle]");
  await page.click(".cp-pop [data-act=cp-switch]");
  await page.locator('.side a.nav[href="#nodes"]').click();
  await expect(page.getByRole("dialog", { name: "有 1 处改动还没保存" })).toBeVisible();
  release();
  await expect(page.locator("#modal.open h2")).toHaveText("切换控制面");
  await expect(page.locator("#modal")).not.toHaveAttribute("aria-labelledby", "sub-leave-title");
  await expect(page.locator("#modal")).toHaveAttribute("role", "alertdialog");
  const btn = page.locator("[data-act=profile-add]");
  if (await btn.count()) {
    await btn.click();
    await expect(page.locator("#modal.open")).toHaveCount(0);
    await page.keyboard.press("Tab"); await page.keyboard.press("Tab");
    expect(await page.evaluate(() => document.getElementById("modal").contains(document.activeElement))).toBe(false);
  }
});
