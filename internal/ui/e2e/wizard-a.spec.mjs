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
  // 内部名不再让用户填,向导里没有这个框。
  await expect(page.locator("#w-name")).toHaveCount(0);
  await expect(page.locator("#drawer")).toContainText("请输入 SSH 密码");

  await page.fill("#w-address", "203.0.113.42");
  await page.fill("#w-ssh-port", "70000");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await expect(page.locator("#drawer")).toContainText("端口要在 1–65535 之间");

  await page.fill("#w-ssh-port", "2222");
  await page.fill("#w-ssh-user", "ubuntu");
  // 界面里不再展示等价命令:抽屉正文不该出现任何 mpx 命令。
  await expect(page.locator("#drawer")).not.toContainText("mpx ");
  await expect(page.locator("#drawer")).not.toContainText(PW);

  await page.click("#w-next");
  await page.click("#w-opt-custom");
  await page.fill("#w-dest", "www.cloudflare.com");
  await page.click("#w-start");
  await expect(page.locator("#drawer")).toContainText("回落目标写成 域名:端口");
});

test("装公钥失败:显示原话,回到第 1 步,没建节点", async ({ page }) => {
  await openWizard(page);
  await page.fill("#w-address", "127.0.0.1");
  await page.fill("#w-ssh-port", "1");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await page.click("#w-start");
  const fail = page.locator(".plist li.fail");
  // 127.0.0.1:1 是连接被拒,不是密码错:提示要说连不上,不能说成密码不对。
  await expect(fail).toContainText("节点还没有建");
  await expect(fail).not.toContainText("密码不对");
  await page.click("#w-back1");
  await expect(page.locator("#w-pw")).toHaveValue("");
  await expect(page.locator("#drawer")).toContainText("请重新输入 SSH 密码");
  const list = await apiJSON("/api/nodes");
  expect(list.length).toBe(0);
  await noLeak(page);
});

test("成功全流程:请求与 CLI 一致", async ({ page }) => {
  // 向导自动起内部名:这个环境里还没有 node-N,第一台就是 node-1。
  const names = ["cli-wz", "node-1"];
  env.takeLog();
  await env.cli("node", "add", "cli-wz", "--address", "127.0.0.1", "--ssh-port", "1");
  const cliNode = (await apiJSON("/api/nodes")).find(n => n.name === "cli-wz");
  await env.cli("inbound", "add", "--node", String(cliNode.id), "--protocol", "vless", "--port", "443",
    "--network", "tcp", "--security", "reality", "--sni", "www.cloudflare.com",
    "--reality-dest", "www.cloudflare.com:443", "--flow", "xtls-rprx-vision");
  const cliInb = (await apiJSON("/api/inbounds")).filter(i => i.node_id === cliNode.id).at(-1);
  // 向导建的入口不起名(订阅里跟随节点显示名),对照的 CLI 也不带 --remark。
  await env.cli("host", "add", "--inbound", String(cliInb.id));
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
  await page.fill("#w-ssh-port", "1");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await page.click("#w-start");
  await expect(page.locator(".done-hd")).toContainText("已就绪");
  expect(seenPw).toBe(PW);

  const uiNode = (await apiJSON("/api/nodes")).find(n => n.name === "node-1");
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
  await page.fill("#w-ssh-port", "1");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await page.click("#w-start");
  await expect(page.locator(".plist li.fail")).toContainText("装核心并下发");
  await expect(page.locator("#w-retry")).toContainText("重试安装");
  await expect(page.locator("#w-later")).toBeVisible();
  const uiNode = (await apiJSON("/api/nodes")).find(n => n.name === "node-2");
  await page.click("#w-later");
  await expect(page).toHaveURL(new RegExp("#nodes/" + uiNode.id));
  await noLeak(page);
});

test("登记链接打开原登记抽屉", async ({ page }) => {
  await openWizard(page);
  await page.click("#drawer .wlnk");
  await expect(page.locator("#n-address")).toBeVisible();
});

// v0.9.5:建入站前先看节点端口。默认的 443 被占时自动换备选端口;自己填的端口被占时
// 停下说清楚,「改端口」后从建入站那步接着跑,不重建节点。
async function stubNodePost(page) {
  await page.route("**/api/nodes", async route => {
    if (route.request().method() !== "POST") return route.continue();
    const body = route.request().postDataJSON();
    delete body.ssh_password;
    await route.continue({ postData: JSON.stringify(body) });
  });
  await page.route("**/api/nodes/*/install", route => route.fulfill({
    status: 200, contentType: "application/json", body: JSON.stringify({ node: "x", core: "xray", version: "v25.1.1" }),
  }));
}
const busyPorts = { known: true, tcp: [{ port: 443, owner: "nginx(进程 1234)" }, { port: 22, owner: "sshd(进程 1)" }], udp: [] };

test("端口:默认 443 被占时自动换 2087", async ({ page }) => {
  await stubNodePost(page);
  await page.route("**/api/nodes/*/ports", route => route.fulfill({ json: busyPorts }));
  const ports = [];
  await page.route("**/api/inbounds", async route => {
    if (route.request().method() === "POST") ports.push(route.request().postDataJSON().port);
    await route.continue();
  });
  await openWizard(page);
  await page.fill("#w-address", "127.0.0.1");
  await page.fill("#w-ssh-port", "1");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await page.click("#w-start");
  await expect(page.locator(".done-hd")).toContainText("已就绪");
  expect(ports).toEqual([2087]);
  await noLeak(page);
});

test("端口:自己填的端口被占时停下,改端口后接着建入站", async ({ page }) => {
  await stubNodePost(page);
  await page.route("**/api/nodes/*/ports", route => route.fulfill({ json: busyPorts }));
  let nodePosts = 0;
  page.on("request", r => { if (r.method() === "POST" && /\/api\/nodes$/.test(r.url())) nodePosts++; });
  await openWizard(page);
  await page.fill("#w-address", "127.0.0.1");
  await page.fill("#w-ssh-port", "1");
  await page.fill("#w-pw", PW);
  await page.click("#w-next");
  await page.click("#w-opt-custom");
  await page.fill("#w-inb-port", "443");
  await page.click("#w-start");
  await expect(page.locator(".plist li.fail")).toContainText("nginx(进程 1234)");
  await page.click("#w-port");
  await page.fill("#w-inb-port", "8443");
  await page.click("#w-start");
  await expect(page.locator(".done-hd")).toContainText("已就绪");
  expect(nodePosts).toBe(1);
  await noLeak(page);
});
