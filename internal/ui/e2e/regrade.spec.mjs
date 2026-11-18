// IP 质量评级(UC-14):节点页「IP 质量评级」按钮 → 弹窗 → 用现有分数预览对照表
// (POST /api/regrade)→ 写入改名(POST /api/regrade {apply:true})。
// 评级写入用 page.route 模拟接口:真实 regrade 需要探测过的分数存档,SSH 替身
// 出不了分;这里验证「预览 → 对照表 → 确认写入」的页面流与请求形状。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  for (const n of ["rg-a", "rg-b"]) await env.seed("node", "add", n, "--address", "127.0.0.1", "--ssh-port", "1");
});
test.afterAll(async () => { await env?.stop(); });

const preview = {
  rows: [
    { id: 1, name: "rg-a", display: "🇺🇸 洛杉矶 B", region: "US", city: "洛杉矶", score: 90, verdict: "ok", old: "B", new: "🇺🇸 洛杉矶 A", changed: true },
    { id: 2, name: "rg-b", display: "🇺🇸 洛杉矶 A", region: "US", city: "洛杉矶", score: 60, verdict: "ok", old: "A", new: "🇺🇸 洛杉矶 B", changed: true },
  ],
  plans: [
    { id: 1, name: "rg-a", metadata: { region: "US", city: "洛杉矶", letter: "A" } },
    { id: 2, name: "rg-b", metadata: { region: "US", city: "洛杉矶", letter: "B" } },
  ],
};

async function mockRegrade(page, calls) {
  await page.route("**/api/regrade", r => {
    const body = r.request().postDataJSON() || {};
    calls.push(body);
    const applied = body.apply === true;
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...preview, applied }) });
  });
}

test("预览 → 对照表 → 确认写入", async ({ page }) => {
  const calls = [];
  await mockRegrade(page, calls);
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  const open = page.locator("[data-act=grade-open]");
  await expect(open).toBeVisible();

  await open.click();
  await expect(page.locator("#modal.open h2")).toHaveText("IP 质量评级");
  await expect(page.locator("#modal")).toContainText("ipapi.is");
  expect(calls).toEqual([]); // 打开弹窗本身不发请求

  // 「用现有分数预览」直接拿对照表,不探测。
  await page.click("#rg-preview");
  await expect(page.locator("#rg-table")).toContainText("洛杉矶 A");
  await expect(page.locator("#rg-table")).toContainText("90");
  await expect(page.locator("#rg-apply")).toHaveText("写入这 2 个改名");
  expect(calls).toEqual([{}]);

  await page.click("#rg-apply");
  await expect(page.locator(".toast")).toContainText("已重排 2 个节点");
  expect(calls.length).toBe(2);
  expect(calls[1]).toEqual({ apply: true });
});

test("取消不发 apply", async ({ page }) => {
  const calls = [];
  await mockRegrade(page, calls);
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await page.click("[data-act=grade-open]");
  await page.click("#rg-preview");
  await expect(page.locator("#rg-apply")).toBeVisible();
  await page.click("[data-act=modal-cancel]");
  await expect(page.locator("#modal.open")).toBeHidden();
  expect(calls.length).toBe(1);
});

test("质量列在有评分后出现", async ({ page }) => {
  await page.route("**/api/quality", r => r.fulfill({
    status: 200, contentType: "application/json",
    body: JSON.stringify({ nodes: [{ node_id: 1, checked_at: "2026-11-18T12:00:00Z", verdict: "ok", score: 95, report: { egress_ip: "1.2.3.4", region: "US" } }] }),
  }));
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  const table = page.locator(".tw table").first();
  await expect(table.locator("th", { hasText: "质量" })).toBeVisible();
  await expect(table.locator("td[data-v=ok]")).toContainText("95");
});
