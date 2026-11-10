// 「外部订阅」页:真控制面 + 可控 Node HTTP 上游,走完 UC-03 的整条链:
// 新增 → 授权 → 混合预览 → 刷新失败留快照 → 停用恢复 → 删除;
// 另测策略组来源筛选(含旧 PUT 不清关系)、表单错误保留、键盘与窄屏。
// 文件内用例串行,前一个用例造的数据后面接着用。
import http from "node:http";
import net from "node:net";
import { mkdirSync } from "node:fs";
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env, upstream, upstreamBody;
const freePort = () => new Promise(res => { const s = net.createServer(); s.listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => res(p)); }); });
const cp = (p, init = {}) => cpFetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
const getJSON = async p => (await cp(p)).json();

// 上游默认应答:两条 SS 外部节点(都能忠实表达;不带 TLS 的协议会被正确跳过,不拿来做冒烟主体)。
const CLASH_YAML = `proxies:
  - name: ext-jp-tokyo
    type: ss
    server: 198.51.100.5
    port: 8388
    cipher: aes-256-gcm
    password: ss-secret
  - name: ext-sg
    type: ss
    server: 198.51.100.9
    port: 8488
    cipher: chacha20-ietf-poly1305
    password: sg-secret
`;

test.beforeAll(async () => {
  env = await startEnv();
  upstreamBody = CLASH_YAML;
  const port = await freePort();
  upstream = http.createServer((req, res) => {
    if (upstreamBody === "__FAIL__") { res.writeHead(500); res.end("upstream exploded"); return; }
    res.writeHead(200, { "Content-Type": "text/yaml" }); res.end(upstreamBody);
  });
  await new Promise(r => upstream.listen(port, "127.0.0.1", r));
  env.upstreamURL = `http://127.0.0.1:${port}/clash-meta?secret=rawsecret`;

  // 自建节点 + 入站 + 入口:让预览里的「自建」行是真数据。
  const node = await (await cp("/api/nodes", { method: "POST", body: JSON.stringify({ name: "smoke-node", address: "203.0.113.10" }) })).json();
  const inbound = await (await cp("/api/inbounds", { method: "POST", body: JSON.stringify({ node_id: node.id, protocol: "shadowsocks", port: 8388, network: "tcp", security: "none" }) })).json();
  await cp(`/api/inbounds/${inbound.id}/hosts`, { method: "POST", body: JSON.stringify({ remark: "自建专线" }) });
  await env.seed("user", "add", "alice");
  await env.seed("user", "add", "bob");
});
test.afterAll(async () => { upstream?.close(); await env?.stop(); });

const open = async page => {
  await login(page, env);
  await page.goto(env.origin + "/#sources");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
};
// 来源行按名定位;详情抽屉的字段统一从 #drawer 里取。
const rowOf = (page, name) => page.locator(".source-row", { hasText: name });
const drawer = page => page.locator("#drawer.open");
// 等控制面把一次刷新落到终态(refreshing 变假)。
const settle = async id => {
  for (let i = 0; i < 100; i++) {
    const v = await getJSON(`/api/sources/${id}`);
    if (!v.refreshing) return v;
    await new Promise(r => setTimeout(r, 200));
  }
  throw new Error("刷新超过 20 秒没落停");
};
const shotDir = ".visual-design/mixed-sources";

// 用例之间是真依赖:前一个造的数据后面接着用。串行声明后,一步失败剩余
// 跳过 —— 不然 worker 重启 beforeAll 会造出空环境,后面的失败全是假象。
test.describe.configure({ mode: "serial" });

