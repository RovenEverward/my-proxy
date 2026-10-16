// 分流规则页:写操作与 mpx rule / mpx group 对照。CLI 做一次、记下请求、把数据恢复原样,
// 再让页面做同一件事。整表 PUT 里的 version 两边各自取当时的值,对照时去掉。
import { test, expect } from "@playwright/test";
import { startEnv, login, cpFetch } from "./env.mjs";

let env;
const cp = (p, init = {}) => cpFetch(env.cpURL + p, { ...init, headers: { Authorization: "Bearer " + env.token, "Content-Type": "application/json", ...(init.headers || {}) } });
const getJSON = async p => (await cp(p)).json();
const putRules = (rules, user) => cp("/api/rules" + (user ? "?user=" + user : ""), { method: "PUT", body: JSON.stringify({ rules }) });
// 写请求摆成可比的样子:去掉 version,路径里的数字 id 换成占位。
const norm = reqs => reqs.map(r => { let b = r.body ? JSON.parse(r.body) : null; if (b && typeof b === "object") delete b.version; return { method: r.method, path: r.path.replace(/\/\d+(?=$|\?)/, "/<id>"), body: b }; });
const strip = rs => rs.map(({ type, value, target, enabled, note }) => ({ type, value, target, enabled, ...(note ? { note } : {}) }));

async function viaCLI(...args) {
  const before = await getJSON("/api/rules");
  env.takeLog();
  await env.cli(...args);
  const reqs = norm(env.takeLog());
  await putRules(strip(before.global));
  await cp("/api/rules/final", { method: "PUT", body: JSON.stringify({ target: before.final }) });
  return reqs;
}

test.beforeAll(async () => {
  env = await startEnv();
  const nodes = [["tokyo-a", "JP", "东京", "A"], ["la-a", "US", "洛杉矶", "A"], ["la-b", "US", "洛杉矶", "B"]];
  for (const [i, [n, region, city, letter]] of nodes.entries()) {
    await env.seed("node", "add", n, "--address", `203.0.113.${10 + i}`, "--ssh-port", "1");
    await env.seed("node", "edit", n, "--region", region, "--city", city, "--letter", letter);
    await env.seed("inbound", "add", "--node", String(i + 1), "--protocol", "vless", "--port", "443", "--security", "reality", "--sni", "www.cloudflare.com", "--reality-dest", "www.cloudflare.com:443");
  }
  await env.seed("user", "add", "alice");
  await env.seed("user", "add", "mom");
});
test.afterAll(async () => { await env?.stop(); });

test("首次启动已迁移:规则表来自内置底稿,订阅按规则表生成", async ({ page }) => {
  const r = await getJSON("/api/routing");
  expect(r.mode).toBe("rules");
  expect(r.migration.status).toBe("migrated");
  await login(page, env);
  await page.goto(env.origin + "/#rules");
  await expect(page.locator(".rp-row").first()).toBeVisible();
  await expect(page.locator(".rp-row.final")).toContainText("其他全部");
  await expect(page.locator(".rp-row").first()).toContainText("局域网与本机");
});

test("添加规则:与 mpx rule add 一致", async ({ page }) => {
  const fromCLI = await viaCLI("rule", "add", "suffix", "example.org", "直连");
  await login(page, env);
  await page.goto(env.origin + "/#rules");
  await page.click("[data-act=rp-add]");
  await page.fill("#re-val", "example.org");
  await page.selectOption("#re-to", "DIRECT");
  await page.click("[data-act=rp-save]");
  await expect(page.locator(".toast")).toContainText("已添加");
  expect(norm(env.takeLog())).toEqual(fromCLI);
  await expect(page.locator(".rp-row").first()).toContainText("example.org");
  await putRules(strip((await getJSON("/api/rules")).global.slice(1)));
});

