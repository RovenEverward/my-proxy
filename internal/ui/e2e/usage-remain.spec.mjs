// 电池式剩余量：从真实列表、总览和详情验证宽度、颜色与边界文案。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
const GiB = 2 ** 30;
test.use({ deviceScaleFactor: 2 });
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });
const samples = [
  ["half", 50, "ok"], ["low", 80, "warn"], ["critical", 95, "bad"],
  ["empty", 100, "bad"], ["over", 102, "bad"], ["unlimited", 2, null],
  ["expiring", 2, "ok"], ["seventy", 70, "warn"], ["ninety", 90, "bad"],
  ["below-seventy", 69.9, "ok"], ["fraction", 90.4, "bad"], ["tiny", 99.6, "bad"],
  ["expired-over", 102, "bad"], ["expired-low", 2, "ok"],
];
async function prepare(page, platform, theme) {
  await page.addInitScript(({ platform, theme }) => {
    Object.defineProperty(navigator, "platform", { value: platform === "mac" ? "MacIntel" : "Win32" });
    Object.defineProperty(navigator, "userAgentData", { value: { platform: platform === "mac" ? "macOS" : "Windows" } });
    localStorage.setItem("mpx-theme", theme);
  }, { platform, theme });
  const now = new Date().toISOString();
  const users = samples.map(([username, used], i) => ({
    id: i + 1, username, enabled: true, used_traffic: Math.round(used * GiB),
    data_limit: username === "unlimited" ? 0 : 100 * GiB,
    status: username.startsWith("expired-") ? "expired" : used >= 100 && username !== "unlimited" ? "limited" : "active",
    expire_at: username.startsWith("expired-") ? new Date(Date.now() - 864e5).toISOString() : ["expiring", "low"].includes(username) ? new Date(Date.now() + 864e5).toISOString() : null,
    reset_strategy: "monthly", created_at: now, presence: { online: false },
  }));
  let overviewUsers = users;
  const presence = { updated_at: now, online: 0, active_24h: 2, stale_nodes: [] };
  const data = {
    "/api/status": { version: "v0.9.13", counts: { users: users.length, nodes: 1, inbounds: 1, hosts: 1 }, backoffs: [], update: {}, uptime_seconds: 3600 },
    "/api/nodes": [{ id: 1, name: "test-node", display: "东京 A", address: "203.0.113.10", status: { reachable: true, active: true } }],
    "/api/inbounds": [], "/api/plans": [],
    "/api/settings": { sub_url_prefix: "https://sub.example.com", notify_usage_percent: 80, notify_days_left: 3 },
    "/api/sub-pulls": [{ username: "half", client: "clash-verge", first_pulled_at: now, last_pulled_at: now }],
  };
  await page.route("**/ui/meta", async r => {
    const response = await r.fetch(), meta = await response.json();
    await r.fulfill({ response, json: { ...meta, server: "203.0.113.10:8080" } });
  });
  await page.route("**/api/**", r => {
    const url = new URL(r.request().url()), p = url.pathname;
    if (p === "/api/users") {
      const list = overviewUsers;
      return r.fulfill({ json: { users: list, total: list.length, presence } });
    }
    const match = p.match(/^\/api\/users\/([^/]+)(\/.*)?$/);
    if (match) {
      const u = users.find(u => u.username === match[1]);
      if (!match[2]) return r.fulfill({ json: u });
      if (match[2] === "/activity") return r.fulfill({ json: { segments: [], ips: [], updated_at: now } });
      if (match[2] === "/links") return r.fulfill({ json: { auto: `https://sub.example.com/sub/demo-${u.username}` } });
    }
    return p in data ? r.fulfill({ json: data[p] }) : r.continue();
  });
  await login(page, env);
  return {
    focusOverview() { overviewUsers = users.filter(u => ["empty", "over", "expired-over", "expiring", "low", "critical"].includes(u.username)); data["/api/status"].counts.users = overviewUsers.length; },
    focusFractions() { overviewUsers = users.filter(u => ["fraction", "tiny", "expired-low"].includes(u.username)); data["/api/status"].counts.users = overviewUsers.length; },
  };
}
const row = (page, name) => page.locator(`tr[data-href="#users/${name}"]`);
async function barMatches(bar, percent, cls, theme) {
  const fill = bar.locator("i");
  await expect(fill).toHaveAttribute("class", cls);
  const [track, ink] = await Promise.all([bar.boundingBox(), fill.boundingBox()]);
  expect(Math.abs(ink.width - track.width * percent / 100)).toBeLessThan(0.2);
  const colors = {
    light: { ok: "rgb(79, 148, 104)", warn: "rgb(201, 151, 0)", bad: "rgb(210, 75, 69)" },
    dark: { ok: "rgb(90, 154, 110)", warn: "rgb(212, 167, 44)", bad: "rgb(222, 95, 92)" },
  };
  await expect(fill).toHaveCSS("background-color", colors[theme][cls]);
}
for (const platform of ["mac", "win"]) for (const theme of ["light", "dark"]) {
  test(`剩余用量 · ${platform} ${theme}`, async ({ page }) => {
    const fixture = await prepare(page, platform, theme);
    const out = process.env.MPX_GRAPHITE_SHOTS, stem = `${out}/${platform}-${theme}`;
    const shot = async name => {
      if (!out) return;
      await page.evaluate(() => document.fonts.ready);
      await page.screenshot({ path: `${stem}-${name}.png` });
    };
    await page.goto(env.origin + "/#users");
    await expect(page.locator("table.ut tbody tr")).toHaveCount(samples.length);
    for (const [name, used, cls] of samples) {
      const use = row(page, name).locator(".use");
      if (!cls) { await expect(use.locator(".bar")).toHaveCount(0); await expect(use).toContainText("不限"); continue; }
      await barMatches(use.locator(".bar"), Math.max(0, 100 - used), cls, theme);
      await expect(use).toHaveAttribute("title", /^已用 .* \/ 上限 /);
      await expect(use.locator(":scope > span")).toHaveText(["over", "expired-over"].includes(name) ? "超 2.0 GB / 100.0 GB" : name === "empty" ? "剩 0 / 100.0 GB" : /^剩 /);
    }
    const bad = theme === "light" ? "rgb(220, 38, 38)" : "rgb(242, 85, 90)";
    await expect(row(page, "empty").locator(".use > span")).toHaveCSS("color", bad);
    await expect(row(page, "over").locator(".use > span")).toHaveCSS("color", bad);
    await shot("users");
    if (out) await page.locator(".brand").screenshot({ path: `${stem}-brand.png` });
    for (const [name, text] of [["empty", "已用完"], ["over", "超出 2.0 GB"], ["half", "50.0 GB"], ["unlimited", "2.0 GB"]]) {
      await page.goto(`${env.origin}/#users/${name}`);
      const use = page.locator("#drawer.open .ud-use");
      await expect(use.locator(".ud-v")).toHaveText(text);
      await expect(use.locator(":scope > .sub").first()).toHaveText(name === "unlimited" ? "本期用量" : "本期剩余");
      if (["empty", "over"].includes(name)) await expect(use.locator(".ud-v")).toHaveCSS("color", bad);
      if (name === "half") await expect(use.locator(".sub").last()).toHaveText("已用 50.0 GB / 上限 100.0 GB · 每月清零");
      if (name === "unlimited") await expect(use.locator(".bar")).toHaveCount(0);
      if (["empty", "half"].includes(name)) await shot(`detail-${name}`);
    }
    fixture.focusOverview();
    // 示例用户集合切换后重新进入文档，让侧栏计数也从同一份状态读取。
    await page.goto(env.origin + "/#overview");
    await page.reload();
    const table = page.locator(".ov-t");
    await expect(table.locator("tr")).toHaveCount(6);
    const onlyExpire = row(page, "expiring").locator("td").nth(2);
    await expect(onlyExpire).toBeEmpty();
    // 已过期用户只显示到期状态，即使同时超限也不显示用量条。
    await expect(row(page, "expired-over").locator("td").nth(2)).toBeEmpty();
    for (const name of ["empty", "over"]) {
      await expect(row(page, name).locator(".ov-use")).toHaveText("剩 0%");
      await expect(row(page, name).locator(".ov-use .sub")).toHaveCSS("color", bad);
      await barMatches(row(page, name).locator(".bar"), 0, "bad", theme);
    }
    await expect(row(page, "critical").locator(".ov-use")).toHaveText("剩 5%");
    // 同时快到期、快用完时保留流量信息。
    await expect(row(page, "low").locator("td").nth(1)).toHaveText("快到期");
    await barMatches(row(page, "low").locator(".bar"), 20, "warn", theme);
    await shot("overview");
    fixture.focusFractions();
    await page.reload();
    await expect(row(page, "fraction").locator(".ov-use")).toHaveText("剩 9%");
    await expect(row(page, "tiny").locator(".ov-use")).toHaveText("剩 <1%");
    await expect(row(page, "expired-low").locator("td").nth(2)).toBeEmpty();
  });
}