test("新增来源、授权用户、按用户与格式混合预览", async ({ page }) => {
  await open(page);
  await expect(page.locator(".ph h1")).toHaveText("外部订阅");
  await expect(page.locator(".source-list")).toContainText("还没有外部订阅");

  // --- 新增 ---
  await page.click('[data-act=source-new]');
  await expect(drawer(page)).toBeVisible();
  await expect(page.locator("#source-drawer-title")).toHaveText("添加外部订阅");
  await page.fill("#source-name", "测试源");
  await page.fill("#source-url", env.upstreamURL);
  await page.fill("#source-interval", "6");
  await page.fill("#source-region", "JP");
  await page.click("#source-save");
  await expect(page.locator(".toast")).toContainText("来源已添加");

  // 请求形状:POST 体只带契约字段,UI 不发明额外键。
  const posted = env.takeLog().find(r => r.method === "POST" && r.path === "/api/sources");
  expect(posted).toBeTruthy();
  expect(JSON.parse(posted.body)).toEqual({ name: "测试源", url: env.upstreamURL, interval_seconds: 21600, region: "JP" });

  // 详情抽屉接着打开(可能正在拉,也可能已拉完):轮询到 2 个节点。
  await expect(drawer(page)).toBeVisible();
  const first = await getJSON("/api/sources");
  const sid = first[0].id;
  await settle(sid);
  await expect(page.locator("#drawer")).toContainText("2 个节点");
  // 地址遮蔽:抽屉与列表都不出现真 URL。
  const drawerText = await page.locator("#drawer").innerText();
  for (const bad of [env.upstreamURL, "rawsecret", "ss-secret", "sg-secret"]) expect(drawerText).not.toContain(bad);
  await page.keyboard.press("Escape");
  await expect(rowOf(page, "测试源")).toContainText("2 个节点");

  // --- 授权:勾选 alice,PUT 整表 ---
  await rowOf(page, "测试源").locator('[data-act=source-open]').click();
  await expect(drawer(page)).toBeVisible();
  await page.locator('#source-grants-form input[name=user][value="alice"]').check();
  await page.click("#source-grants-save");
  await expect(page.locator(".toast")).toContainText("接收用户已保存");
  const granted = env.takeLog().find(r => r.method === "PUT" && /\/api\/sources\/\d+\/grants$/.test(r.path));
  expect(JSON.parse(granted.body)).toEqual({ users: ["alice"] });
  expect((await getJSON(`/api/sources/${sid}/grants`)).users).toEqual(["alice"]);
  await page.keyboard.press("Escape");

  // --- 混合预览:alice 有自建+外部,bob 只有自建 ---
  await page.selectOption("#source-preview-user", "alice");
  await page.selectOption("#source-preview-format", "clash-meta");
  await expect(page.locator("#source-preview")).toContainText("ext-jp-tokyo");
  await expect(page.locator("#source-preview")).toContainText("ext-sg");
  await expect(page.locator("#source-preview")).toContainText("自建专线");
  await expect(page.locator("#source-preview")).toContainText("测试源 · SS"); // 协议短名:SS
  await page.selectOption("#source-preview-user", "bob");
  await expect(page.locator("#source-preview")).not.toContainText("ext-jp-tokyo");
  await expect(page.locator("#source-preview")).toContainText("自建专线");
});

test("刷新失败:旧快照保留、错误上屏、订阅不受影响", async ({ page }) => {
  const list = await getJSON("/api/sources");
  const sid = list.find(s => s.name === "测试源").id;

  upstreamBody = "__FAIL__";
  try {
    await cp(`/api/sources/${sid}/refresh`, { method: "POST" });
    const settled = await settle(sid);
    expect(settled.last_error).toBeTruthy();
    expect(settled.node_count).toBe(2); // 快照不能动
    expect(settled.last_good_at).toBeTruthy();
  } finally { upstreamBody = CLASH_YAML; }

  await open(page);
  const row = rowOf(page, "测试源");
  await expect(row).toContainText("使用上次结果");
  await row.locator('[data-act=source-open]').click();
  await expect(page.locator("#drawer")).toContainText("个节点");
  // last_error 上屏(alert 段落),且说明正在用上次结果。
  await expect(page.locator('#drawer p.c-bad[role="alert"]')).toContainText("正在使用");
  // 订阅侧不受影响:alice 仍拿得到外部节点。
  const preview = await getJSON("/api/users/alice/subscription-preview?format=clash-meta");
  expect(preview.nodes.some(n => n.name.includes("ext-jp-tokyo"))).toBe(true);
  await page.keyboard.press("Escape");
});

