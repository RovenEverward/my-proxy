// v0.9.12 设计验收的小修:每条一个回归测试(M1–M3、N1–N11)。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env;
const cp = (p, init = {}) => cpFetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "tokyo-a", "--address", "203.0.113.10", "--ssh-port", "1");
  await env.seed("node", "edit", "1", "--region", "JP", "--city", "东京");
  await env.seed("inbound", "add", "--node", "1", "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.microsoft.com", "--reality-dest", "www.microsoft.com:443");
  await env.seed("user", "add", "alice");
});
test.afterAll(async () => { await env?.stop(); });
const settle = page => expect(page.locator("html[data-loading]")).toHaveCount(0);
async function go(page, hash) { await login(page, env); await page.goto(env.origin + "/#" + hash); await settle(page); }
// 颜色字符串里 r、g、b 三个分量相近(中性灰/墨色)。认 rgb()/rgba() 和 color(srgb …) 两种写法。
const chans = c => { const m = (c.match(/[\d.]+/g) || []).map(Number); return /^color\(/.test(c) ? m.slice(0, 3).map(x => x * 255) : m.slice(0, 3); };
const neutral = c => { const m = chans(c); return m.length === 3 && Math.abs(m[0] - m[1]) < 6 && Math.abs(m[1] - m[2]) < 6; };
const colorsIn = s => s.match(/(?:rgba?|color)\([^)]*\)/g) || [];
async function openDelete(page) {
  await go(page, "nodes/1");
  await page.click("[data-act=node-rm]");
  await expect(page.locator("#modal.open #okb")).toBeVisible();
}

test("M1 普通 #sub 不自动滚动，页标题露着;#sub/clients 滚到段且段标题不在顶栏下", async ({ page }) => {
  await go(page, "sub");
  await expect(page.locator("#f-title")).toBeVisible();
  await page.waitForTimeout(300);
  expect(await page.locator("main").evaluate(m => m.scrollTop)).toBe(0);
  await expect(page.locator(".ph h1")).toHaveText("订阅");
  await page.goto(env.origin + "/#sub/clients"); await settle(page);
  await expect.poll(() => page.locator("main").evaluate(m => m.scrollTop)).toBeGreaterThan(0);
  const r = await page.evaluate(() => ({ bar: document.querySelector("main > .top").getBoundingClientRect().bottom, sec: document.querySelector("#sec-clients").getBoundingClientRect().top }));
  expect(r.sec).toBeGreaterThanOrEqual(r.bar);
});

test("M2 删除按钮禁用时灰底浅灰字、不透明;可点时才是实心红", async ({ page }) => {
  await openDelete(page);
  const st = () => page.locator("#okb").evaluate(b => { const s = getComputedStyle(b); return { bg: s.backgroundColor, op: s.opacity, dis: b.disabled }; });
  const off = await st();
  expect(off.dis).toBe(true);
  expect(off.op).toBe("1");
  expect(neutral(off.bg)).toBe(true);
  await page.fill("#typed", "tokyo-a");
  await expect(page.locator("#okb")).toBeEnabled();
  // 背景有过渡动画:等它停在实心红上。
  await expect.poll(async () => { const [r, g] = chans((await st()).bg); return r - g; }).toBeGreaterThan(80);
});

test("M3 输入框聚焦光圈是中性色，不是蓝色(删除确认里的输入框)", async ({ page }) => {
  await openDelete(page);
  await page.focus("#typed");
  await page.waitForTimeout(500); // 光圈有过渡动画,等它停下再读
  const sh = await page.locator("#typed").evaluate(e => { const s = getComputedStyle(e); return s.boxShadow + "|" + s.outlineColor; });
  const cs = colorsIn(sh);
  expect(cs.length).toBeGreaterThan(0);
  for (const c of cs) expect(neutral(c), c).toBe(true);
});

test("N1 侧栏版本号去掉伪版本尾巴", async ({ page }) => {
  await page.route("**/ui/meta", async r => { const res = await r.fetch(); const j = await res.json(); await r.fulfill({ response: res, json: { ...j, cli_version: "v0.9.11-0.20261018055517-abcdef123456" } }); });
  await go(page, "overview");
  await expect(page.locator(".brand small")).toHaveText("v0.9.11");
});

test("N2 规则行的去向箭头是图标，不是文字「→」", async ({ page }) => {
  await go(page, "rules");
  const a = page.locator(".rp-list .rp-row .rp-a").first();
  await expect(a.locator("svg")).toHaveCount(1);
  expect((await a.textContent()).trim()).toBe("");
  expect(await page.locator(".rp-list").textContent()).not.toContain("→");
});

test("N3 分流页说明不重复卡片标题，用「匹配」，句末有「。」", async ({ page }) => {
  await go(page, "rules");
  const p = page.locator(".ph p");
  await expect(p).toHaveText("决定每个网站怎么走：直连、走代理还是拦截。改完，客户端更新一次订阅就生效。");
  await expect(page.locator("main")).not.toContainText("比对");
});

