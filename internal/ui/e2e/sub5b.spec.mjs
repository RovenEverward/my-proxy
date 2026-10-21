// 「订阅」页 step5b：发给用户的链接、编辑框里的「用户名」标签、订阅信息三栏、客户端对照表和识别规则抽屉。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env;
const cp = (p, init = {}) => cpFetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
const settings = async () => (await cp("/api/settings")).json();
const putSettings = async patch => {
  const r = await cp("/api/settings", { method: "PUT", body: JSON.stringify({ ...await settings(), ...patch }) });
  expect(r.ok).toBe(true);
};
async function open(page) {
  await login(page, env);
  await page.goto(env.origin + "/#sub");
  await expect(page.locator("#f-title")).toBeVisible();
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
}
const save = page => page.locator("[data-act=s5-save]").click();
// 订阅名是 contenteditable 的编辑框,{USERNAME} 在里面是一块「用户名」标签;直接改写里面的文字。
const setTitle = (page, text) => page.locator("#f-title").evaluate((el, t) => { el.textContent = t; el.dispatchEvent(new Event("input")); }, text);
const linkOf = (page, expected) => expect(page.locator(".r-url")).toHaveText(expected);

test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("user", "add", "alice", "--limit", "100GB", "--expire", "2027-01-01");
  await env.seed("user", "add", "bob", "--limit", "200GB", "--expire", "2028-01-01");
});
test.afterAll(async () => { await env?.stop(); });
test.beforeEach(async () => { await putSettings({ sub_url_prefix: "", profile_title: "{USERNAME}", update_interval: 24, support_link: "", rules: null }); });

// 完整链接按最后一个 /sub/ 切成「开头」和「每人自己的部分」;没填链接开头时开头就是这次请求的地址
// (控制面按请求的 Host 拼;经 mpx ui 转发时 Host 是 mpx ui 的地址,端口与控制面不同),并有一条灰色说明。
test("链接卡片给出完整链接;没填链接开头时用控制面地址开头并说明", async ({ page }) => {
  await open(page);
  // 页面请求的那条(经 mpx ui)核对链接卡片;再直接问一次控制面,比对每人自己的那一段。
  const alice = await (await page.request.get(env.origin + "/api/users/alice/links")).json();
  const cpAlice = await (await cp("/api/users/alice/links")).json();
  await expect(page.locator(".r-link [data-act=copy]")).toHaveAttribute("data-text", alice.auto);
  await linkOf(page, alice.auto);
  // 经 mpx ui 转发和直接问控制面拿到的主机不同(端口不同):控制面按请求的 Host 拼,转发过来的
  // 那一头不是控制面自己的地址。开头各是各的,每人自己的那一段(令牌)一样。
  const tokenOf = auto => auto.slice(auto.lastIndexOf("/sub/") + 5);
  expect(alice.auto.startsWith(alice.base + "/sub/")).toBe(true);
  expect(tokenOf(alice.auto)).toBe(tokenOf(cpAlice.auto));
  // 只加的 base 字段就是这条链接的开头;直连控制面时它就是控制面的地址,经 mpx ui 时不是页面地址。
  expect(cpAlice.base).toBe(env.cpURL);
  // 页面把开头按控制面这次用的那个画出来,经 mpx ui 时它不一定等于页面地址。
  const pre = alice.auto.slice(0, alice.auto.lastIndexOf("/sub/"));
  await expect(page.locator(".lk-pre")).toHaveText(pre);
  await expect(page.locator(".lk-tail")).toHaveText(alice.auto.slice(pre.length));
  const note = page.locator("#pfx-slot");
  await expect(note).toContainText("没填，链接用服务器 IP");
  await expect(note).toContainText("拿到链接的人都能看到。");

  await page.fill("#f-prefix", "https://sub.example.com");
  await expect(note).toHaveText("客户端要能访问到这个地址。");
  const expected = "https://sub.example.com/sub/" + alice.auto.split("/sub/")[1];
  await linkOf(page, expected);
  await expect(page.locator(".lk-pre")).toHaveText("https://sub.example.com");
  await expect(page.locator(".r-link [data-act=copy]")).toHaveAttribute("data-text", expected);
});

