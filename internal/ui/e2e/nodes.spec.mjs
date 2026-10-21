// 节点、连接方式、对外地址页的写操作:每一条都和对应的 mpx node / inbound / host 命令对照。
// 节点地址是 127.0.0.1:1,SSH 立刻被拒,于是下发、安装、体检都会失败 —— 正好检查页面
// 把控制面的原话报出来。安装和体检走后台上任务的接口(POST .../jobs):失败也照样
// 立刻关掉确认框,原话由详情里盯任务的那一步报出来。
import { test, expect } from "@playwright/test";
import { startEnv, normalize, login, cpFetch } from "./env.mjs";

let env;
// FIXED_ID:「添加节点」那一条用例建的节点会被别的用例删掉或改号,后台任务的用例于是另用一台
// 专给它们用的节点(fixed-n),id 在 beforeAll 里建完就记下来,全程不变。
let FIXED_ID;
const cliTry = async (...a) => { try { await env.cli(...a); } catch { /* 失败也要比请求 */ } };
test.beforeAll(async () => {
  env = await startEnv();
  // 节点 1/2、3/4 两两是 twin;连接方式 1/2、3/4 分别挂在节点 1/2 上;对外地址 1/2 挂在连接方式 1/2 上。
  for (const n of ["cli-n", "ui-n", "cli-rm", "ui-rm"]) await env.seed("node", "add", n, "--address", "127.0.0.1", "--ssh-port", "1");
  for (const port of ["443", "2000"]) for (const node of ["1", "2"]) await env.seed("inbound", "add", "--node", node, "--protocol", "vless", "--port", port, "--security", "reality", "--sni", "www.cloudflare.com", "--reality-dest", "www.cloudflare.com:443", "--tag", `t-${node}-${port}`);
  for (const inb of ["1", "2"]) await env.seed("host", "add", "--inbound", inb, "--remark", "入口", "--address", "a.example.com");
  // 后台任务用例专用的节点:建在最后,id 从控制面读回来,不靠按顺序数。
  await env.seed("node", "add", "fixed-n", "--address", "127.0.0.1", "--ssh-port", "1");
  const r = await cpFetch(env.cpURL + "/api/nodes", { headers: { Authorization: "Bearer " + env.token } });
  const nodes = await r.json();
  FIXED_ID = (nodes || []).find(n => n.name === "fixed-n")?.id;
  if (!FIXED_ID) throw new Error("没找到 fixed-n 节点");
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
  // 点行尾的铅笔:行中间可能正好是「N 个对外地址」展开按钮。
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
  // 地址用 127.0.0.1:这台机器上连 127.0.0.9 会一直挂到超时,后面的用例要等应用、刷新,
  // 都跟着慢两分钟。这里比对的是请求,地址换个值不影响结论。
  await env.cli("node", "edit", "1", "--address", "127.0.0.1", "--ssh-user", "deploy");
  const fromCLI = take(names, opt);
  await openNode(page, 2);
  await page.click("[data-act=node-menu]");
  await page.click("[data-act=node-edit]");
  await page.fill("#n-address", "127.0.0.1");
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

// 后台任务的同步形式:界面发的是 .../jobs,和 CLI 的同步接口只差这一段路径(请求体、参数都一样)。
const syncOf = p => p.replace(/\/(diagnostics|install)\/jobs(\?|$)/, "/$1$2");

// softJSON:页面关掉、换页时还在路上的请求会被取消,取回来的响应也跟着作废。这条请求已经
// 没人要了,不当成失败(否则轮询收尾那一下正好撞上,会被记成红)。
const softJSON = async route => {
  try { const res = await route.fetch(); return { res, body: await res.json() }; }
  catch (e) { if (/disposed|Test ended/i.test(String(e))) return null; throw e; }
};

// 用后台上任务的用例:节点 FIXED_ID(fixed-n)的详情、任务列表、任务本身全由 route 回答,不发到真控制面。
// 节点名字固定成 fixed-n,确认框和提示里的名字就有得比。返回两本账:起了几次任务、现场查过几次。
function fakeJobs(page, opts) {
  // started:POST 过之后才有在跑的任务。进页面时就报一条在跑的,按钮会按设计整成禁用,用例点不动。
  const st = { posts: 0, fresh: 0, phase: "running", started: false };
  page.route(new RegExp(`/api/nodes/${FIXED_ID}(\\?|$)`), async route => {
    const u = new URL(route.request().url());
    if (u.searchParams.get("fresh")) st.fresh++;
    const got = await softJSON(route);
    if (!got) return;
    const n = got.body;
    n.id = FIXED_ID; n.name = "fixed-n"; n.display = "";
    await route.fulfill({ response: got.res, json: n });
  });
  // 起任务时控制面会把安装的内核记进任务(os/core),详情里的「正在安装 xray」读的就是它。
  const runningJob = () => J("running", { id: 91, kind: opts.kind || "diagnose", ...(opts.kind === "install" ? { core: "xray" } : {}) });
  page.route(`**/api/nodes/${FIXED_ID}/jobs*`, route => {
    const u = new URL(route.request().url()), run = st.started && st.phase === "running";
    const jobs = opts.active ?? (run ? [runningJob()] : []);
    return route.fulfill({ json: { jobs: u.searchParams.get("active") ? jobs : (opts.recent || []) } });
  });
  // 起任务:POST /api/nodes/{id}/(diagnostics|install)/jobs。路径段和用例里的 kind 对不上
  // (体检是 diagnostics、安装是 install),先换算。Playwright 先看后注册的那一条,** 会一直
  // 匹配到路径末尾,所以这条要放在上面那条 .../jobs* 之后,否则写请求也被当成列表读掉。
  const seg = { diagnose: "diagnostics", install: "install" }[opts.kind || "diagnose"];
  page.route(`**/api/nodes/${FIXED_ID}/${seg}/jobs*`, route => {
    st.posts++; st.started = true; st.phase = "running";
    const job = runningJob();
    return route.fulfill({ status: opts.reused ? 200 : 202, json: opts.reused ? { reused: true, job } : { job } });
  });
  // GET /api/jobs/{id}:轮询只管做完没有;用例把 phase 改成 done 之后,这边就按结束报(opts.end)。
  page.route("**/api/jobs/91", route => route.fulfill({ json: J(st.phase === "running" ? "running" : (opts.end || "done"), { id: 91, kind: opts.kind || "diagnose", node_id: FIXED_ID }) }));
  return st;
}
const J = (status, extra = {}) => ({ id: 91, kind: "diagnose", node_id: FIXED_ID, status, started_at: "2026-01-01T02:00:00Z", finished_at: status === "running" ? null : "2026-01-01T02:05:00Z", code: status === "running" ? "" : "0", error: "", ui_hint: "", result: null, ...extra });

test("体检:开始后立刻关掉确认框并提示已开始,详情里马上显示正在体检", async ({ page }) => {
  await login(page, env);
  const st = fakeJobs(page, { kind: "diagnose" });
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.click("[data-act=node-diagnose]");
  await page.click("#okb");
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await expect(page.locator(".toast")).toContainText("已开始在 fixed-n 上体检");
  expect(st.posts).toBe(1);
  await expect(page.locator("#job-callout")).toContainText("正在体检（开始于 10:00）。可以离开这个页面，做完会自动显示。");
  const btn = page.locator("[data-act=node-diagnose]");
  await expect(btn).toHaveAttribute("aria-disabled", "true");
  await expect(btn).toHaveAttribute("data-tip", "这台节点正在体检，等它做完再点");
  await expect(page.locator("#diag-why")).toHaveText("这台节点正在体检，等它做完再点");
  // 只是 aria-disabled:仍可聚焦,点了也不会再弹确认框。
  await btn.dispatchEvent("click");
  await expect(page.locator("#modal.open")).toHaveCount(0);
});

test("体检:离开这个页面再回来,还在跑;完成后提示体检完成并重读详情", async ({ page }) => {
  test.setTimeout(60_000); // 轮询等 20 秒才收尾
  await login(page, env);
  const st = fakeJobs(page, { kind: "diagnose" });
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.click("[data-act=node-diagnose]");
  await page.click("#okb");
  await expect(page.locator("#job-callout")).toContainText("正在体检");

  // 换到别的页再回来:任务还在跑,详情照样显示正在体检(不是新起的一个)。
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("#job-callout")).toContainText("正在体检（开始于 10:00）");
  expect(st.posts).toBe(1);

  // 做完:轮询(3 秒一次)发现任务收尾,给一句提示,再现场查状态、重读详情。
  st.phase = "done";
  await expect(page.locator(".toast")).toContainText("体检完成", { timeout: 20_000 });
  await expect(page.locator("#job-callout")).not.toContainText("正在体检");
  expect(st.fresh).toBeGreaterThan(0);
});

test("安装:开始后提示已开始在 fixed-n 上安装,详情里显示正在安装", async ({ page }) => {
  await login(page, env);
  fakeJobs(page, { kind: "install" });
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.click("[data-act=node-install]");
  await page.selectOption("#i-core", "xray");
  await page.click("#okb");
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await expect(page.locator(".toast")).toContainText("已开始在 fixed-n 上安装 xray");
  await expect(page.locator("#job-callout")).toContainText("正在安装 xray（开始于 10:00）…");
  await expect(page.locator("[data-act=node-install]")).toHaveAttribute("aria-disabled", "true");
  await expect(page.locator("[data-act=node-install]")).toHaveAttribute("data-tip", "这台节点正在安装，等它做完再点");
});

test("体检:这台已经在体检了,再点提示接着等它做完", async ({ page }) => {
  await login(page, env);
  // 被受理的是一条已经在跑的任务(200 reused),所以起完之后它照样在「正在跑」里。
  fakeJobs(page, { kind: "diagnose", reused: true });
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.click("[data-act=node-diagnose]");
  await page.click("#okb");
  await expect(page.locator("#modal.open")).toHaveCount(0);
  await expect(page.locator(".toast")).toContainText("这台节点已经在体检了，接着等它做完就行");
});

// 控制面重启时正在跑的任务会被标成 interrupted。再进来时没有正在跑的,靠最近一次那句话交代。
test("体检:上次没做完(控制面重启)时给灰字提示,再点就行", async ({ page }) => {
  await login(page, env);
  const recent = J("interrupted", { finished_at: new Date(Date.now() - 60_000).toISOString(), error: "控制面重启了，这次没做完" });
  fakeJobs(page, { kind: "diagnose", active: [], recent: [recent] });
  // 进节点的第一次读先记下「还没有在跑的」,紧接着的那次读才问最近一次做完没有。
  // 第二次读要在同一个页面里再读一遍:去列表再回详情,节点 id 没变,jobAsked 记着,
  // 第二次读就会去问 /jobs?limit=1(goto 同一个地址不会重新加载页面,也不发 hashchange)。
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator("#job-notice")).toContainText("上次体检没做完：控制面重启了。再点一次「体检」就行。");
  const cls = await page.locator("#job-notice .callout").evaluate(el => getComputedStyle(el).backgroundColor);
  // 灰底:红、黄、绿三个分量差不多。
  const m = (cls.match(/[\d.]+/g) || []).map(Number);
  expect(Math.abs(m[0] - m[1])).toBeLessThan(6);
  expect(Math.abs(m[1] - m[2])).toBeLessThan(6);
});

