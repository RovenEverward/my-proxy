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
  await page.selectOption("#f-plan", { label: "测试套餐" });
  await expect(inb.first()).toBeEnabled();
  await page.fill("#f-name", "ui-new");
  await page.fill("#f-limit", "1.5TB");
  await page.fill("#f-expire", "2027-02-03");
  await page.locator("label.chip", { hasText: "每周" }).click();
  await page.locator('label.chip:has(input[name="inb"][value="2"])').click();
  await page.fill("#f-note", "新同事");
  await expect(page.locator("#f-cmd")).toHaveText("mpx user add ui-new --limit 1.5TB --reset-strategy weekly --expire 2027-02-03 --inbounds 2 --plan \"测试套餐\" --note \"新同事\"");
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
  await page.click("[data-act=user-toggle]");
  await expect(page.locator("#modal.open")).toContainText("mpx user disable ui-toggle");
  await page.click("[data-act=modal-cancel]");
  await page.waitForTimeout(300);
  expect(env.takeLog()).toEqual([]);

  await page.click("[data-act=user-toggle]");
  await page.click("#okb");
  await expect(page.locator("#drawer.open .dh")).toContainText("已停用");
  expect(normalize(env.takeLog(), names)).toEqual(disableCLI);

  await page.click("[data-act=user-toggle]"); // 启用不用确认
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
  await page.click("[data-act=user-renew]");
  await expect(page.locator(".toast")).toContainText("2027-06-09");
  expect(normalize(env.takeLog(), names)).toEqual(fromCLI);
});

test("重置流量、吊销订阅:要确认,和 CLI 一致;吊销后显示新地址", async ({ page }) => {
  await login(page, env);
  const names = ["cli-misc", "ui-misc"];
  env.takeLog();
  await env.cli("user", "reset-traffic", "cli-misc");
  const resetCLI = normalize(env.takeLog(), names);
  await env.cli("user", "revoke-sub", "cli-misc");
  const revokeCLI = normalize(env.takeLog(), names);

  await openUser(page, "ui-misc");
  await page.click("[data-act=user-reset]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已重置");
  expect(normalize(env.takeLog(), names)).toEqual(resetCLI);

  const before = await page.locator(".links code").first().textContent();
  await page.click("[data-act=user-revoke]");
  await expect(page.locator("#modal.open")).toContainText("旧地址立刻 404");
  await page.click("#okb");
  await expect(page.locator(".links code").first()).not.toHaveText(before);
  expect(normalize(env.takeLog(), names)).toEqual(revokeCLI);
  const old = await cpFetch(before.replace(/^https?:\/\/[^/]+/, env.cpURL));
  expect(old.status).toBe(404);
});

test("删除用户:要逐字输入用户名才放行,和 mpx user rm 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-misc", "ui-misc"];
  env.takeLog();
  await env.cli("user", "rm", "cli-misc");
  const fromCLI = normalize(env.takeLog(), names);

  await openUser(page, "ui-misc");
  await page.click("[data-act=user-rm]");
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