// 编辑框里「用户名」是整块标签;存进去的仍然只有 {USERNAME},每个人看到自己的名字。
test("订阅名编辑框把 {USERNAME} 显示成「用户名」标签，保存后仍存 {USERNAME}", async ({ page }) => {
  await open(page);
  const title = page.locator("#f-title");
  await expect(title.locator(".tk")).toHaveText("用户名");
  await expect(title).toHaveText(/^用户名$/);
  // 有「用户名」时框下不出「+ 用户名」。
  await expect(page.locator("#title-tok")).toBeEmpty();

  // 在标签后面接着打字:光标放到末尾再打。
  await title.click();
  await page.keyboard.press("Control+End");
  await page.keyboard.type(" 的订阅");
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
  await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  expect((await settings()).profile_title).toBe("{USERNAME} 的订阅");
  await expect(page.locator("#f-title .tk")).toHaveText("用户名");
});

// 订阅名里没有「用户名」时,框下给「+ 用户名」,点了插回标签,按钮随即消失。
test("订阅名去掉「用户名」后出现「+ 用户名」，点一下插回去", async ({ page }) => {
  await open(page);
  await setTitle(page, "MPX 订阅");
  const add = page.locator("#title-tok [data-act=b5-tok]");
  await expect(add).toHaveText("用户名");
  await add.click();
  await expect(page.locator("#f-title .tk")).toHaveText("用户名");
  await expect(page.locator("#title-tok")).toBeEmpty();
});

// 不同客户端拿到什么:按名字列,最后一行永远是「其他客户端」;识别规则收进抽屉,页面上不列正则。
test("客户端对照表里有 Clash Verge,最后一行是其他客户端", async ({ page }) => {
  await open(page);
  const rows = page.locator("#b5-tbl .b5-tr");
  await expect(rows.filter({ hasText: "Clash Verge" })).toHaveCount(1);
  await expect(rows.last()).toHaveClass(/b5-other/);
  expect((await rows.last().locator(".b5-apps").textContent()).startsWith("其他客户端")).toBe(true);
  const heading = page.locator("#b5-tbl .b5-th");
  await expect(heading).toHaveText(/客户端\s*拿到/);
});

// 识别规则抽屉:输入客户端标识就说会拿到什么;不看正则。「不分大小写」不在这一步里。
test("识别规则抽屉的试框说「会拿到」，没有「不分大小写」勾选框", async ({ page }) => {
  await open(page);
  await page.click("[data-act=s5-rules]");
  await expect(page.locator("#drawer.open .dt h2")).toHaveText("识别规则");
  await page.fill("#rd-ua", "clash-verge/v2.0.3");
  // 对上的不一定是第 0 条(内置的前几条都可能先接住这个标识):标准的是唯一一行 hit,
  // 它的格式框就是试框说的那个格式。界面会把正则说成人话(「|」说成「或」),不比对正则原文。
  const row = page.locator(".rd-row.hit");
  await expect(row).toHaveCount(1);
  await expect(page.locator("#rd-hit .b5-res")).toContainText("会拿到 Clash Meta 配置");
  await expect(row.locator("[data-rf]")).toHaveValue("clash-meta");
  await expect(page.getByRole("checkbox")).toHaveCount(0);
  await expect(page.locator("#drawer.open")).not.toContainText("不分大小写");
});

// 链接开头的格式不对:红字落在这一栏下面,用「链接开头」的说法,不冒出「订阅地址」。
test("链接开头填错时字段下出红字，说的是「链接开头」而不是「订阅地址」", async ({ page }) => {
  await open(page);
  await page.fill("#f-prefix", "ftp://x");
  await save(page);
  const err = page.locator("#pfx-err");
  await expect(err).toContainText("链接开头");
  await expect(err).not.toContainText("订阅地址");
  // 红字要比别的字显眼:用的是 --bad(浅色 #dc2626,深色 #f2555a),输入框里的正常文字不是这个颜色。
  const [errColor, inputColor, bad] = await err.evaluate(e => [getComputedStyle(e).color, getComputedStyle(document.querySelector("#f-prefix")).color, getComputedStyle(document.documentElement).getPropertyValue("--bad").trim()]);
  const rgb = c => "rgb(" + [1, 3, 5].map(i => parseInt(c.slice(i, i + 2), 16)).join(", ") + ")";
  expect(errColor).toBe(rgb(bad));
  expect(inputColor).not.toBe(errColor);
  await expect(page.locator("#sb [role=alert]")).toHaveText("没保存上：链接开头的格式不对，改好再保存。");
  await expect(page.locator("#f-prefix")).toBeFocused();
  await expect(page.locator("#f-prefix")).toHaveValue("ftp://x");
});

