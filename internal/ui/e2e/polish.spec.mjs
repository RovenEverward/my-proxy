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
  await expect.poll(() => page.locator("main").evaluate(m => m.scrollTop)).toBe(0);
  await expect(page.locator(".ph h1")).toHaveText("订阅");
  // 应用内从别的页(已经往下滚过)点侧栏进「订阅」,也停在顶上。
  await page.goto(env.origin + "/#rules"); await settle(page);
  await page.locator("main").evaluate(m => { m.scrollTop = 600; });
  await page.locator('.side a.nav[href="#sub"]').click(); await settle(page);
  await expect(page.locator("#f-title")).toBeVisible();
  await expect.poll(() => page.locator("main").evaluate(m => m.scrollTop)).toBe(0);
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
  // 光圈有过渡动画:轮询到它停下再读。
  await expect(async () => {
    const sh = await page.locator("#typed").evaluate(e => { const s = getComputedStyle(e); return s.boxShadow + "|" + s.outlineColor; });
    // 光圈得真的在(box-shadow 不是 none),再看它的颜色。
    expect(sh.split("|")[0]).not.toBe("none");
    const cs = colorsIn(sh);
    expect(cs.length).toBeGreaterThan(0);
    for (const c of cs) expect(neutral(c), c).toBe(true);
  }).toPass();
  const sh = await page.locator("#typed").evaluate(e => { const s = getComputedStyle(e); return s.boxShadow + "|" + s.outlineColor; });
  const cs = colorsIn(sh);
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

// 订阅名是 contenteditable 的编辑框,直接改写里面的文字就能标记改动。
const retitle = (page, text) => page.locator("#f-title").evaluate((el, t) => { el.textContent = t; el.dispatchEvent(new Event("input")); }, text);

test("N4 离开对话框文案带逗号", async ({ page }) => {
  await go(page, "sub");
  await retitle(page, "改一下");
  await page.locator('.side a.nav[href="#nodes"]').click();
  await expect(page.locator("#modal.open p").first()).toHaveText("离开「订阅」页，这些改动就会丢：");
});

test("N5 不拆「暂停分配」;说明段落避免末行孤字", async ({ page }) => {
  await openDelete(page);
  await expect(page.locator("#modal.open .nw")).toHaveText("「暂停分配」");
  expect(await page.locator("#modal.open .nw").evaluate(e => getComputedStyle(e).whiteSpace)).toBe("nowrap");
  expect(await page.locator("#modal.open .mbody p").first().evaluate(e => getComputedStyle(e).textWrapStyle)).toBe("pretty");
  await page.goto(env.origin + "/#sub"); await settle(page);
  // 刷新间隔说明按两个短句断行,不在句中拆。
  await expect(page.locator("#sec-look .h", { hasText: "填 0 由客户端决定" }).locator(".nw")).toHaveText(["客户端多久自动更新一次，", "填 0 由客户端决定。"]);
  for (const nw of await page.locator("#sec-look .h .nw").all()) expect(await nw.evaluate(e => getComputedStyle(e).whiteSpace)).toBe("nowrap");
  // 预览卡片里的用量和到期日各是一段不拆开的文字。
  const pv = page.locator("#pv-app .pv-m .nw");
  await expect(pv).toHaveCount(2);
  await expect(pv.first()).toContainText("不限流量");
  await expect(pv.last()).toContainText("永不过期");
  expect(await pv.first().evaluate(e => getComputedStyle(e).whiteSpace)).toBe("nowrap");
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
  await retitle(page, "改一下");
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
  await page.locator("main").evaluate(m => { m.scrollTop = m.scrollHeight; });
  await expect.poll(async () => { const r = await page.evaluate(() => ({ last: document.querySelectorAll(".zr-tpl")[2].getBoundingClientRect().bottom, sb: document.querySelector("#sb").getBoundingClientRect().top })); return r.last - r.sb; }).toBeLessThanOrEqual(0);
});

