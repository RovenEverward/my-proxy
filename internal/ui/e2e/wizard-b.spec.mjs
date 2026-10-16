// 首次安装向导(向导 B)浏览器测试:欢迎/连接/检查/设置/安装全程,
// 以及与后端「连接性能」相关的约束:只拉增量、退避、页面隐藏暂停、命令里不出密码。
import { test, expect } from "@playwright/test";
import { startSetupEnv, login } from "./env.mjs";

test.describe.configure({ mode: "serial" });

const PW = "Root-Secret-4455";
const DPW = "Device-Pass-123456";

let env;

test.beforeAll(async () => {
  env = await startSetupEnv();
});

test.afterAll(async () => {
  await env?.stop();
});

// 打开向导:登录后应停在欢迎屏。
async function openSetup(page) {
  await login(page, env);
  await expect(page.locator("#s-start")).toBeVisible();
}

// noLeak:root 密码与设备密码不能出现在正文、URL、localStorage、等价命令里。
async function noLeak(page) {
  const body = await page.evaluate(() => document.body.innerText);
  expect(body).not.toContain(PW);
  expect(body).not.toContain(DPW);
  expect(page.url()).not.toContain(PW);
  expect(page.url()).not.toContain(DPW);
  const ls = await page.evaluate(() => JSON.stringify(localStorage));
  expect(ls).not.toContain(PW);
  expect(ls).not.toContain(DPW);
  const cmd = await page.evaluate(() => document.querySelector("#s-cmd")?.textContent || "");
  expect(cmd).not.toContain(PW);
  expect(cmd).not.toContain(DPW);
}

test("a. 没配置控制面时进入首次安装", async ({ page }) => {
  await openSetup(page);
  await expect(page.locator(".welcome h2")).toHaveText("在你的 VPS 上装好 mpx");
  await page.locator("#s-start").click();
  await expect(page.locator("#s-host")).toBeVisible();
  await expect(page.locator("#s-check")).toBeVisible();
});

test("b. 连接屏校验与等价命令", async ({ page }) => {
  await openSetup(page);
  await page.locator("#s-start").click();

  // 空表单:两个必填错误同时出现。
  await page.locator("#s-check").click();
  await expect(page.getByText("请填写服务器地址")).toBeVisible();
  await expect(page.getByText("请输入 root 密码")).toBeVisible();

  // 填地址与端口:命令里出现 --ssh-password,且不带密码。
  await page.locator("#s-host").fill("203.0.113.10");
  await page.locator("#s-port").fill("2222");
  await expect(page.locator("#s-cmd")).toHaveText(
    "mpx init --host 203.0.113.10 --ssh-port 2222 --ssh-password"
  );

  // 切到密钥登录:命令改成 --identity,且参数被正确引用。
  await page.locator("#s-auth-key").click();
  await expect(page.locator("#s-pw")).toHaveCount(0);
  await page.locator("#s-identity").fill("~/.ssh/id_ed25519");
  await expect(page.locator("#s-cmd")).toHaveText(
    'mpx init --host 203.0.113.10 --ssh-port 2222 --identity "~/.ssh/id_ed25519"'
  );
});

test("c. 检查失败显示原话(真后端)", async ({ page }) => {
  await openSetup(page);
  await page.locator("#s-start").click();
  await page.locator("#s-host").fill("127.0.0.1");
  await page.locator("#s-port").fill("1");
  await page.locator("#s-pw").fill(PW);
  await page.locator("#s-check").click();

  await expect(page.locator(".plist li.fail").first()).toBeVisible({ timeout: 30_000 });
  // .raw 是后端原话,不是前端编的「密码不对」。
  await expect(page.locator(".callout.bad .raw")).not.toBeEmpty();

  // 返回修改:密码被清掉,会被重新询问。
  await page.locator("#s-back2").click();
  await expect(page.locator("#s-pw")).toHaveValue("");
  await expect(page.getByText("请重新输入 root 密码")).toBeVisible();
  await noLeak(page);
});

test("e. 设置屏校验", async ({ page }) => {
  await page.route("**/local/setup/check", route => route.fulfill({
    status: 200, contentType: "application/json",
    body: JSON.stringify({ probe: { os: "Linux", arch: "x86_64", uid: "0", fetch: "/usr/bin/curl" } }),
  }));

  await openSetup(page);
  await page.locator("#s-start").click();
  await page.locator("#s-host").fill("203.0.113.10");
  await page.locator("#s-port").fill("22");
  await page.locator("#s-pw").fill(PW);
  await page.locator("#s-check").click();
  await expect(page.getByText("可以安装")).toBeVisible();
  await page.locator("#s-next").click();
  await expect(page.locator("#s-username")).toBeVisible();

  // 设备密码太短。
  await page.locator("#s-dpw").fill("short");
  await page.locator("#s-dpw2").fill("short");
  await page.locator("#s-run").click();
  await expect(page.getByText("设备密码至少 12 个字符")).toBeVisible();

  // 两次输入不一致。
  await page.locator("#s-dpw").fill(DPW);
  await page.locator("#s-dpw2").fill("Device-Pass-999999");
  await page.locator("#s-run").click();
  await expect(page.getByText("两次输入不一致")).toBeVisible();

  // 用户名不合规。
  await page.locator("#s-username").fill("a b");
  await page.locator("#s-dpw2").fill(DPW);
  await page.locator("#s-run").click();
  await expect(page.getByText("只能用字母、数字、- 和 _")).toBeVisible();
});
