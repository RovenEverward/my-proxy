// 用户页「最近在线」:列、排序、工具栏的在线人数、节点连不上的灰条、读不到在线状态,
// 以及详情抽屉里的「最近连接」。
// 这一版控制面还不会算在线状态,所以 /api/users 里的 presence 与 /api/users/*/activity
// 用 page.route 换成原型 mock.js 同形的数据(地址一律 203.0.113.x);节点与订阅拉取记录
// 走真控制面(同一份登录态)。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

const MIN = 60e3;
const at = ms => new Date(Date.now() - ms).toISOString();

let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "la-1", "--address", "203.0.113.11");
  await env.seed("node", "add", "tyo-1", "--address", "203.0.113.12");
  await env.seed("node", "add", "sg-1", "--address", "203.0.113.13");
  await env.seed("node", "add", "hk-1", "--address", "203.0.113.14");
  // 显示名由地区和城市拼出来;内部名(la-1)只留在节点详情里。
  await env.seed("node", "edit", "la-1", "--region", "US", "--city", "洛杉矶", "--letter", "A");
  await env.seed("node", "edit", "tyo-1", "--region", "JP", "--city", "东京", "--letter", "B");
  await env.seed("node", "edit", "sg-1", "--region", "SG", "--city", "新加坡", "--letter", "C");
  await env.seed("node", "edit", "hk-1", "--region", "HK", "--city", "香港", "--letter", "D");
  // 建用户必须显式带 enabled,否则零值是停用。
  for (const n of ["alice", "bob", "chen", "dora", "evan", "fang", "gus", "hana", "ivy", "jun"]) await env.seed("user", "add", n, "--limit", "50GB");
});
test.afterAll(async () => { await env?.stop(); });

// 原型 mock.js 里那几个用户;presence 是另一个 chunk 给 /api/users 加上的字段。
const P = (online, last, node, extra = {}) => ({ online, last_active_at: last, last_node_id: node, online_nodes: online ? [node] : [], online_ips: online ? 0 : 0, stale: false, ...extra });
const MOCK = [
  { username: "alice", note: "自用", status: "active", used_traffic: 18 << 30, data_limit: 100 << 30, presence: P(true, at(0.3 * MIN), 1, { online_ips: 2 }) },
  { username: "bob", note: "家里的电视", status: "active", used_traffic: 41 << 30, data_limit: 200 << 30, presence: P(true, at(0.5 * MIN), 4, { online_nodes: [4, 1], online_ips: 3 }) },
  { username: "chen", note: "", status: "active", used_traffic: 6 << 30, data_limit: 50 << 30, presence: P(true, at(0.2 * MIN), 3) },
  { username: "dora", note: "同事", status: "active", used_traffic: 2 << 30, data_limit: 50 << 30, presence: P(false, at(3 * MIN), 2) },
  { username: "evan", note: "", status: "active", used_traffic: 12 << 30, data_limit: 100 << 30, presence: P(false, at(6 * MIN), 1) },
  { username: "fang", note: "", status: "expired", used_traffic: 33 << 30, data_limit: 100 << 30, presence: P(false, at(150 * MIN), 2) },
  { username: "gus", note: "暂时不用", status: "disabled", used_traffic: 0, data_limit: 50 << 30, presence: P(false, at(10 * 24 * 60 * MIN), 3) },
  // 两个没连过的按创建时间排(ivy 后加)。真控制面建用户的时间只到秒,十条用户常落在同一秒里,
  // 顺序就退化成按用户名;这里把时间写死,断言才不随建用户用了多久而变。
  { username: "hana", note: "新加的", status: "active", used_traffic: 0, data_limit: 50 << 30, presence: P(false, null, null), created_at: "2026-01-02T00:00:00Z" },
  { username: "ivy", note: "", status: "active", used_traffic: 0, data_limit: 50 << 30, presence: P(false, null, null), created_at: "2026-01-03T00:00:00Z" },
  { username: "jun", note: "", status: "limited", used_traffic: 50 << 30, data_limit: 50 << 30, presence: P(false, at(6 * 24 * 60 * MIN), 1) },
];
// 订阅拉取:alice 用 Clash Verge 拉过,bob 没有,hana 导入过却从没用过。
const PULLS = {
  alice: { client: "clash-verge", format: "clash-meta", last_pulled_at: at(42 * MIN) },
  hana: { client: "shadowrocket", format: "base64-links", last_pulled_at: at(2 * 24 * 60 * MIN) },
};
// 最近几段使用(按流量采样拼出来:相邻两次有流量的采样隔不到 2 分钟算同一段)。
const GB = 2 ** 30, MB = 2 ** 20;
const ACT = {
  alice: { segments: [
    { node_id: 1, start: at(40 * MIN), end: null, bytes: 1.2 * GB },
    { node_id: 2, start: at(24 * 60 * MIN), end: at(23 * 60 * MIN), bytes: 310 * MB },
    { node_id: 1, start: at(26 * 60 * MIN), end: at(25 * 60 * MIN), bytes: 2.4 * GB },
    { node_id: 1, start: at(48 * 60 * MIN), end: at(47 * 60 * MIN), bytes: 96 * MB },
  ], ips: [{ ip: "203.0.113.24", node_id: 1, last_seen: at(4e3) }, { ip: "203.0.113.87", node_id: 1, last_seen: at(12e3) }] },
  hana: { segments: [], ips: [] },
};
const LIVE = { updated_at: at(24e3), online: 3, active_24h: 4, stale_nodes: [] };

