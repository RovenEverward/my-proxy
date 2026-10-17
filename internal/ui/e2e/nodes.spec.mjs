// 节点、入站、入口页的写操作:每一条都和对应的 mpx node / inbound / host 命令对照。
// 节点地址是 127.0.0.1:1,SSH 立刻被拒,于是下发、安装、体检都会失败 —— 正好检查页面
// 把控制面的原话报出来。
import { test, expect } from "@playwright/test";
import { startEnv, normalize, login, cpFetch } from "./env.mjs";

let env;
const cliTry = async (...a) => { try { await env.cli(...a); } catch { /* 失败也要比请求 */ } };
test.beforeAll(async () => {
  env = await startEnv();
  // 节点 1/2、3/4 两两是 twin;入站 1/2、3/4 分别挂在节点 1/2 上;入口 1/2 挂在入站 1/2 上。
  for (const n of ["cli-n", "ui-n", "cli-rm", "ui-rm"]) await env.seed("node", "add", n, "--address", "127.0.0.1", "--ssh-port", "1");
  for (const port of ["443", "2000"]) for (const node of ["1", "2"]) await env.seed("inbound", "add", "--node", node, "--protocol", "vless", "--port", port, "--security", "reality", "--sni", "www.cloudflare.com", "--reality-dest", "www.cloudflare.com:443", "--tag", `t-${node}-${port}`);
  for (const inb of ["1", "2"]) await env.seed("host", "add", "--inbound", inb, "--remark", "入口", "--address", "a.example.com");
});
test.afterAll(async () => { await env?.stop(); });

const take = (names, opt) => normalize(env.takeLog(), names, opt);
async function openNode(page, id) {
  await page.goto(env.origin + "/#nodes/" + id);
  await expect(page.locator(".ph").first()).toContainText(id === 2 ? "ui-n" : "ui-rm");
}

// 打开某条连接的编辑抽屉并展开「高级」。保存后列表会整页重画,点到正在被换掉的行
// 会落空。
async function openConnEdit(page, id) {
  await expect(page.locator("#drawer.open")).toHaveCount(0);
  // 保存后整页重画:按下和松开要是落在新旧两个元素上,浏览器就不发 click。所以等这一轮取数、
  // 重画结束(<html data-loading> 摘掉)再点,只点一次 —— 连点会多耗限流桶,控制面回 429 反而更慢。
  // 点行尾的铅笔:行中间可能正好是「N 个入口」展开按钮。
  await expect(page.locator("html[data-loading]")).toHaveCount(0, { timeout: 20000 });
  await page.locator(`.cx-row[data-id='${id}'] button[data-act=conn-edit]`).click();
  await expect(page.locator("#drawer.open #conn-form")).toBeVisible({ timeout: 20000 });
  await page.click("#conn-form details.adv2 > summary");
}

test("添加节点:与 mpx node add 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-new", "ui-new"];
  env.takeLog();
  await env.cli("node", "add", "cli-new", "--address", "198.51.100.7", "--ssh-port", "2222", "--ssh-user", "ubuntu");
  const fromCLI = take(names);
  await page.goto(env.origin + "/#nodes");
  await page.click("[data-act=node-wizard]");
  await page.click("#drawer .wlnk");
  // 内部名收在「高级」里,可不填(自动生成);这里填上,好和 CLI 的请求逐字对照。
  await page.click("details.adv summary");
  await page.fill("#n-name", "ui-new");
  await page.fill("#n-address", "198.51.100.7");
  await page.fill("#n-ssh-port", "2222");
  await page.fill("#n-ssh-user", "ubuntu");
  // 抽屉里不再展示等价命令。
  await expect(page.locator("#node-form")).not.toContainText("mpx ");
  await page.click("#n-save");
  await expect(page.locator("h1")).toContainText("ui-new");
  expect(take(names)).toEqual(fromCLI);
});

test("编辑节点:与 mpx node edit 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-n", "ui-n"], opt = { ids: ["1", "2"] };
  env.takeLog();
  await env.cli("node", "edit", "1", "--address", "127.0.0.9", "--ssh-user", "deploy");
  const fromCLI = take(names, opt);
  await openNode(page, 2);
  await page.click("[data-act=node-menu]");
  await page.click("[data-act=node-edit]");
  await page.fill("#n-address", "127.0.0.9");
  await page.fill("#n-ssh-user", "deploy");
  await expect(page.locator("#node-form")).not.toContainText("mpx ");
  await page.click("#n-save");
  await expect(page.locator(".toast")).toContainText("已更新节点 ui-n");
  expect(take(names, opt)).toEqual(fromCLI);
});

