// 用户页的写操作:每一条都和对应的 mpx user 命令对照,发到控制面的请求要一致。
import { test, expect } from "@playwright/test";
import { startEnv, normalize, login, cpFetch } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "hk", "--address", "203.0.113.10");
  await env.seed("node", "add", "jp", "--address", "203.0.113.11");
  await env.seed("inbound", "add", "--node", "1", "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.cloudflare.com", "--reality-dest", "www.cloudflare.com:443");
  await env.seed("inbound", "add", "--node", "2", "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.apple.com", "--reality-dest", "www.apple.com:443");
  // 每个动作一对 twin:cli-* 交给 CLI,ui-* 交给页面,起点一模一样。
  for (const p of ["cli", "ui"]) {
    await env.seed("user", "add", `${p}-edit`, "--limit", "10GB", "--expire", "2027-01-01", "--note", "旧备注");
    await env.seed("user", "add", `${p}-toggle`, "--limit", "20GB");
    await env.seed("user", "add", `${p}-renew`, "--expire", "2027-05-10", "--reset-strategy", "monthly");
    await env.seed("user", "add", `${p}-misc`, "--inbounds", "1");
  }
});
test.afterAll(async () => { await env?.stop(); });

async function openUser(page, name) {
  await page.goto(env.origin + "/#users/" + encodeURIComponent(name));
  await expect(page.locator("#drawer.open h2")).toHaveText(name);
}

test("新建用户:与 mpx user add 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-new", "ui-new"];
  // 默认套餐是 all_nodes,额外授权和它组合会被控制面摘掉套餐:先建一个只含
  // hk 的套餐,CLI 与 UI 都挂它,--inbounds 2(jp 的入站)才是真正的额外授权。
  await env.cli("plan", "add", "测试套餐", "--node", "hk");
  env.takeLog();
  await env.cli("user", "add", "cli-new", "--limit", "1.5TB", "--reset-strategy", "weekly", "--expire", "2027-02-03", "--inbounds", "2", "--plan", "测试套餐", "--note", "新同事");
  const fromCLI = normalize(env.takeLog(), names);
  expect(fromCLI).toHaveLength(1);

  await page.goto(env.origin + "/#users");
  await page.click("[data-act=user-new]");
  // 新用户默认挂默认套餐(all_nodes):额外授权不可选;切到普通套餐恢复。
  const inb = page.locator('#user-form input[name="inb"]');
  await expect(inb.first()).toBeDisabled();
  await expect(page.locator("#f-inb-h")).toHaveText("全部节点套餐不需要额外授权");
  await page.locator("#f-plans .plan", { hasText: "测试套餐" }).click();
  await expect(inb.first()).toBeEnabled();
  await page.fill("#f-name", "ui-new");
  await page.fill("#f-limit", "1.5TB");
  await page.fill("#f-expire", "2027-02-03");
  await page.click("#f-more summary"); // 清零周期、额外授权、备注收在「更多选项」里
  await page.locator("label.chip", { hasText: "每周" }).click();
  await page.locator('label.chip:has(input[name="inb"][value="2"])').click();
  await page.fill("#f-note", "新同事");
  // 表单里不再展示等价命令。
  await expect(page.locator("#user-form")).not.toContainText("mpx ");
  await page.click("#f-save");
  await expect(page.locator("#drawer.open h2")).toHaveText("ui-new");
  expect(normalize(env.takeLog(), names)).toEqual(fromCLI);
});

test("新建用户:控制面拒绝时原话报出来,不关表单", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#users");
  await page.click("[data-act=user-new]");
  await page.fill("#f-name", "bad name");
  await page.click("#f-save");
  await expect(page.locator("#f-err")).toContainText("用户名只能用字母");
  env.takeLog();
  await page.fill("#f-name", "ui-edit"); // 已存在
  await page.click("#f-save");
  await expect(page.locator("#f-err")).not.toBeEmpty();
  await expect(page.locator("#drawer.open #user-form")).toBeVisible();
});

