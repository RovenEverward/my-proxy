import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

test.use({ deviceScaleFactor: 2 });
let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

async function openCatalog(page) {
  await login(page, env);
  await page.goto(env.origin + "/components.html");
  await expect(page.getByRole("heading", { name: "MPX 组件目录", exact: true })).toBeVisible();
}

test("组件目录复用产品组件，并独立于业务请求", async ({ page }) => {
  await login(page, env);
  const requests = [];
  page.on("request", r => { if (/\/(api|ui)\//.test(new URL(r.url()).pathname)) requests.push(r.url()); });
  await page.goto(env.origin + "/components.html");
  await expect(page.locator("[data-example=empty] h4")).toHaveText("还没有节点");
  await expect(page.locator("[data-example=error] [role=alert]")).toContainText("连接超时");
  await expect(page.locator("script[src='/components.js']")).toHaveCount(1);
  await expect(page.locator("script[src='/app.js']")).toHaveCount(0);
  await expect(page.locator("[data-example=disabled] button")).toBeDisabled();
  expect(requests).toEqual([]);
});

test("主题与密度独立切换，文字保持同一尺度", async ({ page }) => {
  await openCatalog(page);
  await expect(page.locator(".catalog h1")).toHaveCSS("font-size", "15px");
  await expect(page.locator(".catalog h3").first()).toHaveCSS("font-size", "15px");
  const button = page.locator("[data-example=primary] .btn");
  await expect(button).toHaveCSS("height", "32px");
  await expect(button).toHaveCSS("font-size", "13.5px");
  await page.getByLabel("界面密度").selectOption("compact");
  await expect(button).toHaveCSS("height", "28px");
  await expect(button).toHaveCSS("font-size", "13.5px");
  await page.getByLabel("界面密度").selectOption("comfortable");
  await expect(button).toHaveCSS("height", "36px");
  await expect(button).toHaveCSS("font-size", "13.5px");
  await page.getByLabel("界面密度").selectOption("standard");
  if (process.env.MPX_UX_SHOTS) await page.screenshot({ path: `${process.env.MPX_UX_SHOTS}/components-light.png`, animations: "disabled" });
  await page.getByRole("button", { name: "切换主题", exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  if (process.env.MPX_UX_SHOTS) await page.screenshot({ path: `${process.env.MPX_UX_SHOTS}/components-dark.png`, animations: "disabled" });
});

test("所有图标均能对应本地官方 SVG", async ({ page }) => {
  await openCatalog(page);
  const names = await page.evaluate(() => [...new Set(Object.values(ICON_NAMES))]);
  expect(names.length).toBeGreaterThan(30);
  for (const name of names) {
    const response = await page.request.get(env.origin + `/icons/lucide/${name}.svg`);
    expect(response.ok(), name).toBe(true);
    const svg = await response.text();
    expect(svg).toContain('viewBox="0 0 24 24"');
  }
  expect(await page.locator("[data-icon-name]").evaluateAll(items => items.every(item => item.querySelector("svg[data-lucide]")?.children.length > 0))).toBe(true);
  const output = await page.evaluate(() => { try { ic("missing-icon"); return "silent"; } catch { return "error"; } });
  expect(output).toBe("error");
});

test("主页主题由根元素持有，抽屉继承相同字体与表面", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("mpx-theme", "dark"));
  await login(page, env);
  await expect(page.locator("html")).toHaveAttribute("data-ui", "workspace");
  await expect(page.locator(".side")).toHaveCSS("background-color", "rgb(23, 23, 23)");
  await page.locator(".home-add").click();
  await expect(page.locator("#drawer.open #user-form")).toBeVisible();
  const styles = await page.evaluate(() => ({
    body: getComputedStyle(document.body).fontFamily,
    input: getComputedStyle(document.querySelector("#user-form input")).fontFamily,
    drawer: getComputedStyle(document.querySelector("#drawer")).backgroundColor,
    main: getComputedStyle(document.querySelector("main")).backgroundColor,
  }));
  expect(styles.input).toBe(styles.body);
  expect(styles.body).toContain("Inter");
  expect(styles.drawer).toBe(styles.main);
  await page.keyboard.press("Escape");
  await page.goto(env.origin + "/#users");
  await expect(page.locator("html")).toHaveAttribute("data-ui", "classic");
});

for (const theme of ["light", "dark"]) test(`组件目录窄屏与键盘 · ${theme}`, async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openCatalog(page);
  await page.evaluate(t => document.documentElement.dataset.theme = t, theme);
  expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth)).toBe(false);
  await page.getByLabel("用户名").fill("alice");
  await expect(page.getByLabel("用户名")).toHaveValue("alice");
  await page.keyboard.press("Tab");
  await expect(page.locator(":focus")).toBeInViewport();
  if (process.env.MPX_UX_SHOTS) await page.screenshot({ path: `${process.env.MPX_UX_SHOTS}/components-${theme}-390.png`, animations: "disabled" });
});