test("出口 IP:与 mpx node edit --outbound-ip 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-n", "ui-n"], opt = { ids: ["1", "2"] };
  env.takeLog();
  await env.cli("node", "edit", "1", "--outbound-ip", "v4");
  const fromCLI = take(names, opt);
  await openNode(page, 2);
  await page.click("[data-act=node-menu]");
  await page.click("[data-act=node-edit]");
  await page.click("details.adv summary");
  await page.selectOption("#n-outbound-ip", "v4");
  await expect(page.locator("#node-form")).not.toContainText("mpx ");
  await page.click("#n-save");
  await expect(page.locator(".toast")).toContainText("已更新节点 ui-n");
  expect(take(names, opt)).toEqual(fromCLI);
});

test("显示名:与 mpx node edit --region / --city 一致,只发 metadata", async ({ page }) => {
  await login(page, env);
  const names = ["cli-n", "ui-n"], opt = { ids: ["1", "2"] };
  env.takeLog();
  await env.cli("node", "edit", "1", "--region", "US", "--city", "洛杉矶");
  const fromCLI = take(names, opt);
  expect(fromCLI.map(r => r.method + " " + r.path)).toEqual(["PUT /api/nodes/<id>/metadata"]);
  await openNode(page, 2);
  await page.click("[data-act=node-menu]");
  await page.click("[data-act=node-edit]");
  await page.fill("#n-region", "us");
  await page.fill("#n-city", "洛杉矶");
  await expect(page.locator("#node-form")).not.toContainText("mpx ");
  // 国旗在页面上是 <img class="flag">,文本里只剩城市;服务端仍存 emoji(下面的请求对照)。
  await expect(page.locator("#n-display img.flag")).toHaveAttribute("src", "/flags/us.svg");
  await expect(page.locator("#n-display")).toContainText("洛杉矶");
  await page.click("#n-save");
  await expect(page.locator(".toast img.flag")).toHaveAttribute("src", "/flags/us.svg");
  await expect(page.locator(".toast")).toContainText("洛杉矶 B");
  expect(take(names, opt)).toEqual(fromCLI);
  // 列表只显示显示名;内部名只在详情里作为次要信息。
  await page.goto(env.origin + "/#nodes");
  const row = page.locator("tbody tr", { hasText: "洛杉矶 B" }).first();
  await expect(row).toBeVisible();
  await expect(row.locator("img.flag")).toHaveAttribute("src", "/flags/us.svg");
  await expect(row).not.toContainText("ui-n");
  await expect(row).not.toContainText("#2");
  await openNode(page, 2);
  await expect(page.locator("h1")).toContainText("洛杉矶 B");
  await expect(page.locator("h1 img.flag")).toHaveAttribute("src", "/flags/us.svg");
  await expect(page.locator(".ph p").first()).toContainText("内部名 ui-n");
});

test("排水要确认、取消不发请求;排水与取消排水和 CLI 一致", async ({ page }) => {
  await login(page, env);
  const opt = { ids: ["1", "2"] };
  env.takeLog();
  await env.cli("node", "drain", "1");
  const drainCLI = take([], opt);
  await env.cli("node", "undrain", "1");
  const undrainCLI = take([], opt);
  await openNode(page, 2);
  await page.click("[data-act=node-menu]");
  await page.click("[data-act=node-drain]");
  await page.click("[data-act=modal-cancel]");
  expect(env.takeLog()).toEqual([]);
  await page.click("[data-act=node-menu]");
  await page.click("[data-act=node-drain]");
  await page.click("#okb");
  await expect(page.locator("h1")).toContainText("已暂停分配");
  expect(take([], opt)).toEqual(drainCLI);
  await page.click("[data-act=node-menu]");
  await page.click("[data-act=node-drain]"); // 恢复分配不用确认
  await expect(page.locator("[data-act=node-drain]")).toHaveText("暂停分配");
  expect(take([], opt)).toEqual(undrainCLI);
});