test("体检:上次没做完是 24 小时以前的,不再提", async ({ page }) => {
  await login(page, env);
  const recent = J("interrupted", { finished_at: new Date(Date.now() - 2 * 864e5).toISOString() });
  fakeJobs(page, { kind: "diagnose", active: [], recent: [recent] });
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await expect(page.locator("#job-notice")).toHaveCount(1);
  await expect(page.locator("#job-notice")).not.toContainText("没做完");
});

test("体检/安装:确认文案说可以关掉页面,做完会显示结果", async ({ page }) => {
  await login(page, env);
  fakeJobs(page, { kind: "diagnose", active: [] });
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await page.click("[data-act=node-diagnose]");
  await expect(page.locator("#modal.open .mbody p").first()).toHaveText("在节点上做一次完整体检：运行状况、能访问哪些网站、网速（不测硬件）。最长要十几分钟。可以关掉这个页面，做完会在节点上显示结果。");
  await page.click("[data-act=modal-cancel]");
  await page.click("[data-act=node-install]");
  await expect(page.locator("#modal.open .mbody p").first()).toHaveText("控制面下载发布包（下过的会复用），送到节点上，应用当前配置并启动。慢链路上要几分钟。可以关掉这个页面，做完会在节点上显示结果。");
  await page.click("[data-act=modal-cancel]");
});