test("停用后订阅不再有外部节点,恢复后回来", async ({ page }) => {
  const sid = (await getJSON("/api/sources")).find(s => s.name === "测试源").id;
  await open(page);
  await rowOf(page, "测试源").locator('[data-act=source-open]').click();
  await page.locator('#drawer [data-act=source-toggle]').click();
  await expect(page.locator(".toast")).toContainText("已停用");
  const offPatch = env.takeLog().filter(r => r.method === "PATCH" && r.path === `/api/sources/${sid}`).pop();
  expect(JSON.parse(offPatch.body)).toEqual({ enabled: false });
  await expect(rowOf(page, "测试源")).toContainText("已停用");
  await expect(page.locator("#source-preview")).not.toContainText("ext-jp-tokyo");

  await rowOf(page, "测试源").locator('[data-act=source-open]').click();
  await page.locator('#drawer [data-act=source-toggle]').click();
  await expect(page.locator(".toast")).toContainText("已恢复");
  await expect(rowOf(page, "测试源")).not.toContainText("已停用");
  await page.selectOption("#source-preview-user", "alice");
  await expect(page.locator("#source-preview")).toContainText("ext-jp-tokyo");
});

test("策略组来源筛选:界面勾选、旧 PUT 保留、清空等于全部", async ({ page }) => {
  // 造组 + 来源条件(经控制面直发,等价于 mpx group add --source)。
  const g = await (await cp("/api/proxy-groups", { method: "POST", body: JSON.stringify({ name: "混合组", mode: "manual" }) })).json();
  const sid = (await getJSON("/api/sources")).find(s => s.name === "测试源").id;
  const wanted = ["managed", "source:" + sid];
  await cp(`/api/proxy-groups/${g.id}/sources`, { method: "PUT", body: JSON.stringify({ sources: wanted }) });

  await login(page, env);
  await page.goto(env.origin + "/#rules/groups");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  const row = page.locator(".rp-g", { hasText: "混合组" });
  await expect(row).toContainText("自建节点 + 测试源");
  await expect(row).toContainText("自建专线");
  // 未选预览用户时外部节点不进预览(授权按人),行内只列自建。
  await expect(row).not.toContainText("ext-jp-tokyo");

  // 旧式整组 PUT(不带 sources 字段)不许清掉来源条件 —— AC-14。
  const model = await getJSON(`/api/proxy-groups/${g.id}`);
  await cp(`/api/proxy-groups/${g.id}`, { method: "PUT", body: JSON.stringify(model) });
  expect((await getJSON(`/api/proxy-groups/${g.id}/sources`)).sources).toEqual(wanted);
  await page.reload();
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator(".rp-g", { hasText: "混合组" })).toContainText("自建节点 + 测试源");

  // 界面编辑:勾选是真实 chips;去掉「测试源」只留下「自建节点」。
  await page.locator(".rp-g", { hasText: "混合组" }).click();
  await expect(page.locator("#gp-form")).toBeVisible();
  await expect(page.locator('input[data-source-choice][value="managed"]')).toBeChecked();
  await expect(page.locator(`input[data-source-choice][value="source:${sid}"]`)).toBeChecked();
  // 选预览用户 alice 才能看到外部节点进组。
  await page.selectOption("#gp-user", "alice");
  const admitted = (hasText) => page.locator("#gp-pv .pp-pvr:not(.miss)", { hasText });
  const missed = (hasText) => page.locator("#gp-pv .pp-pvr.miss", { hasText });
  await expect(admitted("ext-jp-tokyo")).toHaveCount(1);
  // chip 的 input 是透明的(可访问性走 label),点 chip 文本。
  await page.locator('.chip', { hasText: "测试源" }).click();
  await expect(page.locator(`input[data-source-choice][value="source:${sid}"]`)).not.toBeChecked();
  // 被来源条件排除的节点仍列出并标原因(UC 要求看到为什么被排掉),只是不入选。
  await expect(admitted("ext-jp-tokyo")).toHaveCount(0);
  await expect(missed("ext-jp-tokyo")).toContainText("来源不符");
  await expect(admitted("自建专线")).toHaveCount(1);
  await page.click("#gp-save");
  await expect(page.locator(".toast")).toContainText("已保存");
  expect((await getJSON(`/api/proxy-groups/${g.id}/sources`)).sources).toEqual(["managed"]);
  const sourcesPut = env.takeLog().filter(r => r.method === "PUT" && r.path === `/api/proxy-groups/${g.id}/sources`).pop();
  expect(JSON.parse(sourcesPut.body)).toEqual({ sources: ["managed"] });
  await expect(page.locator(".rp-g", { hasText: "混合组" })).toContainText("自建节点");
  await expect(page.locator(".rp-g", { hasText: "混合组" })).not.toContainText("测试源");

  // 全不选 = 全部来源(空数组),组条件显示回「全部节点」。
  await page.locator(".rp-g", { hasText: "混合组" }).click();
  await page.locator('.chip', { hasText: "自建节点" }).click();
  await expect(page.locator('input[data-source-choice][value="managed"]')).not.toBeChecked();
  const emptyPut = page.waitForResponse(r => r.url().includes(`/api/proxy-groups/${g.id}/sources`) && r.request().method() === "PUT");
  await page.click("#gp-save");
  await emptyPut;
  expect((await getJSON(`/api/proxy-groups/${g.id}/sources`)).sources).toEqual([]);
  await expect(page.locator(".rp-g", { hasText: "混合组" })).toContainText("全部节点");
  // 还原回来,供后面的删除用例看「来源已删除」。
  await cp(`/api/proxy-groups/${g.id}/sources`, { method: "PUT", body: JSON.stringify({ sources: wanted }) });
});