// 下发、安装、体检会 SSH 到节点:这里连不上,页面要把控制面给的原话留在确认框里。
for (const [title, args, act, fill] of [
  ["下发", ["node", "sync"], "node-sync", null],
  ["安装(指定 sing-box 与版本)", ["node", "install", "--core", "sing-box", "--version", "1.12.0"], "node-install", async page => { await page.selectOption("#i-core", "sing-box"); await page.fill("#i-ver", "1.12.0"); await expect(page.locator("#modal")).not.toContainText("mpx "); }],
  ["体检(默认参数)", ["node", "diagnose"], "node-diagnose", null],
]) {
  test(`${title}:与 CLI 请求一致,失败时显示控制面原话`, async ({ page }) => {
    await login(page, env);
    const opt = { ids: ["1", "2"] };
    env.takeLog();
    const [verb, sub, ...rest] = args;
    await cliTry(verb, sub, "1", ...rest);
    const fromCLI = take([], opt);
    expect(fromCLI.length).toBe(1);
    await openNode(page, 2);
    await page.click(`[data-act=${act}]`);
    if (fill) await fill(page);
    await page.click("#okb");
    const err = page.locator("#modal-err");
    await expect(err).not.toBeEmpty({ timeout: 60_000 });
    const res = env.lastResponse();
    expect(take([], opt)).toEqual(fromCLI);
    // 原话:页面显示的就是控制面这次响应里的 error。
    expect(res.status).toBeGreaterThanOrEqual(400);
    await expect(err).toHaveText(JSON.parse(res.body).error);
  });
}

test("添加入站:与 mpx inbound add 一致(含 QUIC 默认 udp + tls)", async ({ page }) => {
  await login(page, env);
  const opt = { ids: ["1", "2"] };
  env.takeLog();
  await env.cli("inbound", "add", "--node", "1", "--protocol", "vless", "--port", "8080", "--network", "ws", "--ws-path", "/ws", "--max-users", "50");
  await env.cli("inbound", "add", "--node", "1", "--protocol", "hysteria2", "--port", "8443", "--sni", "hy.example.com", "--cert-file", "/etc/c.pem", "--key-file", "/etc/k.pem");
  const fromCLI = take([], opt);
  // 节点详情的「连接方式」卡片:添加连接 → 其他协议… 打开完整入站表单。
  await page.goto(env.origin + "/#nodes/2");
  await page.click(".conn-card [data-act=conn-new]");
  await page.click("#drawer.open [data-act=conn-other]");
  await page.fill("#i-port", "8080");
  await page.selectOption("#i-network", "ws");
  await page.fill("#i-ws_path", "/ws");
  await page.click("details.adv summary");
  await page.fill("#i-max_users", "50");
  await expect(page.locator("#inb-form")).not.toContainText("mpx ");
  await page.click("#x-later");
  await expect(page.locator(".toast")).toContainText("已创建入站");
  await page.click(".conn-card [data-act=conn-new]");
  await page.click("#drawer.open [data-act=conn-other]");
  await expect(page.locator("#drawer.open #i-port")).toHaveValue(""); // 旧抽屉关上后还在 DOM 里,等新表单
  await page.selectOption("#i-protocol", "hysteria2");
  await expect(page.locator("#i-network")).toHaveValue("udp");
  await expect(page.locator("#i-security")).toHaveValue("tls");
  await page.fill("#i-port", "8443");
  await page.fill("#i-sni", "hy.example.com");
  await page.fill("#i-cert_file", "/etc/c.pem");
  await page.fill("#i-key_file", "/etc/k.pem");
  await expect(page.locator("#inb-form")).not.toContainText("mpx ");
  await page.click("#x-later");
  await expect(page.locator(".toast")).toContainText("已创建入站");
  expect(take([], opt)).toEqual(fromCLI);
});

test("编辑入站:与 mpx inbound edit 一致", async ({ page }) => {
  await login(page, env);
  const names = ["t-1-443", "t-2-443"], opt = { ids: ["1", "2"], drop: ["reality_pub", "short_ids"] };
  env.takeLog();
  await env.cli("inbound", "edit", "1", "--port", "9443", "--sni", "www.example.com", "--max-users", "10", "--mux");
  const fromCLI = take(names, opt);
  await page.goto(env.origin + "/#nodes/2");
  await page.click(".cx-row[data-act=conn-edit][data-id='2']");
  // 入站字段收在编辑连接抽屉的「高级」里。
  await page.click("#conn-form details.adv2 > summary");
  await page.fill("#i-port", "9443");
  await page.fill("#i-sni", "www.example.com");
  await page.click("details.adv summary");
  await page.fill("#i-max_users", "10");
  await page.check("input[name=mux]");
  await expect(page.locator("#conn-form")).not.toContainText("mpx ");
  await expect(page.locator("#i-save")).toHaveText("保存并应用到节点");
  await page.click("#x-later");
  // 提示里的节点名不带国旗。
  await expect(page.locator(".toast")).toContainText("洛杉矶 B 显示「有改动未应用」");
  expect(take(names, opt)).toEqual(fromCLI);
});