// 交接稿的四段结构与键盘提示；截图沿用同一套真实测试控制面。
test.describe("订阅页定稿", () => {
  test.use({ viewport: { width: 1024, height: 1400 }, deviceScaleFactor: 2 });
  test("四段平级，没有客户端预览，提示可以用键盘访问", async ({ page }) => {
    await putSettings({ sub_url_prefix: "http://203.0.113.10:8080", profile_title: "{USERNAME} 的订阅", update_interval: 12, support_link: "https://sub.example.com/support" });
    await page.route("**/ui/meta", async r => {
      const response = await r.fetch(), meta = await response.json();
      await r.fulfill({ response, json: { ...meta, server: "203.0.113.10:8080" } });
    });
    await open(page);
    await expect(page.locator(".r-page > section h2")).toHaveText(["订阅链接", "订阅信息", "客户端格式", "高级"]);
    expect(await page.locator(".r-page > section").evaluateAll(s => s.map(x => x.id))).toEqual(["sec-link", "sec-info", "sec-clients", "sec-adv"]);
    await expect(page.locator(".r-page > .card, .r-page .nb, .lk-key, .b5-cap")).toHaveCount(0);
    // 客户端预览、客户端切换和字段↔预览联动都不在了。
    await expect(page.locator("#pv, #pv-app, .pv-app, [data-act=s5-pv], [data-hl], #sec-look, .b5-f, [data-p]")).toHaveCount(0);
    // 订阅信息三栏和「链接开头」同一种写法:左对齐、同宽。
    const boxes = await Promise.all(["#f-prefix", "#f-title", "#f-int", "#f-help"].map(id => page.locator(id).boundingBox()));
    for (const b of boxes) { expect(b.x).toBe(boxes[0].x); expect(b.width).toBe(boxes[0].width); }
    // 只有订阅名和刷新间隔带提示;帮助链接只有占位。
    await expect(page.locator('label[for="f-help"] .r-tip')).toHaveCount(0);
    await expect(page.locator("#f-help")).toHaveAttribute("placeholder", "例如 https://help.example.com");
    const prefix = page.locator("#f-prefix"), link = page.locator("#lk");
    await prefix.focus();
    await expect(link).toHaveClass(/hl/);
    await prefix.fill("https://sub.example.com");
    await expect(link).toHaveClass(/hl/);
    await expect(link.locator(".lk-pre")).toHaveText("https://sub.example.com");
    await page.locator("#f-int").focus();
    const tips = page.locator(".r-tip"), refreshTip = page.locator('label[for="f-int"] .r-tip');
    await expect(tips).toHaveCount(3);
    await expect(refreshTip).toHaveAttribute("tabindex", "0");
    await expect(refreshTip).toHaveAttribute("aria-label", "客户端多久自动更新一次订阅，单位小时。填 0 由客户端决定。");
    await expect(page.locator('label[for="f-title"] .r-tip')).toHaveAttribute("aria-label", /^在客户端的订阅列表里显示的名字。/);
    await page.keyboard.press("Shift+Tab");
    await expect(refreshTip).toBeFocused();
    expect(await refreshTip.evaluate(e => getComputedStyle(e, "::after").content)).toContain("单位小时");
    // 截图只在显式指定目录时输出，不把本机路径写入仓库。
    const out = process.env.MPX_SUB_REDESIGN_SHOTS;
    await page.locator("[data-act=s5-discard]").click();
    await expect(page.locator(".toast")).toBeHidden();
    await page.mouse.move(1, 1);
    await page.locator("#f-title").blur();
    for (const theme of ["light", "dark"]) {
      await page.evaluate(t => { document.documentElement.dataset.theme = t; }, theme);
      if (out) {
        await page.evaluate(() => Promise.all(document.getAnimations().map(a => a.finished.catch(() => {}))));
        await page.screenshot({ path: `${out}/sub-${theme}.png`, fullPage: true });
      }
    }
    await page.click("[data-act=s5-adv]");
    await expect(page.locator(".zr-tpl")).toHaveCount(3);
    await expect(page.locator("#sec-adv .r-tip")).toHaveAttribute("aria-label", /分流规则、策略组和 DNS/);
    await page.mouse.move(1, 1);
    if (out) await page.screenshot({ path: `${out}/sub-advanced-dark.png`, fullPage: true });
    await refreshTip.hover();
    if (out) await page.screenshot({ path: `${out}/sub-tip-dark.png`, fullPage: true });
    // 自定义底稿只改测试控制面的正文空白，结束时恢复内置。
    const builtin = await (await cp("/api/settings/templates/sing-box")).text();
    try {
      expect((await cp("/api/settings/templates/sing-box", { method: "PUT", headers: { "Content-Type": "text/plain" }, body: builtin + "\n" })).ok).toBe(true);
      await page.goto(env.origin + "/#sub/templates");
      const pill = page.locator('.zr-tpl[data-f="sing-box"] .pill.on');
      await expect(pill).toHaveText("自定义");
      const colors = await pill.evaluate(e => {
        const probe = document.createElement("span"); probe.style.color = "var(--ink)"; e.append(probe);
        const pair = [getComputedStyle(e, "::before").backgroundColor, getComputedStyle(probe).color]; probe.remove(); return pair;
      });
      expect(colors[0]).toBe(colors[1]);
      await page.mouse.move(1, 1);
      if (process.env.MPX_SUB_CUSTOM_SHOT) await page.screenshot({ path: process.env.MPX_SUB_CUSTOM_SHOT, fullPage: true });
    } finally {
      expect((await cp("/api/settings/templates/sing-box", { method: "DELETE" })).ok).toBe(true);
    }
  });
});

