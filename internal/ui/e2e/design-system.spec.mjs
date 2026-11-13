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
  const loadedScripts = new Set();
  page.on("request", request => { if (request.resourceType() === "script") loadedScripts.add(request.url()); });
  await login(page, env);
  await expect(page.locator(".app")).toBeVisible();
  const productEntries = await page.locator('script[type="module"][src]').evaluateAll(elements => elements.map(element => element.src));
  const classicScripts = new Set(await page.locator('script:not([type="module"])[src]').evaluateAll(elements => elements.map(element => element.src)));
  expect(productEntries.length).toBeGreaterThan(0);
  const productScripts = new Set(loadedScripts);
  await page.goto("about:blank");
  loadedScripts.clear();
  const requests = [];
  page.on("request", request => { if (/^\/(api|ui|local)(?:\/|$)/.test(new URL(request.url()).pathname)) requests.push(request.url()); });
  await page.goto(env.origin + "/components.html");
  await expect(page.locator("[data-example=empty] h4")).toHaveText("还没有节点");
  await expect(page.locator("[data-example=error] [role=alert]")).toContainText("连接超时");
  const catalogEntries = await page.locator('script[type="module"][src]').evaluateAll(elements => elements.map(element => element.src));
  expect(catalogEntries.length).toBeGreaterThan(0);
  expect(catalogEntries.some(entry => productEntries.includes(entry))).toBe(false);
  // Compare actual loaded chunks. Build hashes and chunk names may change.
  const shared = [...loadedScripts].filter(script => productScripts.has(script) && !classicScripts.has(script)
    && !productEntries.includes(script) && !catalogEntries.includes(script));
  expect(shared.length, "产品与目录必须加载同一份公共 React 组件 chunk").toBeGreaterThan(0);
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
  const icons = await page.locator("[data-icon-name]").evaluateAll(items => items.map(item => {
    const svg = item.querySelector("svg[data-lucide]");
    return { alias: item.dataset.iconName, name: svg?.getAttribute("data-lucide"), paths: svg?.children.length || 0 };
  }));
  expect(icons.length).toBeGreaterThan(30);
  expect(icons.every(icon => icon.alias && icon.name && icon.paths > 0)).toBe(true);
  expect(new Set(icons.map(icon => icon.alias)).size).toBe(icons.length);
  const names = [...new Set(icons.map(icon => icon.name))];
  expect(names.length).toBeGreaterThan(30);
  for (const name of names) {
    const response = await page.request.get(env.origin + `/icons/lucide/${name}.svg`);
    expect(response.ok(), name).toBe(true);
    const svg = await response.text();
    expect(svg).toContain('viewBox="0 0 24 24"');
  }
  // Unknown aliases are rejected by the offline icon and type checks.
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
