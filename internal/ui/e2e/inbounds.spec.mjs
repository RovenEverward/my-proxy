// 「入站与入口」重设计:按节点分组、默认值与自定义的显示、多入口展开、名称与节点不符提醒、
// 添加连接抽屉的端口顺延。数据只造 203.0.113.x / 198.51.100.x 与 example.com。
import { test, expect } from "@playwright/test";
import { startEnv, login } from "./env.mjs";

let env;
test.beforeAll(async () => {
  env = await startEnv();
  // 东京 A:一个 Reality(默认名)+ 一个 WS,WS 有两个入口。
  await env.seed("node", "add", "tokyo-a", "--address", "203.0.113.21", "--ssh-port", "1");
  await env.seed("node", "edit", "tokyo-a", "--region", "JP", "--city", "东京", "--letter", "A");
  await env.seed("node", "add", "la-a", "--address", "198.51.100.2", "--ssh-port", "1");
  await env.seed("node", "edit", "la-a", "--region", "US", "--city", "洛杉矶", "--letter", "A");
  await env.seed("node", "add", "empty", "--address", "198.51.100.7", "--ssh-port", "1");
  await env.seed("node", "edit", "empty", "--region", "US", "--city", "洛杉矶", "--letter", "B");

  await env.seed("inbound", "add", "--node", "1", "--tag", "jp-reality", "--protocol", "vless", "--port", "8443", "--security", "reality", "--sni", "www.example.com", "--reality-dest", "www.example.com:443", "--flow", "xtls-rprx-vision");
  await env.seed("inbound", "add", "--node", "1", "--tag", "jp-ws", "--protocol", "vless", "--port", "2087", "--network", "ws", "--ws-path", "/ws", "--security", "none");
  await env.seed("inbound", "add", "--node", "2", "--tag", "us-reality", "--protocol", "vless", "--port", "8443", "--security", "reality", "--sni", "www.example.com", "--reality-dest", "www.example.com:443");
  // 东京 WS 的第一个入口写错了地方(美国),节点在东京 → 该提醒;
  // 第二个入口完全跟随节点(没起名、没地址),用来验证「N 个入口」展开与默认值。
  // 它必须真的全默认,否则 customAddr() 会按「用了自定义地址」显示。
  await env.seed("host", "add", "--inbound", "2", "--remark", "🇺🇸 美国 CDN", "--address", "cdn.example.com", "--port", "443");
  await env.seed("host", "add", "--inbound", "2", "--remark", "", "--address", "");
});
test.afterAll(async () => { await env?.stop(); });

const goto = async page => {
  await login(page, env);
  await page.goto(env.origin + "/#inbounds");
  await expect(page.locator(".ng").first()).toBeVisible();
};
const group = (page, name) => page.locator(".ng", { has: page.locator(".ng-n", { hasText: name }) });

test("按节点分组:组头、连接数、空状态与页头", async ({ page }) => {
  await goto(page);
  await expect(page.locator(".ph h1")).toHaveText("连接方式");
  await expect(page.locator(".ph p")).toHaveText("每条连接会出现在用户的订阅里");
  await expect(page.locator(".ph [data-act=conn-new]")).toHaveCount(1);
  await expect(page.locator(".ng")).toHaveCount(3);
  const jp = group(page, "东京 A");
  await expect(jp.locator(".ng-n img.flag")).toHaveAttribute("src", "/flags/jp.svg");
  await expect(jp.locator(".ng-a")).toHaveText("203.0.113.21");
  await expect(jp.locator(".ng-c")).toHaveText("2 个连接");
  await expect(jp.locator(".cx-row")).toHaveCount(2);
  const empty = group(page, "洛杉矶 B");
  await expect(empty.locator(".cx-row")).toHaveCount(0);
  await expect(empty.locator(".ng-empty")).toContainText("还没有连接");
  await expect(empty.locator("[data-act=conn-new]")).toContainText("添加连接");
});

