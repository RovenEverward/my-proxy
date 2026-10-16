// 添加节点向导:第 1 步前端校验、第 2 步入站校验、装公钥失败回退、
// 成功全流程与 CLI 逐条对照,以及装核心失败与登记链接。
import { test, expect } from "@playwright/test";
import { startEnv, normalize, login, cpFetch } from "./env.mjs";

let env;
test.beforeAll(async () => { env = await startEnv(); });
test.afterAll(async () => { await env?.stop(); });

const PW = "Wizard-Secret-7788";

async function noLeak(page) {
  const body = await page.evaluate(() => document.body.innerText);
  expect(body).not.toContain(PW);
  expect(page.url()).not.toContain(PW);
  const ls = await page.evaluate(() => JSON.stringify(localStorage));
  expect(ls).not.toContain(PW);
  const vals = await page.evaluate(() =>
    [...document.querySelectorAll("input")].filter(i => i.id !== "w-pw").map(i => i.value));
  for (const v of vals) expect(v).not.toContain(PW);
}

const auth = () => ({ headers: { Authorization: "Bearer " + env.token } });
const apiJSON = async p => (await cpFetch(env.cpURL + p, auth())).json();
// 页面 nodes 请求体去掉 ssh_password 后转发;install 不进记录。只比那三条写请求。
const threeOnly = reqs => reqs.filter(r => !r.path.includes("/install"));
async function openWizard(page) {
  await login(page, env);
  await page.goto(env.origin + "/#nodes");
  await page.click("[data-act=node-wizard]");
}

test("前端校验文案", async ({ page }) => {
  await openWizard(page);
  await page.click("#w-next");
  await expect(page.locator("#drawer")).toContainText("请填写服务器地址");
  await expect(page.locator("#drawer")).toContainText("请填写节点名");
  await expect(page.locator("#drawer")).toContainText("请输入 SSH 密码");

  await page.fill("#w-address", "203.0.113.42");
  await page.fill("#w-name", "123");
  await page.fill("#w-ssh-port", "70000");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await expect(page.locator("#drawer")).toContainText("节点名不能是纯数字");
  await expect(page.locator("#drawer")).toContainText("端口要在 1–65535 之间");

  await page.fill("#w-name", "jp-2");
  await page.fill("#w-ssh-port", "2222");
  await page.fill("#w-ssh-user", "ubuntu");
  await expect(page.locator("#w-cmd")).toHaveText("mpx node add jp-2 --address 203.0.113.42 --ssh-port 2222 --ssh-user ubuntu --ssh-password");
  await expect(page.locator("#w-cmd")).not.toContainText(PW);

  await page.click("#w-next");
  await page.click("#w-opt-custom");
  await page.fill("#w-dest", "www.cloudflare.com");
  await page.click("#w-start");
  await expect(page.locator("#drawer")).toContainText("回落目标写成 域名:端口");
});

test("装公钥失败:显示原话,回到第 1 步,没建节点", async ({ page }) => {
  await openWizard(page);
  await page.fill("#w-address", "127.0.0.1");
  await page.fill("#w-name", "wz-fail");
  await page.fill("#w-ssh-port", "1");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await page.click("#w-start");
  const fail = page.locator(".plist li.fail");
  await expect(fail).toContainText("密码不对");
  await page.click("#w-back1");
  await expect(page.locator("#w-pw")).toHaveValue("");
  await expect(page.locator("#drawer")).toContainText("请重新输入 SSH 密码");
  const list = await apiJSON("/api/nodes");
  expect(list.some(n => n.name === "wz-fail")).toBe(false);
  await noLeak(page);
});

test("成功全流程:请求与 CLI 一致", async ({ page }) => {
  const names = ["cli-wz", "ui-wz"];
  env.takeLog();
  await env.cli("node", "add", "cli-wz", "--address", "127.0.0.1", "--ssh-port", "1");
  const cliNode = (await apiJSON("/api/nodes")).find(n => n.name === "cli-wz");
  await env.cli("inbound", "add", "--node", String(cliNode.id), "--protocol", "vless", "--port", "443",
    "--network", "tcp", "--security", "reality", "--sni", "www.cloudflare.com",
    "--reality-dest", "www.cloudflare.com:443", "--flow", "xtls-rprx-vision");
  const cliInb = (await apiJSON("/api/inbounds")).filter(i => i.node_id === cliNode.id).at(-1);
  await env.cli("host", "add", "--inbound", String(cliInb.id), "--remark", "cli-wz");
  const cliRaw = threeOnly(env.takeLog());

  let seenPw = null;
  await page.route("**/api/nodes", async route => {
    if (route.request().method() !== "POST") return route.continue();
    const body = route.request().postDataJSON();
    expect(body.ssh_password).toBe(PW);
    seenPw = body.ssh_password;
    delete body.ssh_password;
    await route.continue({ postData: JSON.stringify(body) });
  });
  await page.route("**/api/nodes/*/install", route => route.fulfill({
    status: 200, contentType: "application/json",
    body: JSON.stringify({ node: "ui-wz", core: "xray", version: "v25.1.1" }),
  }));

  await openWizard(page);
  await page.fill("#w-address", "127.0.0.1");
  await page.fill("#w-name", "ui-wz");
  await page.fill("#w-ssh-port", "1");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await page.click("#w-start");
  await expect(page.locator(".done-hd")).toContainText("已就绪");
  expect(seenPw).toBe(PW);

  const uiNode = (await apiJSON("/api/nodes")).find(n => n.name === "ui-wz");
  const uiInb = (await apiJSON("/api/inbounds")).filter(i => i.node_id === uiNode.id).at(-1);
  const uiRaw = threeOnly(env.takeLog());
  const ids = [cliNode.id, cliInb.id, uiNode.id, uiInb.id].map(String);
  expect(normalize(uiRaw, names, { ids })).toEqual(normalize(cliRaw, names, { ids }));

  await expect(page.locator(".kvw")).toContainText("REALITY · 443 · 1 个入口");
  await page.click("#w-view");
  await expect(page).toHaveURL(new RegExp("#nodes/" + uiNode.id));
  await noLeak(page);
});

test("装核心失败:给出原因和重试", async ({ page }) => {
  await page.route("**/api/nodes", async route => {
    if (route.request().method() !== "POST") return route.continue();
    const body = route.request().postDataJSON();
    expect(body.ssh_password).toBe(PW);
    delete body.ssh_password;
    await route.continue({ postData: JSON.stringify(body) });
  });
  await openWizard(page);
  await page.fill("#w-address", "127.0.0.1");
  await page.fill("#w-name", "ui-wz2");
  await page.fill("#w-ssh-port", "1");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await page.click("#w-start");
  await expect(page.locator(".plist li.fail")).toContainText("装核心并下发");
  await expect(page.locator("#w-retry")).toContainText("重试安装");
  await expect(page.locator("#w-later")).toBeVisible();
  const uiNode = (await apiJSON("/api/nodes")).find(n => n.name === "ui-wz2");
  await page.click("#w-later");
  await expect(page).toHaveURL(new RegExp("#nodes/" + uiNode.id));
  await noLeak(page);
});

test("登记链接打开原登记抽屉", async ({ page }) => {
  await openWizard(page);
  await page.click("#drawer .wlnk");
  await expect(page.locator("#n-name")).toBeVisible();
});