test("N10 分流卡片头「添加规则」吸顶", async ({ page }) => {
  await go(page, "rules");
  const ch = page.locator(".rp-card > .ch");
  await page.locator("main").evaluate(m => { m.scrollTop = 600; });
  await expect.poll(async () => { const r = await page.evaluate(() => ({ bar: document.querySelector("main > .top").getBoundingClientRect().bottom, ch: document.querySelector(".rp-card > .ch").getBoundingClientRect().top })); return Math.abs(r.ch - r.bar); }).toBeLessThan(2);
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

// 实际画中文用的字体(Chromium 报的 platform fonts):随程序带的 Noto Sans SC,是网页字体不是系统字体。
async function usedFonts(page, selector) {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("DOM.enable"); await cdp.send("CSS.enable");
  const { root } = await cdp.send("DOM.getDocument", { depth: -1 });
  const { nodeId } = await cdp.send("DOM.querySelector", { nodeId: root.nodeId, selector });
  const { fonts } = await cdp.send("CSS.getPlatformFontsForNode", { nodeId });
  await cdp.detach();
  return fonts;
}
const cjkByNoto = fonts => fonts.some(f => /Noto Sans SC/.test(f.familyName) && f.isCustomFont) && !fonts.some(f => /YaHei|雅黑/i.test(f.familyName));

test("M4 中文用随程序带的 Noto Sans SC(本地加载);页面、弹窗、抽屉、深色都一样", async ({ page }) => {
  const external = [];
  page.on("request", r => { if (!r.url().startsWith(env.origin) && /font|woff/i.test(r.url())) external.push(r.url()); });
  await go(page, "nodes");
  await page.evaluate(() => document.fonts.ready);
  expect(await page.evaluate(() => [...document.fonts].some(f => f.family.replace(/"/g, "") === "Noto Sans SC" && f.status === "loaded"))).toBe(true);
  expect(cjkByNoto(await usedFonts(page, ".ph h1"))).toBe(true);
  // 等宽栈里的中文也回退到 Noto Sans SC。
  await page.evaluate(() => { const c = document.createElement("code"); c.id = "cjk-mono"; c.textContent = "订阅 sub"; document.querySelector(".ph").append(c); });
  await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))); // 等排版完再问用了哪个字体
  expect(cjkByNoto(await usedFonts(page, "#cjk-mono"))).toBe(true);
  await page.goto(env.origin + "/#nodes/1"); await settle(page);
  await page.click("[data-act=node-rm]");
  await expect(page.locator("#modal.open")).toBeVisible();
  expect(cjkByNoto(await usedFonts(page, "#modal.open .mbody p"))).toBe(true);
  await page.click("[data-act=modal-cancel]");
  await page.goto(env.origin + "/#sub"); await settle(page);
  await page.click("[data-act=s5-rules]");
  await expect(page.locator("#drawer.open")).toBeVisible();
  expect(cjkByNoto(await usedFonts(page, "#drawer.open .rd-test label"))).toBe(true);
  await page.keyboard.press("Escape");
  await page.evaluate(() => { if (document.documentElement.dataset.theme !== "dark") document.querySelector("[data-act=theme]").click(); });
  await expect(page.locator("html[data-theme=dark]")).toHaveCount(1);
  expect(cjkByNoto(await usedFonts(page, ".ph h1"))).toBe(true);
  expect(external).toEqual([]);
});

// Windows 桌面窗口:右上角 138×52 是最小化/最大化/关闭。浏览器里用 mygo 注入的同名 CSS 变量模拟。
async function winFrame(page) {
  await page.evaluate(() => { const st = document.documentElement.style; st.setProperty("--mygo-titlebar-height", "52px"); st.setProperty("--mygo-titlebar-inset-right", "138px"); dispatchEvent(new Event("resize")); });
  await expect(page.locator("html.tb-right")).toHaveCount(1);
}
async function clearOfCaption(page, sel) {
  const box = page.locator(sel).first();
  await expect(box).toBeVisible();
  // 等滑入/淡入动画结束:轮询到位置不再变,再量它压不压窗口按钮。
  let r, vw = page.viewportSize().width;
  await expect(async () => {
    const a = await box.boundingBox();
    await page.waitForFunction(() => document.getAnimations().every(x => x.playState !== "running"));
    r = await box.boundingBox();
    expect(r).toEqual(a);
  }).toPass();
  const hitsCaption = r.y < 52 && r.x + r.width > vw - 138;
  expect(hitsCaption, `${sel} 压到了窗口按钮:${JSON.stringify(r)}`).toBe(false);
}

test("W1 Windows 窗口里抽屉和弹窗都不压右上角的窗口按钮", async ({ page }) => {
  await go(page, "nodes/1"); await winFrame(page);
  for (const act of ["node-online", "conn-new"]) {
    await page.click(`[data-act=${act}]`);
    await clearOfCaption(page, "#drawer.open");
    await page.keyboard.press("Escape"); await expect(page.locator("#drawer.open")).toHaveCount(0);
  }
  for (const act of ["node-rm", "node-install", "node-diagnose"]) {
    await page.click(`[data-act=${act}]`);
    await clearOfCaption(page, "#modal.open");
    await page.click("[data-act=modal-cancel]"); await expect(page.locator("#modal.open")).toHaveCount(0);
  }
  await page.goto(env.origin + "/#sub"); await settle(page); await winFrame(page);
  await page.click("[data-act=s5-rules]");
  await clearOfCaption(page, "#drawer.open");
  // 所有抽屉/弹窗容器共用 .drawer / .modal,规则对它们全部生效。
  const tops = await page.evaluate(() => [...document.querySelectorAll(".drawer,.modal")].map(e => e.classList.contains("drawer") ? parseFloat(getComputedStyle(e).top) : -1));
  for (const t of tops) if (t >= 0) expect(t).toBeGreaterThanOrEqual(52);
});

test("O1 在线名单读不到时说读不到，不说「没有在线用户」", async ({ page }) => {
  await go(page, "nodes/1");
  await page.click("[data-act=node-online]");
  await expect(page.locator("#drawer.open")).toContainText("读不到在线名单");
  await expect(page.locator("#drawer.open")).not.toContainText("没有在线用户");
});

test("O2 节点配置没开在线统计(409)时提示应用配置", async ({ page }) => {
  await page.route("**/api/nodes/1/online", r => r.fulfill({ status: 409, json: { error: "这台节点的配置还没打开在线统计，读不到在线用户；把配置应用到节点一次就能读到" } }));
  await go(page, "nodes/1");
  await page.click("[data-act=node-online]");
  await expect(page.locator("#drawer.open")).toContainText("还没打开在线统计");
  await expect(page.locator("#drawer.open [data-act=node-sync]")).toBeVisible();
});

// 控制面写进体检报告的每种结论句式各一条(TestSummaryCorpusCoversCheckup 对照 internal/control/checkup.go)。
const SUMMARY_CORPUS = [
  "读取配置漂移基准失败: sql: no rows",
  "融合怪 v0.2.12: canceled",
  "融合怪测评未完成: official fusion report status: cancelled",
  "节点地址在体检期间变化: 健康检查地址 203.0.113.10，网络检测地址 203.0.113.11",
  "网络检测未完成: context deadline exceeded",
  "测评未运行: context canceled",
  "体检中断，已保存完成部分: context canceled",
  "融合怪缺少可用测速结果；独立速度补测与融合怪报告分别保存",
  "独立速度补测未运行: 融合怪原有时间预算已耗尽",
  "独立速度补测未完成: dial tcp: i/o timeout",
  "独立速度补测已完成；融合怪原始状态与错误保持不变",
  "sing-box 配置漂移检查: unknown",
  "节点代理服务未运行",
  "节点实测配置哈希与最后下发的配置不一致",
  "节点没有已下发配置哈希，无法判断配置漂移",
  "节点配置哈希未读到，无法判断配置漂移",
  "xray 服务未运行",
  "sing-box 检查失败: exit status 1",
  "入站 1 证书检查失败: x509: certificate has expired",
  "入站 1 证书剩余 12 天",
  "网络检测工具不可用: mtr",
  "Cloudflare DNS failed: no such host",
  "Cloudflare 路由 skipped: mtr missing",
  "speedtest.example.com download 测速 failed: timeout",
  "ipv6 IP 检测 skipped: no ipv6",
  "读取入站失败: database is locked",
  "节点没有配置入站，未进行入口探测",
  "入站 1/入口 8 检测失败: dial tcp 203.0.113.10:443: connect: connection refused",
  "入站 1/入口 8 netflix 本次实测 unknown: parser_error: netflix_title_evidence",
  "报告超过 256 KiB，部分长文本、证书和入口详情已省略",
];
const RAW_TERMS = ["入站", "入口", "巡检", "下发", "排水", "核心", "比对", "融合怪", "哈希", "漂移", "Fusion", "fusion"];

test("D1 体检结论翻成人话:没有内部叫法和英文原话，原话收在「详情」里", async ({ page }) => {
  await page.route("**/api/nodes/1/diagnostics?from=node", r => r.fulfill({ json: { id: 1, node_id: 1, created_at: "2026-10-11T01:56:00Z", report: { summary: SUMMARY_CORPUS } } }));
  await go(page, "nodes/1");
  const items = await page.locator("ul.summary:not(.mono) li").allTextContents();
  expect(items.length).toBeGreaterThan(15);
  const allowed = /^(Netflix|YouTube|ChatGPT|Disney|xray|sing|box|ping|IPv4|IPv6|Cloudflare|speedtest|example|com)$/;
  for (const t of items) {
    for (const w of RAW_TERMS) expect(t, `「${t}」含内部叫法 ${w}`).not.toContain(w);
    for (const w of t.replace(/「[^」]*」/g, "").match(/[A-Za-z_][A-Za-z0-9_]{2,}/g) || []) expect(w, `「${t}」含英文原话`).toMatch(allowed);
  }
  expect(items).toContain("完整测评被中断，没有跑完");
  expect(items).not.toContain("完整测评没有跑完");
  expect(items.some(t => /^通过「.+」访问 Netflix：没测出结果$/.test(t))).toBe(true);
  const raw = page.locator("details.sum-raw");
  await expect(raw.locator("ul")).toBeHidden();
  await raw.locator("summary").click();
  await expect(raw).toContainText("parser_error: netflix_title_evidence");
});

test("C1 安装和体检的确认文案:全角括号，没有内部叫法，不再说「期间别关这个页面」", async ({ page }) => {
  await go(page, "nodes/1");
  for (const act of ["node-install", "node-diagnose"]) {
    await page.click(`[data-act=${act}]`);
    const p = (await page.locator("#modal.open .mbody p").first().innerText());
    expect(p).not.toMatch(/期间别关这个页面|Fusion|测速开|硬件测试关|SSH|[()]/);
    await page.click("[data-act=modal-cancel]"); await expect(page.locator("#modal.open")).toHaveCount(0);
  }
});
