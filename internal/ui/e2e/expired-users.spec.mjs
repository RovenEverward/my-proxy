import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch, normalize } from "./env.mjs";

let env;
const GB = 2 ** 30;
const dateAt = days => { const d = new Date(); return new Date(d.getFullYear(), d.getMonth(), d.getDate() + days, 23, 59, 59).toISOString(); };
const cp = async (p, method = "GET", body) => {
  const r = await cpFetch(env.cpURL + p, { method, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json" }, ...(body ? { body: JSON.stringify(body) } : {}) });
  expect(r.ok).toBe(true); return r.status === 204 || method === "DELETE" ? null : r.json();
};
const row = (page, name) => page.locator(`tr[data-href="#users/${name}"]`);
async function add(name, days, used = 0, enabled = true) {
  await cp("/api/users", "POST", { username: name, enabled, expire_at: days === null ? null : dateAt(days), data_limit: 50 * GB });
  const u = await cp(`/api/users/${name}`);
  await cp(`/api/users/${name}`, "PUT", { ...u, used_traffic: used * GB });
}
async function open(page, hash = "overview") {
  await login(page, env);
  await page.goto(`${env.origin}/#${hash}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
}
async function expired(page) {
  await page.goto(env.origin + "/#users");
  await page.locator('[data-act=user-filter][data-status=expired]').click();
  await expect(page.locator("table.ut .td-pick input").first()).toBeVisible();
}
async function modalRenew(page, name) {
  await page.goto(`${env.origin}/#users/${name}`);
  await expect(page.locator("#drawer.open h2")).toHaveText(name);
  await page.click('[data-act=user-menu]');
  await page.click('[data-act=user-renew]');
  await expect(page.locator("#modal.open h2")).toHaveText(`给 ${name} 续期`);
}
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });
test.beforeEach(async () => {
  const p = await cp("/api/users?limit=1000");
  for (const u of p.users) await cp(`/api/users/${u.username}`, "DELETE");
  await add("recent", -3, 52); await add("thirty", -30); await add("old", -31); await add("disabled", -90, 0, false);
  env.takeLog();
});

test("30 天线、最近过期排序、只剩旧用户的空态与跳转", async ({ page }) => {
  await open(page);
  await expect(page.locator(".ov-t tr b")).toHaveText(["recent", "thirty"]);
  await expect(row(page, "old")).toHaveCount(0); await expect(row(page, "disabled")).toHaveCount(0);
  await expect(page.locator(".ov-stale")).toContainText("另有 1 个用户过期超过 30 天");
  await page.click('[data-act=go-expired]');
  await expect(page).toHaveURL(/#users$/);
  await expect(page.locator('[data-status=expired]')).toHaveClass(/on/);
  await cp("/api/users/recent", "DELETE"); await cp("/api/users/thirty", "DELETE");
  await page.goto(env.origin + "/#overview");
  await expect(page.locator(".ov-t")).toHaveCount(0);
  await expect(page.locator(".ov-main .card").first()).toContainText("没有超限、30 天内过期");
  await expect(page.locator(".ov-stale")).toContainText("另有 1 个用户");
});

test("过期行的名字、用量和状态圈在深浅主题置灰", async ({ page }) => {
  await open(page);
  for (const theme of ["light", "dark"]) {
    await page.evaluate(t => document.documentElement.dataset.theme = t, theme);
    const muted = theme === "light" ? "rgb(113, 113, 122)" : "rgb(143, 143, 150)";
    const faint = theme === "light" ? "rgb(161, 161, 170)" : "rgb(91, 93, 100)";
    await expect(row(page, "recent")).toHaveClass(/is-exp/);
    await expect(row(page, "recent").locator("b")).toHaveCSS("color", muted);
    await expect(row(page, "recent").locator(".st")).toHaveText("已过期 3 天");
    await expect(row(page, "recent").locator(".ov-use")).toHaveCount(0);
    expect(await row(page, "recent").locator(".dot").evaluate(e => getComputedStyle(e).boxShadow)).toContain(faint);
  }
  await page.goto(env.origin + "/#users");
  await expect(row(page, "recent").locator(".use > span")).toHaveCSS("color", "rgb(143, 143, 150)");
  await expect(row(page, "recent").locator(".bar i")).toHaveCSS("background-color", "rgb(91, 93, 100)");
});

test("续期选项与 CLI 一致，开框不请求，指定日期校验", async ({ page }) => {
  await add("cli-date", -3); await add("ui-date", -3);
  await open(page, "users/ui-date");
  const requests = []; page.on("request", r => { if (new URL(r.url()).pathname.startsWith("/api/")) requests.push(r.method()); });
  await page.click('[data-act=user-menu]'); await page.click('[data-act=user-renew]');
  expect(requests).toEqual([]);
  for (const n of [90, 365, 30]) {
    await page.click(`[data-rn="${n}"]`);
    await expect(page.locator(`[data-rn="${n}"]`)).toHaveAttribute("aria-checked", "true");
    await expect(page.locator("#rn-to b")).toHaveText(dateAt(n).slice(0, 10));
  }
  await page.click('[data-rn=date]'); await page.fill("#rn-in", "2020-01-01"); await page.click("#okb");
  await expect(page.locator("#modal-err")).toHaveText("新的到期日要晚于今天");
  await expect(page.locator("#modal.open")).toBeVisible();
  env.takeLog(); await env.cli("user", "renew", "cli-date", "--expire", "2027-03-31");
  const expected = normalize(env.takeLog(), ["cli-date", "ui-date"]);
  await page.fill("#rn-in", "2027-03-31"); await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("2027-03-31");
  expect(normalize(env.takeLog(), ["cli-date", "ui-date"])).toEqual(expected);
});

test("超限续期可清零；清零失败重试不重复顺延", async ({ page }) => {
  await open(page);
  await page.click('[data-act=user-renew][data-name=recent]');
  await expect(page.locator("#rn-reset")).not.toBeChecked();
  await page.check("#rn-reset");
  let fail = true;
  await page.route("**/api/users/recent/reset-traffic", r => fail ? r.fulfill({ status: 500, json: { error: "测试清零失败" } }) : r.continue());
  env.takeLog(); await page.click("#okb");
  await expect(page.locator("#okb")).toHaveText("重试清零");
  const saved = (await cp("/api/users/recent")).expire_at;
  expect(env.takeLog().filter(r => r.method === "PUT")).toHaveLength(1);
  fail = false; await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("本期用量已清零");
  expect(env.takeLog().filter(r => r.method !== "GET").map(r => [r.method, r.path])).toEqual([["POST", "/api/users/recent/reset-traffic"]]);
  const u = await cp("/api/users/recent"); expect(u.expire_at).toBe(saved); expect(u.used_traffic).toBe(0);
  await modalRenew(page, "thirty"); await expect(page.locator("#rn-reset")).toHaveCount(0);
});

test("行菜单单开、外点和 Esc 收起，总览删除后不离页", async ({ page }) => {
  await open(page);
  const menus = page.locator(".ov-act .menu:not([hidden])");
  await row(page, "recent").locator('[data-act=row-menu]').click();
  await row(page, "thirty").locator('[data-act=row-menu]').click(); await expect(menus).toHaveCount(1);
  await page.keyboard.press("Escape"); await expect(menus).toHaveCount(0);
  await row(page, "recent").locator('[data-act=row-menu]').click(); await page.locator("h1").click(); await expect(menus).toHaveCount(0);
  await page.setViewportSize({ width: 1280, height: 360 });
  const last = row(page, "thirty").locator('[data-act=row-menu]');
  await last.scrollIntoViewIfNeeded();
  await last.evaluate(e => { const main = document.querySelector("main"); main.scrollTop += e.getBoundingClientRect().bottom - innerHeight + 12; });
  await last.click();
  await expect(row(page, "thirty").locator(".menu")).toHaveClass(/up/);
  expect(await row(page, "thirty").locator(".menu").evaluate(e => e.getBoundingClientRect().bottom <= innerHeight)).toBe(true);
  await page.keyboard.press("Escape"); await page.setViewportSize({ width: 1440, height: 900 });
  await row(page, "recent").locator('[data-act=row-menu]').click(); await row(page, "recent").locator('[data-act=user-rm]').click();
  await expect(page.locator("#okb")).toBeDisabled(); await page.fill("#typed", "recent"); await page.click("#okb");
  await expect(row(page, "recent")).toHaveCount(0); await expect(page).toHaveURL(/#overview$/);
});

test("只有已过期筛选可多选，半选和切换筛选、分页清空", async ({ page }) => {
  await open(page, "users"); await expect(page.locator(".td-pick")).toHaveCount(0);
  await expired(page);
  await page.check('[data-pick=recent]'); await expect(page.locator("#drawer.open")).toHaveCount(0);
  expect(await page.locator('[data-pick="*"]').evaluate(e => e.indeterminate)).toBe(true);
  await page.check('[data-pick="*"]'); await expect(page.locator(".bulk")).toContainText("已选 3 个");
  await page.click('[data-status=active]'); await page.click('[data-status=expired]');
  await expect(page.locator(".bulk")).not.toContainText("已选");
  await page.click('[data-act=pick-old]'); await expect(page.locator(".bulk")).toContainText("已选 1 个");
  for (let i = 0; i < 48; i++) await add(`page-${i}`, -40);
  await page.reload(); await expired(page);
  await page.locator('.ut tbody [data-pick]').first().check();
  await page.click('[data-act=user-page][data-dir="1"]');
  await expect(page.locator(".bulk")).not.toContainText("已选");
  await page.locator('.ut tbody [data-pick]').first().check();
  await page.click('[data-act=user-page][data-dir="-1"]');
  await expect(page.locator(".bulk")).not.toContainText("已选");
  await page.route("**/ui/meta", async r => { const response = await r.fetch(); const j = await response.json(); await r.fulfill({ response, json: { ...j, allow: ["GET /api"] } }); });
  await page.reload(); await page.goto(env.origin + "/#users"); await page.click("[data-status=expired]");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator(".td-pick")).toHaveCount(0);
});

test("批删确认、逐条请求、执行锁定与失败项重试", async ({ page }) => {
  await open(page, "users"); await expired(page);
  await page.check('[data-pick="*"]');
  for (const name of ["cli-old", "cli-thirty", "cli-recent"]) await add(name, -3);
  env.takeLog(); await env.cli("user", "rm", "cli-old", "cli-thirty", "cli-recent");
  const twins = ["cli-old", "cli-thirty", "cli-recent", "old", "thirty", "recent"];
  const cliDeletes = normalize(env.takeLog(), twins);
  await page.click('[data-act=user-rm-many]');
  await expect(page.locator("#okb")).toBeDisabled(); await page.fill("#typed", "删除 3 个用户");
  let fail = true;
  await page.route("**/api/users/thirty", async r => {
    if (r.request().method() !== "DELETE") return r.continue();
    await new Promise(resolve => setTimeout(resolve, 200));
    return fail ? r.fulfill({ status: 500, json: { error: "测试删除失败" } }) : r.continue();
  });
  const events = []; page.on("request", r => { if (new URL(r.url()).pathname.startsWith("/api/")) events.push([r.method(), new URL(r.url()).pathname]); });
  await page.click("#okb"); await expect(page.locator("#modal")).toHaveAttribute("aria-busy", "true");
  await page.keyboard.press("Escape"); await expect(page.locator("#modal.open")).toBeVisible();
  await expect(page.locator("#typed")).toBeDisabled();
  await expect(page.locator("#okb")).toHaveText("重试 1 个");
  expect(normalize(events.filter(r => r[0] === "DELETE").map(([method, path]) => ({ method, path })), twins)).toEqual(cliDeletes);
  await expect(page.locator("#modal-err")).toContainText("thirty");
  const deletes = events.map((r, i) => r[0] === "DELETE" ? i : -1).filter(i => i >= 0);
  expect(events.slice(deletes[0], deletes.at(-1) + 1).every(r => r[0] === "DELETE")).toBe(true);
  events.length = 0; fail = false; await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已删除 3 个用户");
  expect(events.filter(r => r[0] === "DELETE")).toEqual([["DELETE", "/api/users/thirty"]]);
  await expect(page.locator(".ut .is-exp")).toHaveCount(0);
  // 当前页 50 人全失败时，长错误列表不能把重试和取消挤出小窗口。
  for (let i = 0; i < 50; i++) await add(`fail-${i}`, -45);
  await page.reload(); await expired(page); await page.setViewportSize({ width: 640, height: 400 });
  await page.check('[data-pick="*"]'); await page.click('[data-act=user-rm-many]');
  await page.fill("#typed", "删除 50 个用户");
  await page.route("**/api/users/fail-*", r => r.request().method() === "DELETE" ? r.fulfill({ status: 500, json: { error: "测试错误详情".repeat(30) } }) : r.continue());
  await page.click("#okb"); await expect(page.locator("#okb")).toHaveText("重试 50 个");
  const bounds = await page.locator("#okb").boundingBox(); expect(bounds.y).toBeGreaterThanOrEqual(0); expect(bounds.y + bounds.height).toBeLessThanOrEqual(400);
  expect(await page.locator("#modal .mbody").evaluate(e => e.scrollHeight > e.clientHeight)).toBe(true);
});

test("过期用户验收图：平台、主题、尺寸和处理状态", async ({ browser }) => {
  test.skip(!process.env.MPX_EXPIRED_SHOTS, "仅生成验收图时运行");
  test.setTimeout(180_000);
  const users = await cp("/api/users?limit=1000");
  for (const u of users.users) await cp(`/api/users/${u.username}`, "DELETE");
  for (const [name, days, used] of [["alice", null, 2], ["bob", null, 52], ["carol", -3, 52], ["frank", -12, 2], ["grace", -26, 2], ["heidi", -45, 2], ["ivan", -120, 2], ["dave", 2, 2], ["erin", null, 45]]) await add(name, days, used);
  const settings = await cp("/api/settings"); await cp("/api/settings", "PUT", { ...settings, sub_url_prefix: "https://sub.example.com" });
  const out = process.env.MPX_EXPIRED_SHOTS;
  for (const platform of ["mac", "win"]) for (const theme of ["light", "dark"]) for (const [width, height] of [[1280, 800], [900, 720], [640, 900]]) {
    const context = await browser.newContext({ viewport: { width, height }, deviceScaleFactor: 2 });
    try {
      await context.addInitScript(({ platform, theme }) => {
        Object.defineProperty(navigator, "platform", { value: platform === "mac" ? "MacIntel" : "Win32" });
        Object.defineProperty(navigator, "userAgentData", { value: { platform: platform === "mac" ? "macOS" : "Windows" } });
        localStorage.setItem("mpx-theme", theme);
        if (platform === "win") document.addEventListener("DOMContentLoaded", () => {
          document.documentElement.style.setProperty("--mygo-titlebar-inset-right", "138px");
          document.documentElement.style.setProperty("--mygo-titlebar-height", "52px");
        });
      }, { platform, theme });
      const page = await context.newPage();
      await page.route("**/ui/meta", async r => { const response = await r.fetch(), j = await response.json(); await r.fulfill({ response, json: { ...j, server: "203.0.113.10:8080" } }); });
      const nodes = ["东京 A", "新加坡 A", "法兰克福 A", "洛杉矶 A"].map((display, i) => ({ id: i + 1, name: `demo-${i}`, display, address: `203.0.113.${20 + i}`, status: { reachable: true, active: true } }));
      await page.route("**/api/nodes", r => r.fulfill({ json: nodes }));
      await page.route("**/api/status", async r => { const response = await r.fetch(), j = await response.json(); await r.fulfill({ response, json: { ...j, version: "v0.9.13", counts: { ...j.counts, nodes: 4, inbounds: 4, hosts: 4 } } }); });
      await page.route("**/api/sub-pulls", r => r.fulfill({ json: [{ username: "alice", client: "clash-verge", first_pulled_at: new Date().toISOString(), last_pulled_at: new Date().toISOString() }] }));
      await open(page);
      await expect(page.locator(".ov-t tr")).toHaveCount(6);
      await page.evaluate(() => document.fonts.ready);
      if (width < 861) await page.locator(".ov-main").scrollIntoViewIfNeeded();
      else expect(await page.locator("main").evaluate(e => e.scrollHeight <= e.clientHeight)).toBe(true);
      expect(await page.locator(".ov-t").evaluate(e => e.getBoundingClientRect().right <= e.closest(".card").getBoundingClientRect().right)).toBe(true);
      const shot = async state => { await page.mouse.move(width - 6, height - 6); await page.screenshot({ path: `${out}/${state}-${width}x${height}-${platform}-${theme}@2x.png` }); };
      await shot("ov-expired");
      await row(page, "grace").locator('[data-act=row-menu]').click();
      await expect(row(page, "grace").locator(".menu")).toBeVisible();
      await shot("ov-menu");
      await row(page, "grace").locator('[data-act=user-rm]').click();
      await expect(page.locator("#typed")).toBeVisible(); await shot("ov-delete");
      await page.click('[data-act=modal-cancel]');
      await row(page, "carol").locator('[data-act=user-renew]').click();
      await expect(page.locator("#rn-reset")).toBeVisible(); await shot("renew");
      await page.click('[data-act=modal-cancel]');
      if (width === 1280) {
        await row(page, "frank").locator('[data-act=user-renew]').click();
        await page.click('[data-rn=date]'); await page.fill("#rn-in", "2027-03-31"); await shot("renew-date");
        await page.click('[data-act=modal-cancel]');
      }
      await expired(page); await page.click('[data-act=pick-old]'); await page.check('[data-pick=grace]');
      await expect(page.locator(".bulk")).toContainText("已选 3 个"); await shot("users-expired");
      await page.click('[data-act=user-rm-many]'); await page.fill("#typed", "删除 3 个用户"); await shot("users-batch-delete");
    } finally { await context.close(); }
  }
});