test("停用、拖动排序、删除、兜底:与 mpx rule toggle / move / rm / final 一致", async ({ page }) => {
  // toggle 发 PATCH,不重建规则,恢复也用 PATCH(id 不变)。
  const first = (await getJSON("/api/rules")).global[0];
  env.takeLog(); await env.cli("rule", "toggle", "1"); const cliToggle = norm(env.takeLog());
  await cp("/api/rules/" + first.id, { method: "PATCH", body: JSON.stringify({ enabled: true }) });
  await login(page, env);
  await page.goto(env.origin + "/#rules");
  await page.locator("[data-act=rp-toggle]").first().click();
  await expect(page.locator(".toast")).toContainText("已停用");
  expect(norm(env.takeLog())).toEqual(cliToggle);
  await expect(page.locator(".rp-row.off").first()).toBeVisible();
  await cp("/api/rules/" + first.id, { method: "PATCH", body: JSON.stringify({ enabled: true }) });

  const cliMove = await viaCLI("rule", "move", "2", "1");
  await page.reload();
  await page.locator(".rp-row[data-i='1']").dragTo(page.locator(".rp-row[data-i='0']"));
  await expect(page.locator(".toast")).toContainText("已移到第 1 条");
  expect(norm(env.takeLog())).toEqual(cliMove);
  const moved = await getJSON("/api/rules");
  await putRules(strip([moved.global[1], moved.global[0], ...moved.global.slice(2)]));

  const cliRm = await viaCLI("rule", "rm", "3");
  await page.reload();
  await page.locator("[data-act=rp-edit][data-i='2']").click();
  await page.click("[data-act=rp-del]");
  await expect(page.locator(".toast")).toContainText("已删除");
  expect(norm(env.takeLog())).toEqual(cliRm);
  await putRules(strip((await viaCLIRestoreSource()).global));

  const cliFinal = await viaCLI("rule", "final", "直连");
  await page.reload();
  await page.selectOption("#rp-final", "DIRECT");
  await expect(page.locator(".toast")).toContainText("其他全部 → 直连");
  expect(norm(env.takeLog())).toEqual(cliFinal);
});
let snapshot;
async function viaCLIRestoreSource() { return snapshot; }
test.beforeEach(async () => { snapshot = await getJSON("/api/rules"); });

test("用户规则:只改 alice,排在全局前面;与 mpx rule --user alice add 一致", async ({ page }) => {
  env.takeLog(); await env.cli("rule", "--user", "alice", "add", "keyword", "bili", "直连"); const fromCLI = norm(env.takeLog());
  await putRules([], "alice");
  await login(page, env);
  await page.goto(env.origin + "/#rules");
  await page.selectOption("#rp-scope", "alice");
  await expect(page.locator(".rp-sec").first()).toContainText("只对 alice 生效");
  await page.click("[data-act=rp-add]");
  await page.selectOption("#re-type", "keyword");
  await page.fill("#re-val", "bili");
  await page.selectOption("#re-to", "DIRECT");
  await page.click("[data-act=rp-save]");
  await expect(page.locator(".toast")).toContainText("已添加");
  expect(norm(env.takeLog())).toEqual(fromCLI);
  await expect(page.locator(".rp-row.own")).toContainText("bili");
  await page.fill("#rp-test", "www.bilibili.com");
  await expect(page.locator("#rp-res")).toContainText("仅 alice");
  await expect(page.locator("#rp-res .rp-big")).toContainText("直连");
});

test("策略组:新建与 mpx group add 结果一致,预览随条件变化;被引用时删不掉", async ({ page }) => {
  await env.cli("group", "add", "美国节点", "--region", "US", "--mode", "manual");
  const groups = await getJSON("/api/proxy-groups");
  const cliGroup = groups.find(g => g.name === "美国节点");
  await env.cli("group", "rm", "美国节点");
  await login(page, env);
  await page.goto(env.origin + "/#rules/groups");
  await page.click("[data-act=gp-edit][data-id='']");
  await page.fill("#gp-name", "美国节点");
  await page.locator("[data-key=regions] label", { hasText: "美国" }).click();
  await expect(page.locator("#gp-cnt")).toContainText("2 / 3");
  await page.click("#gp-mode [data-m=manual]");
  await expect(page.locator("#gp-cmd")).toHaveText('mpx group add "美国节点" --mode manual --region US');
  await page.click("#gp-save");
  await expect(page.locator(".toast")).toContainText("已保存");
  const uiGroup = (await getJSON("/api/proxy-groups")).find(g => g.name === "美国节点");
  expect({ ...uiGroup, id: 0 }).toEqual({ ...cliGroup, id: 0 });
  await expect(page.locator(".rp-g", { hasText: "美国节点" })).toContainText("洛杉矶 B");

  // 被规则引用:控制面拒绝,原话报出来
  const final = (await getJSON("/api/rules")).final;
  await page.locator(".rp-g", { hasText: "自动选择" }).click();
  await page.click("[data-act=gp-rm]");
  await page.click("#okb");
  await expect(page.locator("#modal-err")).not.toBeEmpty();
  expect((await getJSON("/api/rules")).final).toBe(final);
});

test("测试网址:命中第几条、去向和组内入口", async ({ page }) => {
  const auto = (await getJSON("/api/proxy-groups")).find(g => g.name === "自动选择");
  await cp("/api/rules/final", { method: "PUT", body: JSON.stringify({ target: "group:" + auto.id }) });
  await login(page, env);
  await page.goto(env.origin + "/#rules");
  await page.fill("#rp-test", "https://www.example.net/path");
  await expect(page.locator("#rp-res")).toContainText("www.example.net");
  await expect(page.locator("#rp-res")).toContainText("没有规则对上");
  await expect(page.locator("#rp-res")).toContainText("自动在 3 个入口里挑最快的");
  await expect(page.locator("#rp-res")).toContainText("按 IP 匹配");
});
