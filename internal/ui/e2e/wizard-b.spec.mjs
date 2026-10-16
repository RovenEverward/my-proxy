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

// d. 完整流程:check/run/jobs 全部用 page.route 模拟,断言增量拉取与请求形状。
test("d. 完整流程(接口用 page.route 模拟)", async ({ page }) => {
  const sinceArgs = [];
  const respLens = [];
  let jobCalls = 0;

  await page.route("**/local/setup/check", async route => {
    const body = route.request().postDataJSON();
    expect(body.password).toBe(PW);
    const body0 = JSON.stringify({ probe: { os: "Linux", arch: "x86_64", uid: "0", fetch: "/usr/bin/curl" } });
    respLens.push(body0.length);
    await route.fulfill({ status: 200, contentType: "application/json", body: body0 });
  });
  await page.route("**/local/setup/run", async route => {
    const body = route.request().postDataJSON();
    expect(body.password).toBeUndefined();
    expect(body.device_password).toBe(DPW);
    expect(body.username).toBe("alice");
    expect(body.name).toBe("tokyo");
    const body0 = JSON.stringify({ id: "j1" });
    respLens.push(body0.length);
    await route.fulfill({ status: 202, contentType: "application/json", body: body0 });
  });
  await page.route("**/local/setup/jobs/j1?since=*", async route => {
    sinceArgs.push(new URL(route.request().url()).searchParams.get("since"));
    jobCalls++;
    let obj;
    if (jobCalls === 1) {
      obj = { id: "j1", seq: 3, state: "running", steps: [
        { title: "看清这台机器", state: "done", secs: 1.2 },
        { title: "在 VPS 上装控制面(监听 0.0.0.0:8080)", state: "run", secs: 0 },
      ], lines: ["a", "b"], elapsed: 3 };
    } else if (jobCalls === 2) {
      obj = { id: "j1", seq: 4, state: "running", more: true, lines: ["c"], elapsed: 4 };
    } else if (jobCalls === 3) {
      obj = { id: "j1", seq: 4, state: "running", elapsed: 5 };
    } else {
      obj = { id: "j1", seq: 9, state: "done", steps: [
        { title: "看清这台机器", state: "done", secs: 1.2 },
        { title: "在 VPS 上装控制面(监听 0.0.0.0:8080)", state: "done", secs: 6.1 },
      ], result: { host_port: "203.0.113.10:8080", fingerprint: "SHA256:abc", connected: false, username: "alice" }, elapsed: 9 };
    }
    const body0 = JSON.stringify(obj);
    respLens.push(body0.length);
    await route.fulfill({ status: 200, contentType: "application/json", body: body0 });
  });

  await openSetup(page);
  await page.locator("#s-start").click();
  await page.locator("#s-host").fill("203.0.113.10");
  await page.locator("#s-port").fill("2222");
  await page.locator("#s-pw").fill(PW);
  await page.locator("#s-check").click();
  await expect(page.locator(".probe")).toContainText("Linux");
  await expect(page.getByText("可以安装")).toBeVisible();
  await page.locator("#s-next").click();
  await expect(page.locator("#s-username")).toBeVisible();
  await page.locator("#s-username").fill("alice");
  await page.locator("#s-name").fill("tokyo");
  await page.locator("#s-dpw").fill(DPW);
  await page.locator("#s-dpw2").fill(DPW);
  await page.locator("#s-run").click();

  await expect(page.locator(".logt")).toContainText("a");
  await expect(page.locator(".logt")).toContainText("c");
  await expect(page.locator(".done-hd")).toContainText("装好了", { timeout: 30_000 });
  await expect(page.getByText("mpx connect 203.0.113.10:8080", { exact: true })).toBeVisible();
  await expect(page.getByText("本机没有自动接入")).toBeVisible();

  expect(sinceArgs.slice(0, 4)).toEqual(["0", "3", "4", "4"]);
  for (const n of respLens) expect(n).toBeLessThan(4096);
  await noLeak(page);
});

