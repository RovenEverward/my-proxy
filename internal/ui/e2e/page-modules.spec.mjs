// Exercise the delivered UI through its real server and public routes.
// This suite does not depend on script names, module exports, or CSS file layout.
import { test, expect } from "@playwright/test";
import { startEnv, startSetupEnv, login, cpFetch } from "./env.mjs";

const USER = "module-cruise-user";
const NODE = "module-cruise-node";
const PLAN = "module-cruise-plan";
const RULE = "module-cruise.example";
const resourceTypes = new Set(["script", "stylesheet", "font", "image"]);
let env, setupEnv, nodeID;

test.use({ deviceScaleFactor: 2 });
test.beforeAll(async () => {
  env = await startEnv();
  setupEnv = await startSetupEnv();
  await env.seed("node", "add", NODE, "--address", "127.0.0.1", "--ssh-port", "1");
  const response = await cpFetch(env.cpURL + "/api/nodes", { headers: { Authorization: "Bearer " + env.token } });
  expect(response.ok).toBe(true);
  nodeID = (await response.json()).find(node => node.name === NODE)?.id;
  expect(nodeID).toBeTruthy();
  await env.seed("inbound", "add", "--node", String(nodeID), "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.example.com", "--reality-dest", "www.example.com:443");
  await env.seed("plan", "add", PLAN, "--node", NODE);
  await env.seed("user", "add", USER, "--plan", PLAN, "--limit", "10GB");
  await env.seed("rule", "add", "suffix", RULE, "直连");
});
test.afterAll(async () => { await Promise.all([env?.stop(), setupEnv?.stop()]); });

// HTTP 404 is an existing optional-API result, not an uncaught script error.
// Keep these exact cases in the report, so an unexpected 404 remains a failure.
const optional404 = pathname => pathname === "/local/app/theme"
  || pathname === "/api/sources"
  || pathname === "/api/reach/last"
  || /^\/api\/nodes\/\d+\/diagnostics$/.test(pathname);
function observeHealth(page) {
  const scriptErrors = [], consoleErrors = [], resourceErrors = [], optionalResponses = [];
  page.on("pageerror", error => scriptErrors.push(error.stack || error.message));
  page.on("console", message => {
    if (message.type() === "error") consoleErrors.push({ text: message.text(), url: message.location().url });
  });
  page.on("requestfailed", request => {
    if (resourceTypes.has(request.resourceType())) resourceErrors.push({ url: request.url(), error: request.failure()?.errorText });
  });
  page.on("response", response => {
    const pathname = new URL(response.url()).pathname;
    if (resourceTypes.has(response.request().resourceType()) && response.status() >= 400) {
      resourceErrors.push({ url: response.url(), status: response.status() });
    }
    if (response.status() === 404 && optional404(pathname)) optionalResponses.push(response.url());
  });
  return {
    async check(label) {
      const unexpectedConsole = consoleErrors.filter(message => {
        if (!/^Failed to load resource:.*404/.test(message.text) || !message.url) return true;
        return !optionalResponses.some(url => url === message.url);
      });
      expect(scriptErrors, `${label}: uncaught script errors`).toEqual([]);
      expect(resourceErrors, `${label}: failed UI resources`).toEqual([]);
      expect(unexpectedConsole, `${label}: console errors`).toEqual([]);
    },
    async report(testInfo) {
      if (optionalResponses.length) await testInfo.attach("optional-api-404", {
        body: JSON.stringify([...new Set(optionalResponses)], null, 2), contentType: "application/json",
      });
    },
  };
}

async function settled(page) {
  await expect(page.locator("html[data-loading], main[aria-busy=true]")).toHaveCount(0);
  await page.evaluate(() => document.fonts.ready);
}
async function withinViewport(page, label) {
  const overflow = await page.evaluate(() => {
    return [document.documentElement, document.body, document.querySelector("main"), document.querySelector("#drawer.open"), document.querySelector("#modal.open")]
      .filter(element => element && element.scrollWidth > element.clientWidth + 1)
      .map(element => ({ element: element.tagName.toLowerCase(), width: element.clientWidth, content: element.scrollWidth }));
  });
  expect(overflow, `${label}: horizontal overflow`).toEqual([]);
}
async function scrollPage(page, label) {
  await withinViewport(page, label);
  await page.locator("main").evaluate(main => { main.scrollTop = main.scrollHeight; });
  await expect.poll(() => page.locator("main").evaluate(main => Math.abs(main.scrollHeight - main.clientHeight - main.scrollTop))).toBeLessThanOrEqual(1);
  await withinViewport(page, label + " at bottom");
}
async function coldRoute(page, target, hash) {
  // A fresh document catches modules or styles that only work after another page.
  await page.goto("about:blank");
  await page.goto(target.origin + "/#" + hash);
}
async function theme(page, target, value) {
  await page.addInitScript(({ origin, value }) => {
    if (location.origin === origin && localStorage.getItem("mpx-theme") === null) localStorage.setItem("mpx-theme", value);
  }, { origin: target.origin, value });
}

