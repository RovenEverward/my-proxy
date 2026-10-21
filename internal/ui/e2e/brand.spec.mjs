// 侧栏品牌：浅色显示石墨 lockup，深色显示浅灰 lockup，图片都真的加载出来。
// 设置 SHOT_DIR 时顺便把侧栏截图存下来，供人工检查。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

for (const scheme of ["light", "dark"]) {
  test(`侧栏品牌 lockup(${scheme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await page.addInitScript(t => localStorage.setItem("mpx-theme", t), scheme);
    // 未登录时的静态兜底页也使用相同的品牌墨色。
    await page.goto(env.origin + "/");
    await expect(page.locator("h1")).toHaveText("需要重新打开");
    await expect(page.locator("h1")).toHaveCSS("color", scheme === "light" ? "rgb(47, 48, 51)" : "rgb(221, 222, 224)");
    await login(page, env);
    await expect(page).toHaveTitle("MPX");
    const shown = page.locator(`.brand img.lk-${scheme === "light" ? "l" : "d"}`);
    const hidden = page.locator(`.brand img.lk-${scheme === "light" ? "d" : "l"}`);
    await expect(shown).toBeVisible();
    await expect(hidden).toBeHidden();
    expect(await shown.evaluate(i => i.complete && i.naturalWidth > 0)).toBe(true);
    const svg = await (await page.request.get(await shown.evaluate(i => i.src))).text();
    expect(svg).toContain(`stroke="${scheme === "light" ? "#2F3033" : "#DDDEE0"}"`);
    await expect(page.locator("body")).toHaveCSS("color", scheme === "light" ? "rgb(47, 48, 51)" : "rgb(221, 222, 224)");
    const h = await shown.evaluate(i => i.getBoundingClientRect().height);
    expect(h).toBeGreaterThanOrEqual(20);
    if (process.env.SHOT_DIR) await page.locator("aside.side").screenshot({ path: `${process.env.SHOT_DIR}/sidebar-${scheme}.png` });
  });
}

for (const saved of [null, "dark"]) test(`系统深色时默认浅色，已存选择保留 · ${saved || "首次"}`, async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  if (saved) await page.addInitScript(t => localStorage.setItem("mpx-theme", t), saved);
  const themes = [];
  await page.route("**/local/app/theme", r => { themes.push(r.request().postDataJSON().theme); return r.fulfill({ json: {} }); });
  await login(page, env);
  await expect(page.locator("html")).toHaveAttribute("data-theme", saved || "light");
  await expect.poll(() => themes.at(-1)).toBe(saved || "light");
});