test("默认值灰色、自定义带铅笔;只有多入口才可展开", async ({ page }) => {
  await goto(page);
  // 东京 Reality:没有入口 → 名称跟随节点、地址用节点地址。
  const rows = group(page, "东京 A").locator(".cx-row");
  const plain = rows.filter({ has: page.locator('.ptag[title="VLESS Reality"]') });
  await expect(plain.locator(".cx-name .cx-v")).not.toHaveClass(/\bon\b/);
  await expect(plain.locator(".cx-addr .cx-v")).not.toHaveClass(/\bon\b/);
  await expect(plain.locator(".cx-name .cx-pen")).toHaveCount(0);
  await expect(plain.locator(".cx-more")).toHaveCount(0);
  // WS:第一个入口自定义了名字和地址,且有两个入口。
  const ws = rows.filter({ has: page.locator('.ptag[title="VLESS WS"]') });
  // 「N 个入口」展开的是行下面的一段(.cx-subs 与 .cx-row 平级,不在行里面)。
  const wsCard = group(page, "东京 A").locator(".cx", { hasText: "2 个入口" });
  await expect(ws.locator(".cx-name .cx-v")).toHaveClass(/on/);
  await expect(ws.locator(".cx-name .cx-pen")).toHaveCount(1);
  await expect(ws.locator(".cx-addr .cx-v")).toHaveClass(/on/);
  await expect(ws.locator(".cx-more")).toContainText("2 个入口");
  await expect(wsCard.locator(".cx-subs")).toHaveCount(0);
  await ws.locator(".cx-more").click();
  await expect(wsCard.locator(".cx-subs")).toBeVisible();
  await expect(wsCard.locator(".cx-sub")).toHaveCount(2);
  await expect(wsCard.locator(".cx-sub").first()).toContainText("自定义地址");
  await expect(wsCard.locator(".cx-sub").nth(1)).toContainText("节点地址");
  // 展开状态在内存里:重绘后仍展开。
  await page.locator(".ph h1").click();
  await expect(wsCard.locator(".cx-subs")).toBeVisible();
});

test("名称与节点不符:提醒 + 改回跟随节点名发出的 PUT 体 remark 为空", async ({ page }) => {
  await goto(page);
  const jp = group(page, "东京 A");
  const warn = jp.locator(".cx-warn");
  await expect(warn).toHaveCount(1);
  await expect(warn).toContainText("名称写的是美国，节点在东京");
  await warn.locator("[data-act=conn-fix]").click();
  await expect(page.locator(".toast")).toContainText("已改回跟随节点名");
  await expect(group(page, "东京 A").locator(".cx-warn")).toHaveCount(0, { timeout: 15000 });
  // 找那次 PUT /api/hosts/1 的请求体:remark 恰好是空串。
  const put = env.takeLog().find(r => r.method === "PUT" && /^\/api\/hosts\/\d+$/.test(r.path));
  expect(put, "要发出 PUT /api/hosts/{id}").toBeTruthy();
  const body = JSON.parse(put.body);
  expect(body.remark).toBe("");
  expect(body.id).toBe(1);
  // 别的字段照旧整体回写(不是只发 remark)。
  expect(body).toHaveProperty("inbound_id");
  expect(body).toHaveProperty("address");
});

test("名称与节点一致时不提醒", async ({ page }) => {
  await goto(page);
  // 洛杉矶的 Reality 名字是默认的,不提醒。
  await expect(group(page, "洛杉矶 A").locator(".cx-warn")).toHaveCount(0);
});

test("组件可单独渲染:CONN.load([1]) + CONN.groups(d,{node:1}) 只有节点 1 的连接行", async ({ page }) => {
  await goto(page);
  // 不经过入站页:直接调组件拉数据、渲染一个节点的连接列表(节点详情页以后就是这么用)。
  const info = await page.evaluate(async () => {
    const d = await CONN.load([1]);
    const box = document.createElement("div");
    box.id = "conn-only";
    box.innerHTML = CONN.groups(d, { node: 1 }).toString();
    document.body.append(box);
    return {
      nodes: d.nodes.map(n => n.id),
      inbounds: d.inbounds.map(i => [i.id, i.node_id]),
      rows: [...box.querySelectorAll(".cx-row")].map(r => +r.dataset.id),
      hasGroupHead: !!box.querySelector(".ng-h"),
    };
  });
  expect(info.nodes).toEqual([1]);
  expect(info.inbounds.every(([, nid]) => nid === 1)).toBe(true);
  // 东京 A 上两条连接(1 和 2),洛杉矶的那条不在。
  expect(info.rows).toEqual([1, 2]);
  expect(info.hasGroupHead).toBe(false);
  const box = page.locator("#conn-only");
  await expect(box.locator(".cx-row")).toHaveCount(2);
  await expect(box).not.toContainText("🇺🇸 洛杉矶 A");
  await expect(box.locator(".cx-row").first()).toHaveAttribute("data-act", "conn-edit");
});

