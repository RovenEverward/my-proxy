import { test, expect } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { startEnv, login } from "./env.mjs";

let env;
test.use({ deviceScaleFactor: 2 });
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

async function overview(page, platform, theme, extreme = false) {
  await page.addInitScript(({ platform, theme }) => {
    Object.defineProperty(navigator, "platform", { value: platform === "mac" ? "MacIntel" : "Win32" });
    Object.defineProperty(navigator, "userAgentData", { value: { platform: platform === "mac" ? "macOS" : "Windows" } });
    localStorage.setItem("mpx-theme", theme);
    if (platform === "win") document.addEventListener("DOMContentLoaded", () => {
      const s = document.documentElement.style;
      s.setProperty("--mygo-titlebar-inset-right", "138px"); s.setProperty("--mygo-titlebar-height", "52px");
    });
  }, { platform, theme });
  const now = Date.now(), day = 864e5, iso = days => new Date(now + days * day).toISOString();
  const users = ["alice", "bob", "carol", "dave", "erin", "frank"].map((username, i) => ({
    id: i + 1, username: extreme ? username + "x".repeat(50) : username, status: i < 2 ? "limited" : i < 4 ? "expired" : "active",
    used_traffic: (i < 2 ? 50 : 45) * 2 ** 30, data_limit: 50 * 2 ** 30,
    expire_at: iso(i < 4 ? -3 : 2), created_at: iso(-30 - i),
  }));
  const names = ["洛杉矶 A", "东京 A", "洛杉矶 B", "新加坡 A", "法兰克福 A", "香港 A", "首尔 A", "伦敦 A"];
  const flags = ["🇺🇸", "🇯🇵", "🇺🇸", "🇸🇬", "🇩🇪", "🇭🇰", "🇰🇷", "🇬🇧"];
  const nodes = names.map((name, i) => { const display = flags[i] + " " + name; return ({ id: i + 1, name: `n${i + 1}`, display: extreme ? display + "很长的节点名称".repeat(12) : display,
    address: `203.0.113.${21 + i}`, draining: i === 5,
    status: { reachable: i !== 6, active: i !== 7, certificates: i === 2 ? [{ not_after: iso(9), days_left: 9 }] : [] },
  }); });
  const settings = { sub_url_prefix: "https://sub.example.com", webhook_url: "https://sub.example.com/" + (extreme ? "webhook/".repeat(30) : "notify"), notify_days_left: 3, notify_usage_percent: 80 };
  const status = { version: "v0.9.13", uptime_seconds: 3 * 86400 + 4 * 3600, update: { latest: "v0.9.13", behind: 0 },
    counts: { users: 6, nodes: 8, inbounds: 8, hosts: 8 }, last_tick_at: iso(0), last_tick_seconds: 1.4,
    backoffs: (extreme ? nodes : [nodes[6]]).map(n => ({ node_id: n.id, node_name: n.name, failures: 4, skip_until: iso(1) })),
  };
  await page.route("**/ui/meta", async r => {
    const response = await r.fetch(), meta = await response.json();
    await r.fulfill({ response, json: { ...meta, server: "203.0.113.10:8080" } });
  });
  const data = { "/api/status": status, "/api/nodes": nodes, "/api/settings": settings,
    "/api/users": { users, total: users.length, presence: { active_24h: 3 } },
    "/api/sub-pulls": [{ username: users[0].username, client: "clash-verge", first_pulled_at: iso(-5), last_pulled_at: iso(0) }],
  };
  if (process.env.MPX_OVERVIEW_SHOTS && !extreme) writeFileSync(`${process.env.MPX_OVERVIEW_SHOTS}/fixtures.json`, JSON.stringify(data));
  await page.route("**/api/**", r => {
    const p = new URL(r.request().url()).pathname;
    return p in data ? r.fulfill({ json: data[p] }) : r.continue();
  });
  await login(page, env);
  await page.goto(env.origin + "/#overview");
  await expect(page.locator(".ph #start-done")).toBeVisible();
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator(".ov-n")).toHaveCount(6);
  expect(await page.locator(".ov-n").evaluateAll(rows => rows.map(r => r.getAttribute("href")))).toEqual([7, 3, 8, 6, 1, 2].map(i => `#nodes/${i}`));
  await expect(page.locator("#ov-notify a")).toHaveAttribute("href", "#system");
  await expect(page.locator("#ov-notify")).toContainText("到期提醒");
  await expect(page.locator("#ov-notify")).toContainText("用量提醒");
}

