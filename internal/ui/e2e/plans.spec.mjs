// 套餐页与用户页的套餐相关操作:每条写操作都和对应的 mpx plan / mpx user 命令
// 对照,发到控制面的请求要一致(做法同 rules.spec.mjs)。
import { test, expect } from "@playwright/test";
import { startEnv, normalize, login, cpFetch } from "./env.mjs";

let env;
const cp = (p, init = {}) => cpFetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
const getJSON = async p => (await cp(p)).json();
const planByID = async id => (await getJSON("/api/plans")).find(p => p.id === id);
const planByName = async name => (await getJSON("/api/plans")).find(p => p.name === name);
const inboundByTag = async tag => (await getJSON("/api/inbounds")).find(i => i.tag === tag);

test.beforeAll(async () => {
  env = await startEnv();
  const nodes = [["tokyo-a", "JP", "东京", "A"], ["la-a", "US", "洛杉矶", "A"], ["la-b", "US", "洛杉矶", "B"]];
  for (const [i, [n, region, city, letter]] of nodes.entries()) {
    await env.seed("node", "add", n, "--address", `203.0.113.${10 + i}`, "--ssh-port", "1");
    await env.seed("node", "edit", n, "--region", region, "--city", city, "--letter", letter);
  }
  // 东京 1 个入站;洛杉矶 A 2 个;洛杉矶 B 1 个。tag 唯一,方便 CLI 按名字解析。
  const ins = [
    ["1", "jp-reality", "443"],
    ["2", "la-reality", "443"],
    ["2", "la-ws", "8443"],
    ["3", "lb-reality", "443"],
  ];
  for (const [node, tag, port] of ins) {
    await env.seed("inbound", "add", "--node", node, "--tag", tag, "--protocol", "vless", "--port", port, "--security", "reality", "--sni", "www.cloudflare.com", "--reality-dest", "www.cloudflare.com:443");
  }
  // 默认套餐里的用户:一个正常、一个停用(活跃数按 User.Status 判定)。
  await env.seed("user", "add", "alice");
  await env.seed("user", "add", "bob", "--disabled");
});
test.afterAll(async () => { await env?.stop(); });

const gotoPlans = async page => {
  await page.goto(env.origin + "/#plans");
  await expect(page.locator(".pl-row").first()).toBeVisible();
};
const openPlan = async (page, name) => {
  await gotoPlans(page);
  await page.locator(".pl-row", { hasText: name }).click();
  await expect(page.locator("#drawer.open #pl-form")).toBeVisible();
};

test("列表:默认套餐在首行、人数格式、只有默认时的提示", async ({ page }) => {
  await login(page, env);
  await page.goto(env.origin + "/#plans");
  const first = page.locator(".pl-row").first();
  await expect(first).toContainText("默认套餐");
  await expect(first.locator(".pl-def")).toHaveText("默认");
  // 「默认」是灰色文字,不是彩色 pill。
  await expect(first.locator(".pill")).toHaveCount(0);
  // 人数按 N(活跃 M) 显示:alice 正常、bob 停用。
  await expect(first.locator(".pl-uc")).toContainText("2（活跃 1）");
  // 只有默认套餐时,列表下方给出提示。
  await expect(page.locator(".callout.stone")).toContainText("都在默认套餐");
});

test("新建套餐(整节点):与 mpx plan add 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-基础", "ui-基础"];
  env.takeLog();
  await env.cli("plan", "add", "cli-基础", "--node", "🇺🇸 洛杉矶 A", "--note", "给美国");
  const fromCLI = normalize(env.takeLog(), names);
  expect(fromCLI).toHaveLength(1);

  await gotoPlans(page);
  await page.click("[data-act=pl-edit][data-id='']");
  await page.fill("#pl-name", "ui-基础");
  await page.fill("#pl-note", "给美国");
  await page.locator(".pl-n label", { hasText: "洛杉矶 A" }).click();
  await expect(page.locator("#pl-cnt")).toContainText("已选 1");
  await page.click("#pl-save");
  await expect(page.locator(".toast")).toContainText("已保存");
  expect(normalize(env.takeLog(), names)).toEqual(fromCLI);
  await expect(page.locator(".pl-row", { hasText: "ui-基础" })).toContainText("洛杉矶 A");
});