test("添加连接:Reality 端口 8443 已占用时顺延到 8444,预览跟着变", async ({ page }) => {
  await goto(page);
  await group(page, "东京 A").locator("[data-act=conn-new]").click();
  const dr = page.locator("#drawer.open");
  await expect(dr.locator("h2")).toHaveText("添加连接");
  await expect(dr.locator("#a-node")).toHaveValue("1");
  await expect(dr.locator("#a-port")).toHaveValue("8444");
  await expect(dr.locator("#a-port-h")).toContainText("这台节点上 8443 已经在用，自动换成 8444");
  // 端口提示前面是 info 线性图标,不再带国旗。
  await expect(dr.locator("#a-port-h svg")).toHaveCount(1);
  await expect(dr.locator("#a-port-h img.flag")).toHaveCount(0);
  // 底部:主按钮「创建并应用到节点」+ 次要「只创建，稍后应用」+ 一行灰字。
  await expect(dr.locator("#a-save")).toHaveText("创建并应用到节点");
  await expect(dr.locator("#x-later")).toHaveText("只创建，稍后应用");
  await expect(dr.locator("#x-note")).toHaveText("会重启东京 A上的代理，已连着的用户会断开几秒");
  await expect(dr).not.toContainText("创建后要到节点页");
  await expect(dr.locator(".pv-r")).toContainText("东京 A · Reality");
  await expect(dr.locator("#pv-a")).toHaveText("203.0.113.21:8444");
  // 切到 WS:走 2087,已被占 → 2088。
  await dr.locator("[data-act=conn-add-proto][data-k=ws]").click();
  await expect(page.locator("#drawer.open #a-port")).toHaveValue("2088");
  await expect(page.locator("#drawer.open .pv-r")).toContainText("· WS");
  // 手改端口后不再自动覆盖。
  await page.fill("#drawer.open #a-port", "2090");
  await expect(page.locator("#drawer.open #pv-a")).toHaveText("203.0.113.21:2090");
  // 选「稍后应用」:只建不下发,组头亮「有改动未应用」和「应用」。
  await page.click("#add-form #x-later");
  await expect(page.locator(".toast")).toContainText("显示「有改动未应用」");
  await expect(group(page, "东京 A").locator(".cx-row")).toHaveCount(3, { timeout: 15000 });
  await expect(group(page, "东京 A").locator(".ng-h .tag.pend")).toHaveText("有改动未应用");
  await expect(group(page, "东京 A").locator(".ng-h [data-act=node-sync]")).toHaveText("应用");
});

test("编辑连接:名称与地址在顶上,入站字段在「高级」里", async ({ page }) => {
  await goto(page);
  await group(page, "洛杉矶 A").locator(".cx-row").first().click();
  const dr = page.locator("#drawer.open");
  await expect(dr.locator("h2")).toContainText("洛杉矶 A");
  await expect(dr.locator("#d-name")).toBeVisible();
  await expect(dr.locator("#d-addr")).toBeVisible();
  // 入站字段收在折叠的「高级」里,展开前不可见。
  await expect(dr.locator("#i-port")).not.toBeVisible();
  await dr.locator("details.adv2 > summary").click();
  await expect(dr.locator("#i-port")).toHaveValue("8443");
  await expect(dr.locator("#i-protocol")).toHaveValue("vless");
  await expect(dr.locator("#i-security")).toHaveValue("reality");
  // 没改协议、端口这些:只有一个「保存」,不带应用选项。
  await expect(dr.locator("#i-save")).toHaveText("保存");
  await expect(dr.locator("#x-later")).toBeHidden();
  // 下拉显示成 VLESS / REALITY 这样的名字,点开是我们自己的灰底列表。
  await expect(dr.locator("#i-protocol option:checked")).toHaveText("VLESS");
  await expect(dr.locator("#i-security option:checked")).toHaveText("REALITY");
  await expect(dr.locator("label[for=i-short_ids]")).toHaveText("Short ID");
  await dr.locator("#i-network").click();
  const menu = page.locator(".selmenu");
  await expect(menu).toBeVisible();
  await expect(menu.locator(".selopt.on")).toHaveText("TCP");
  await menu.locator(".selopt", { hasText: "WebSocket" }).click();
  await expect(menu).toHaveCount(0);
  await expect(dr.locator("#i-network")).toHaveValue("ws");
  // 改了传输 → 要应用:主按钮变成「保存并应用到节点」,出现「只保存，稍后应用」。
  await expect(dr.locator("#i-save")).toHaveText("保存并应用到节点");
  await expect(dr.locator("#x-later")).toHaveText("只保存，稍后应用");
  await expect(dr.locator("#x-note")).toContainText("上的代理，已连着的用户会断开几秒");
});