test("入口:add / edit / rm 与 mpx host 一致,删除要确认", async ({ page }) => {
  await login(page, env);
  const opt = { ids: ["1", "2"] };
  env.takeLog();
  await env.cli("host", "add", "--inbound", "1", "--remark", "直连", "--port", "8443", "--alpn", "h2,http/1.1");
  const addCLI = take([], opt);
  await env.cli("host", "edit", "1", "--inbound", "1", "--address", "", "--remark", "备用");
  const editCLI = take([], opt);
  await env.cli("host", "rm", "1");
  const rmCLI = take([], opt);

  await page.goto(env.origin + "/#nodes/2");
  // 入口按钮在编辑连接抽屉的「高级」里。
  await openConnEdit(page, 2);
  await page.click("[data-act=conn-host-new][data-inb='2']");
  await page.fill("#h-remark", "直连");
  await page.fill("#h-port", "8443");
  await page.fill("#h-alpn", "h2,http/1.1");
  await expect(page.locator("#host-form")).not.toContainText("mpx ");
  await page.click("#h-save");
  await expect(page.locator(".toast")).toContainText("已创建入口「直连」");
  expect(take([], opt)).toEqual(addCLI);

  await openConnEdit(page, 2);
  await page.click("[data-act=conn-host-edit][data-id='2']");
  await expect(page.locator("#drawer.open h2")).toHaveText("编辑入口「入口」");
  await page.fill("#h-address", "");
  await page.fill("#h-remark", "备用");
  await expect(page.locator("#host-form")).not.toContainText("mpx ");
  await page.click("#h-save");
  await expect(page.locator(".toast")).toContainText("已更新入口「备用」");
  expect(take([], opt)).toEqual(editCLI);

  await openConnEdit(page, 2);
  await page.click("[data-act=conn-host-edit][data-id='2']");
  await expect(page.locator("#drawer.open h2")).toHaveText("编辑入口「备用」");
  // 删除入口和节点详情一样是灰卡灰按钮,红色只留给确认框里最后那下。
  const hzone = page.locator("#drawer.open .card").filter({ hasText: "删除入口" });
  await expect(hzone).not.toHaveClass(/danger-zone/);
  await expect(page.locator("[data-act=conn-host-rm]")).not.toHaveClass(/c-bad/);
  await page.click("[data-act=conn-host-rm]");
  await page.click("[data-act=modal-cancel]");
  expect(env.takeLog()).toEqual([]);
  await page.click("[data-act=conn-host-rm]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已删除入口「备用」");
  expect(take([], opt)).toEqual(rmCLI);
});