test("新建套餐(单个入站):与 mpx plan add --inbound 一致", async ({ page }) => {
  await login(page, env);
  const names = ["cli-单入站", "ui-单入站"];
  env.takeLog();
  await env.cli("plan", "add", "cli-单入站", "--inbound", "la-reality");
  const fromCLI = normalize(env.takeLog(), names);

  await gotoPlans(page);
  await page.click("[data-act=pl-edit][data-id='']");
  await page.fill("#pl-name", "ui-单入站");
  await page.locator(".pl-n", { hasText: "洛杉矶 A" }).locator("[data-act=pl-exp]").click();
  await page.locator(".pl-ibs label.pl-i", { hasText: "la-reality" }).click();
  await page.click("#pl-save");
  await expect(page.locator(".toast")).toContainText("已保存");
  expect(normalize(env.takeLog(), names)).toEqual(fromCLI);
  await expect(page.locator(".pl-row", { hasText: "ui-单入站" })).toContainText("仅 la-reality");
});

test("编辑套餐:与 mpx plan edit 一致", async ({ page }) => {
  await login(page, env);
  await env.seed("plan", "add", "cli-改前", "--node", "🇺🇸 洛杉矶 A");
  await env.seed("plan", "add", "ui-改前", "--node", "🇺🇸 洛杉矶 A");
  const cliPlan = await planByName("cli-改前");
  const uiPlan = await planByName("ui-改前");
  const names = ["cli-改后", "ui-改后"];

  env.takeLog();
  await env.cli("plan", "edit", "cli-改前", "--add-node", "🇯🇵 东京 A", "--name", "cli-改后", "--note", "换过");
  const fromCLI = normalize(env.takeLog(), names, { ids: [String(cliPlan.id), String(uiPlan.id)] });
  expect(fromCLI).toHaveLength(1);

  await openPlan(page, "ui-改前");
  await page.fill("#pl-name", "ui-改后");
  await page.fill("#pl-note", "换过");
  await page.locator(".pl-n label", { hasText: "东京 A" }).click();
  // 抽屉里不再展示等价命令。
  await expect(page.locator("#pl-form")).not.toContainText("mpx ");
  await page.click("#pl-save");
  await expect(page.locator(".toast")).toContainText("已保存");
  expect(normalize(env.takeLog(), names, { ids: [String(cliPlan.id), String(uiPlan.id)] })).toEqual(fromCLI);
  await expect(page.locator(".pl-row", { hasText: "ui-改后" })).toContainText("东京 A");
});

test("删除有用户的套餐:下拉默认选中默认套餐、没有移出选项、请求带 reassign", async ({ page }) => {
  await login(page, env);
  await env.seed("plan", "add", "cli-要删", "--node", "🇺🇸 洛杉矶 B");
  await env.seed("plan", "add", "ui-要删", "--node", "🇺🇸 洛杉矶 B");
  await env.seed("user", "add", "cli-del-user", "--plan", "cli-要删");
  await env.seed("user", "add", "ui-del-user", "--plan", "ui-要删");
  const cliPlan = await planByName("cli-要删");
  const uiPlan = await planByName("ui-要删");
  const def = (await getJSON("/api/plans")).find(p => p.is_default);

  env.takeLog();
  await env.cli("plan", "rm", "cli-要删", "--reassign", "默认套餐");
  const fromCLI = normalize(env.takeLog(), ["cli-要删", "ui-要删"], { ids: [String(cliPlan.id), String(uiPlan.id), String(def.id)] });
  expect(fromCLI).toHaveLength(1);

  await openPlan(page, "ui-要删");
  await page.click("[data-act=pl-del]");
  await expect(page.locator("#modal.open")).toContainText("个用户挂着这个套餐");
  const sel = page.locator("#pl-to");
  await expect(sel).toBeVisible();
  await expect(sel.locator("option:checked")).toHaveText("「默认套餐」");
  // 没有「移出套餐」这种去向。
  const opts = await sel.locator("option").allTextContents();
  expect(opts.join(" ")).not.toContain("移出");
  expect(opts.join(" ")).not.toContain("不挂");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已删除");
  expect(normalize(env.takeLog(), ["cli-要删", "ui-要删"], { ids: [String(cliPlan.id), String(uiPlan.id), String(def.id)] })).toEqual(fromCLI);
  // 用户被改挂到默认套餐。
  const moved = await getJSON("/api/users/ui-del-user");
  expect(moved.plan_id).toBe(def.id);
});