// 安装、体检:页面发 .../jobs,请求体和参数与 CLI 的同步接口逐字相同(只多 /jobs 这一段)。
// 走后台上任务时控制面回 202(任务已经起了),节点连不上是任务自己在后面失败的,所以确认框
// 照常关掉、先提示「已开始在 …」,失败的原话随后由盯任务的那一步用「没做成：」的提示报出来。
for (const [title, args, act, fill] of [
  ["安装(指定 sing-box 与版本)", ["node", "install", "--core", "sing-box", "--version", "1.12.0"], "node-install", async page => { await page.selectOption("#i-core", "sing-box"); await page.fill("#i-ver", "1.12.0"); await expect(page.locator("#modal")).not.toContainText("mpx "); }],
  ["体检(默认参数)", ["node", "diagnose"], "node-diagnose", null],
]) {
  test(`${title}:与 CLI 的同步请求一致,失败由任务提示报出控制面原话`, async ({ page }) => {
    test.setTimeout(60_000); // 节点连不上,任务在后面自己失败,提示要等
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
    // 节点直接拒连,任务失败得很快:3 秒一次的轮询可能抢在断言之前把「已开始在」换成
    // 「没做成」。把出现过的提示按顺序记下来,两条都核对,也能核对先后。
    await page.evaluate(() => {
      window.__toasts = [];
      new MutationObserver(() => {
        const t = document.querySelector(".toast");
        if (t && window.__toasts[window.__toasts.length - 1] !== t.textContent) window.__toasts.push(t.textContent);
      }).observe(document.body, { childList: true, subtree: true, characterData: true });
    });
    await page.click("#okb");
    // 202:任务起了,确认框立刻关掉。
    await expect(page.locator("#modal.open")).toHaveCount(0);
    const res = env.lastResponse();
    expect(res.status).toBe(202);
    const jobID = JSON.parse(res.body)?.job?.id;
    expect(jobID).toBeGreaterThan(0);
    const mine = take([], opt);
    expect(mine.length).toBe(1);
    expect(mine[0].path).toContain("/jobs");
    expect(mine.map(r => ({ ...r, path: syncOf(r.path) }))).toEqual(fromCLI);
    // 任务在连不上的节点上失败:盯任务的那一步用「没做成：」加控制面的原话提示出来,提示就是
    // 控制面这次任务收尾时的 error;控制面给了界面说法就用界面说法。
    const failed = page.locator(".toast").filter({ hasText: "没做成：" });
    await expect(failed).toBeVisible({ timeout: 60_000 });
    // 控制面把同步接口的错误存进任务,没有错误说明的任务(比如只是超时)提示里只有「没做成」。
    const done = await cpFetch(env.cpURL + "/api/jobs/" + jobID, { headers: { Authorization: "Bearer " + env.token } }).then(r => r.json());
    expect(done.status).toBe("failed");
    expect(done.error).not.toBe("");
    await expect(failed).toContainText(done.ui_hint || done.error);
    // 先「已开始在…」再「没做成…」:被受理时立刻给回执,失败随后由盯任务的那一步报。
    const toasts = await page.evaluate(() => window.__toasts);
    const started = toasts.findIndex(t => t.includes("已开始在"));
    expect(started).toBeGreaterThanOrEqual(0);
    expect(toasts.findIndex(t => t.includes("没做成："))).toBeGreaterThan(started);
  });
}

