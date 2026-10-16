// 侧栏品牌：浅色显示黑色 lockup，深色显示白色 lockup，图片都真的加载出来。
// 设置 SHOT_DIR 时顺便把侧栏截图存下来，供人工检查。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

for (const scheme of ["light", "dark"]) {
  test(`侧栏品牌 lockup(${scheme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await login(page, env);
    await expect(page).toHaveTitle("MPX");
    const shown = page.locator(`.brand img.lk-${scheme === "light" ? "l" : "d"}`);
    const hidden = page.locator(`.brand img.lk-${scheme === "light" ? "d" : "l"}`);
    await expect(shown).toBeVisible();
    await expect(hidden).toBeHidden();
    expect(await shown.evaluate(i => i.complete && i.naturalWidth > 0)).toBe(true);
    const h = await shown.evaluate(i => i.getBoundingClientRect().height);
    expect(h).toBeGreaterThanOrEqual(20);
    if (process.env.SHOT_DIR) await page.locator("aside.side").screenshot({ path: `${process.env.SHOT_DIR}/sidebar-${scheme}.png` });
  });
}