for (const platform of ["mac", "win"]) for (const theme of ["light", "dark"]) {
  for (const [width, height] of [[900, 720], [1024, 768], [1024, 600], [640, 720]]) {
    test(`${platform} ${theme} 总览 ${width}×${height}`, async ({ page }) => {
      await page.setViewportSize({ width, height });
      await overview(page, platform, theme);
      const main = page.locator("main");
      const bounds = await main.evaluate(m => ({ x: m.scrollWidth - m.clientWidth, y: m.scrollHeight - m.clientHeight }));
      expect(bounds.x).toBeLessThanOrEqual(0);
      if (width >= 861 && height >= 720) expect(bounds.y).toBeLessThanOrEqual(0);
      if (width < 861) expect(await page.locator(".ov-main").evaluate(e => getComputedStyle(e).gridTemplateColumns.split(" ").length)).toBe(1);
      if (height < 720) {
        expect(bounds.y).toBeGreaterThan(0);
        await main.evaluate(m => { m.scrollTop = m.scrollHeight; });
        await expect(main).toHaveClass(/scrolling/);
        expect(await main.evaluate(m => Math.abs(m.scrollHeight - m.clientHeight - m.scrollTop))).toBeLessThanOrEqual(1);
      }
      if (platform === "mac") {
        await expect(page.locator("html")).toHaveClass(/os-mac/);
        expect(await main.evaluate(m => getComputedStyle(m, "::-webkit-scrollbar").width)).not.toBe("8px");
      } else {
        await expect(page.locator("html")).toHaveClass(/tb-right/);
        expect(await main.evaluate(m => getComputedStyle(m, "::-webkit-scrollbar").width)).toBe("8px");
      }
      // 不靠 main 的 overflow-x:hidden 掩盖子元素溢出。
      expect(await page.locator(".ov-t").evaluate(e => e.getBoundingClientRect().right <= e.closest(".card").getBoundingClientRect().right)).toBe(true);
      expect(await page.locator(".ov-n .tag").evaluateAll(tags => tags.every(e => getComputedStyle(e).whiteSpace === "nowrap"))).toBe(true);
      const out = process.env.MPX_OVERVIEW_SHOTS;
      if (out) {
        await page.evaluate(() => Promise.all(document.getAnimations().filter(a => a.effect.getTiming().iterations !== Infinity).map(a => a.finished.catch(() => {}))));
        await page.screenshot({ path: `${out}/${platform}-${theme}-${width}x${height}.png` });
      }
      if (width < 861) {
        await main.evaluate(m => { m.scrollTop = m.scrollHeight; });
        expect(await page.evaluate(() => document.querySelector("#ov-notify").getBoundingClientRect().bottom <= document.querySelector(".mnav").getBoundingClientRect().top)).toBe(true);
        if (out) await page.screenshot({ path: `${out}/${platform}-${theme}-${width}x${height}-bottom.png` });
      }
    });
  }
}

for (const platform of ["mac", "win"]) test(`${platform} 最小窗口容纳超长名字和多节点退避`, async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 720 });
  await overview(page, platform, "light", true);
  const main = page.locator("main");
  expect(await main.evaluate(m => m.scrollHeight - m.clientHeight)).toBeLessThanOrEqual(0);
  expect(await main.evaluate(m => m.scrollWidth - m.clientWidth)).toBeLessThanOrEqual(0);
});