test("编辑用户:与 mpx user edit 一致", async ({ page }) => {
  await login(page, env);
  // 两个 twin 建好时都在默认套餐(all_nodes,额外授权不可选),先挂到上面建的
  // 测试套餐再对照 --inbounds。
  await env.seed("user", "edit", "cli-edit", "--plan", "测试套餐");
  await env.seed("user", "edit", "ui-edit", "--plan", "测试套餐");
  const names = ["cli-edit", "ui-edit"];
  env.takeLog();
  await env.cli("user", "edit", "cli-edit", "--limit", "200GB", "--expire", "", "--reset-strategy", "monthly", "--inbounds", "1,2", "--note", "改过");
  const fromCLI = normalize(env.takeLog(), names);
  expect(fromCLI).toHaveLength(1);

  await openUser(page, "ui-edit");
  await page.click("[data-act=user-edit]");
  await page.fill("#f-limit", "200GB");
  await page.fill("#f-expire", "");
  await page.locator("label.chip", { hasText: "每月" }).click();
  await page.locator('label.chip:has(input[name="inb"][value="1"])').click();
  await page.locator('label.chip:has(input[name="inb"][value="2"])').click();
  await page.fill("#f-note", "改过");
  await page.click("#f-save");
  await expect(page.locator(".toast")).toContainText("已更新用户 ui-edit");
  expect(normalize(env.takeLog(), names)).toEqual(fromCLI);
});

test("停用要确认、取消不发请求;停用与启用和 CLI 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-toggle", "ui-toggle"];
  env.takeLog();
  await env.cli("user", "disable", "cli-toggle");
  const disableCLI = normalize(env.takeLog(), names);
  await env.cli("user", "enable", "cli-toggle");
  const enableCLI = normalize(env.takeLog(), names);

  await openUser(page, "ui-toggle");
  await page.click("[data-act=user-menu]"); await page.click("[data-act=user-toggle]");
  await expect(page.locator("#modal.open")).toContainText("停用 ui-toggle？");
  await page.click("[data-act=modal-cancel]");
  await page.waitForTimeout(300);
  expect(env.takeLog()).toEqual([]);

  await page.click("[data-act=user-menu]"); await page.click("[data-act=user-toggle]");
  await page.click("#okb");
  await expect(page.locator("#drawer.open .dh")).toContainText("已停用");
  expect(normalize(env.takeLog(), names)).toEqual(disableCLI);

  await page.click("[data-act=user-menu]"); await page.click("[data-act=user-toggle]"); // 启用不用确认
  await expect(page.locator("#drawer.open .dh")).toContainText("正常");
  expect(normalize(env.takeLog(), names)).toEqual(enableCLI);
});

test("续期 30 天:与 mpx user renew --days 30 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-renew", "ui-renew"];
  env.takeLog();
  await env.cli("user", "renew", "cli-renew", "--days", "30");
  const fromCLI = normalize(env.takeLog(), names);
  await openUser(page, "ui-renew");
  await page.click("[data-act=user-menu]"); await page.click("[data-act=user-renew]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("2027-06-09");
  expect(normalize(env.takeLog(), names)).toEqual(fromCLI);
});

test("重置流量、换一条新链接:要确认,和 CLI 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-misc", "ui-misc"];
  env.takeLog();
  await env.cli("user", "reset-traffic", "cli-misc");
  const resetCLI = normalize(env.takeLog(), names);
  await env.cli("user", "revoke-sub", "cli-misc");
  const revokeCLI = normalize(env.takeLog(), names);

  await openUser(page, "ui-misc");
  await page.click("[data-act=user-menu]"); await page.click("[data-act=user-reset]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已重置");
  expect(normalize(env.takeLog(), names)).toEqual(resetCLI);
  // 重置的确认框是异步收尾的:等它真的关掉再开换链接的框,否则前一个 async run
  // 结束时会把后开的框一起关掉(满负载下偶发,单独跑不复现)。
  await expect(page.locator("#modal.open")).toHaveCount(0);

  // 换链接本身(确认框、请求、拉取状态)单独在下面的用例里测,这里只比对和 CLI 一致。
  await page.click("[data-act=user-menu]"); await page.click("[data-act=user-revoke]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已换成新链接");
  expect(normalize(env.takeLog(), names)).toEqual(revokeCLI);
});

