// 「订阅」页 step5b：发给用户的链接、编辑框里的「用户名」标签、编号与预览对应、客户端对照表和识别规则抽屉。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env;
const cp = (p, init = {}) => cpFetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
const settings = async () => (await cp("/api/settings")).json();
const putSettings = async patch => {
  const r = await cp("/api/settings", { method: "PUT", body: JSON.stringify({ ...await settings(), ...patch }) });
  expect(r.ok).toBe(true);
};
async function open(page) {
  await login(page, env);
  await page.goto(env.origin + "/#sub");
  await expect(page.locator("#f-title")).toBeVisible();
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
}
const save = page => page.locator("[data-act=s5-save]").click();
// 订阅名是 contenteditable 的编辑框,{USERNAME} 在里面是一块「用户名」标签;直接改写里面的文字。
const setTitle = (page, text) => page.locator("#f-title").evaluate((el, t) => { el.textContent = t; el.dispatchEvent(new Event("input")); }, text);
const linkOf = (page, expected) => expect(page.locator(".lk-url")).toHaveText(expected);

test.beforeAll(async () => {
  env = await startEnv();
  await env.seed("user", "add", "alice", "--limit", "100GB", "--expire", "2027-01-01");
  await env.seed("user", "add", "bob", "--limit", "200GB", "--expire", "2028-01-01");
});
test.afterAll(async () => { await env?.stop(); });
test.beforeEach(async () => { await putSettings({ sub_url_prefix: "", profile_title: "{USERNAME}", update_interval: 24, support_link: "", rules: null }); });

// 完整链接按最后一个 /sub/ 切成「开头」和「每人自己的部分」;没填链接开头时开头就是控制面的地址
// (控制面按这次请求的 Host 拼;请求经 mpx ui 转发过来,Host 是控制面的地址,不是页面地址),并有一条灰色说明。
test("链接卡片给出完整链接;没填链接开头时用控制面地址开头并说明", async ({ page }) => {
  await open(page);
  // 页面请求的那条(经 mpx ui)核对链接卡片;开头本身拿控制面的地址核对。
  const alice = await (await page.request.get(env.origin + "/api/users/alice/links")).json();
  const cpAlice = await (await cp("/api/users/alice/links")).json();
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", alice.auto);
  await linkOf(page, alice.auto);
  expect(alice.auto.startsWith(cpAlice.auto.slice(0, cpAlice.auto.lastIndexOf("/sub/")) + "/sub/")).toBe(true);
  expect(alice.auto.endsWith("/sub/" + cpAlice.auto.split("/sub/").pop())).toBe(true);
  // mpx ui 转发时自己改写了 Host(mpx ui 的地址),控制面按它拼;只加的 base 字段说的就是这个开头。
  expect(alice.base).toBe(env.origin);
  // 页面把开头按控制面这次用的那个画出来,经 mpx ui 时它不一定等于页面地址。
  const pre = alice.auto.slice(0, alice.auto.lastIndexOf("/sub/"));
  await expect(page.locator(".lk-pre")).toHaveText(pre);
  await expect(page.locator(".lk-tail")).toHaveText(alice.auto.slice(pre.length));
  const note = page.locator("#pfx-warn");
  await expect(note).toHaveClass(/b5-note/);
  await expect(note).toContainText("没填，链接现在用服务器地址开头。");
  await expect(note).toContainText("拿到链接的人都能看到这台服务器的 IP");

  await page.fill("#f-prefix", "https://sub.example.com");
  await expect(note).toHaveCount(0);
  const expected = "https://sub.example.com/sub/" + alice.auto.split("/sub/")[1];
  await linkOf(page, expected);
  await expect(page.locator(".lk-pre")).toHaveText("https://sub.example.com");
  await expect(page.locator(".lk-box [data-act=copy]")).toHaveAttribute("data-text", expected);
  await expect(page.locator(".lk-key")).toContainText("链接开头 · 在下面填");
});