test("N4 离开对话框文案带逗号", async ({ page }) => {
  await go(page, "sub");
  await page.fill("#f-title", "改一下");
  await page.locator('.side a.nav[href="#nodes"]').click();
  await expect(page.locator("#modal.open p").first()).toHaveText("离开「订阅」页，这些改动就会丢：");
});

test("N5 不拆「暂停分配」;说明段落避免末行孤字", async ({ page }) => {
  await openDelete(page);
  await expect(page.locator("#modal.open .nw")).toHaveText("「暂停分配」");
  expect(await page.locator("#modal.open .nw").evaluate(e => getComputedStyle(e).whiteSpace)).toBe("nowrap");
  expect(await page.locator("#modal.open .mbody p").first().evaluate(e => getComputedStyle(e).textWrapStyle)).toBe("pretty");
  await page.goto(env.origin + "/#sub"); await settle(page);
  await expect(page.locator("#sec-addr")).toContainText("客户端多久自动更新一次，填 0 由客户端决定。");
  const hint = page.locator("#sec-addr .h", { hasText: "填 0 由客户端决定" });
  expect(await hint.evaluate(e => getComputedStyle(e).textWrapStyle)).toBe("balance");
  // 预览里格式说明(如「带完整分流和策略组」)整段不拆行。
  await expect(page.locator(".pv-f .nw")).toHaveCount(1);
  expect(await page.locator(".pv-f .nw").evaluate(e => getComputedStyle(e).whiteSpace)).toBe("nowrap");
});

test("N6 删除确认标题里旗帜按正常空格排，不吃弹性间距", async ({ page }) => {
  await openDelete(page);
  const h2 = page.locator("#modal.open h2");
  await expect(h2.locator(".flag-w, .flag-code")).toHaveCount(1);
  // 旗帜不是 h2 的直接子元素(flex gap 只加在图标和文字之间)。
  expect(await h2.evaluate(h => [...h.children].some(c => c.matches(".flag-w, .flag-code")))).toBe(false);
  const gap = await h2.evaluate(h => { const f = h.querySelector(".flag-w, .flag-code").getBoundingClientRect(); const r = document.createRange(); const t = h.querySelector(".mt-t").firstChild; r.setStart(t, 0); r.setEnd(t, t.length); return f.left - r.getBoundingClientRect().right; });
  expect(gap).toBeLessThan(8);
});

test("N7 界面不说「核心」:节点详情和删除确认用「代理程序」", async ({ page }) => {
  await go(page, "nodes/1");
  await expect(page.locator(".node-danger .sub")).toHaveText("它上面的连接方式会一起删掉，订阅里不再有它。节点上已装的代理程序和配置文件会留着。");
  await expect(page.locator("main")).not.toContainText("核心");
  await page.click("[data-act=node-rm]");
  await expect(page.locator("#modal.open")).not.toContainText("核心");
});

test("N8 节点列表里没有值的格子留空，不用「–」「—」", async ({ page }) => {
  await go(page, "nodes");
  const cells = await page.locator("tbody td").allTextContents();
  for (const c of cells) expect(c.trim()).not.toMatch(/^[–—-]$/);
});

test("N9 有改动时滚到底，保存条不压住最后一行底稿(Xray)", async ({ page }) => {
  await go(page, "sub");
  await page.click("[data-act=s5-adv]");
  await expect(page.locator(".zr-tpl")).toHaveCount(3);
  await page.fill("#f-title", "改一下");
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
  await page.locator("main").evaluate(m => { m.scrollTop = m.scrollHeight; });
  await page.waitForTimeout(200);
  const r = await page.evaluate(() => ({ last: document.querySelectorAll(".zr-tpl")[2].getBoundingClientRect().bottom, sb: document.querySelector("#sb").getBoundingClientRect().top }));
  expect(r.last).toBeLessThanOrEqual(r.sb);
});

test("N10 分流卡片头「添加规则」吸顶", async ({ page }) => {
  await go(page, "rules");
  const ch = page.locator(".rp-card > .ch");
  await page.locator("main").evaluate(m => { m.scrollTop = 600; });
  await page.waitForTimeout(200);
  const r = await page.evaluate(() => ({ bar: document.querySelector("main > .top").getBoundingClientRect().bottom, ch: document.querySelector(".rp-card > .ch").getBoundingClientRect().top }));
  expect(Math.abs(r.ch - r.bar)).toBeLessThan(2);
  await expect(ch.locator("[data-act=rp-add]")).toBeInViewport();
});

test("N11 订阅地址占位文字带「例如」，用浅灰", async ({ page }) => {
  await go(page, "sub");
  const pf = page.locator("#f-prefix");
  await expect(pf).toHaveAttribute("placeholder", "例如 https://sub.example.com");
  const c = await pf.evaluate(e => [getComputedStyle(e, "::placeholder").color, getComputedStyle(document.documentElement).getPropertyValue("--faint").trim()]);
  const hex = c[1].replace("#", ""); const rgb = [0, 2, 4].map(i => parseInt(hex.slice(i, i + 2), 16));
  expect(c[0].match(/\d+/g).slice(0, 3).map(Number)).toEqual(rgb);
});