// 验收图复用测试控制面，只有指定输出目录时运行。
test("订阅信息验收图：平台、主题、尺寸与只读", async ({ browser }) => {
  const out = process.env.MPX_SUB_INFO_SHOTS;
  test.skip(!out, "仅在生成验收图时运行");
  test.setTimeout(120_000);
  await putSettings({ sub_url_prefix: "https://sub.example.com", profile_title: "{USERNAME} 的订阅", update_interval: 12, support_link: "https://help.example.com" });
  for (const platform of ["MacIntel", "Win32"]) for (const theme of ["light", "dark"]) {
    const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2 });
    try {
      await context.addInitScript(({ platform, theme }) => {
        Object.defineProperty(navigator, "platform", { value: platform });
        Object.defineProperty(navigator, "userAgentData", { value: { platform: platform === "MacIntel" ? "macOS" : "Windows" } });
        localStorage.setItem("mpx-theme", theme);
      }, { platform, theme });
      const page = await context.newPage();
      let readonly = false;
      await page.route("**/ui/meta", async r => {
        const response = await r.fetch(), meta = await response.json();
        await r.fulfill({ response, json: { ...meta, server: "https://203.0.113.10:8080", ...(readonly ? { allow: ["GET /api"] } : {}) } });
      });
      await open(page);
      await page.evaluate(() => document.fonts.ready);
      const stem = `${out}/${platform === "MacIntel" ? "mac" : "win"}-${theme}`;
      for (const [width, height] of [[1280, 800], [900, 720], [640, 720]]) {
        await page.setViewportSize({ width, height });
        await page.locator("main").evaluate(e => { e.scrollTop = 0; });
        await page.screenshot({ path: `${stem}-${width}x${height}.png` });
      }
      await page.setViewportSize({ width: 1280, height: 800 });
      // main 是滚动容器，整页图把视口拉高到四段内容的实际高度。
      await page.setViewportSize({ width: 1280, height: await page.locator("main").evaluate(e => e.scrollHeight + e.getBoundingClientRect().top + 40) });
      await page.mouse.move(1, 1);
      await page.screenshot({ path: `${stem}-full.png`, fullPage: true });
      await page.locator('label[for="f-int"] .r-tip').hover();
      await page.screenshot({ path: `${stem}-tip.png`, fullPage: true });
      await setTitle(page, "示例订阅");
      await expect(page.locator("#title-tok button")).toBeVisible();
      await page.mouse.move(1, 1);
      await page.screenshot({ path: `${stem}-add-username.png`, fullPage: true });
      await page.locator("[data-act=s5-discard]").click();
      readonly = true;
      await page.reload();
      await expect(page.locator("#f-title")).toHaveAttribute("contenteditable", "false");
      await expect(page.locator("#sb")).toBeEmpty();
      await page.evaluate(() => document.fonts.ready);
      await page.setViewportSize({ width: 1280, height: await page.locator("main").evaluate(e => e.scrollHeight + e.getBoundingClientRect().top + 40) });
      await page.screenshot({ path: `${stem}-readonly.png`, fullPage: true });
    } finally { await context.close(); }
  }
});