// 安装做完:任务结果里带 node / core / version,提示用起任务时记下的显示名拼。
test("安装:做完提示已装好并启动,带上版本", async ({ page }) => {
  test.setTimeout(60_000); // 轮询等 20 秒才收尾
  await login(page, env);
  const st = fakeJobs(page, { kind: "install" });
  await page.unroute("**/api/jobs/91");
  await page.route("**/api/jobs/91", route => route.fulfill({ json: J(st.phase === "running" ? "running" : "done", { kind: "install", result: { node: "fixed-n", core: "xray", version: "26.4.1" } }) }));
  await page.goto(env.origin + `/#nodes/${FIXED_ID}`);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  await page.click("[data-act=node-install]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已开始在 fixed-n 上安装 xray");
  st.phase = "done";
  await expect(page.locator(".toast")).toContainText("已在 fixed-n 上装好并启动 xray 26.4.1", { timeout: 20_000 });
});

test("添加连接方式:与 mpx inbound add 一致(含 QUIC 默认 udp + tls)", async ({ page }) => {
  await login(page, env);
  const opt = { ids: ["1", "2"] };
  env.takeLog();
  await env.cli("inbound", "add", "--node", "1", "--protocol", "vless", "--port", "8080", "--network", "ws", "--ws-path", "/ws", "--max-users", "50");
  await env.cli("inbound", "add", "--node", "1", "--protocol", "hysteria2", "--port", "8443", "--sni", "hy.example.com", "--cert-file", "/etc/c.pem", "--key-file", "/etc/k.pem");
  const fromCLI = take([], opt);
  // 节点详情的「连接方式」卡片:添加连接 → 其他协议… 打开完整连接方式表单。
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
  await expect(page.locator(".toast")).toContainText("已创建连接方式");
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
  await expect(page.locator(".toast")).toContainText("已创建连接方式");
  expect(take([], opt)).toEqual(fromCLI);
});

