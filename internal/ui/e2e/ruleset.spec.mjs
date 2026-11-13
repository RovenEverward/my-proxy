// 规则集私有化分发:分流页侧栏卡的开关写 settings.ruleset_mode(整表 PUT),
// 镜像管理抽屉列规则组、按组刷新走 POST /api/rulesets/refresh。
// 沙箱里上游规则集取不回来,镜像就绪与否不断言;只验开关的写请求、抽屉结构与刷新请求形状。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env;
const cp = (p, init = {}) => cpFetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
const settings = async () => (await cp("/api/settings")).json();
const restoreMode = async value => { const s = await settings(); await cp("/api/settings", { method: "PUT", body: JSON.stringify({ ...s, ruleset_mode: value }) }); };

test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("user", "add", "alice");
});
test.afterAll(async () => { await restoreMode(""); await env?.stop(); });

test("侧栏卡开关切换写 ruleset_mode,镜像组出现在卡里", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#rules");
  const card = page.locator(".rp-side .card", { has: page.locator(".ch", { hasText: "规则集分发" }) });
  await expect(card).toBeVisible();

  const puts = [];
  page.on("request", r => { if (r.method() === "PUT" && r.url().endsWith("/api/settings")) puts.push(r.postDataJSON()); });
  // 受控 input:写请求回来前 checked 还停在旧值,点外层 .sw 而不是 check()。
  await card.locator("label.sw").click();
  await expect.poll(() => puts.at(-1)?.ruleset_mode).toBe("privatized");
  expect((await settings()).ruleset_mode).toBe("privatized");
  // 内置迁移规则引用了规则组,开启后卡里要能列出来(就绪与否取决于网络,不断言就绪)。
  await expect(card.locator(".rs-mr").first()).toBeVisible();

  await card.locator("label.sw").click();
  await expect.poll(() => puts.at(-1)?.ruleset_mode).toBe("upstream");
  expect((await settings()).ruleset_mode).toBe("upstream");
});

test("管理镜像抽屉列出规则组,按组和全部刷新走同一接口", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#rules");
  await page.locator("[data-act=rs-manage]").first().click();
  const drawer = page.locator("#drawer");
  await expect(drawer).toBeVisible();
  await expect(drawer.locator(".rs-gr").first()).toBeVisible();

  const posts = [];
  page.on("request", r => { if (r.method() === "POST" && r.url().endsWith("/api/rulesets/refresh")) posts.push(r.postDataJSON()); });
  await drawer.locator("[data-act=rs-refresh]").first().click();
  await expect.poll(() => posts.length).toBe(1);
  expect(Object.keys(posts[0] || {})).toEqual(["keys"]);
  expect(posts[0].keys).toHaveLength(1);

  await drawer.locator("[data-act=rs-refresh-all]").click();
  await expect.poll(() => posts.length).toBe(2);
  // 无 body 的 POST:playwright 的 postDataJSON 此时解析成 {}。
  expect(Object.keys(posts[1] || {})).toHaveLength(0);
  await drawer.locator("[data-act=close]").click();
  await expect(drawer).not.toBeVisible();
});