function routes() {
  return [
    ["overview", "总览", page => expect(page.getByRole("button", { name: "添加用户", exact: true }).first()).toBeVisible()],
    ["nodes", "节点", page => expect(page.locator("main").getByText(NODE, { exact: true }).first()).toBeVisible()],
    [`nodes/${nodeID}`, new RegExp("^" + NODE + "(?:\\s|$)"), page => expect(page.locator("main")).toContainText("连接方式")],
    ["users", "用户", page => expect(page.locator("main").getByText(USER, { exact: true })).toBeVisible()],
    [`users/${USER}`, "用户", async page => {
      await expect(page.locator("#drawer").getByRole("heading", { name: USER, exact: true })).toBeVisible();
      await expect(page.locator("#drawer #send")).toBeVisible();
      await expect(page.locator("#drawer [aria-busy=true]")).toHaveCount(0);
    }],
    ["users/plans", "用户", page => expect(page.locator("main").getByText(PLAN, { exact: true })).toBeVisible()],
    ["sub", "订阅", page => expect(page.locator("#sec-link")).toBeVisible()],
    ["sub/info", "订阅", page => expect(page.locator("#sec-info")).toBeInViewport()],
    ["sub/clients", "订阅", page => expect(page.locator("#sec-clients")).toBeInViewport()],
    ["sub/templates", "订阅", page => expect(page.locator("#sec-adv [data-act=tpl-open]").first()).toBeVisible()],
    ["rules", "分流", page => expect(page.locator("main").getByText(RULE, { exact: true })).toBeVisible()],
    ["rules/rules", "分流", page => expect(page.getByLabel("其他全部的去向")).toBeVisible()],
    ["rules/groups", "分流", page => expect(page.locator(".rp-g").first()).toBeVisible()],
    ["doctor", "体检", page => expect(page.getByRole("button", { name: "开始体检", exact: true })).toBeVisible()],
    ["system", "系统", page => expect(page.getByLabel("用量提醒(%)", { exact: true })).toBeVisible()],
    ["system/devices", "系统", page => expect(page.getByLabel("新密码", { exact: true })).toBeVisible()],
    ["system/backup", "系统", page => expect(page.getByRole("button", { name: "下载备份", exact: true })).toBeVisible()],
    ["system/update", "系统", page => expect(page.getByRole("button", { name: "检查更新", exact: true })).toBeVisible()],
  ];
}