test("删除没有用户的套餐:直接确认,与 mpx plan rm 一致", async ({ page }) => {
  await login(page, env);
  await env.seed("plan", "add", "cli-空删");
  await env.seed("plan", "add", "ui-空删");
  const cliPlan = await planByName("cli-空删");
  const uiPlan = await planByName("ui-空删");

  env.takeLog();
  await env.cli("plan", "rm", "cli-空删");
  const fromCLI = normalize(env.takeLog(), ["cli-空删", "ui-空删"], { ids: [String(cliPlan.id), String(uiPlan.id)] });

  await openPlan(page, "ui-空删");
  await page.click("[data-act=pl-del]");
  await expect(page.locator("#modal.open")).toContainText("没有用户挂着");
  await expect(page.locator("#pl-to")).toHaveCount(0);
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已删除");
  expect(normalize(env.takeLog(), ["cli-空删", "ui-空删"], { ids: [String(cliPlan.id), String(uiPlan.id)] })).toEqual(fromCLI);
});

test("用户页:套餐列、按套餐筛选、+N 只算套餐外的额外授权", async ({ page }) => {
  await login(page, env);
  await env.seed("plan", "add", "用户A", "--node", "🇺🇸 洛杉矶 A");
  await env.seed("plan", "add", "用户B", "--node", "🇯🇵 东京 A");
  const planA = await planByName("用户A");
  const jp = await inboundByTag("jp-reality");
  const la = await inboundByTag("la-reality");

  await env.seed("user", "add", "plain", "--plan", "用户A");
  await env.seed("user", "add", "overlap", "--plan", "用户A", "--inbounds", `${la.id},${jp.id}`);
  await env.seed("user", "add", "bplan", "--plan", "用户B");
  await env.seed("user", "add", "allnodes", "--plan", "默认套餐", "--inbounds", String(jp.id));

  await page.goto(env.origin + "/#users");
  const row = name => page.locator("tr", { hasText: name });
  await expect(row("plain").locator(".pl-ptag")).toContainText("用户A");
  await expect(row("plain").locator(".pl-ptag .sub")).toHaveCount(0);
  // la-reality 在套餐内,只算 jp-reality 这一个额外授权。
  await expect(row("overlap").locator(".pl-ptag .sub")).toHaveText("+1");
  // 套餐是 all_nodes 时不显示 +N。
  await expect(row("allnodes").locator(".pl-ptag")).toContainText("默认套餐");
  await expect(row("allnodes").locator(".pl-ptag .sub")).toHaveCount(0);

  // 按套餐筛选:选用户B 只剩 bplan;选用户A 有 plain/overlap。
  await page.selectOption("#uf-plan", String((await planByName("用户B")).id));
  await expect(row("bplan")).toBeVisible();
  await expect(row("plain")).toHaveCount(0);
  await page.selectOption("#uf-plan", String(planA.id));
  await expect(row("plain")).toBeVisible();
  await expect(row("overlap")).toBeVisible();
  await expect(row("bplan")).toHaveCount(0);
});

test("用户改套餐:与 mpx user edit --plan 一致", async ({ page }) => {
  await login(page, env);
  await env.seed("plan", "add", "改A", "--node", "🇺🇸 洛杉矶 A");
  await env.seed("plan", "add", "改B", "--node", "🇯🇵 东京 A");
  const planB = await planByName("改B");
  await env.seed("user", "add", "cli-move-user", "--plan", "改A");
  await env.seed("user", "add", "ui-move-user", "--plan", "改A");
  const names = ["cli-move-user", "ui-move-user"];

  env.takeLog();
  await env.cli("user", "edit", "cli-move-user", "--plan", "改B");
  const fromCLI = normalize(env.takeLog(), names);
  expect(fromCLI).toHaveLength(1);

  await page.goto(env.origin + "/#users/ui-move-user");
  await page.click("[data-act=user-edit]");
  await page.click(`#f-plans .plan:has(input[value="${planB.id}"])`);
  await page.click("#f-save");
  await expect(page.locator(".toast")).toContainText("已更新用户 ui-move-user");
  expect(normalize(env.takeLog(), names)).toEqual(fromCLI);
  await expect(page.locator("#drawer.open")).toContainText("改B");
});

// 一台节点有两条连接方式时,预览标题分开写节点数和连接方式数,不把连接方式数当节点数。
test("套餐预览:节点数和连接方式数分开写", async ({ page }) => {
  await login(page, env);
  await gotoPlans(page);
  await page.click("[data-act=pl-edit][data-id='']");
  await page.locator(".pl-n label", { hasText: "洛杉矶 A" }).click();
  await expect(page.locator("#pl-cnt")).toContainText("已选 1");
  await expect(page.locator(".pl-pv h5")).toContainText("1 个节点、2 个连接方式");
  await expect(page.locator(".pl-pv h5")).not.toContainText("2 个节点");
});
