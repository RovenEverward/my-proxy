// 批量识别显示名:节点页标题旁的「批量识别显示名」按钮 → 弹窗 → 生成对照表(plan)
// → 写入这 N 个节点(apply)。用 page.route 模拟两个本地接口,不碰真实 ipwho.is;
// 断言先 plan 后 apply、apply 原样回传 plan 的 plans、取消不发 apply。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  // --auto-locate=false:节点建出来没有显示名,页面上才会出现按钮。
  for (const n of ["rl-a", "rl-b"]) await env.seed("node", "add", n, "--address", "127.0.0.1", "--ssh-port", "1");
});
test.afterAll(async () => { await env?.stop(); });

const rows = [
  { id: 1, name: "rl-a", old: "rl-a", new: "🇺🇸 洛杉矶 A", note: "ipwho.is 1.2.3.4" },
  { id: 2, name: "rl-b", old: "rl-b", new: "🇺🇸 洛杉矶 B", note: "ipwho.is 1.2.3.5" },
];
const plans = [
  { id: 1, name: "rl-a", metadata: { region: "US", city: "洛杉矶", letter: "A" } },
  { id: 2, name: "rl-b", metadata: { region: "US", city: "洛杉矶", letter: "B" } },
];

// mockRelabel:返回计划表,并把 plan/apply 的请求记下来。
async function mockRelabel(page, calls) {
  await page.route("**/local/relabel/plan", r => {
    calls.push({ kind: "plan", body: r.request().postDataJSON() });
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ rows, plans }) });
  });
  await page.route("**/local/relabel/apply", r => {
    calls.push({ kind: "apply", body: r.request().postDataJSON() });
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ results: [{ id: 1, name: "rl-a", display: "🇺🇸 洛杉矶 A" }, { id: 2, name: "rl-b", display: "🇺🇸 洛杉矶 B" }] }) });
  });
}

test("按钮只在有没显示名的节点时出现,plan 先于 apply", async ({ page }) => {
  const calls = [];
  await mockRelabel(page, calls);
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  const open = page.locator("[data-act=relabel-open]");
  await expect(open).toBeVisible();

  await open.click();
  await expect(page.locator("#modal.open h2")).toHaveText("批量识别显示名");
  // 说明里明确节点 IP 会发给第三方;弹窗里不再出现命令。
  await expect(page.locator("#modal")).toContainText("ipwho.is");
  await expect(page.locator("#modal")).not.toContainText("mpx ");
  expect(calls).toEqual([]); // 打开弹窗本身不发请求

  await page.click("#rl-plan");
  await expect(page.locator("#rl-table")).toContainText("🇺🇸 洛杉矶 B");
  await expect(page.locator("#rl-apply")).toHaveText("写入这 2 个节点");
  expect(calls.map(c => c.kind)).toEqual(["plan"]);
  expect(calls[0].body).toEqual({ all: true });

  await page.click("#rl-apply");
  await expect(page.locator(".toast")).toContainText("已写入 2 个节点");
  // apply 原样回传 plan 的 plans,不再重新识别。
  expect(calls.length).toBe(2);
  expect(calls[1].kind).toBe("apply");
  expect(calls[1].body).toEqual({ plans: plans.map(p => ({ id: p.id, name: p.name, metadata: p.metadata })) });
});

test("取消不发 apply", async ({ page }) => {
  const calls = [];
  await mockRelabel(page, calls);
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await page.click("[data-act=relabel-open]");
  await page.click("#rl-plan");
  await expect(page.locator("#rl-apply")).toBeVisible();
  await page.click("[data-act=modal-cancel]");
  await expect(page.locator("#modal.open")).toBeHidden();
  expect(calls.map(c => c.kind)).toEqual(["plan"]);
});