test("编辑连接方式:与 mpx inbound edit 一致", async ({ page }) => {
  await login(page, env);
  const names = ["t-1-443", "t-2-443"], opt = { ids: ["1", "2"], drop: ["reality_pub", "short_ids"] };
  env.takeLog();
  await env.cli("inbound", "edit", "1", "--port", "9443", "--sni", "www.example.com", "--max-users", "10", "--mux");
  const fromCLI = take(names, opt);
  await page.goto(env.origin + "/#nodes/2");
  await page.click(".cx-row[data-act=conn-edit][data-id='2']");
  // 连接方式字段收在编辑连接抽屉的「高级」里。
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

test("对外地址:add / edit / rm 与 mpx host 一致,删除要确认", async ({ page }) => {
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
  // 对外地址按钮在编辑连接抽屉的「高级」里。
  await openConnEdit(page, 2);
  await page.click("[data-act=conn-host-new][data-inb='2']");
  await page.fill("#h-remark", "直连");
  await page.fill("#h-port", "8443");
  await page.fill("#h-alpn", "h2,http/1.1");
  await expect(page.locator("#host-form")).not.toContainText("mpx ");
  await page.click("#h-save");
  await expect(page.locator(".toast")).toContainText("已创建对外地址「直连」");
  expect(take([], opt)).toEqual(addCLI);

  await openConnEdit(page, 2);
  await page.click("[data-act=conn-host-edit][data-id='2']");
  await expect(page.locator("#drawer.open h2")).toHaveText("编辑对外地址「入口」");
  await page.fill("#h-address", "");
  await page.fill("#h-remark", "备用");
  await expect(page.locator("#host-form")).not.toContainText("mpx ");
  await page.click("#h-save");
  await expect(page.locator(".toast")).toContainText("已更新对外地址「备用」");
  expect(take([], opt)).toEqual(editCLI);

  await openConnEdit(page, 2);
  await page.click("[data-act=conn-host-edit][data-id='2']");
  await expect(page.locator("#drawer.open h2")).toHaveText("编辑对外地址「备用」");
  // 删除对外地址和节点详情一样是灰卡灰按钮,红色只留给确认框里最后那下。
  const hzone = page.locator("#drawer.open .card").filter({ hasText: "删除对外地址" });
  await expect(hzone).not.toHaveClass(/danger-zone/);
  await expect(page.locator("[data-act=conn-host-rm]")).not.toHaveClass(/c-bad/);
  await page.click("[data-act=conn-host-rm]");
  await page.click("[data-act=modal-cancel]");
  expect(env.takeLog()).toEqual([]);
  await page.click("[data-act=conn-host-rm]");
  await page.click("#okb");
  await expect(page.locator(".toast")).toContainText("已删除对外地址「备用」");
  expect(take([], opt)).toEqual(rmCLI);
});

test("删除节点确认框:没输入名字时禁用,输入后按钮实心红底白字", async ({ page }) => {
  await login(page, env);
  await openNode(page, 2);
  await page.click("[data-act=node-rm]");
  const okb = page.locator("#okb");
  // 逐字确认之前按钮禁用(可以浅、可以灰)。
  await expect(okb).toBeDisabled();
  await page.fill("#typed", "ui-n");
  await expect(okb).toBeEnabled();
  const badRGB = await page.evaluate(() => {
    const probe = document.createElement("span");
    probe.style.color = getComputedStyle(document.documentElement).getPropertyValue("--bad").trim();
    document.body.append(probe);
    const c = getComputedStyle(probe).color;
    probe.remove();
    return c;
  });
  const solid = async () => okb.evaluate(el => { const s = getComputedStyle(el); return { bg: s.backgroundColor, fg: s.color }; });
  // 禁用时是灰底,背景有过渡动画:等它停在实心红上再比。
  await expect.poll(async () => (await solid()).bg).toBe(badRGB);
  let st = await solid();
  expect(st.fg).toBe("rgb(255, 255, 255)");
  // 悬停、键盘聚焦时也必须是实心红底白字。
  await okb.hover();
  st = await solid();
  expect(st.bg).toBe(badRGB);
  expect(st.fg).toBe("rgb(255, 255, 255)");
  await okb.focus();
  st = await solid();
  expect(st.bg).toBe(badRGB);
  expect(st.fg).toBe("rgb(255, 255, 255)");
  await page.click("#modal [data-act=modal-cancel]");
});

test("删除连接方式与节点:要逐字输入名字才放行,和 CLI 一致", async ({ page }) => {
  await login(page, env);
  let opt = { ids: ["3", "4"] };
  env.takeLog();
  await env.cli("inbound", "rm", "3");
  const inbCLI = take([], opt);
  await page.goto(env.origin + "/#nodes/2");
  await page.click(".cx-row[data-act=conn-edit][data-id='4']");
  await page.click("#conn-form details.adv2 > summary");
  // 删除连接方式也是灰卡灰按钮(这里打开的是「编辑连接」抽屉),不再用 danger-zone / c-bad。
  const izone = page.locator("#drawer.open .dz").filter({ hasText: "删除连接" });
  await expect(izone.locator("[data-act=conn-rm]")).not.toHaveClass(/c-bad/);
  await page.click("[data-act=conn-rm]");
  await expect(page.locator("#okb")).toBeDisabled();
  await expect(page.locator("#okb")).toHaveText("删除并应用到节点");
  await expect(page.locator("#altb")).toBeDisabled();
  await page.fill("#typed", "t-2-2000");
  await page.click("#altb");
  await expect(page.locator(".toast")).toContainText("已删除连接方式「t-2-2000」");
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

// 「创建并应用到节点」:先建连接方式再走现有的 POST /api/nodes/{id}/sync。这里 SSH 连不上,
// 应用失败要把原因告诉使用者,入站照样建好,卡片留着「有改动未应用」,头部换成刷新状态。
test("创建并应用到节点:应用失败时连接方式保留,连不上时头部换成刷新", async ({ page }) => {
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

test("旧连接方式地址与子路径跳到节点列表", async ({ page }) => {
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
  const install = page.locator(".row").filter({ hasText: "安装代理程序" });
  await expect(install.locator(".sub")).toHaveText("把代理程序装到这台节点上并启动，会用当前配置");
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
  test.setTimeout(60_000); // 现场刷新要等 SSH 那边超时
  // 权限来自 /ui/meta,必须在页面第一次加载前就把写权限摘掉。
  await page.route("**/ui/meta", async route => {
    const got = await softJSON(route);
    if (!got) return;
    const m = got.body;
    m.allow = (m.allow || []).filter(a => a.startsWith("GET "));
    await route.fulfill({ response: got.res, json: m });
  });
  await page.route("**/api/nodes/2", async route => {
    const got = await softJSON(route);
    if (!got) return;
    const n = got.body;
    n.deployed_hash = "deployed";
    n.status = { ...n.status, observed_hash: "deployed", reachable: false, error: "dial tcp: i/o timeout" };
    await route.fulfill({ response: got.res, json: n });
  });
  await page.route("**/api/nodes/pending", route => route.fulfill({ json: [] }));
  // 这套环境里的控制面还没有后台任务那组接口(404):页面把它当作没有,照旧显示详情。
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

// 模板漂移:控制面说这台的配置写法和新版本对不上(template_drift),详情里「应用到节点」
// 就是主按钮,并说明原因;列表里同一台也标「需要应用」(和「有改动未应用」同一个位置)。
test("模板漂移:详情提示「应用一次才生效」,列表标「需要应用」", async ({ page }) => {
  await login(page, env);
  await page.route("**/api/nodes/2", async route => {
    const got = await softJSON(route);
    if (!got) return;
    const n = got.body;
    n.deployed_hash = "deployed"; n.template_drift = true;
    n.status = { ...n.status, observed_hash: "deployed", reachable: true, error: "" };
    await route.fulfill({ response: got.res, json: n });
  });
  await page.route("**/api/nodes", async route => {
    const got = await softJSON(route);
    if (!got) return;
    const j = got.body;
    await route.fulfill({ response: got.res, json: (j || []).map(n => n.id === 2 ? { ...n, deployed_hash: "deployed", template_drift: true, status: { ...(n.status || {}), observed_hash: "deployed" } } : n) });
  });
  await page.route("**/api/nodes/pending", route => route.fulfill({ json: [] }));
  await openNode(page, 2);
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  const primary = page.locator(".ph .btn.pri");
  await expect(primary).toHaveText("应用到节点");
  await expect(primary).toHaveAttribute("data-act", "node-sync");
  const whyID = await primary.getAttribute("aria-describedby");
  await expect(page.locator("#" + whyID)).toHaveText("新版本改了节点配置的写法，应用一次才生效");
  await page.goto(env.origin + "/#nodes");
  await expect(page.locator("html[data-loading]")).toHaveCount(0);
  // 按行上的节点 id 找,不按名字:列表显示的是显示名,内部名可能已经不是 ui-n;
  // 「ui-n」还会命中后面用例建的 ui-new。
  const row = page.locator('tbody tr[data-href="#nodes/2"]');
  await expect(row).toContainText("需要应用");
});