// f. 空增量退避到 5s/10s;页面隐藏时不再取,恢复可见立刻补一次。
test("f. 轮询退避与页面隐藏时暂停", async ({ page }) => {
  await page.clock.install();
  let jobCalls = 0;

  await page.route("**/local/setup/check", route => route.fulfill({
    status: 200, contentType: "application/json",
    body: JSON.stringify({ probe: { os: "Linux", arch: "x86_64", uid: "0", fetch: "/usr/bin/curl" } }),
  }));
  await page.route("**/local/setup/run", route => route.fulfill({
    status: 202, contentType: "application/json", body: JSON.stringify({ id: "j2" }),
  }));
  await page.route("**/local/setup/jobs/j2?since=*", async route => {
    jobCalls++;
    await route.fulfill({
      status: 200, contentType: "application/json",
      body: JSON.stringify({ id: "j2", seq: 1, state: "running", elapsed: 1 }),
    });
  });

  await openSetup(page);
  await page.locator("#s-start").click();
  await page.locator("#s-host").fill("203.0.113.10");
  await page.locator("#s-pw").fill(PW);
  await page.locator("#s-check").click();
  await expect(page.getByText("可以安装")).toBeVisible();
  await page.locator("#s-next").click();
  await page.locator("#s-username").fill("alice");
  await page.locator("#s-dpw").fill(DPW);
  await page.locator("#s-dpw2").fill(DPW);
  await page.locator("#s-run").click();
  await expect.poll(() => jobCalls).toBe(1);

  // 手动驱动轮询:3 次空增量后退避到 5s,6 次后到 10s。
  for (let i = 0; i < 2; i++) await page.evaluate(() => window.__setupPoll.poll());
  expect(await page.evaluate(() => window.__setupPoll.delay)).toBe(5000);
  for (let i = 0; i < 3; i++) await page.evaluate(() => window.__setupPoll.poll());
  expect(await page.evaluate(() => window.__setupPoll.delay)).toBe(10000);
  expect(await page.evaluate(() => window.__setupPoll.idle)).toBeGreaterThanOrEqual(6);

  // 隐藏页面:定时器被清掉,推进 30 秒也不再多发请求。
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(await page.evaluate(() => window.__setupPoll.timer)).toBeNull();
  const paused = jobCalls;
  await page.clock.runFor(30_000);
  expect(jobCalls).toBe(paused);

  // 恢复可见:立即补一次请求。
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await expect.poll(() => jobCalls).toBeGreaterThan(paused);
});

// g. 任务失败:显示后端原话与重新运行;失败屏等价命令里不能有密码。
test("g. 失败屏与重新运行", async ({ page }) => {
  await page.route("**/local/setup/check", route => route.fulfill({
    status: 200, contentType: "application/json",
    body: JSON.stringify({ probe: { os: "Linux", arch: "x86_64", uid: "0", fetch: "/usr/bin/curl" } }),
  }));
  await page.route("**/local/setup/run", route => route.fulfill({
    status: 202, contentType: "application/json", body: JSON.stringify({ id: "j3" }),
  }));
  await page.route("**/local/setup/jobs/j3?since=*", route => route.fulfill({
    status: 200, contentType: "application/json",
    body: JSON.stringify({
      id: "j3", seq: 2, state: "failed", error: "download mpx: exit status 28", elapsed: 4,
      steps: [
        { title: "看清这台机器", state: "done", secs: 1.2 },
        { title: "在 VPS 上装控制面(监听 0.0.0.0:8080)", state: "fail", secs: 2.8 },
      ],
    }),
  }));

  await openSetup(page);
  await page.locator("#s-start").click();
  await page.locator("#s-host").fill("203.0.113.10");
  await page.locator("#s-pw").fill(PW);
  await page.locator("#s-check").click();
  await expect(page.getByText("可以安装")).toBeVisible();
  await page.locator("#s-next").click();
  await page.locator("#s-username").fill("alice");
  await page.locator("#s-dpw").fill(DPW);
  await page.locator("#s-dpw2").fill(DPW);
  await page.locator("#s-run").click();

  await expect(page.locator(".callout.bad .raw")).toContainText("download mpx: exit status 28");
  await expect(page.locator(".callout.bad")).toContainText("安装失败");
  await expect(page.locator(".plist li.fail").last()).toBeVisible();
  await expect(page.locator("#s-rerun")).toBeVisible();

  const cmd = await page.locator(".setup .cmd").last().textContent();
  expect(cmd).not.toContain(PW);
  expect(cmd).not.toContain(DPW);
  await noLeak(page);
});