// 用户列表接口:GET /api/users,带不带查询串(?limit=…)都是它;/api/users/<名字>、
// /api/users/<名字>/links、/api/users/<名字>/activity 是另外的接口,不在这里套 MOCK。
const USERS_PAGE = u => new URL(u).pathname === "/api/users";
// mockAPI:列表里按用户名套上上面的 presence、状态和备注(用户本身来自真控制面);
// activity 按 opts.activity 回(Error 表示 500)。节点与拉取记录原样走控制面。
async function mockAPI(page, opts) {
  await page.route(/\/api\/users(\?.*)?$/, async r => {
    const res = await r.fetch();
    // 别的接口(/user、/links、/activity)原样回。
    if (!USERS_PAGE(r.request().url())) return r.fulfill({ response: res });
    const j = await res.json();
    const users = j.users.map(u => { const m = MOCK.find(x => x.username === u.username); return m ? { ...u, ...m } : u; });
    const out = { ...j, users };
    // 在线情况读不到:整个字段缺席,不是 null。
    if (opts.presence) out.presence = opts.presence; else delete out.presence;
    return r.fulfill({ response: res, json: out });
  });
  await page.route(/\/api\/users\/[^/]+\/activity$/, r => {
    const a = opts.activity || { segments: [], ips: [] };
    return a instanceof Error ? r.fulfill({ status: 500, json: { error: a.message } }) : r.fulfill({ json: { ...a, updated_at: opts.presence?.updated_at || null } });
  });
  await page.route(/\/api\/sub-pulls$/, r => r.fulfill({ json: Object.entries(PULLS).map(([username, p]) => ({ username, ...p, first_pulled_at: p.last_pulled_at })) }));
}
async function open(page, opts = {}) {
  await mockAPI(page, { presence: opts.presence === undefined ? LIVE : opts.presence, activity: opts.activity });
  await login(page, env);
  await page.goto(env.origin + "/#users");
  // 等列表画完(文字不是空白都能立刻满足,所以等表格骨架消失、行出来)。
  await expect(page.locator("table.ut tbody tr")).toHaveCount(MOCK.length);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
}
async function openUser(page, name) {
  await page.goto(env.origin + "/#users/" + name);
  await expect(page.locator("#drawer.open h2")).toHaveText(name);
  await expect(page.locator("#drawer.open #ud-pres")).toBeVisible();
}
const cell = (page, name) => page.locator(`tr[data-href="#users/${name}"] .td-pres`);
const order = page => page.locator("table.ut tbody tr td:first-child b").allTextContents();

test("「最近在线」在「用户」后面:在线写「在线」和「2 个 IP · 洛杉矶 A」,节点用显示名", async ({ page }) => {
  await open(page);
  await expect(page.locator("table.ut thead th")).toHaveText(["用户", "最近在线", "状态", "套餐", "本期剩余", "到期", "重置"]);
  // 只有这一列能排序:表头是按钮,默认最新在前。
  const th = page.locator("th.th-sort");
  await expect(th.locator("button")).toHaveText("最近在线");
  await expect(th).toHaveAttribute("aria-sort", "descending");
  await expect(cell(page, "alice").locator(".pres-l.on")).toHaveText("在线");
  await expect(cell(page, "alice").locator(".sub")).toHaveText("2 个 IP · 洛杉矶 A");
  await expect(cell(page, "bob").locator(".sub")).toHaveText("3 个 IP · 香港 D 等 2 个节点");
  // 悬停提示是绝对时间(时区标签按浏览器偏移算,这里固定 Asia/Shanghai);多个节点在提示里列全,
  // 提示是纯文本,节点只有地区名。
  await expect(cell(page, "bob").locator(".pres")).toHaveAttribute("data-tip",
    /^最后一次有流量：\d{4} 年 \d+ 月 \d+ 日 \d{2}:\d{2}:\d{2}（UTC\+8） · 香港 D · 香港 D、洛杉矶 A$/);
  await expect(cell(page, "dora").locator(".pres-l")).toHaveText("3 分钟前");
  await expect(cell(page, "gus").locator(".pres-l")).toHaveClass(/old/);
  // 没连过的:最浅的灰;导入过订阅的下一行写一句。
  await expect(cell(page, "hana").locator(".pres-l.never")).toHaveText("从未连接");
  await expect(cell(page, "hana").locator(".sub")).toHaveText("导入过订阅，还没连上");
  await expect(cell(page, "ivy").locator(".sub")).toHaveCount(0);
  // 内部名不出现在用户页上。
  await expect(page.locator("main")).not.toContainText(/\b(la-1|tyo-1|sg-1|hk-1)\b/);
  // 工具栏右边的在线人数,不在状态筛选里。
  const sum = page.locator(".tb .pres-sum");
  await expect(sum).toHaveText("3 人在线");
  await expect(sum).toHaveAttribute("data-tip", /^在线状态 \d+ (秒|分钟)前更新，每 30 秒一次$/);
  // 筛选是账号状态,没有「在线」:在线人数只在右边。
  await expect(page.locator(".tb .seg button")).toHaveCount(5);
  await expect(page.locator(".tb .seg")).not.toContainText("在线");
});

