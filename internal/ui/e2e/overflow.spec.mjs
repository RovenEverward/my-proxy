// 长地址、长域名、IPv6、长用户名 / 节点名 / emoji 名:任何卡片、侧栏、抽屉、确认框里的内容都不许冲出边框,
// 页面也不许横向滚动。表格包在 .tw 里本来就允许自己横向滚,不算溢出。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
const LONG_USER = "zhang.san_" + "x".repeat(54); // 用户名只允许 [A-Za-z0-9_.-],最长 64;emoji 放在备注和节点名里
const LONG_NODE = "hk-日本-🚀-very-long-node-name-for-overflow-check";
const SERVERS = ["https://144.34.233.37:8080", "https://control-plane.my-very-long-subdomain.example-proxy.net:8443", "https://[2001:db8:85a3::8a2e:370:7334]:8080"];

test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", LONG_NODE, "--address", "very-long-hostname.region-1.example-provider.cloud");
  await env.seed("inbound", "add", "--node", "1", "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.a-really-long-sni-domain-name-for-testing.example.com", "--reality-dest", "www.cloudflare.com:443");
  await env.seed("user", "add", LONG_USER, "--note", "🚀 备注也很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长很长");
});
test.afterAll(async () => { await env?.stop(); });

// 找出越过所在框右边的元素;祖先里有能自己滚动/裁剪的(overflow 非 visible)就不算。
const overflowing = page => page.evaluate(() => {
  const bad = [];
  if (document.documentElement.scrollWidth > innerWidth + 1) bad.push("page scrollWidth " + document.documentElement.scrollWidth);
  for (const box of document.querySelectorAll(".profile, .card, #drawer.open, #modal.open, .top")) {
    const right = box.getBoundingClientRect().right;
    for (const el of box.querySelectorAll("*")) {
      const r = el.getBoundingClientRect();
      if (!r.width || r.right <= right + 1) continue;
      let clipped = false;
      for (let a = el.parentElement; a && a !== box; a = a.parentElement) if (getComputedStyle(a).overflowX !== "visible") { clipped = true; break; }
      if (!clipped) bad.push(`${box.className || box.id} > ${el.tagName.toLowerCase()}.${el.className} 越界 ${Math.round(r.right - right)}px: ${el.textContent.trim().slice(0, 40)}`);
    }
  }
  return bad;
});

for (const theme of ["light", "dark"]) for (const server of SERVERS) {
  test(`无横向溢出 · ${theme} · ${server}`, async ({ page }) => {
    await page.addInitScript(t => localStorage.setItem("mpx-theme", t), theme);
    await page.route("**/ui/meta", async r => { const res = await r.fetch(); const j = await res.json(); j.server = server; await r.fulfill({ response: res, json: j }); });
    await login(page, env);
    for (const h of ["overview", "users", "users/" + encodeURIComponent(LONG_USER), "nodes", "nodes/1", "inbounds", "settings", "settings/templates"]) {
      await page.goto(env.origin + "/#" + h); await page.reload();
      await expect(page.locator(".profile")).toContainText(server.replace(/^https:\/\//, ""));
      await page.waitForTimeout(300);
      expect(await overflowing(page), h).toEqual([]);
    }
    await expect(page.locator(".profile span[title]")).toHaveAttribute("title", server);
  });
}