test("删除入站与节点:要逐字输入名字才放行,和 CLI 一致", async ({ page }) => {
  await login(page, env);
  let opt = { ids: ["3", "4"] };
  env.takeLog();
  await env.cli("inbound", "rm", "3");
  const inbCLI = take([], opt);
  await page.goto(env.origin + "/#nodes/2");
  await page.click(".cx-row[data-act=conn-edit][data-id='4']");
  await page.click("#conn-form details.adv2 > summary");
  // 删除入站也是灰卡灰按钮(这里打开的是「编辑连接」抽屉),不再用 danger-zone / c-bad。
  const izone = page.locator("#drawer.open .dz").filter({ hasText: "删除连接" });
  await expect(izone.locator("[data-act=conn-rm]")).not.toHaveClass(/c-bad/);
  await page.click("[data-act=conn-rm]");
  await expect(page.locator("#okb")).toBeDisabled();
  await expect(page.locator("#okb")).toHaveText("删除并应用到节点");
  await expect(page.locator("#altb")).toBeDisabled();
  await page.fill("#typed", "t-2-2000");
  await page.click("#altb");
  await expect(page.locator(".toast")).toContainText("已删除入站「t-2-2000」");
  expect(take([], opt)).toEqual(inbCLI);

  env.takeLog();
  await env.cli("node", "rm", "3");
  const nodeCLI = take([], opt);
  await openNode(page, 4);
  await page.click("[data-act=node-rm]");
  await page.fill("#typed", "ui-r");
  await expect(page.locator("#okb")).toBeDisabled();
  await page.fill("#typed", "ui-rm");
  await page.click("#okb");
  await expect(page).toHaveURL(/#nodes$/);
  expect(take([], opt)).toEqual(nodeCLI);
  const r = await cpFetch(env.cpURL + "/api/nodes/4", { headers: { Authorization: "Bearer " + env.token } });
  expect(r.status).toBe(404);
});

// 「创建并应用到节点」:先建入站再走现有的 POST /api/nodes/{id}/sync。这里 SSH 连不上,
// 应用失败要把原因告诉使用者,入站照样建好,卡片留着「有改动未应用」,头部换成刷新状态。
test("创建并应用到节点:应用失败时入站保留,连不上时头部换成刷新", async ({ page }) => {
  await login(page, env);
  // 节点 1 的巡检状态取决于后台那一轮跑没跑到,这里固定成「连不上」,断言才稳。
  await page.route("**/api/nodes/1", async route => {
    const response = await route.fetch();
    const n = await response.json();
    n.status = { ...(n.status || {}), node_id: 1, checked_at: new Date().toISOString(), reachable: false, error: "dial tcp: i/o timeout" };
    await route.fulfill({ response, json: n });
  });
  await page.goto(env.origin + "/#nodes/1");
  await page.click(".conn-card [data-act=conn-new]");
  const dr = page.locator("#drawer.open");
  await expect(dr.locator("#a-save")).toHaveText("创建并应用到节点");
  await dr.locator("#a-port").fill("18443");
  const sync = page.waitForRequest(r => r.method() === "POST" && /\/api\/nodes\/1\/sync$/.test(r.url()));
  await dr.locator("#a-save").click();
  await sync;
  await expect(page.locator(".toast")).toContainText("但应用到");
  await expect(page.locator(".conn-card .ch .tag.pend")).toHaveText("有改动未应用");
  await expect(page.locator(".conn-card [data-act=node-sync]")).toHaveCount(0);
  // 节点连不上:头部主按钮是「刷新状态」,「应用到节点」灰掉带提示。
  await expect(page.locator(".ph .btn.pri")).toHaveText("刷新状态");
  const dis = page.locator(".ph [data-act=node-sync]");
  await expect(dis).toHaveAttribute("aria-disabled", "true");
  // 只是 aria-disabled,原生 disabled 没有,所以仍可聚焦、仍能读提示。
  expect(await dis.evaluate(el => el.disabled)).toBe(false);
  await expect(dis).toHaveAttribute("data-tip", "节点连不上，连上后再应用");
  await expect(page.locator(".ph .vh")).toHaveText("节点连不上，连上后再应用");
  await expect(page.locator(".conn-card .ch .tag.pend")).toHaveText("有改动未应用");
});

test("旧入站地址与子路径跳到节点列表", async ({ page }) => {
  await login(page, env);
  for (const hash of ["inbounds", "inbounds/2"]) {
    await page.goto(env.origin + "/#" + hash);
    await expect(page).toHaveURL(/#nodes$/);
    await expect(page.locator(".ph h1")).toHaveText("节点");
    await expect(page.locator('a.nav[href="#nodes"]')).toHaveClass(/on/);
  }
});

// 覆盖 pending、哈希漂移、从未应用三种主按钮条件,不需要真的 SSH 下发。
for (const state of ["最新", "待应用", "漂移", "从未应用"]) {
  test(`节点详情头部操作:${state}`, async ({ page }) => {
    await login(page, env);
    await page.route("**/api/nodes/2", async route => {
      const response = await route.fetch();
      const n = await response.json();
      n.deployed_hash = state === "从未应用" ? "" : "deployed";
      n.status = { ...n.status, observed_hash: state === "漂移" ? "changed" : "deployed", reachable: true, error: "" };
      await route.fulfill({ response, json: n });
    });
    await page.route("**/api/nodes/pending", route => route.fulfill({
      json: state === "待应用" ? [{ node_id: 2, since: new Date().toISOString() }] : [],
    }));
    await openNode(page, 2);
    await expect(page.locator("html[data-loading]")).toHaveCount(0);
    const primary = page.locator(".ph .btn.pri");
    if (state === "最新") {
      await expect(primary).toHaveCount(0);
      await page.click("[data-act=node-menu]");
      const menu = page.locator("#node-menu");
      await expect(menu).toBeVisible();
      await expect(menu.locator("[data-act=node-check]")).toHaveText("刷新状态");
      await expect(menu.locator("[data-act=node-drain]")).toHaveText("暂停分配");
      await expect(menu.locator("[data-act=node-edit]")).toHaveText("编辑");
      // 没有待应用时也能手动应用(改出口 IP 之类控制面不记待应用的改动)。
      await expect(menu.locator("[data-act=node-sync]")).toHaveText("应用到节点");
      await page.locator(".ph h1").click();
      await expect(menu).toBeHidden();
      await page.click("[data-act=node-menu]");
      await menu.locator("[data-act=node-edit]").click();
      await expect(page.locator("#drawer.open #node-form")).toBeVisible();
      await expect(menu).toBeHidden();
    } else {
      await expect(page.locator("#node-menu [data-act=node-sync]")).toHaveCount(0);
      await expect(primary).toHaveText("应用到节点");
      await expect(primary).toHaveAttribute("data-act", "node-sync");
    }
    const card = page.locator(".conn-card");
    await expect(card.locator("[data-act=node-sync]")).toHaveCount(0);
    await expect(card.locator(".tag.pend")).toHaveCount(state === "待应用" ? 1 : 0);
    if (state === "待应用") await expect(card.locator(".tag.pend")).toHaveText("有改动未应用");
  });
}

test("节点详情:只有 sing-box 的节点没有 xray 下发记录,不因此一直挂着主按钮", async ({ page }) => {
  await login(page, env);
  await page.route("**/api/nodes/2", async route => {
    const response = await route.fetch();
    const n = await response.json();
    n.deployed_hash = "";
    n.status = { ...n.status, observed_hash: "", reachable: true, error: "", cores: [{ core: "sing-box", active: true, version: "1.12.0", observed_hash: "sb" }] };
    await route.fulfill({ response, json: n });
  });
  await page.route("**/api/nodes/pending", route => route.fulfill({ json: [] }));
  await openNode(page, 2);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator(".ph .btn.pri")).toHaveCount(0);
  await page.click("[data-act=node-menu]");
  await expect(page.locator("#node-menu [data-act=node-sync]")).toHaveText("应用到节点");
});

test("节点详情文案:证书卡、在线用户、安装与体检按钮", async ({ page }) => {
  await login(page, env);
  await openNode(page, 2);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  const cert = page.locator(".card.stat").filter({ hasText: "证书" });
  await expect(cert.locator(".s")).toHaveText("没有用到 TLS 的连接方式");
  const online = page.locator(".row").filter({ hasText: "在线用户" });
  await expect(online.locator(".sub")).toHaveText("实时查看每个用户连着几个 IP");
  const install = page.locator(".row").filter({ hasText: "安装核心" });
  await expect(install.locator(".sub")).toHaveText("把核心装到这台节点上并启动，会用当前配置");
  await expect(install.locator("[data-act=node-install]")).toHaveText("安装");
  await expect(page.locator("[data-act=node-diagnose]")).toHaveText("开始体检");
});

test("危险操作是灰卡,只有确认框里是红按钮", async ({ page }) => {
  await login(page, env);
  await openNode(page, 2);
  const zone = page.locator(".node-danger");
  await expect(zone).toHaveCount(1);
  await expect(zone).not.toHaveClass(/danger-zone/);
  await expect(zone.locator(".ch")).not.toHaveClass(/c-bad/);
  const rm = zone.locator("[data-act=node-rm]");
  await expect(rm).toHaveText("删除节点");
  await expect(rm).not.toHaveClass(/c-bad/);
  await rm.click();
  await expect(page.locator("#modal.open #okb")).toHaveClass(/danger/);
});

test("节点连不上:主按钮是刷新状态,应用到节点变灰带提示", async ({ page }) => {
  await login(page, env);
  let pending = [{ node_id: 2, since: new Date().toISOString() }];
  await page.route("**/api/nodes/2", async route => {
    const response = await route.fetch();
    const n = await response.json();
    n.deployed_hash = "deployed";
    n.status = { ...n.status, observed_hash: "deployed", reachable: false, error: "dial tcp: i/o timeout" };
    await route.fulfill({ response, json: n });
  });
  await page.route("**/api/nodes/pending", route => route.fulfill({ json: pending }));
  await openNode(page, 2);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  // pending 非空:头部主按钮是「刷新状态」,灰的「应用到节点」在它左边,提示用 aria-describedby 关联。
  const primary = page.locator(".ph .btn.pri");
  await expect(primary).toHaveText("刷新状态");
  await expect(primary).toHaveAttribute("data-act", "node-check");
  const dis = page.locator(".ph [data-act=node-sync]");
  await expect(dis).toHaveAttribute("aria-disabled", "true");
  // 只是 aria-disabled,原生 disabled 没有,所以仍可聚焦、仍能读提示。
  expect(await dis.evaluate(el => el.disabled)).toBe(false);
  await expect(dis).toHaveAttribute("data-tip", "节点连不上，连上后再应用");
  // 键盘/读屏:按钮可聚焦,aria-describedby 指向视觉隐藏的提示文字。
  await dis.focus();
  expect(await dis.evaluate(el => document.activeElement === el)).toBe(true);
  const whyID = await dis.getAttribute("aria-describedby");
  await expect(page.locator("#" + whyID)).toHaveText("节点连不上，连上后再应用");
  // 悬停/键盘聚焦时,提示真的画出来(data-tip 的 ::after),不是只有一个属性。
  await dis.focus();
  expect(await dis.evaluate(el => getComputedStyle(el, "::after").content)).toContain("节点连不上");
  // 点击/回车都不能触发下发(不弹确认框,也不发请求)。
  // Playwright 把 aria-disabled 也当不可点,这里直接派发 click 事件,测的是页面自己的分发。
  await dis.dispatchEvent("click");
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await page.waitForTimeout(300);
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await expect(page.locator(".conn-card .tag.pend")).toHaveText("有改动未应用");

  // pending 为空:没有未应用改动,禁用项落进「⋯」菜单,菜单里不再有「刷新状态」。
  pending = [];
  await page.reload();
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator(".ph .btn.pri")).toHaveText("刷新状态");
  await page.click("[data-act=node-menu]");
  const menu = page.locator("#node-menu");
  await expect(menu).toBeVisible();
  const mdis = menu.locator("[data-act=node-sync]");
  await expect(mdis).toHaveAttribute("aria-disabled", "true");
  expect(await mdis.evaluate(el => el.disabled)).toBe(false);
  await expect(mdis).toHaveAttribute("data-tip", "节点连不上，连上后再应用");
  const mwhyID = await mdis.getAttribute("aria-describedby");
  await expect(page.locator("#" + mwhyID)).toHaveText("节点连不上，连上后再应用");
  await mdis.dispatchEvent("click");
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await expect(menu.locator("[data-act=node-check]")).toHaveCount(0);
});