test("换一条新链接:确认框不是红色;取消不发请求;确认后 POST revoke-sub、链接变了、拉取状态清空", async ({ page }) => {
  await login(page, env);
  await env.seed("user", "add", "ui-relink");
  await openUser(page, "ui-relink");
  await expect(page.locator("#send-pull")).toHaveText("还没有客户端导入过");
  const before = await page.locator(".send-link").getAttribute("title");
  const qrBefore = await page.locator(".qr-tile img").getAttribute("src");
  // 客户端拉一次订阅 → 详情里写「Clash Verge 刚刚拉取过」。
  const pulled = await cpFetch(before.replace(/^https?:\/\/[^/]+/, env.cpURL), { headers: { "User-Agent": "clash-verge/v2.0.0" } });
  expect(pulled.status).toBe(200);
  await page.reload();
  await expect(page.locator("#send-pull")).toHaveText("Clash Verge 刚刚拉取过");

  const posts = [];
  // 只数控制面真收下的(被限流 429 时页面会照 Retry-After 自己重发一次)。
  page.on("response", r => { const q = r.request(); if (q.method() === "POST" && q.url().includes("/revoke-sub") && r.status() !== 429) posts.push(q.url()); });
  await page.click("[data-act=user-menu]");
  await expect(page.locator("#user-menu")).toBeVisible();
  await page.click("[data-act=user-revoke]");
  await expect(page.locator("#user-menu")).toBeHidden();
  const modal = page.locator("#modal.open");
  await expect(modal).toContainText("给 ui-relink 换一条新链接？");
  await expect(modal).toContainText("旧链接马上失效");
  await expect(modal).toContainText("重新扫码或粘贴新链接");
  // 不是删除:主按钮不用红色危险样式。
  await expect(page.locator("#okb")).toHaveText("换一条新链接");
  await expect(page.locator("#okb")).not.toHaveClass(/danger|dg/);
  await modal.locator("button", { hasText: "取消" }).click();
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await page.waitForTimeout(300);
  expect(posts).toHaveLength(0);
  await expect(page.locator(".send-link")).toHaveAttribute("title", before);

  await page.click("[data-act=user-menu]"); await page.click("[data-act=user-revoke]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已换成新链接，旧链接已失效");
  await expect.poll(() => posts.length).toBe(1);
  expect(posts[0]).toContain("/api/users/ui-relink/revoke-sub");
  await expect(page.locator(".send-link")).not.toHaveAttribute("title", before);
  await expect(page.locator(".qr-tile img")).not.toHaveAttribute("src", qrBefore);
  await expect(page.locator("#send-pull")).toHaveText("还没有客户端导入过");
  const old = await cpFetch(before.replace(/^https?:\/\/[^/]+/, env.cpURL));
  expect(old.status).toBe(404);
});

test("添加用户:套餐没有节点时灰色提示、创建置灰;换成全部节点能建;建好后同一抽屉显示「已创建」和「发给他」", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("mpx-theme", "dark"));
  await login(page, env);
  await env.seed("plan", "add", "空的套餐");
  await page.goto(env.origin + "/#users");
  await page.click("[data-act=user-new]");
  const empty = page.locator("#f-plans .plan", { hasText: "空的套餐" });
  await expect(empty.locator(".plan-n")).toHaveText("没有节点");
  await expect(page.locator("#f-plans .plan", { hasText: "默认套餐" }).locator(".plan-n")).toHaveText("2 个节点");
  // 数字用正文字体(不是等宽),和「个」之间一个普通空格。
  const pn = page.locator("#f-plans .plan", { hasText: "默认套餐" }).locator(".plan-n");
  expect(await pn.evaluate(el => el.textContent)).toBe("2 个节点");
  expect(await pn.evaluate(el => getComputedStyle(el).fontFamily)).toBe(await page.evaluate(() => getComputedStyle(document.body).fontFamily));
  await empty.click();
  const block = page.locator('#user-form .block:not([hidden])');
  await expect(block).toContainText("「空的套餐」里还没有节点");
  await expect(block.locator("a")).toHaveText("给「空的套餐」加节点");
  // 提示块是中性灰,不是红色。
  const bg = await block.evaluate(el => getComputedStyle(el).backgroundColor);
  expect(bg).not.toMatch(/rgb\((2[0-9]{2}), ([0-9]{1,2}), ([0-9]{1,2})\)/);
  await expect(page.locator("#f-save")).toBeDisabled();
  await expect(page.locator("#f-why")).toHaveText("选的套餐没有节点");
  await block.locator("button", { hasText: "换成「默认套餐」" }).click();
  await expect(page.locator("#user-form .block:not([hidden])")).toHaveCount(0);
  await expect(page.locator("#user-form .block").first()).toBeHidden(); // [hidden] 不能被 display 盖掉
  await expect(page.locator("#f-save")).toBeEnabled();
  await expect(page.locator("#f-why")).toBeHidden();
  await page.fill("#f-name", "ui-fresh");
  await page.click("#f-save");
  await expect(page.locator("#drawer.open h2")).toHaveText("ui-fresh");
  await expect(page.locator("#drawer .created")).toContainText("已创建 ui-fresh");
  await expect(page.locator("#send h3")).toHaveText("发给他");
  await expect(page.locator("#send-pull")).toHaveText("还没有客户端导入过");
  // 二维码:暗色主题下也放在白底上,图由本机按用户名画。
  expect(await page.evaluate(() => document.documentElement.dataset.theme)).toBe("dark");
  await expect(page.locator(".qr-tile")).toHaveCSS("background-color", "rgb(255, 255, 255)");
  await expect(page.locator(".qr-tile img")).toHaveAttribute("src", /^\/local\/qr\.svg\?u=ui-fresh&v=/);
  const ok = await page.evaluate(async () => { const i = document.querySelector(".qr-tile img"); await i.decode(); return i.naturalWidth > 0; });
  expect(ok).toBe(true);
  await expect(page.locator("#send [data-act=copy]").first()).toHaveText("复制订阅链接");
  await expect(page.locator("#send a[download]")).toHaveAttribute("href", /^\/local\/qr\.svg\?d=http/);
  await page.click("#send details.fmts summary");
  await expect(page.locator("#send .fmt")).toHaveCount(6);
  // 再打开一次就没有「已创建」那行了。
  await page.reload();
  await expect(page.locator("#drawer.open h2")).toHaveText("ui-fresh");
  await expect(page.locator("#drawer .created")).toHaveCount(0);
  // 本机向控制面取链接失败时,二维码改用页面手里的链接画,不留一张坏图。
  await page.route(/\/local\/qr\.svg\?u=/, r => r.fulfill({ status: 502, contentType: "application/json", body: "{\"error\":\"x\"}" }));
  // 退回的二维码也是本机画的:整个过程页面只请求本机界面,不碰任何外部服务。
  const hosts = new Set();
  page.on("request", q => hosts.add(new URL(q.url()).origin));
  await page.reload();
  await expect(page.locator(".qr-tile img")).toHaveAttribute("src", /\/local\/qr\.svg\?d=http/);
  expect(await page.evaluate(async () => { const i = document.querySelector(".qr-tile img"); await i.decode(); return i.naturalWidth > 0; })).toBe(true);
  expect([...hosts]).toEqual([env.origin]);
});

