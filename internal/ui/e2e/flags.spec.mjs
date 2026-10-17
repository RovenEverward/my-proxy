// SVG 国旗:节点显示名里的区域指示符 emoji(🇯🇵)在页面上换成本地 /flags/jp.svg,
// 图片真的加载出来;页面文本里不再残留区域指示符字符(Windows 上会显示成两个字母)。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "jp-1", "--address", "127.0.0.1", "--ssh-port", "1");
  await env.seed("node", "edit", "1", "--region", "JP", "--city", "东京");
});
test.afterAll(async () => { await env?.stop(); });

// 成对的区域指示符 U+1F1E6–U+1F1FF。
const RI_PAIR = /[\u{1F1E6}-\u{1F1FF}]{2}/u;

test("节点页国旗是加载成功的 SVG,文本里不留 emoji", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes/1");
  await expect(page.locator(".ph h1")).toContainText("东京");
  const flag = page.locator(".ph h1 img.flag").first();
  await expect(flag).toBeVisible();
  await expect(flag).toHaveAttribute("src", "/flags/jp.svg");
  await expect(flag).toHaveAttribute("alt", "🇯🇵");
  await expect.poll(() => flag.evaluate(i => i.complete && i.naturalWidth > 0)).toBe(true);
  // 列表页同样换掉,不只详情页标题。
  await page.goto(env.origin + "/#nodes");
  const rowFlag = page.locator("tbody img.flag").first();
  await expect(rowFlag).toBeVisible();
  await expect.poll(() => rowFlag.evaluate(i => i.complete && i.naturalWidth > 0)).toBe(true);
  // 页面上(含 <title>)不能再出现区域指示符字符;alt 是属性,不算文本。
  expect(await page.evaluate(() => document.body.innerText)).not.toMatch(RI_PAIR);
  expect(await page.title()).not.toMatch(RI_PAIR);
});

test("国旗是圆角矩形,亮暗主题都有覆盖在图片上的 1px 内描边", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#nodes/1");
  const wrap = page.locator(".ph h1 .flag-w").first();
  await expect(wrap).toBeVisible();
  await expect(wrap).toHaveAttribute("data-flag", "1");
  const flag = wrap.locator("img.flag");
  await expect.poll(() => flag.evaluate(i => i.complete && i.naturalWidth > 0)).toBe(true);
  await expect(flag).toHaveCSS("border-radius", "2px");
  await expect(flag).toHaveAttribute("draggable", "false");
  const size = await flag.boundingBox();
  expect(size.width).toBeGreaterThan(size.height);
  expect(size.width / size.height).toBeCloseTo(4 / 3, 1);
  for (const theme of ["light", "dark"]) {
    await page.evaluate(t => { document.documentElement.dataset.theme = t; }, theme);
    const edge = await wrap.evaluate(el => getComputedStyle(el, "::after").boxShadow);
    expect(edge).toContain("inset");
    expect(edge).toContain("1px");
    expect(edge).toContain(theme === "dark" ? "rgba(255, 255, 255, 0.22)" : "rgba(0, 0, 0, 0.14)");
  }
});

test("国旗加载失败后用灰底地区代码替换,不留下破损图片", async ({ page }) => {
  await login(page, env);
  await page.route("**/flags/jp.svg", r => r.fulfill({ status: 404 }));
  await page.goto(env.origin + "/#nodes/1");
  const fallback = page.locator(".ph h1 .flag-code");
  await expect(fallback).toBeVisible();
  await expect(fallback).toHaveText("JP");
  await expect(fallback).toHaveAttribute("title", "地区 JP");
  await expect(fallback).toHaveCSS("border-radius", "2px");
  for (const theme of ["light", "dark"]) {
    await page.evaluate(t => { document.documentElement.dataset.theme = t; }, theme);
    const background = await fallback.evaluate(el => getComputedStyle(el).backgroundColor);
    expect(background).not.toBe("transparent");
    expect(background).not.toBe("rgba(0, 0, 0, 0)");
  }
  await expect(page.locator("img.flag")).toHaveCount(0);
  await expect(page.locator(".flag-w")).toHaveCount(0);
  expect(await page.evaluate(() => document.body.innerText)).not.toMatch(RI_PAIR);
});