// 只有读权限(GET /api)时,连不上的节点仍要能「刷新状态」——那是 GET ?fresh=1,不是写操作。
test("只读权限且节点连不上:仍能刷新状态", async ({ page }) => {
  // 权限来自 /ui/meta,必须在页面第一次加载前就把写权限摘掉。
  await page.route("**/ui/meta", async route => {
    const response = await route.fetch();
    const m = await response.json();
    m.allow = (m.allow || []).filter(a => a.startsWith("GET "));
    await route.fulfill({ response, json: m });
  });
  await page.route("**/api/nodes/2", async route => {
    const response = await route.fetch();
    const n = await response.json();
    n.deployed_hash = "deployed";
    n.status = { ...n.status, observed_hash: "deployed", reachable: false, error: "dial tcp: i/o timeout" };
    await route.fulfill({ response, json: n });
  });
  await page.route("**/api/nodes/pending", route => route.fulfill({ json: [] }));
  await login(page, env);
  await openNode(page, 2);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  const primary = page.locator(".ph .btn.pri");
  await expect(primary).toHaveText("刷新状态");
  await expect(primary).toHaveAttribute("data-act", "node-check");
  await expect(primary).toBeEnabled();
  // 菜单里不再重复放刷新项(主按钮已经换成「刷新状态」了)。
  await page.click("[data-act=node-menu]");
  await expect(page.locator("#node-menu [data-act=node-check]")).toHaveCount(0);
  // 写操作按钮因为没写权限都不出现。
  await expect(page.locator(".ph [data-act=node-sync]")).toHaveCount(0);
  await primary.click();
  await expect(page.locator(".toast")).toContainText("状态已刷新", { timeout: 30_000 });
});

