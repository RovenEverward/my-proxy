import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

test.use({ deviceScaleFactor: 2 });
let env;
test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("node", "add", "tokyo-a", "--address", "203.0.113.25", "--ssh-port", "1");
  await env.seed("user", "add", "typography-user");
});
test.afterAll(async () => { await env?.stop(); });

for (const theme of ["light", "dark"]) for (const width of [1440, 390]) test(`七档字号在主页、列表和表单保持角色一致 · ${theme} ${width}`, async ({ page }) => {
  await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
  await page.addInitScript(t => localStorage.setItem("mpx-theme", t), theme);
  await login(page, env);
  await expect(page.locator(".ov > .ph h1")).toHaveCSS("font-size", "15px");
  await expect(page.locator(".ov-main .ch").first()).toHaveCSS("font-size", "15px");
  await expect(page.locator(".ov-node-id b").first()).toHaveCSS("font-size", "14.5px");
  await expect(page.locator(".ov-n .st").first()).toHaveCSS("font-size", "13.5px");
  await expect(page.locator(".ov .stat .v").first()).toHaveCSS("font-size", "21px");
  await expect(page.locator(".home-add")).toHaveCSS("font-size", "13.5px");
  await expect(page.locator(".top .sub")).toHaveCSS("font-size", "11.5px");
  if (process.env.MPX_TYPE_SHOTS) await page.screenshot({ path: `${process.env.MPX_TYPE_SHOTS}/overview-${theme}-${width}.png`, animations: "disabled" });
  await page.goto(env.origin + "/#users");
  await expect(page.locator(".ph h1")).toHaveCSS("font-size", "15px");
  await expect(page.locator("tbody .u b").first()).toHaveCSS("font-size", "14.5px");
  await expect(page.locator("tbody .st").first()).toHaveCSS("font-size", "13.5px");
  await expect(page.locator(".page .sub").first()).toHaveCSS("font-size", "12.5px");
  if (width === 390) await expect(page.locator(".mnav a").first()).toHaveCSS("font-size", "13.5px");
  else await expect(page.locator(".side .nav").first()).toHaveCSS("font-size", "13.5px");
  if (process.env.MPX_TYPE_SHOTS) await page.screenshot({ path: `${process.env.MPX_TYPE_SHOTS}/users-${theme}-${width}.png`, animations: "disabled" });
  await page.locator('[data-act="user-new"]').first().click();
  await expect(page.locator("#drawer.open h2")).toHaveCSS("font-size", "15px");
  await expect(page.locator("#user-form .field label").first()).toHaveCSS("font-size", "13.5px");
  await expect(page.locator("#user-form input").first()).toHaveCSS("font-size", "14.5px");
  await expect(page.locator("#user-form .btn").last()).toHaveCSS("font-size", "13.5px");
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  if (process.env.MPX_TYPE_SHOTS) await page.screenshot({ path: `${process.env.MPX_TYPE_SHOTS}/form-${theme}-${width}.png`, animations: "disabled" });
});
