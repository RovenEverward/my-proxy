// 总览退避横幅:单节点时给「立即重试」按钮,点了打到 /api/nodes/{id}/backoff/retry。
// 成功:banner 消失;失败:报错、banner 保留。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.use({ deviceScaleFactor: 2 });
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const BACKOFF = { node_id: 1, node_name: "la-1", failures: 2299, skip_until: "2036-11-12T17:29:00Z" };
const nodes = [{ id: 1, name: "la-1", display: "洛杉矶 A", address: "203.0.113.10", draining: false, status: { reachable: false, active: false } }];

async function prepare(page, { succeed = true } = {}) {
  let backing = [BACKOFF];
  const posts = [];
  const data = {
    "/api/status": { version: "v0.9.13", counts: { users: 0, nodes: 1, inbounds: 1, hosts: 1 }, backoffs: backing, update: {}, uptime_seconds: 3600 },
    "/api/nodes": nodes,
    "/api/inbounds": [], "/api/plans": [],
    "/api/settings": { sub_url_prefix: "https://sub.example.com", notify_usage_percent: 80, notify_days_left: 3 },
    "/api/sub-pulls": [],
  };
  await page.route("**/ui/meta", async r => {
    const response = await r.fetch(), meta = await response.json();
    await r.fulfill({ response, json: { ...meta, server: "203.0.113.10:8080" } });
  });
  await page.route("**/api/**", r => {
    const url = new URL(r.request().url()), p = url.pathname;
    if (p === "/api/status") return r.fulfill({ json: { ...data["/api/status"], backoffs: backing } });
    if (p.match(/^\/api\/nodes\/\d+\/backoff\/retry$/) && r.request().method() === "POST") {
      posts.push(p);
      if (succeed) { backing = []; return r.fulfill({ json: { node: "la-1", retried: true } }); }
      return r.fulfill({ status: 502, json: { error: "ssh: connect timeout" } });
    }
    if (p === "/api/users") return r.fulfill({ json: { users: [], total: 0, presence: { updated_at: new Date().toISOString(), online: 0, active_24h: 0, stale_nodes: [] } } });
    return p in data ? r.fulfill({ json: data[p] }) : r.continue();
  });
  await login(page, env);
  return { posts };
}

test("退避横幅给出立即重试", async ({ page }) => {
  const { posts } = await prepare(page);
  const banner = page.locator(".ov .callout", { hasText: "在退避中" });
  await expect(banner).toBeVisible();
  await expect(banner.locator("[data-act=node-retry]")).toBeVisible();
  await expect(banner.getByRole("link", { name: "查看节点" })).toHaveAttribute("href", "#nodes/1");
  // 点击:打到新端点;成功后 toast 说通了、退避清零,banner 消失。
  await banner.locator("[data-act=node-retry]").click();
  await expect.poll(() => posts.length, { timeout: 5000 }).toBe(1);
  expect(posts[0]).toBe("/api/nodes/1/backoff/retry");
  await expect(page.locator(".toast", { hasText: "重试成功" })).toBeVisible();
  await expect(banner).toBeHidden();
});

test("重试未通报错且横幅保留", async ({ page }) => {
  await prepare(page, { succeed: false });
  const banner = page.locator(".ov .callout", { hasText: "在退避中" });
  await banner.locator("[data-act=node-retry]").click();
  await expect(page.locator(".toast.bad", { hasText: "connect timeout" })).toBeVisible();
  await expect(banner).toBeVisible();
});