// 悬停提示:data-tip 只是数据,真正浮出来的是挂在 body 上的 .ftip(固定定位),表格的滚动区裁不掉它。
test("悬停「在线」格子和右侧在线人数:浮出 .ftip 显示绝对时间,鼠标移开就收起", async ({ page }) => {
  await open(page);
  const online = cell(page, "alice").locator(".pres-l.on");
  await expect(online).toHaveText("在线");
  await online.hover();
  const tip = page.locator("body > .ftip");
  await expect(tip).toBeVisible();
  await expect(tip).toHaveText(/^最后一次有流量：\d{4} 年 \d+ 月 \d+ 日 \d{2}:\d{2}:\d{2}（UTC\+8）/);
  await expect(tip).toContainText("最后一次有流量：");
  expect(await tip.evaluate(e => [getComputedStyle(e).position, e.parentElement === document.body])).toEqual(["fixed", true]);
  await page.mouse.move(1, 1);
  await expect(page.locator(".ftip")).toHaveCount(0);
  // 工具栏右边的在线人数也有提示。
  await page.locator(".tb .pres-sum").hover();
  await expect(page.locator("body > .ftip")).toHaveText(/^在线状态 \d+ (秒|分钟)前更新，每 30 秒一次$/);
});

test("排序:默认最新在前;点表头反过来;从未连接的不管正反都排最后", async ({ page }) => {
  await open(page);
  // 在线的排最前(IP 多的靠前),再按最后有流量的时间;两个没连过的在最后,顺序按创建时间(ivy 后加)。
  expect(await order(page)).toEqual(["bob", "alice", "chen", "dora", "evan", "fang", "jun", "gus", "ivy", "hana"]);
  await page.click("[data-act=pres-sort]");
  await expect(page.locator("th.th-sort")).toHaveAttribute("aria-sort", "ascending");
  // 反过来以后时间近的在后、最久没连的在前(gus 10 天前比 jun 6 天前更早),没连过的仍在最后。
  const asc = await order(page);
  expect(asc).toEqual(["gus", "jun", "fang", "evan", "dora", "chen", "alice", "bob", "hana", "ivy"]);
  await page.click("[data-act=pres-sort]");
  await expect(page.locator("th.th-sort")).toHaveAttribute("aria-sort", "descending");
  expect(await order(page)).toEqual(["bob", "alice", "chen", "dora", "evan", "fang", "jun", "gus", "ivy", "hana"]);
});