async function catalog(page, value, health) {
  await page.goto("about:blank");
  const businessRequests = [];
  const record = request => {
    if (/^\/(?:api|ui|local)(?:\/|$)/.test(new URL(request.url()).pathname)) businessRequests.push(request.url());
  };
  page.on("request", record);
  try {
    await page.goto(env.origin + "/components.html");
    await expect(page.getByRole("heading", { name: "MPX 组件目录", exact: true })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("data-theme", value);
    await page.getByLabel("用户名", { exact: true }).fill(USER);
    await expect(page.getByLabel("用户名", { exact: true })).toHaveValue(USER);
    for (const density of ["compact", "comfortable", "standard"]) await page.getByLabel("界面密度").selectOption(density);
    await page.getByRole("button", { name: "切换主题", exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("data-theme", value === "light" ? "dark" : "light");
    await settled(page);
    await scrollPage(page, "component catalog");
    await health.check("component catalog");
    expect(businessRequests, "the catalog must not read or write business APIs").toEqual([]);
  } finally { page.off("request", record); }
}

for (const value of ["light", "dark"]) for (const width of [1440, 390]) {
  test(`全页面真实入口巡航 · ${value} · ${width}`, async ({ page }, testInfo) => {
    test.setTimeout(120_000);
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await theme(page, env, value);
    const health = observeHealth(page);
    await login(page, env);
    try {
      for (const [hash, title, ready] of routes()) await test.step("direct #" + hash, async () => {
        await coldRoute(page, env, hash);
        await expect(page).toHaveURL(env.origin + "/#" + hash);
        if (hash === `users/${USER}`) {
          // A modal keeps the base DOM, while hiding it from accessibility APIs.
          await expect(page.locator("main h1")).toHaveText(title);
          await expect(page.locator("#drawer").getByRole("heading", { level: 2, name: USER, exact: true })).toBeVisible();
        } else {
          await expect(page.locator("main").getByRole("heading", { level: 1, name: title, exact: true })).toBeVisible();
        }
        await ready(page);
        await settled(page);
        await expect(page.locator("html")).toHaveAttribute("data-theme", value);
        await expect(page.locator("#page [role=alert]").filter({ hasText: /\S/ })).toHaveCount(0);
        await scrollPage(page, hash);
        await health.check(hash);
      });
      await coldRoute(page, env, "overview");
      await settled(page);
      const navigation = page.locator(width === 390 ? ".mnav" : ".side");
      for (const hash of ["nodes", "users", "sub", "overview"]) {
        await navigation.locator(`a[href="#${hash}"]`).click();
        await expect(page).toHaveURL(env.origin + "/#" + hash);
        await settled(page);
      }
      await page.getByRole("button", { name: "切换主题", exact: true }).click();
      await expect(page.locator("html")).toHaveAttribute("data-theme", value === "light" ? "dark" : "light");
      await page.reload();
      await settled(page);
      await expect(page.locator("html")).toHaveAttribute("data-theme", value === "light" ? "dark" : "light");
      await page.getByRole("button", { name: "切换主题", exact: true }).click();
      await expect(page.locator("html")).toHaveAttribute("data-theme", value);
      await health.check("navigation and theme");
      await catalog(page, value, health);
    } finally { await health.report(testInfo); }
  });

  test(`首次安装与接入入口巡航 · ${value} · ${width}`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
    await theme(page, setupEnv, value);
    const health = observeHealth(page);
    await login(page, setupEnv);
    try {
      await expect(page.getByRole("heading", { name: "在你的 VPS 上装好 MPX", exact: true })).toBeVisible();
      await withinViewport(page, "setup welcome");
      await page.getByRole("button", { name: "接入已有控制面", exact: true }).click();
      await expect(page.locator("#s-cp-addr")).toBeVisible();
      await withinViewport(page, "connect control plane");
      await page.getByRole("button", { name: "上一步", exact: true }).click();
      await page.getByRole("button", { name: "首次安装（新服务器）", exact: true }).click();
      await expect(page.locator("#s-host")).toBeVisible();
      await withinViewport(page, "setup connection");
      await expect(page.locator("html")).toHaveAttribute("data-theme", value);
      await health.check("setup and connect entries");
    } finally { await health.report(testInfo); }
  });
}

test("旧地址保持兼容，并加载正确页面", async ({ page }, testInfo) => {
  test.setTimeout(90_000);
  const health = observeHealth(page);
  await login(page, env);
  try {
    for (const [old, current, title] of [
      ["plans", "users/plans", "用户"],
      ["settings", "sub", "订阅"],
      ["settings/general", "sub", "订阅"],
      ["settings/rules", "sub/clients", "订阅"],
      ["settings/templates", "sub/templates", "订阅"],
      ["settings/devices", "system/devices", "系统"],
      ["settings/backup", "system/backup", "系统"],
      ["inbounds", "nodes", "节点"],
      [`inbounds/${nodeID}`, "nodes", "节点"],
    ]) await test.step("legacy #" + old, async () => {
      await coldRoute(page, env, old);
      await expect(page).toHaveURL(env.origin + "/#" + current);
      await expect(page.locator("main").getByRole("heading", { level: 1, name: title, exact: true })).toBeVisible();
      await settled(page);
      await health.check(old);
    });
    await coldRoute(page, env, "sub/look");
    await expect(page.locator("#sec-info")).toBeInViewport();
    await health.check("legacy sub/look anchor");
  } finally { await health.report(testInfo); }
});