test("来源表单:拒收保留输入,修正后能存", async ({ page }) => {
  await open(page);
  await page.click('[data-act=source-new]');
  await page.fill("#source-name", "会被拒的源");
  // 服务端校验:非 http/https 地址拒收(客户端只查非空)。
  await page.fill("#source-url", "ftp://up.example/sub");
  await page.click("#source-save");
  await expect(page.locator("#source-err")).not.toBeEmpty();
  // 输入一个不丢。
  await expect(page.locator("#source-name")).toHaveValue("会被拒的源");
  await expect(page.locator("#source-url")).toHaveValue("ftp://up.example/sub");
  await expect(page.locator("#source-url")).toHaveAttribute("aria-invalid", "true");

  // 修成能用的地址就能存。
  await page.fill("#source-url", env.upstreamURL);
  await page.click("#source-save");
  await expect(page.locator(".toast")).toContainText("来源已添加");
  // 清理:这个用例的来源不属于主链,删走别留名字冲突。
  const extra = (await getJSON("/api/sources")).find(s => s.name === "会被拒的源");
  await settle(extra.id);
  await cp(`/api/sources/${extra.id}`, { method: "DELETE" });
});

test("抽屉键盘:进入聚焦、Tab 不跑出去、Escape 还回触发点", async ({ page }) => {
  await open(page);
  const trigger = page.locator('[data-act=source-new]');
  await trigger.focus();
  await page.keyboard.press("Enter");
  await expect(drawer(page)).toBeVisible();
  // 进入焦点在表单内。
  await expect(page.locator("#drawer input:focus")).toHaveCount(1);
  // Tab 一路按到底再回绕,始终不出抽屉。
  const inside = async () => page.evaluate(() => !!document.activeElement?.closest("#drawer"));
  for (let i = 0; i < 30; i++) { await page.keyboard.press("Tab"); expect(await inside()).toBe(true); }
  await page.keyboard.press("Shift+Tab");
  expect(await inside()).toBe(true);
  // Escape 关掉并把焦点还给触发按钮。
  await page.keyboard.press("Escape");
  await expect(page.locator("#drawer.open")).toHaveCount(0);
  await expect(page.locator('[data-act=source-new]:focus')).toHaveCount(1);
});

