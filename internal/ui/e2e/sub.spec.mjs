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
  for (const id of ["sec-addr", "sec-clients", "sec-adv"]) await expect(page.locator("#" + id)).toHaveCount(1);
  await expect(page.locator("[data-act=s5-adv]")).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator(".zr-tpl")).toHaveCount(0);
  expect(requests).toEqual([]);
  await expect(page.locator("#sec-clients .zr").nth(1)).toContainText("Clash Verge、Mihomo Party、FlClash、Stash");
  await page.click("[data-act=s5-adv]");
  await expect(page.locator(".zr-tpl")).toHaveCount(3);
  await expect(page.locator(".zr-tpl").first()).toContainText(/\d+ 行/);
  expect(requests).toHaveLength(3);
  await page.goto(env.origin + "/#system");
  await page.goto(env.origin + "/#sub");
  await expect(page.locator("[data-act=s5-adv]")).toHaveAttribute("aria-expanded", "true");
});

test("改订阅名即时预览，输入焦点保留，保存条列出一处改动", async ({ page }) => {
  await open(page);
  await page.fill("#f-title", "测试 {USERNAME}");
  await expect(page.locator("#f-title")).toBeFocused();
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
  await expect(page.locator("#sb")).toContainText("订阅名");
  await expect(page.locator(".pv-t")).toHaveText("测试 alice");
  await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  await expect(page.locator("#sb")).toBeEmpty();
});

test("前缀缺协议时字段红字提示且不发 PUT，刷新间隔也校验非负整数", async ({ page }) => {
  const puts = [];
  page.on("request", r => { if (r.method() === "PUT") puts.push(r); });
  await open(page);
  await page.fill("#f-prefix", "sub.example.com");
  await save(page);
  await expect(page.locator("#pfx-err")).toContainText("http://");
  await expect(page.locator("#sb [role=alert]")).toContainText("http://");
  const colors = await page.locator("#pfx-err").evaluate(e => [getComputedStyle(e).color, getComputedStyle(document.querySelector("#f-prefix")).color]);
  expect(colors[0]).not.toBe(colors[1]);
  await page.fill("#f-prefix", "https://sub.example.com");
  for (const value of ["-1", "1.5", "abc", ""]) {
    await page.fill("#f-int", value); await save(page);
    await expect(page.locator("#int-err")).toContainText("非负整数");
  }
  expect(puts).toEqual([]);
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
  const message = "订阅地址要以 http:// 或 https:// 开头，比如 https://sub.example.com";
  await open(page);
  await page.route("**/api/settings", r => r.request().method() === "PUT" ? r.fulfill({ status: 400, json: { error: message } }) : r.continue());
  await page.fill("#f-prefix", "https://sub.example.com"); await save(page);
  await expect(page.locator("#pfx-err")).toHaveText(message);
  await expect(page.locator("#sb")).toContainText("改动还在，可以再点一次保存。");
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
  await expect(page.locator(".pv-link [data-act=copy]")).toHaveAttribute("data-text", uiBob.auto);
  await page.selectOption("#pv-user", "alice");
  const alice = await (await page.request.get(env.origin + "/api/users/alice/links")).json();
  await expect(page.locator(".pv-link [data-act=copy]")).toHaveAttribute("data-text", alice.auto);
  await expect(page.locator(".pv-t")).toHaveText("alice");
  await expect(page.locator(".pv-m")).toContainText("2027-01-01");
  await page.fill("#f-prefix", "https://new.example.com");
  await expect(page.locator(".pv-link [data-act=copy]")).toHaveAttribute("data-text", "https://new.example.com/sub/" + alice.auto.split("/sub/")[1]);
});

test("没有用户时标记示例", async ({ page }) => {
  await page.route("**/api/users?*", r => r.fulfill({ json: { users: [], total: 0 } }));
  await open(page);
  await expect(page.locator(".pv-h .tag")).toHaveText("示例");
  await expect(page.locator("#pv-user")).toHaveCount(0);
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
  await expect(callout).toHaveClass(/callout stone/);
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
  for (const id of ["f-prefix", "f-title", "f-int", "f-help"]) await expect(page.locator("#" + id)).toHaveAttribute("readonly", "");
  await expect(page.getByRole("button", { name: "插入用户名" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "自定义规则", exact: true })).toHaveCount(0);
  await page.locator("#sec-clients .zr").first().click();
  await expect(page.locator("#drawer.open")).toHaveCount(0);
  await expect(page.locator("#sb")).toBeEmpty();
});

test("保存先读最新设置，只覆盖改过的键和规则", async ({ page }) => {
  await open(page);
  await page.fill("#f-title", "新名字 {USERNAME}");
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
    { id: 3, name: "extra", display: "东京 A" }, { id: 4, name: "other", display: "其他 A" },
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
  await expect(page.locator(".pv-m")).toContainText("1.0 KB / 2.0 KB");
  await page.selectOption("#pv-user", "bob");
  await expect(page.locator(".pv-node")).toContainText("其他 A");
});
