// 并排的表单字段(设置、抽屉、确认框):同一行的 label 顶部相等、输入框顶部相等。
// 一边有提示文字、一边没有时,曾因 .field 是 grid 且被拉伸到行高,行间多出的高度
// 被平摊到 label/输入框两行上,没提示的那边输入框就往下掉。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "hk", "--address", "203.0.113.10");
  await env.seed("inbound", "add", "--node", "1", "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.cloudflare.com", "--reality-dest", "www.cloudflare.com:443");
  await env.seed("user", "add", "alice");
});
test.afterAll(async () => { await env?.stop(); });

// 每个多列容器里的 .field 按行分组(同一行 = 字段框顶部相同),比较 label 与控件的 top。
const misaligned = page => page.evaluate(() => {
  const bad = [];
  const vis = e => e && e.getClientRects().length;
  for (const g of document.querySelectorAll(".two, .g2, .g3, .g4")) {
    if (!vis(g)) continue;
    const rows = {};
    for (const f of g.children) {
      if (!f.classList.contains("field") || !vis(f)) continue;
      const t = Math.round(f.getBoundingClientRect().top);
      (rows[t] ||= []).push(f);
    }
    for (const fs of Object.values(rows)) {
      if (fs.length < 2) continue;
      for (const sel of ["label", "input:not([type=checkbox]):not([type=radio]), select, textarea, .in"]) {
        const tops = fs.map(f => f.querySelector(sel)).filter(vis).map(e => Math.round(e.getBoundingClientRect().top));
        if (new Set(tops).size > 1) bad.push(`${sel.split(",")[0]} tops ${tops.join("/")}: ${fs.map(f => f.querySelector("label")?.textContent.trim()).join(" | ")}`);
      }
    }
  }
  return bad;
});

for (const theme of ["light", "dark"]) {
  test(`并排字段顶部对齐 · ${theme}`, async ({ page }) => {
    await page.addInitScript(t => localStorage.setItem("mpx-theme", t), theme);
    await login(page, env);
    // 只改 hash 不会关掉已打开的抽屉,换页一律重新载入。
    const go = async h => { await page.goto(env.origin + "/#" + h); await page.reload(); };
    const check = async name => { await page.waitForTimeout(400); expect(await misaligned(page), name).toEqual([]); };
    await go("settings"); await expect(page.locator("input").first()).toBeVisible(); await check("设置 · 通用");
    await go("users"); await page.click("[data-act=user-new]"); await check("新建用户抽屉");
    await go("users/alice"); await expect(page.locator("#drawer.open")).toBeVisible();
    if (await page.locator("[data-act=user-edit]").count()) { await page.click("[data-act=user-edit]"); await check("编辑用户抽屉"); }
    await go("nodes"); await page.click("[data-act=node-new]"); await check("新建节点抽屉");
    await go("nodes/1"); await expect(page.locator("h1")).toContainText("hk");
    await page.click("[data-act=node-install]"); await check("安装核心确认框");
    await go("inbounds"); await page.click("[data-act=inb-new] >> nth=0"); await check("新建入站抽屉");
    await go("inbounds"); await page.click("[data-act=host-new] >> nth=0"); await check("新建入口抽屉");
    await go("purposes"); await check("用途页");
  });
}