// 编辑框里「用户名」是整块标签;存进去的仍然只有 {USERNAME},每个人看到自己的名字。
test("订阅名编辑框把 {USERNAME} 显示成「用户名」标签，保存后仍存 {USERNAME}", async ({ page }) => {
  await open(page);
  const title = page.locator("#f-title");
  await expect(title.locator(".tk")).toHaveText("用户名");
  await expect(title).toHaveText(/^用户名$/);
  await expect(page.locator("#title-res")).toContainText("alice 看到「alice」");
  await expect(page.locator("#title-res")).toContainText("bob 看到「bob」");
  await expect(page.locator(".pv-t")).toHaveText("alice");

  // 在标签后面接着打字:光标放到末尾再打。
  await title.click();
  await page.keyboard.press("Control+End");
  await page.keyboard.type(" 的订阅");
  await expect(page.locator(".pv-t")).toHaveText("alice 的订阅");
  await expect(page.locator("#sb")).toContainText("有 1 处改动");
  await save(page);
  await expect(page.locator(".toast")).toContainText("已保存 1 处改动");
  expect((await settings()).profile_title).toBe("{USERNAME} 的订阅");
  await expect(page.locator("#f-title .tk")).toHaveText("用户名");
});

// 右边三栏带编号,鼠标悬停或焦点落在第 2 栏时,卡片上「刷新间隔」那一块一起高亮。
test("悬停或聚焦第 2 栏时预览卡片对应那块高亮", async ({ page }) => {
  await open(page);
  const app = page.locator("#pv-app");
  const f2 = page.locator('.b5-f[data-p="2"]');
  // 鼠标停上去后,滚动跟随会让元素在指针下错开再触发别处的 mouseenter:可能停在 2 上,也可能清空。
  await f2.hover();
  await expect(app).toHaveAttribute("data-hl", /^(2)?$/);
  await page.mouse.move(1, 1);
  await expect(app).toHaveAttribute("data-hl", "");
  await f2.locator("#f-int").focus();
  await expect(app).toHaveAttribute("data-hl", "2");
});

// 不同客户端拿到什么:按名字列,最后一行永远是「其他客户端」;识别规则收进抽屉,页面上不列正则。
test("客户端对照表里有 Clash Verge,最后一行是其他客户端", async ({ page }) => {
  await open(page);
  const rows = page.locator("#b5-tbl .b5-tr");
  await expect(rows.filter({ hasText: "Clash Verge" })).toHaveCount(1);
  await expect(rows.last()).toHaveClass(/b5-other/);
  expect((await rows.last().locator(".b5-apps").textContent()).startsWith("其他客户端")).toBe(true);
  const heading = page.locator("#b5-tbl .b5-th");
  await expect(heading).toHaveText(/客户端\s*拿到/);
});

// 识别规则抽屉:输入客户端标识就说会拿到什么;不看正则。「不分大小写」不在这一步里。
test("识别规则抽屉的试框说「会拿到」，没有「不分大小写」勾选框", async ({ page }) => {
  await open(page);
  await page.click("[data-act=s5-rules]");
  await expect(page.locator("#drawer.open .dt h2")).toHaveText("识别规则");
  await page.fill("#rd-ua", "clash-verge/v2.0.3");
  await expect(page.locator("#rd-hit")).toContainText("会拿到");
  await expect(page.locator("#rd-hit")).toContainText("Clash Meta 配置");
  await expect(page.locator('.rd-row[data-i="0"]')).toHaveClass(/hit/);
  await expect(page.getByRole("checkbox")).toHaveCount(0);
  await expect(page.locator("#drawer.open")).not.toContainText("不分大小写");
});

// 链接开头的格式不对:红字落在这一栏下面,用「链接开头」的说法,不冒出「订阅地址」。
test("链接开头填错时字段下出红字，说的是「链接开头」而不是「订阅地址」", async ({ page }) => {
  await open(page);
  await page.fill("#f-prefix", "ftp://x");
  await save(page);
  const err = page.locator("#pfx-err");
  await expect(err).toContainText("链接开头");
  await expect(err).not.toContainText("订阅地址");
  // 红字要比别的字显眼:用的是 --bad(浅色 #dc2626,深色 #f2555a),输入框里的正常文字不是这个颜色。
  const [errColor, inputColor, bad] = await err.evaluate(e => [getComputedStyle(e).color, getComputedStyle(document.querySelector("#f-prefix")).color, getComputedStyle(document.documentElement).getPropertyValue("--bad").trim()]);
  const rgb = c => "rgb(" + [1, 3, 5].map(i => parseInt(c.slice(i, i + 2), 16)).join(", ") + ")";
  expect(errColor).toBe(rgb(bad));
  expect(inputColor).not.toBe(errColor);
  await expect(page.locator("#sb [role=alert]")).toHaveText("没保存上：链接开头的格式不对，改好再保存。");
  await expect(page.locator("#f-prefix")).toBeFocused();
  await expect(page.locator("#f-prefix")).toHaveValue("ftp://x");
});