test("节点连不上:灰条写从几点起连不上,「看节点」去节点页;那台上的人写「连不上，可能还在线」", async ({ page }) => {
  await open(page, { presence: { ...LIVE, stale_nodes: [{ node_id: 2, since: at(3 * MIN) }] } });
  const note = page.locator(".pres-note");
  // 灰条里还有时钟图标和旗帜,整句是它第一个直接的 span 子元素。
  // 句首有旗帜图,文本前带一个空格。
  await expect(note.locator(":scope > span").first()).toHaveText(/^\s*东京 B 从 \d{2}:\d{2} 起连不上，这台上的在线状态停在 \d+ (秒|分钟)前。$/);
  await expect(note.locator("a")).toHaveAttribute("href", "#nodes/2");
  // 灰色,不是红色。
  const [r, g, b] = await note.evaluate(e => getComputedStyle(e).backgroundColor.match(/\d+/g).slice(0, 3).map(Number));
  expect(Math.max(r, g, b) - Math.min(r, g, b)).toBeLessThan(12);
  await expect(cell(page, "dora").locator(".sub")).toHaveText("东京 B 连不上，可能还在线");
  await note.locator("a").click();
  await expect(page).toHaveURL(/#nodes\/2$/);
});

test("读不到在线状态:工具栏写「在线状态读不到」,从不写「0 人在线」;格子里只有最后在线的时间", async ({ page }) => {
  await open(page, { presence: null });
  const sum = page.locator(".tb .pres-sum");
  await expect(sum).toHaveText("在线状态读不到");
  await expect(sum).toHaveAttribute("data-tip", "控制面这次没算出在线状态，刷新页面再试");
  await expect(page.locator(".card")).not.toContainText("0 人在线");
  await expect(page.locator(".card")).not.toContainText("从未连接");
  await expect(cell(page, "alice").locator(".pres-l.on")).toHaveCount(0);
  await expect(cell(page, "alice").locator(".pres-l")).toHaveText("刚刚");
  await expect(cell(page, "dora").locator(".pres-l")).toHaveText("3 分钟前");
  // 没有最后在线时间的人这一格是空的。
  await expect(cell(page, "hana")).toHaveText("");
});

test("抽屉「最近连接」:在线状态、最近几段、此刻的 IP、说明,以及单独的「订阅导入」一行", async ({ page }) => {
  await open(page, { activity: ACT.alice });
  await openUser(page, "alice");
  const block = page.locator("#drawer.open #ud-pres");
  await expect(block.locator("h3")).toHaveText("最近连接");
  await expect(block.locator(".udp-h .sub")).toHaveText(/^在线状态 \d+ 秒前更新$/);
  await expect(block.locator(".udp-now .pres-l.on")).toHaveText("在线");
  await expect(block.locator(".udp-now .sub")).toHaveText("2 个 IP · 洛杉矶 A");
  const rows = block.locator(".udp-r");
  await expect(rows).toHaveCount(4);
  await expect(rows.nth(0).locator(".udp-t")).toHaveText(/^今天 \d{2}:\d{2} – 现在$/);
  await expect(rows.nth(0).locator(".udp-n")).toHaveText("洛杉矶 A");
  await expect(rows.nth(0).locator(".udp-b")).toHaveText("1.2 GB");
  await expect(block.locator(".udp-ip .mono")).toHaveText(["203.0.113.24", "203.0.113.87"]);
  await expect(block.locator(".udp-foot")).toContainText("按节点上的流量算，每 30 秒更新一次。");
  await expect(block.locator(".udp-foot")).toContainText("客户端开着但没有流量，不算在线。");
  const pull = block.locator(".udp-pull");
  await expect(pull.locator("b")).toHaveText("订阅导入");
  await expect(pull.locator(".sub")).toHaveText(/^Clash Verge \d+ 分钟前拉取过 · Clash Meta$/);
  // 连过的人「最近连接」排最前;它的订阅导入已经有了,「发给他」标题右边不再重复。
  await expect(page.locator("#drawer.open .db > section").first()).toHaveClass(/ud-pres/);
  await expect(page.locator("#send-pull")).toHaveText("");
});

test("导入过却没连上:抽屉写「拉取过订阅，但之后一直没有流量」,「去体检」打开体检页", async ({ page }) => {
  await open(page, { activity: ACT.hana });
  await openUser(page, "hana");
  const block = page.locator("#drawer.open #ud-pres");
  await expect(block.locator(".udp-now .pres-l.never")).toHaveText("还没连接过");
  const hint = block.locator(".udp-hint");
  await expect(hint.locator("p")).toHaveText(/^Shadowrocket .+拉取过订阅，但之后一直没有流量。多半是客户端里没打开代理，或者连不上节点。$/);
  const go = hint.locator('a[href="#doctor"]');
  await expect(go).toHaveText("去体检");
  await go.click();
  await expect(page).toHaveURL(/#doctor$/);
});

test("最近连接读不到:那一块写「读不到最近连接」,错误原文收在「详情」里,抽屉其余照常", async ({ page }) => {
  await open(page, { activity: new Error("控制面没有这个接口") });
  await openUser(page, "alice");
  const block = page.locator("#drawer.open #ud-pres");
  await expect(block.locator(".udp-foot", { hasText: "读不到最近连接" })).toBeVisible();
  await expect(block.locator(".udp-r")).toHaveCount(0);
  const more = block.locator("details.sum-raw");
  await expect(more.locator("summary")).toHaveText("详情");
  await more.locator("summary").click();
  await expect(more.locator(".summary")).toContainText("控制面没有这个接口");
  await expect(block.locator(".udp-now .pres-l.on")).toHaveText("在线");
  await expect(block.locator(".udp-pull")).toBeVisible();
  await expect(page.locator("#drawer.open #send")).toBeVisible();
});
