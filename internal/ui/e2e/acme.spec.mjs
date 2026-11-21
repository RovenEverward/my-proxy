// 系统页「域名与证书」tab(UC-26):PUT /api/acme/config 走真实控制面接口,
// 签发 POST /api/acme/issue 与进度用路由摆成假任务,免得依赖真的 CA 与 DNS。
// 覆盖:配置保存与回显、签发事件流走完出完成文案、改域名提示证书作废、
// 关闭 ACME 要确认且发 DELETE。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const statusBody = (over = {}) => ({ domain: "", cf_token_set: false, staging: false, cert_issued: false, issue_state: "idle", ...over });
async function mockStatus(page, body) {
  await page.route("**/api/acme/status", r => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }));
}

test("填域名与 CF token 保存,刷新后域名还在;签发按钮从禁用变可用", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#system/acme");
  const issue = page.locator("[data-act=acme-issue]");
  await expect(issue).toBeDisabled();
  await expect(page.locator("#acme-token")).toHaveAttribute("placeholder", /Cloudflare|粘贴/);
  await page.fill("#acme-domain", "cp.example.net");
  await page.fill("#acme-token", "cf-token-example-1234567890");
  await page.click("#acme-save");
  await expect(page.locator(".toast")).toContainText("已保存");
  // token 存进去了但表单清空不回显。
  await expect(page.locator("#acme-token")).toHaveValue("");
  await expect(page.locator("#acme-token")).toHaveAttribute("placeholder", /已保存/);
  await expect(issue).toBeEnabled();
  await expect(page.locator("#acme-form")).toContainText("已保存");
  // 刷新页面再进 tab:域名是控制面真实返回的。
  await page.goto(env.origin + "/#overview");
  await page.goto(env.origin + "/#system/acme");
  await expect(page.locator("#acme-domain")).toHaveValue("cp.example.net");
});

test("签发:POST 起任务,事件逐条出现,done 后出完成文案", async ({ page }) => {
  const events = [
    { seq: 1, msg: "向 ACME 下订单" },
    { seq: 2, msg: "写入 TXT 记录,等公网生效" },
    { seq: 3, msg: "证书已签发并存库" },
  ];
  const state = { v: "running" };
  let posts = 0, gets = 0;
  await page.route("**/api/acme/issue*", r => {
    if (r.request().method() === "POST") { posts++; return r.fulfill({ status: 202, contentType: "application/json", body: JSON.stringify({ job: 3 }) }); }
    gets++;
    const since = +(new URL(r.request().url()).searchParams.get("since") || 0);
    return r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ job: 3, state: state.v, seq: 3, events: events.filter(e => e.seq > since) }) });
  });
  await login(page, env);
  await page.goto(env.origin + "/#system/acme");
  // 上一条用例把配置真的写进了库里,域名与 token 都在,按钮直接可用。
  await page.click("[data-act=acme-issue]");
  const panel = page.locator("[data-act=acme-events]");
  await expect(panel).toContainText("向 ACME 下订单", { timeout: 10000 });
  await expect(panel).toContainText("写入 TXT 记录", { timeout: 10000 });
  state.v = "done";
  await expect(page.locator(".toast")).toContainText("证书签发完成", { timeout: 10000 });
  await expect(page.locator(".c-ok")).toContainText("签发完成");
  expect(posts).toBe(1);
  expect(gets).toBeGreaterThan(1);
});

test("改已有证书的域名:保存后提示证书作废;关闭 ACME 走确认且发 DELETE", async ({ page }) => {
  await mockStatus(page, statusBody({ domain: "cp.example.net", cf_token_set: true, cert_issued: true, cert_not_after: "2027-02-01T00:00:00Z" }));
  const deletes = [];
  await page.route("**/api/acme/config", r => {
    if (r.request().method() === "DELETE") { deletes.push(1); return r.fulfill({ status: 200, json: {} }); }
    return r.fallback();
  });
  await login(page, env);
  await page.goto(env.origin + "/#system/acme");
  await expect(page.locator(".card", { hasText: "证书状态" })).toContainText("cp.example.net");
  await page.fill("#acme-domain", "new-cp.example.net");
  await page.click("#acme-save");
  await expect(page.locator(".toast")).toContainText("原证书已作废");
  // 关闭 ACME:确认框取消不发请求,确认才发 DELETE。
  await page.click("[data-act=acme-off]");
  await expect(page.locator("#modal.open")).toContainText("关闭域名与证书");
  await page.click("[data-act=modal-cancel]");
  expect(deletes).toEqual([]);
  await page.click("[data-act=acme-off]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已关闭 ACME");
  expect(deletes).toEqual([1]);
});