test("抽屉窄视口:没设订阅前缀的提示一行显示,不折行且完整文案在 title", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 800 });
  await login(page, env);
  await openUser(page, "ui-misc");
  const line = page.locator("#drawer .send-ip-t");
  await expect(line).toBeVisible();
  const full = "链接里是服务器 IP，设好订阅地址后新旧链接都能用";
  await expect(line).toHaveText(full);
  await expect(line).toHaveAttribute("title", full);
  const oneLine = await line.evaluate(el => {
    const lh = parseFloat(getComputedStyle(el).lineHeight) || 20;
    return el.scrollHeight <= lh * 1.5 && el.getClientRects().length === 1;
  });
  expect(oneLine).toBe(true);
  // 390 宽也放得下整句(不靠省略号):「去设置」可以换到下一行。
  expect(await line.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
  await expect(page.locator("#drawer .send-ip a", { hasText: "去设置" })).toBeVisible();
});

test("删除用户:要逐字输入用户名才放行,和 mpx user rm 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-misc", "ui-misc"];
  env.takeLog();
  await env.cli("user", "rm", "cli-misc");
  const fromCLI = normalize(env.takeLog(), names);

  await openUser(page, "ui-misc");
  await page.click("[data-act=user-menu]"); await page.click("[data-act=user-rm]");
  await expect(page.locator("#okb")).toBeDisabled();
  await page.fill("#typed", "ui-mis");
  await expect(page.locator("#okb")).toBeDisabled();
  await page.fill("#typed", "ui-misc");
  await page.click("#okb");
  await expect(page).toHaveURL(/#users$/);
  expect(normalize(env.takeLog(), names)).toEqual(fromCLI);
  const r = await cpFetch(env.cpURL + "/api/users/ui-misc", { headers: { Authorization: "Bearer " + env.token } });
  expect(r.status).toBe(404);
});

test("页面拿不到 token;未登录进不来", async ({ page, browser }) => {
  await login(page, env);
  const html = await page.content();
  expect(html).not.toContain(env.token);
  const meta = await page.evaluate(() => fetch("/ui/meta").then(r => r.text()));
  expect(meta).not.toContain(env.token);
  const ctx = await browser.newContext();
  const p2 = await ctx.newPage();
  const r = await p2.goto(env.origin + "/");
  expect(r.status()).toBe(401);
  await ctx.close();
});