test("删除来源:确认框点名,删后列表清空、组里显示来源已删除", async ({ page }) => {
  await open(page);
  await rowOf(page, "测试源").locator('[data-act=source-open]').click();
  await page.locator('#drawer [data-act=source-delete]').click();
  await expect(page.locator("#modal")).toContainText("删除订阅来源 测试源");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已删除来源");
  await expect(page.locator(".source-list")).toContainText("还没有外部订阅");
  expect(await getJSON("/api/sources")).toEqual([]);

  // 策略组的来源条件还在,但名字只能显示「来源已删除」。
  await page.goto(env.origin + "/#rules/groups");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator(".rp-g", { hasText: "混合组" })).toContainText("来源已删除");
});

test("窄屏 390:列表可读、抽屉不溢出、预览可选", async ({ page }) => {
  // 重造一个来源再验窄屏。
  upstreamBody = CLASH_YAML;
  const r = await cp("/api/sources", { method: "POST", body: JSON.stringify({ name: "测试源", url: env.upstreamURL, interval_seconds: 21600 }) });
  const created = await r.json();
  await settle(created.id);
  await cp(`/api/sources/${created.id}/grants`, { method: "PUT", body: JSON.stringify({ users: ["alice"] }) });

  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  await expect(page.locator(".ph h1")).toHaveText("外部订阅");
  await expect(page.locator(".ph h1")).toBeVisible();
  await expect(rowOf(page, "测试源")).toBeVisible();
  // 页面不许横向滚动。
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await rowOf(page, "测试源").locator('[data-act=source-open]').click();
  await expect(drawer(page)).toBeVisible();
  // 抽屉不许比视口宽。
  const box = await drawer(page).boundingBox();
  expect(box.width).toBeLessThanOrEqual(390);
  await page.keyboard.press("Escape");
  await page.selectOption("#source-preview-user", "alice");
  await expect(page.locator("#source-preview")).toContainText("ext-jp-tokyo");
  await cp(`/api/sources/${created.id}`, { method: "DELETE" });
});

// 截图基线:真装配(真控制面+上游)的桌面浅/深与手机两主题,2x。
// 存 .visual-design/mixed-sources/,文件名带视口与主题。
test("视觉取证:桌面 1440 与手机 390,深浅两主题", async ({ page: _p, browser }) => {
  upstreamBody = CLASH_YAML;
  const r = await cp("/api/sources", { method: "POST", body: JSON.stringify({ name: "测试源", url: env.upstreamURL, interval_seconds: 21600 }) });
  const created = await r.json();
  await settle(created.id);
  await cp(`/api/sources/${created.id}/grants`, { method: "PUT", body: JSON.stringify({ users: ["alice"] }) });
  mkdirSync(shotDir, { recursive: true });

  for (const [label, width, height, mobile] of [["desktop", 1440, 900, false], ["mobile", 390, 844, true]]) {
    for (const theme of ["light", "dark"]) {
      const context = await browser.newContext({
        viewport: { width, height }, deviceScaleFactor: 2,
        locale: "zh-CN", timezoneId: "Asia/Shanghai",
        isMobile: mobile, hasTouch: mobile,
      });
      const page = await context.newPage();
      await page.addInitScript(t => localStorage.setItem("mpx-theme", t), theme);
      await login(page, env);
      await page.goto(env.origin + "/#sources");
      await expect(page.locator("html[data-loading]")).toHaveCount(0);
      await page.selectOption("#source-preview-user", "alice");
      await expect(page.locator("#source-preview")).toContainText("ext-jp-tokyo");
      await expect(page.locator("#source-preview")).toContainText("自建专线");
      await page.mouse.move(1, 1);
      await page.screenshot({ path: `${shotDir}/sources-list-${label}-${theme}@2x.png` });
      // 详情抽屉也留一张(desktop light 与 mobile dark 已覆盖两种形态)。
      if ((label === "desktop" && theme === "light") || (label === "mobile" && theme === "dark")) {
        await rowOf(page, "测试源").locator('[data-act=source-open]').click();
        await expect(page.locator("#drawer")).toContainText("格式兼容");
        await page.mouse.move(1, 1);
        await page.screenshot({ path: `${shotDir}/source-drawer-${label}-${theme}@2x.png` });
      }
      await context.close();
    }
  }
  await cp(`/api/sources/${created.id}`, { method: "DELETE" });
});
