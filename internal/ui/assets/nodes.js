// 节点页:列表、详情,以及 add / edit / rm / sync / install / drain / undrain / diagnose。
// 每个写操作发出的请求与对应的 mpx node 命令一致;节点状态、在线、配置预览、体检报告
// 只读。下发、安装、体检真去 SSH,失败时把控制面的原话(含 501 没接下发通道)报出来。
// 安装与体检走后台任务(POST .../jobs):回来就关确认框,详情里一直显示到做完为止。
"use strict";

const nodePath = id => "/api/nodes/" + id;
// asModelNode:GET 回来的 NodeView 摆成 model.Node 的 JSON 形状(CLI 的 node edit 正是
// 解出 v.Node 再整体 PUT;deployed_hash / deployed_at 原样带回)。
function asModelNode(v) {
  const o = { id: v.id, name: v.name, address: v.address, ssh_port: v.ssh_port || 0, ssh_user: v.ssh_user || "", api_port: v.api_port || 0, config_path: v.config_path || "", service_name: v.service_name || "" };
  // outbound_ip 在 Go 侧是 omitempty:空串时不能带这个键,非空才带,否则 PUT/POST 的
  // JSON 与 CLI 逐字对不上(CLI 走 model.Node 的序列化)。
  if (v.outbound_ip) o.outbound_ip = v.outbound_ip;
  if (v.deployed_hash) o.deployed_hash = v.deployed_hash;
  if (v.deployed_at) o.deployed_at = v.deployed_at;
  o.draining = !!v.draining;
  return o;
}
// dropEmptyOutbound:model.Node.OutboundIP 在 Go 侧是 omitempty,新建的 POST 体与编辑的
// PUT 体都必须和 CLI 序列化逐字一致 —— 空串时删掉这个键,非空才留。
function dropEmptyOutbound(o) { if (!o.outbound_ip) delete o.outbound_ip; return o; }
// 核心:status.cores 有就逐个列;老控制面只给 version/active(那是 xray 的)。
function coreText(s) {
  if (!s) return "还没检查过";
  if (s.cores?.length) return s.cores.map(c => `${c.core} ${c.active ? "运行中" : "未运行"}${c.version ? " · " + c.version : ""}`).join(" / ");
  return s.version ? `xray ${s.active ? "运行中" : "未运行"}` : "没装代理程序";
}
// previewDisplay:和服务端 model.NodeDisplay 同一套拼法(旗帜 + 城市/国家 + 字母),只用来预览;
// 国家中文名浏览器自己有(Intl.DisplayNames),HK/MO 用 CLDR 短名,与服务端一致。
function previewDisplay(m) {
  const r = (m.region || "").trim().toUpperCase();
  if (!r) return "";
  const iso = /^[A-Z]{2}$/.test(r);
  const flag = iso ? String.fromCodePoint(...[...r].map(ch => 0x1F1E6 + ch.charCodeAt(0) - 65)) : "";
  let place = (m.city || "").trim();
  if (!place) { place = { HK: "香港", MO: "澳门" }[r] || r; if (iso && !/^(HK|MO)$/.test(r)) { try { place = new Intl.DisplayNames(["zh-Hans"], { type: "region" }).of(r) || r; } catch { /* 老浏览器 */ } } }
  return [flag, place, m.letter || ""].filter(Boolean).join(" ");
}
const sshText = n => `${n.ssh_user || "root"}@${n.address || "?"}:${n.ssh_port || 22}`;
const certText = c => c.error ? "读不到" : c.days_left < 0 ? "已过期" : `剩 ${c.days_left} 天`;

PAGES.nodes = {
  title: "节点",
  count: () => NODES_TOTAL ?? STATUS?.counts?.nodes ?? "",
  crumb: (r, d) => r[1] ? H`<a href="#nodes">节点</a><i>/</i><b>${nodeLabel(d?.node) || "节点"}</b>` : H`<b>节点</b>`,
  async load(r) {
    if (r[1]) {
      const id = +r[1];
      if (jobNode !== id) { jobNode = id; jobAsk = {}; jobAsked = new Set(); jobsAfterReady = false; jobRunning = new Map(); jobPollStop(); }
      // 体检报告没有就是 404,不算页面错误。
      const diag = api(nodePath(id) + "/diagnostics?from=node").catch(e => e.status === 404 ? null : { error: e.message });
      // 连接方式由 conn.js 组件取数与渲染。
      const [node, conn, report] = await Promise.all([api(nodePath(id)), CONN.load([id]), diag]);
      const jobs = await loadNodeJobs(id);
      return { node, inbounds: conn.inbounds, conn, report, jobs };
    }
    const nodes = await api("/api/nodes");
    NODES_TOTAL = nodes.length;
    for (const n of nodes) NODE_LABEL_BY_ID[n.id] = n;
    return { nodes };
  },
  render(d, r) { return r[1] ? nodeDetail(d) : nodeList(d); },
  // after:详情画好后(innerHTML 已换),先把体检页带过来的 AUTO_APPLY 用掉:能应用就替用户
  // 打开「应用到节点」的确认弹窗,不能就说明白。之后才接手正在跑的体检/安装 —— 顺序不能反:
  // 没有正在跑的任务时 jobsAfter 会把轮询停掉,那一停会把确认框一起收掉。
  // d 为空是骨架那一次,确认框不做,但标志照样清掉(与以前一样)。
  after(d, r) { if (r[1]) autoApplyPending(+r[1], d); if (d && r[1]) jobsAfter(d); },
};
// NODE_LABEL_BY_ID:节点 id → 显示名。列表页一拉到节点就顺手记下(见 PAGES.nodes.load),
// 提示里那句「已开始在 🇯🇵 东京 A 上安装 xray」和轮询时的提示都用得着它。
let NODE_LABEL_BY_ID = {};
// JOB_TEXT 把体检、安装两种任务的界面说法放在一处:只是叫法的差别,请求与数据共用一套。
const JOB_TEXT = {
  diagnose: { started: label => `已开始在 ${label} 上体检`, reused: "这台节点已经在体检了，接着等它做完就行", done: () => "体检完成", running: t => `正在体检（开始于 ${t}）。可以离开这个页面，做完会自动显示。`, last: "上次体检没做完：控制面重启了。再点一次「体检」就行。" },
  install: { started: (label, core) => `已开始在 ${label} 上安装 ${core}`, reused: "这台节点已经在安装了，接着等它做完就行", done: (label, core, v) => `已在 ${label} 上装好并启动 ${core} ${v || ""}`.trim(), running: (t, core) => `正在安装 ${core}（开始于 ${t}）…`, last: "上次安装没做完：控制面重启了。再点一次「安装」就行。" },
};
// jobLabel:这台节点在界面上怎么叫(显示名)。
const jobLabel = id => nodeLabel(NODE_LABEL_BY_ID[id]) || "这台节点";
// loadNodeJobs:这个节点最近的体检、安装任务。GET /jobs 是老控制面没有的接口(404):
// 当作没有,页面和以前一样。其它错误同样只是这次没有,不能挡住详情本身。
async function loadNodeJobs(id) {
  const active = await api(nodePath(id) + "/jobs?active=1&limit=5").catch(() => null);
  if (!Array.isArray(active?.jobs)) return null;
  // 「上次没做完」:不仅这次还没起过任务,而且要连着两次读都没有正在跑(中间那次是起任务
  // 时的重读)。这样「开始体检」的那次重读不会把自己刚起的任务当成上次没做完。
  let last = null;
  if (!active.jobs.length && !jobAsk[id] && jobAsked.has(id)) {
    const recent = await api(nodePath(id) + "/jobs?limit=1").catch(() => null), j = recent?.jobs?.[0];
    if (j && j.status === "interrupted" && Date.now() - new Date(j.finished_at || j.started_at) < DAY) last = j;
  }
  jobAsk[id] = !!active.jobs.length;
  jobAsked.add(id);
  // 任务结果里的 core/version 只在 install 的 result 里;「正在安装 xray」也读它。
  return { active: active.jobs.map(j => j.kind === "install" ? { ...j, core: j.core || j.result?.core || "" } : j), last };
}
// jobAsk:节点 id → 上一次读到过正在跑的任务;jobAsked:这次进节点以来已经读过没有。
// 普通刷新(每 30 秒、写完再读)不再反复问「最近一次做完没有」;重新进一次节点才重新问。
// jobNode 是现在打开的是哪台节点,换了就从头算。jobsAfterReady 用来跳过进节点后的第一次画。
// jobRunning 是上一次读到的在跑任务(id → 任务),只属于 jobNode 这台节点:画的时候
// current.data 已经是新的了,要跟「刚做完的」比只能靠它。
let jobAsk = {}, jobAsked = new Set(), jobNode = null, jobsAfterReady = false;
let jobRunning = new Map();

// 详情里「正在…」和「上次没做完」两块的位置:任务一变就在原地换掉,不整页重画
// (整页重画会把焦点、滚动位置都抖一下)。占位块排在详情页正文的最前面,跟着标题走。
const JOB_SLOTS = ["callout", "notice"];
function jobsAfter(d) {
  const id = +route[1];
  if (!d?.node || d.node.id !== id) return;
  const jobs = d.jobs || null, run = (jobs?.active || []).find(j => j.status === "running") || null;
  // 刚做完的那一个:第一次读到 d 时先记下来(不提示:那是进来之前的事),
  // 之后从「正在跑」里消失或不再在跑的就是刚做完的,要提示并刷新详情。
  const seen = jobRunning, now = (jobs?.active || []).filter(j => j.status === "running");
  jobRunning = new Map(now.map(j => [j.id, j]));
  if (!jobsAfterReady) jobsAfterReady = true;
  else {
    // 一件任务刚做完、另一件刚开始时两件一起处理,不能只认其中一件。
    for (const j of seen.values()) if (!now.some(x => x.id === j.id)) jobSettle(j);
  }
  fillJobSlots(d);
  // 正在跑就接着盯;jobPollActive 表示已经有一个在盯(或正在重读),不再起第二个。
  if (run) jobPollStart(id);
  else jobPollStop();
}
// jobFresh:去 GET /api/jobs/{id} 拿一件任务的最新样子。读不到就算了,退回手里这一份。
async function jobFresh(j) { return await api("/api/jobs/" + j.id).catch(() => null) || j; }
function fillJobSlots(d) {
  const jobs = d?.jobs, n = d?.node;
  if (!jobs || !n) return;
  const run = (jobs.active || []).find(j => j.status === "running") || null, last = jobs.last || null;
  const showRun = !!run, showLast = !run && !!last;
  for (const k of JOB_SLOTS) {
    const old = $("#job-" + k);
    if (!old || old.dataset.on === (k === "callout" ? String(showRun) : String(showLast))) continue;
    const el = document.createElement("div");
    el.id = "job-" + k; el.dataset.on = k === "callout" ? String(showRun) : String(showLast);
    el.innerHTML = val(jobCallout(k, d));
    old.replaceWith(el);
  }
}
// jobCallout:正在跑的是墨色提示,上次没做完的是灰色提示。
function jobCallout(kind, d) {
  const jobs = d.jobs, j = kind === "callout" ? (jobs.active || []).find(x => x.status === "running") : jobs.last;
  if (!j) return "";
  const t = JOB_TEXT[j.kind];
  if (!t) return "";
  if (kind === "callout") return H`<div class="callout stone mb" role="status">${ic(j.kind === "diagnose" ? "pulse" : "dl")}<div><b>${t.running(hm(new Date(j.started_at)), j.core)}</b></div></div>`;
  return H`<div class="callout mb mt">${ic("clock")}<div>${t.last}</div></div>`;
}
// 盯一个正在跑的任务:每 3 秒重读一次详情(里面带着这台节点的任务),换页、换节点就停。
// jobPollFor 是正在盯的节点,jobPollActive 表示这一轮还没走完(计时器在等,或正在重读):
// 重读期间画出来的 jobsAfter 看到它就不要再起一轮,否则会自己叠出好几个计时器。
// 下一轮只在这次重读走完之后才排。
let jobTimer = 0, jobPollFor = null, jobPollActive = false;
function jobPollStop() { clearTimeout(jobTimer); jobTimer = 0; jobPollFor = null; jobPollActive = false; }
function jobStillHere(id) { return jobPollActive && jobPollFor === id && route[0] === "nodes" && +route[1] === id && !!$("#job-callout"); }
function jobPollStart(id) {
  if (jobPollActive && jobPollFor === id) return;
  jobPollStop(); jobPollFor = id; jobPollActive = true;
  jobPollTick(id);
}
async function jobPollTick(id) {
  jobTimer = 0;
  if (!jobStillHere(id)) return;
  await load(true);
  // 停了就不再排下一轮:这一轮的重读可能把停止时留下的旧计时器又写了回来。
  if (!jobStillHere(id)) { jobTimer = 0; return; }
  jobTimer = setTimeout(() => jobPollTick(id), 3000);
}
// jobSettle:任务刚结束。做完给一句提示;没做完、被打断就把原话(界面说法优先)显示在详情里;
// 然后现场查一次状态、重读详情 —— 体检报告、安装后的版本都跟着出来。
async function jobSettle(done) {
  done = await jobFresh(done);
  const id = done.node_id, t = JOB_TEXT[done.kind];
  jobRunning.delete(done.id);
  if (done.status === "done") toast(t.done(jobLabel(id), done.result?.core || done.core || "", done.result?.version || done.version || ""));
  else if (done.status !== "interrupted") toast((done.ui_hint || done.error) ? `${done.kind === "diagnose" ? "体检" : "安装"}没做成：${done.ui_hint || done.error}` : `${done.kind === "diagnose" ? "体检" : "安装"}没做成`, true);
  jobPollStop();
  await api(nodePath(id) + "?fresh=1").catch(() => null);
  if (route[0] === "nodes" && +route[1] === id) await load(true, true);
}

let NODES_TOTAL = null;

// autoApplyPending:体检页的「应用到<节点>」跳到详情后,在这里打开确认弹窗。只认一次:
// 找到可点的 node-sync 就触发它(头部主按钮或「⋯」菜单项都行),找不到(比如节点
// 连不上,按钮是灰的)就说明白并把人引到「刷新状态」;两种情况都立刻清掉标志。
// 标志带时间戳,超过 10 秒就当过期 —— 详情加载失败或用户中途离开都不会留到下次。
// 名字从这一份详情(d)里取:按钮上的 data-label 可能还是列表页记下的旧名字。
function autoApplyPending(id, d) {
  const a = window.AUTO_APPLY;
  if (!a || a.id !== id) return;
  window.AUTO_APPLY = null;
  if (Date.now() - a.at > 10000) return;
  const btn = document.querySelector('[data-act="node-sync"]:not([disabled]):not([aria-disabled=true])');
  const meta = btn ? { ...btn.dataset } : { id };
  if (d?.node && d.node.id === id) Object.assign(meta, { name: d.node.name, label: nodeLabel(d.node) });
  if (btn) ACTS["node-sync"]({ ...meta, from: "doctor" });
  else {
    toast("节点连不上，先点「刷新状态」，连上后再应用");
    document.querySelector('[data-act="node-check"]')?.focus();
  }
}

function nodeList(d) {
  const W = can("POST", "/api/nodes");
  const head = H`<tr><th>节点</th><th>状态</th><th class="hide-m">代理程序</th><th class="hide-m">配置</th><th class="hide-m">SSH</th><th class="num hide-m">检查于</th></tr>`;
  let inner;
  if (!d) inner = H`<table><thead>${head}</thead><tbody>${skRows(5, 6)}</tbody></table>`;
  else if (!d.nodes.length) inner = emptyBox("nodes", "还没有节点", "填 VPS 的地址和 SSH 密码，自动装好并加进订阅。点下面的「添加节点」开始。", "", W ? H`<div style="display:grid;gap:10px;justify-items:center"><button class="btn pri sm" data-act="node-wizard">${ic("plus")}添加节点</button><a class="lnk wlnk" data-act="node-new" href="#">已能免密 SSH，直接登记</a></div>` : "");
  else inner = H`<div class="tw"><table><thead>${head}</thead><tbody>${d.nodes.map(n => H`<tr data-href="#nodes/${n.id}" tabindex="0"><td><b class="bold">${nodeLabel(n)}</b><div class="sub mono">${n.address}</div></td><td>${nodeSt(n)}</td>
    <td class="hide-m">${n.status?.cores?.length ? n.status.cores.map(c => H`<span class="tag">${c.core}</span> `) : n.status?.version ? H`<span class="tag">xray</span> <span class="sub">${n.status.version}</span>` : ""}</td>
    <td class="hide-m">${driftCell(n)}</td><td class="hide-m mono sub">${sshText(n)}</td><td class="num sub hide-m">${n.status ? sinceText(n.status.checked_at) : ""}</td></tr>`)}</tbody></table></div>`;
  // 「批量识别显示名」只在有实现、且确实还有没显示名的节点时出现。
  const needRelabel = d && META.relabel && d.nodes.some(n => !n.display);
  return H`<div class="ph"><div><h1>节点</h1><p>状态来自控制面的定时检查</p></div><div class="sp"></div>
    ${needRelabel ? H`<button class="btn" data-act="relabel-open">${ic("globe")}批量识别显示名</button>` : ""}
    ${W ? H`<button class="btn pri" data-act="node-wizard">${ic("plus")}添加节点</button>` : ""}</div><div class="card">${inner}</div>`;
}
// 配置:节点上读回的哈希与控制面最后一次下发的比对(同 mpx node list 的漂移判定)。
function driftCell(n) {
  if (!n.deployed_hash) return H`<span class="sub">还没应用过</span>`;
  if (!n.status?.observed_hash) return H`<span class="sub">未知</span>`;
  // 新版本改了配置的写法,和详情页一样算「需要应用」。
  if (n.template_drift) return stBadge("warn", "需要应用");
  return n.status.observed_hash === n.deployed_hash ? stBadge("ok", "已是最新") : stBadge("warn", "有改动未应用");
}

// 体检结论是控制面写进报告的原话(存在库里,旧报告也照原样回来),里面有内部叫法和英文状态。
// 这里逐条翻成人话;原话收进「详情」,排查时还看得到。新加的结论句式要在 polish.spec 的
// SUMMARY_CORPUS 里补一条样例(TestSummaryCorpusCoversCheckup 会对照 checkup.go 检查)。
const SUM_TARGET = { netflix: "Netflix", youtube: "YouTube", chatgpt: "ChatGPT", openai: "ChatGPT", disney: "Disney+", "disney+": "Disney+" };
const SUM_STATUS = { unknown: "没测出结果", unavailable: "不能用", failed: "没通过", partial: "只通过了一部分", skipped: "跳过了", canceled: "被中断了", cancelled: "被中断了", timeout: "超时了" };
const SUM_NET = new Map([["DNS", "域名解析"], ["ICMP", "ping"], ["HTTP", "网页请求"], ["路由", "路由追踪"]]);
const sumStatus = st => SUM_STATUS[st] || "没通过";
function plainSummary(lines, d) {
  const conn = id => { const i = (d.inbounds || []).find(x => x.id === +id); return i ? `「${inbTitle(i, d.node)}」` : "一个连接方式"; };
  const rules = [
    [/^融合怪 \S+: (ok|success|done|completed|finished)$/i, () => ""],
    [/^融合怪 \S+: (canceled|cancelled)$/i, () => "完整测评被中断，没有跑完"],
    [/^融合怪 \S+: /, () => "完整测评没有跑完"],
    [/^融合怪测评未完成/, () => "完整测评没有跑完"],
    [/^融合怪缺少可用测速结果/, () => "完整测评没测出网速，另外补测了一次"],
    [/^独立速度补测已完成/, () => "补测网速已完成"],
    [/^独立速度补测未运行/, () => "时间用完了，没有补测网速"],
    [/^独立速度补测未完成/, () => "补测网速没有完成"],
    [/^入站 (\d+)\/入口 \d+ (\S+) 本次实测 (\S+):/, (m) => `通过${conn(m[1])}访问 ${SUM_TARGET[m[2].toLowerCase()] || m[2]}：${sumStatus(m[3])}`],
    [/^入站 (\d+)\/入口 \d+ 检测失败/, (m) => `${conn(m[1])}的连通检测没通过`],
    [/^入站 (\d+) 证书检查失败/, (m) => `${conn(m[1])}的证书检查没通过`],
    [/^入站 (\d+) 证书剩余 (-?\d+) 天/, (m) => +m[2] > 0 ? `${conn(m[1])}的证书还剩 ${m[2]} 天` : `${conn(m[1])}的证书已过期`],
    [/^节点代理服务未运行/, () => "节点上的代理程序没在运行"],
    [/^节点实测配置哈希与最后下发的配置不一致/, () => "节点上的配置和控制面不一致"],
    [/^节点没有已下发配置哈希/, () => "还没把配置应用到节点，没法判断配置是否一致"],
    [/^(节点配置哈希未读到|读取配置漂移基准失败)/, () => "没读到节点上的配置，没法判断配置是否一致"],
    [/^(xray|sing-box) 服务未运行/, (m) => `${m[1]} 没在运行`],
    [/^(xray|sing-box) 检查失败/, (m) => `${m[1]} 检查没通过`],
    [/^sing-box 配置漂移检查/, () => "sing-box 的配置没法确认是否一致"],
    [/^节点地址在体检期间变化/, () => "体检期间节点地址变了，结果可能不准"],
    [/^网络检测未完成/, () => "网络检测没有跑完"],
    [/^测评未运行/, () => "这次没有跑测评"],
    [/^体检中断/, () => "体检中途被打断，已保存做完的部分"],
    [/^网络检测工具不可用: (.+)$/, (m) => `节点上缺少检测工具「${m[1]}」`],
    [/^(\S+) (\S+) 测速 (\S+):/, (m) => `测网速（${m[1]}）${sumStatus(m[3])}`],
    [/^(IPv4|IPv6|ipv4|ipv6) IP 检测 (\S+):/, (m) => `${m[1].toUpperCase().replace("IPV", "IPv")} 地址检测${sumStatus(m[2])}`],
    [/^(.+) (DNS|ICMP|HTTP|路由) (\S+):/, (m) => `访问 ${m[1]} 的${SUM_NET.get(m[2])}${sumStatus(m[3])}`],
    [/^读取入站失败/, () => "读不到这台节点的连接方式"],
    [/^节点没有配置入站/, () => "节点还没有连接方式，没做连通检测"],
    [/^报告超过 256 KiB/, () => "报告太长，部分细节没有保存"],
  ];
  const out = [];
  for (const line of lines) {
    if (!line) continue;
    const r = rules.find(([re]) => re.test(line));
    const t = r ? r[1](line.match(r[0])) : "有一项检查没通过，原话见「详情」";
    if (t && !out.includes(t)) out.push(t);
  }
  // 「被中断」已经说明没跑完,不再重复一条「没有跑完」。
  return out.includes("完整测评被中断，没有跑完") ? out.filter(t => t !== "完整测评没有跑完") : out;
}

function nodeDetail(d) {
  if (!d) return H`<div class="ph"><div><h1><span class="sk" style="width:160px"></span></h1><p><span class="sk" style="width:220px"></span></p></div></div><div class="grid g3"><div class="card stat"><span class="sk w80"></span></div><div class="card stat"><span class="sk w80"></span></div><div class="card stat"><span class="sk w80"></span></div></div>`;
  const n = d.node, s = n.status, id = n.id, W = can("POST", "/api/nodes/x");
  const certs = s?.certificates || [];
  const rep = d.report && !d.report.error ? d.report : null;
  let summary = [];
  try { const j = rep ? (typeof rep.report === "string" ? JSON.parse(rep.report) : rep.report) : null; summary = (j && j.summary) || []; } catch { summary = []; }
  // 「应用到节点」做主按钮的情况:有未应用的连接改动、xray 配置和下发的不一致、或者从没应用过
  // (只有 sing-box 的节点不看 xray 的下发记录)。其余情况(比如改了出口 IP、只有 sing-box 漂移)
  // 控制面不记待应用,仍可以从「⋯」里手动应用,不会被藏起来。
  const sbOnly = (s?.cores || []).length > 0 && !(s.cores || []).some(c => c.core === "xray");
  // 新版本改了节点配置的写法:控制面只能指出这台节点的配置要对一次才生效,不算漂移。
  const drift = !!n.template_drift;
  const hashDrift = !!s?.observed_hash && !!n.deployed_hash && s.observed_hash !== n.deployed_hash;
  const neverApplied = !n.deployed_hash && !sbOnly;
  const needApply = INB_PENDING.has(id) || hashDrift || neverApplied || drift;
  // 只有「模板换了写法」这一条要找应用时,按它说,不混进「配置被改过」「还没应用过」。
  const applyWhy = INB_PENDING.has(id) || hashDrift || neverApplied ? "" : drift ? "新版本改了节点配置的写法，应用一次才生效" : "";
  // 标题与确认框里的节点名用显示名:详情拉回来的这一份(可能被改写)先记下,没有显示名时
  // 退到内部名(同 jobStarted)。页眉按钮上的 data-label 只是兜底。
  NODE_LABEL_BY_ID[id] = { id, name: n.name, display: n.display || "" };
  // 连不上时主按钮改成「刷新状态」—— 它是只读的 GET ?fresh=1,有读权限就能点;要应用也
  // 推不动,按 needApply 的位置换成灰按钮/灰菜单项(aria-disabled,仍可聚焦,读屏读得到原因)。
  // 只有主按钮真的换成「刷新状态」时,「⋯」里才去掉刷新项。
  const down = !!s && !s.reachable;
  // 刷新是 GET,不受写权限限制;主按钮真的换成「刷新状态」时,菜单里才不重复放一份。
  const refreshPrimary = down;
  const tip = "节点连不上，连上后再应用";
  // 正在跑的体检/安装:对应的按钮换成灰的(aria-disabled 仍可聚焦,读屏读得到原因),避免再起一个。
  const run = (d.jobs?.active || []).find(j => j.status === "running") || null;
  const runWhy = run ? (run.kind === "diagnose" ? "这台节点正在体检，等它做完再点" : "这台节点正在安装，等它做完再点") : "";
  const busyAttrs = (kind, whyID) => run && run.kind === kind ? ` aria-disabled="true" aria-describedby="${whyID}" data-tip="${runWhy}"` : "";
  const busyWhy = (kind, whyID) => run && run.kind === kind ? H`<span id="${whyID}" class="vh">${runWhy}</span>` : "";
  return H`<div class="ph"><div><h1 class="hrow">${nodeLabel(n)} ${nodeSt(n)}</h1><p class="mono">${n.address} · ${sshText(n)}${n.display ? H` · <span title="命令行与数据里用的名字">内部名 ${n.name}</span>` : ""}</p></div><div class="sp"></div>
    ${W && down && needApply ? H`<button type="button" class="btn" data-act="node-sync" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}" aria-disabled="true" aria-describedby="sync-why" data-tip="${tip}">应用到节点</button><span id="sync-why" class="vh">${tip}</span>` : ""}
    ${refreshPrimary ? H`<button class="btn pri" data-act="node-check" data-id="${id}">刷新状态</button>` : W && needApply ? H`<button class="btn pri" data-act="node-sync" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}"${applyWhy ? raw(` aria-describedby="sync-drift-why"`) : ""}>应用到节点</button>${applyWhy ? H`<span id="sync-drift-why" class="vh">${applyWhy}</span>` : ""}` : ""}
    <span class="mw"><button class="ib" title="更多" aria-label="更多" aria-haspopup="menu" data-act="node-menu">${ic("more")}</button><div class="menu" role="menu" id="node-menu" hidden>
      ${refreshPrimary ? "" : H`<button type="button" data-act="node-check" data-id="${id}">${ic("refresh")}刷新状态</button>`}
      ${W && !needApply ? (down ? H`<button type="button" data-act="node-sync" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}" aria-disabled="true" aria-describedby="sync-why-m" data-tip="${tip}">${ic("upload")}应用到节点</button><span id="sync-why-m" class="vh">${tip}</span>` : H`<button type="button" data-act="node-sync" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}">${ic("upload")}应用到节点</button>`) : ""}
      ${W ? H`<button type="button" data-act="node-drain" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}" data-on="${n.draining ? "1" : ""}">${ic("pause")}${n.draining ? "恢复分配" : "暂停分配"}</button>` : ""}
      ${can("PUT", "/api/nodes/x") ? H`<button type="button" data-act="node-edit" data-id="${id}">${ic("pen")}编辑</button>` : ""}
    </div></span></div>
  <div id="job-callout" data-on="${run ? "true" : "false"}">${d.jobs ? jobCallout("callout", d) : ""}</div>
  <div id="job-notice" data-on="${!run && d.jobs?.last ? "true" : "false"}">${d.jobs ? jobCallout("notice", d) : ""}</div>
  ${s?.error ? H`<div class="callout bad mb" role="alert">${ic("warn", "ico-bad")}<div><b>上次检查${s.reachable ? "有问题" : "连不上"}</b><div class="mono mt2">${s.error}</div></div></div>` : ""}
  ${n.draining ? H`<div class="callout mb">${ic("warn", "ico-warn")}<div><b>已暂停分配</b> · 新订阅不再发这台节点;已连上的用户会在下次刷新订阅时换到别的节点。</div></div>` : ""}
  <div class="grid g3">
    <div class="card stat"><div class="l">代理程序</div><div class="v f18">${s?.version || ""}</div><div class="s">${coreText(s)}</div></div>
    <div class="card stat"><div class="l">节点配置</div><div class="v f18" title="${s?.observed_hash || ""}">${driftCell(n)}</div><div class="s">${n.deployed_at ? H`应用于 ${ymd(new Date(n.deployed_at))} ${hm(new Date(n.deployed_at))}` : ""}</div></div>
    <div class="card stat"><div class="l">证书</div><div class="v f18">${certs.length ? certs.map(certText).join(" · ") : ""}</div><div class="s">${certs.map(c => c.inbound_name).join(" · ") || "没有用到 TLS 的连接方式"}</div></div>
  </div>
  <div class="card mt conn-card"><div class="ch">连接方式<small>· ${d.inbounds.length}</small><span class="sp"></span>${CONN.pending(n, { tagOnly: true })}${can("POST", "/api/inbounds") ? H`<button class="btn sm" data-act="conn-new" data-node="${id}">${ic("plus")}添加连接</button>` : ""}</div>
    ${CONN.groups(d.conn, { node: id })}</div>
  <div class="card mt"><div class="ch">维护</div>
    <div class="row"><div><b class="bold">体检</b><div class="sub">${rep ? H`上次 ${ymd(new Date(rep.checked_at))} ${hm(new Date(rep.checked_at))}${rep.measured_address ? H` · 测量地址 ${rep.measured_address}` : ""}` : d.report?.error ? H`<span class="c-bad">${d.report.error}</span>` : "还没有体检报告"}</div>
      ${summary.length ? H`<ul class="summary">${plainSummary(summary, d).map(x => H`<li>${x}</li>`)}</ul><details class="sum-raw"><summary>详情${ic("down")}</summary><ul class="summary mono">${summary.filter(Boolean).map(x => H`<li>${x}</li>`)}</ul></details>` : ""}</div><span class="sp"></span>${W ? H`<button class="btn sm" data-act="node-diagnose" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}"${raw(busyAttrs("diagnose", "diag-why"))}>开始体检</button>${busyWhy("diagnose", "diag-why")}` : ""}</div>
    ${W ? H`<div class="row"><div><b class="bold">安装代理程序</b><div class="sub">把代理程序装到这台节点上并启动，会用当前配置</div></div><span class="sp"></span><button class="btn sm" data-act="node-install" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}"${raw(busyAttrs("install", "inst-why"))}>安装</button>${busyWhy("install", "inst-why")}</div>` : ""}
    <div class="row"><div><b class="bold">在线用户</b><div class="sub">实时查看每个用户连着几个 IP</div></div><span class="sp"></span><button class="btn sm" data-act="node-online" data-id="${id}" data-label="${nodeLabel(n)}">查看</button></div>
    <div class="row"><div><b class="bold">配置预览</b><div class="sub">「应用到节点」会推的 config.json，不碰节点</div></div><span class="sp"></span><button class="btn sm" data-act="node-config" data-id="${id}" data-label="${nodeLabel(n)}">查看</button></div>
  </div>
  ${can("DELETE", "/api/nodes/x") ? H`<div class="card mt node-danger"><div class="ch">危险操作</div><div class="row"><div><b class="bold">删除节点</b><div class="sub">它上面的连接方式会一起删掉，订阅里不再有它。节点上已装的代理程序和配置文件会留着。</div></div><span class="sp"></span><button class="btn sm" data-act="node-rm" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}">删除节点</button></div></div>` : ""}`;
}

/* ---- 编辑器:add / edit ---- */
async function nodeEditor(id) {
  let v = null;
  let meta = null;
  if (id) { try { [v, meta] = await Promise.all([api(nodePath(id)), api(nodePath(id) + "/metadata")]); } catch (e) { toast(e.message, true); return; } }
  const isNew = !v, n = v || { name: "", address: "", ssh_port: 0, ssh_user: "", api_port: 0, config_path: "", service_name: "", outbound_ip: "" };
  const num = x => x ? String(x) : "";
  openDrawer(H`<form id="node-form" novalidate><div class="dh"><h2>${isNew ? "添加节点" : "编辑 " + nodeLabel(v)}</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    <div class="field"><label for="n-address">对外地址</label><input class="in mono" id="n-address" name="address" value="${n.address}" placeholder="203.0.113.10"><span class="h">连接方式没填对外地址时，订阅里用它${isNew ? ";显示名按它自动识别" : ""}</span></div>
    <div class="two"><div class="field"><label for="n-ssh-port">SSH 端口</label><input class="in mono" id="n-ssh-port" name="ssh_port" value="${num(n.ssh_port)}" placeholder="22" inputmode="numeric"></div>
    <div class="field"><label for="n-ssh-user">SSH 用户</label><input class="in mono" id="n-ssh-user" name="ssh_user" value="${n.ssh_user}" placeholder="root"></div></div>
    ${isNew ? "" : H`<div class="field"><label>显示名<span class="sub" id="n-display"></span></label><div class="three">
      <input class="in mono" id="n-region" name="region" value="${meta.region || ""}" placeholder="地区 US" aria-label="地区" autocomplete="off">
      <input class="in" id="n-city" name="city" value="${meta.city || ""}" placeholder="城市，留空用国家名" aria-label="城市" autocomplete="off">
      <input class="in mono" id="n-letter" name="letter" value="${meta.letter || ""}" placeholder="字母，留空自动" aria-label="字母" autocomplete="off"></div>
      <div class="flex8 mt"><button type="button" class="btn sm" id="n-locate">重新识别</button><span class="h" id="n-locate-msg">按节点 IP 问 ipwho.is 给建议，只填进表单，保存才写入;节点 IP 会发给这个第三方。</span></div></div>`}
    <details class="adv"><summary>高级</summary>
    <div class="field"><label for="n-name">内部名</label><input class="in mono" id="n-name" name="name" value="${n.name}" placeholder="${isNew ? "留空自动生成" : ""}" autocomplete="off"><span class="h">只在命令行和数据里用;界面和订阅里显示的是显示名</span></div>
    <div class="two"><div class="field"><label for="n-api-port">xray API 端口</label><input class="in mono" id="n-api-port" name="api_port" value="${num(n.api_port)}" placeholder="默认" inputmode="numeric"></div>
    <div class="field"><label for="n-service">服务名</label><input class="in mono" id="n-service" name="service_name" value="${n.service_name}" placeholder="mpx-xray"></div></div>
    <div class="field"><label for="n-config">配置路径</label><input class="in mono" id="n-config" name="config_path" value="${n.config_path}" placeholder="/etc/mpx/xray/config.json"></div>
    <div class="field"><label for="n-outbound-ip">出口 IP</label><select class="in" id="n-outbound-ip" name="outbound_ip">
      <option value="" ${!n.outbound_ip || n.outbound_ip === "v4v6" ? "selected" : ""}>默认(先 IPv4)</option>
      <option value="v4" ${n.outbound_ip === "v4" ? "selected" : ""}>仅 IPv4</option>
      <option value="v6" ${n.outbound_ip === "v6" ? "selected" : ""}>仅 IPv6</option>
      <option value="asis" ${n.outbound_ip === "asis" ? "selected" : ""}>不指定(AsIs)</option>
    </select><span class="h">节点直连出口的 IP 版本;改完要「应用到节点」才生效</span></div></details>
    ${isNew ? H`<p class="sub">这里只登记已能免密 SSH 的机器。要用 SSH 密码添加，请用「添加节点」向导。</p>` : H`<p class="sub">改地址会影响订阅里没单独填对外地址的连接方式;改 SSH 参数只影响控制面以后怎么连它。都不会自动应用到节点。</p>`}
    <div class="err" id="n-err" role="alert"></div>
  </div><div class="df"><button type="button" class="btn" data-act="close">取消</button><button type="submit" class="btn pri" id="n-save">${isNew ? "创建" : "保存"}</button></div></form>`);
  const form = $("#node-form");
  const FIELDS = [["name", "名称"], ["address", "对外地址"], ["ssh_port", "SSH 端口"], ["ssh_user", "SSH 用户"], ["api_port", "API 端口"], ["config_path", "配置路径"], ["service_name", "服务名"], ["outbound_ip", "出口 IP"]];
  const INTS = new Set(["ssh_port", "api_port"]);
  const collect = () => {
    const f = new FormData(form), out = {}, err = [], changed = {};
    for (const [k, label] of FIELDS) {
      const s = String(f.get(k) || "").trim();
      if (INTS.has(k)) { if (s && !/^\d+$/.test(s)) err.push(`${label} 要是整数`); out[k] = s ? +s : 0; } else out[k] = s;
      changed[k] = isNew ? out[k] !== "" && out[k] !== 0 : out[k] !== (n[k] || (INTS.has(k) ? 0 : ""));
    }
    if (!isNew) {
      const region = String(f.get("region") || "").trim().toUpperCase(), city = String(f.get("city") || "").trim(), letter = String(f.get("letter") || "").trim().toUpperCase();
      if (letter && !/^[A-Z]{1,2}$/.test(letter)) err.push("字母要是 1–2 位");
      out.meta = { region, city, letter };
      out.metaChanged = region !== (meta.region || "") || city !== (meta.city || "") || letter !== (meta.letter || "");
      out.nodeChanged = FIELDS.some(([k]) => changed[k] && !(isNew && k === "name"));
    }
    if (!isNew && !out.name) err.push("内部名不能为空");
    if (isNew && !out.address) err.push("必须给对外地址");
    return { out, err };
  };
  const sync = () => {
    const c = collect();
    if (!isNew) {
      // 字母留空、或换了地区/城市而没指定字母时,由控制面在保存时分配,这里先不猜。
      const m = { ...c.out.meta }, group = x => (x.region || "").toUpperCase() + "\0" + (x.city || "");
      if (m.letter === (meta.letter || "") && group(m) !== group(meta)) m.letter = "";
      $("#n-display").textContent = m.region ? " · " + previewDisplay(m) + (m.letter ? "" : "(字母保存时自动分配)") : "";
    }
  };
  if (!isNew) $("#n-locate").onclick = () => busy($("#n-locate"), async () => {
    const sg = await api(nodePath(id) + "/locate", { method: "POST" }); // 只是建议,不写入,不刷新页面
    $("#n-region").value = sg.region; $("#n-city").value = sg.city || "";
    $("#n-locate-msg").textContent = `建议：${sg.display}(${sg.source}，依据${sg.via === "node" ? "节点出口" : "节点"} IP ${sg.ip})${!sg.city && sg.city_en ? `;${sg.city_en} 不在对照表里，可手填城市` : ""}`;
    sync();
  });
  form.oninput = form.onchange = sync; sync();
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const c = collect(), btn = $("#n-save");
    $("#n-err").textContent = c.err.join("\n");
    if (c.err.length) return;
    btn.disabled = true; btn.classList.add("busy");
    try {
      if (isNew) {
        // 与 node add 一致:model.Node 整体序列化,没有 ssh_password;outbound_ip 空串不带键。
        if (!c.out.name) c.out.name = await autoNodeNameFor();
        const created = await write("POST", "/api/nodes", dropEmptyOutbound({ id: 0, ...c.out, draining: false }));
        toast(`已添加节点 ${created.display || created.address}`);
        closeOverlays(); location.hash = "nodes/" + created.id;
      } else {
        // 与 node edit 一致:只改显示名时只发 metadata;读回整份再改 region/city/letter,
        // 换了地区或城市且没指定字母时发空字母,让控制面在新分组里重新取。
        const { meta: m, metaChanged, nodeChanged, ...nodeOut } = c.out;
        if (metaChanged) {
          const fresh = await api(nodePath(id) + "/metadata");
          const group = x => (x.region || "").toUpperCase() + "\0" + (x.city || "");
          const body = { ...fresh, city: fresh.city || "", letter: fresh.letter || "" };
          if (m.region !== (meta.region || "")) body.region = m.region;
          if (m.city !== (meta.city || "")) body.city = m.city;
          if (m.letter !== (meta.letter || "")) body.letter = m.letter;
          else if (group(body) !== group(fresh)) body.letter = "";
          const saved = await write("PUT", nodePath(id) + "/metadata", body);
          if (!nodeChanged) { toast(`显示名已改为 ${previewDisplay(saved) || "(没有地区，不显示)"}`); closeOverlays(); load(true, true); return; }
        }
        const fresh = asModelNode(await api(nodePath(id)));
        const updated = await write("PUT", nodePath(id), dropEmptyOutbound({ ...fresh, ...nodeOut }));
        toast(`已更新节点 ${nodeLabel(v)}`);
        closeOverlays(); load(true, true);
      }
    } catch (e2) { $("#n-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

/* ---- 批量识别显示名:plan 先出对照表,确认后 apply 只写这份 ---- */
// 页面把 plan 返回的 plans 原样留着;apply 时原样回传,不重新识别、不重新拼名字。
function relabelModal() {
  const m = $("#modal");
  let plans = null;
  modalTake(); m.innerHTML = val(H`<div class="mbody"><h2>批量识别显示名</h2>
  <p>对还没有显示名的节点各做一次识别(控制面按节点 IP 问 ipwho.is,<b>节点 IP 会发给这个第三方</b>)，先出旧名和新名的对照表。已经有显示名的节点会跳过，用户写过的值不会被覆盖。</p>
  <div id="rl-table"></div><div class="err" id="rl-err" role="alert"></div></div>
  <div class="mf"><button class="btn" data-act="modal-cancel">取消</button>
  <button class="btn" id="rl-plan">${ic("globe")}生成对照表</button>
  <button class="btn pri" id="rl-apply" style="display:none"></button></div>`);
  $("#scrim").classList.add("open"); m.classList.add("open");
  $("#rl-plan").addEventListener("click", () => busy($("#rl-plan"), async () => {
    const r = await api("/local/relabel/plan", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ all: true }) });
    plans = r.plans || [];
    const rows = r.rows || [];
    $("#rl-table").innerHTML = val(rows.length
      ? H`<div class="field"><label>对照表</label><div class="tw"><table><thead><tr><th class="num">ID</th><th>内部名</th><th>旧名</th><th>新名</th><th>依据</th></tr></thead><tbody>${rows.map(x => H`<tr><td class="num mono">${x.id}</td><td class="mono">${x.name}</td><td>${x.old || "—"}</td><td><b class="bold">${x.new}</b></td><td class="sub">${x.note}</td></tr>`)}</tbody></table></div></div>`
      : H`<p class="sub">没有要处理的节点。</p>`);
    const btn = $("#rl-apply");
    btn.style.display = plans.length ? "" : "none";
    btn.textContent = `写入这 ${plans.length} 个节点`;
  }));
  $("#rl-apply").addEventListener("click", () => busy($("#rl-apply"), async () => {
    // 原样回传 plan 的结果:确认看到什么就写什么。
    const r = await api("/local/relabel/apply", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ plans }) });
    const res = r.results || [];
    const skipped = res.filter(x => x.skipped).length;
    const ok = res.length - skipped;
    toast(`已写入 ${ok} 个节点${skipped ? `,${skipped} 个已被改过、跳过` : ""}`);
    closeOverlays(); load(true, true);
  }));
}

/* ---- 结果抽屉:在线、配置预览 ---- */
function infoDrawer(title, sub, body) {
  openDrawer(H`<div class="dh"><div style="min-width:0"><h2 class="ell">${title}</h2><div class="sub">${sub}</div></div><span class="sp" style="flex:1"></span><button class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div><div class="db">${body}</div>`);
}
async function busy(btn, fn) {
  btn.disabled = true; btn.classList.add("busy");
  try { await fn(); } catch (e) { toast(e.message, true); } finally { btn.disabled = false; btn.classList.remove("busy"); }
}
// jobStarted:后台任务接下了(202 新的、200 这台已经在跑)。确认框立刻关掉:接下来要等几分钟
// 到十几分钟,不该把人留在弹窗里。提示一句已经在跑,再用刚从任务带回的那一份重画详情
// (「正在…」的提示和变灰的按钮都由它决定)。job 随确认框进来,名字也就带得动。
function jobStarted(d, kind, res, core) {
  // 控制面没给任务(既不是收下的 202、也不是复用的 200):这不算开始,照着原来的做法
  // 把原话留在确认框里,别关框、别提示「已开始」。
  if (!res?.job) throw new Error("控制面没有说明这次" + (kind === "diagnose" ? "体检" : "安装") + "有没有开始");
  const id = +d.id, label = d.label || d.name, j = res.job;
  NODE_LABEL_BY_ID[id] = NODE_LABEL_BY_ID[id] || { id, name: d.name, display: d.label };
  // 刚起过一次:这次进节点以来的「上次没做完」不再问,免得被自己刚起的任务盖住。
  jobAsked.add(id); jobAsk[id] = true;
  // 先把这次接下的任务摆进当前详情(data 还是同一份,只是任务换了),重画一遍让人马上看见。
  // 这一步在关确认框之前:画出来时框还开着,不会顺手去问一次「最近一次做完没有」。
  if (route[0] === "nodes" && +route[1] === id) {
    const run = [j, ...(current.data?.jobs?.active || []).filter(x => x.id !== j.id)];
    current = { ...current, data: { ...(current.data || {}), jobs: { active: run, last: null } }, loading: false, at: new Date() };
    paint();
  }
  closeModal();
  toast(res.reused ? JOB_TEXT[kind].reused : JOB_TEXT[kind].started(label, core));
  load(true, true);
}

Object.assign(ACTS, {
  "node-menu": () => { const m = $("#node-menu"); if (m) m.hidden = !m.hidden; },
  "node-new": () => nodeEditor(null),
  "node-wizard": () => openNodeWizard(),
  "relabel-open": () => relabelModal(),
  "node-edit": d => nodeEditor(+d.id),
  // 现场检查 = mpx node status <id>:GET ?fresh=1,结果由控制面落库。
  "node-check": (d, b) => busy(b, async () => { await api(nodePath(d.id) + "?fresh=1"); toast("状态已刷新"); await load(true, true); }),
  // 标题与「已应用到…」用显示名:按钮上的 data-label 可能还是列表页记下的旧名字。
  "node-sync": d => { const label = nodeLabel(NODE_LABEL_BY_ID[+d.id]) || d.label || d.name; return confirmBox({ title: `把配置应用到 ${label}？`, text: "把当前配置推到节点并重启代理程序：这台节点上所有人的连接会断一次。加人、删人、到期、超限不用手动应用，控制面每 30 秒自动跟上。" + (d.from === "doctor" ? "这次是从体检过来的：节点上的配置和控制面不一致，可能是你自己改的，也可能是别人改的。" : ""), ok: "应用到节点并重启",
    run: async () => { await write("POST", nodePath(d.id) + "/sync", undefined, `已应用到 ${label}，代理程序已重启`); await api(nodePath(d.id) + "?fresh=1").catch(() => null); // 应用完马上现场查一次:不然卡片要等下一轮检查才变回「已是最新」
      await load(true, true); } }); },
  "node-drain": (d, b) => {
    if (d.on) return busy(b, async () => { await write("POST", nodePath(d.id) + "/undrain", undefined, `${d.label || d.name} 已恢复分配`); await load(true, true); });
    confirmBox({ title: `暂停给 ${d.label || d.name} 分配？`, text: "新订阅不再包含这台节点;已连上的用户不会立刻断线，会在下次刷新订阅时换到别的节点。随时可以恢复分配。", ok: "暂停分配",
      run: async () => { await write("POST", nodePath(d.id) + "/drain", undefined, `${d.label || d.name} 已暂停分配`); await load(true, true); } });
  },
  // 安装 = mpx node install <id> --core <core> [--version <v>],走后台上任务的接口:回来就
  // 关掉确认框,进度和结果在节点详情里看(做完会刷新详情)。
  "node-install": d => confirmBox({ title: `在 ${d.label || d.name} 上安装代理程序？`, text: "控制面下载发布包（下过的会复用），送到节点上，应用当前配置并启动。慢链路上要几分钟。可以关掉这个页面，做完会在节点上显示结果。", ok: "安装",
    extra: H`<div class="two"><div class="field"><label for="i-core">代理程序</label><select class="in" id="i-core"><option value="xray">xray</option><option value="sing-box">sing-box（hysteria2 / tuic）</option></select></div>
      <div class="field"><label for="i-ver">版本</label><input class="in mono" id="i-ver" placeholder="留空用最新版" autocomplete="off"></div></div>`,
    run: async () => {
      const core = $("#i-core").value, v = $("#i-ver").value.trim();
      let path = nodePath(d.id) + "/install/jobs?core=" + encodeURIComponent(core);
      if (v) path += "&version=" + encodeURIComponent(v);
      const res = await write("POST", path);
      jobStarted(d, "install", res, core);
    } }),
  // 体检 = mpx node diagnose <id> 的默认参数:Fusion、测速开、硬件测试关。同样走后台上任务。
  "node-diagnose": d => confirmBox({ title: `体检 ${d.label || d.name}？`, text: "在节点上做一次完整体检：运行状况、能访问哪些网站、网速（不测硬件）。最长要十几分钟。可以关掉这个页面，做完会在节点上显示结果。", ok: "开始体检",
    run: async () => {
      const res = await write("POST", nodePath(d.id) + "/diagnostics/jobs", { engine: "fusion", position: "node", node_id: String(d.id), speed: true });
      jobStarted(d, "diagnose", res);
    } }),
  "node-online": (d, b) => busy(b, async () => {
    let r;
    // 读不到就明说读不到,不能落成「没有在线用户」。409 = 节点配置没开在线统计,应用一次配置就好。
    // 其他错误原话是英文的 SSH 报错,还带内部名:主句只说显示名和怎么办,原话收进「详情」。
    try { r = await api(nodePath(d.id) + "/online"); } catch (e) {
      if (!e.status || e.status < 400) throw e;
      if (e.status !== 409) return infoDrawer(`${d.label || d.name} 在线`, H`现场读出的结果`, H`<div class="empty" role="alert"><div class="ill bad">${ic("warn")}</div><h4>读不到在线名单</h4><p>连不上${d.label || "这台节点"}，这次读不到在线名单。稍后再试，或者先「刷新状态」看看节点是不是在线。</p><details class="sum-raw"><summary>详情${ic("down")}</summary><div class="mono">${e.message}</div></details></div>`);
      const fix = can("POST", "/api/nodes/x/sync") ? H`<button type="button" class="btn sm mt" data-act="node-sync" data-id="${d.id}" data-label="${d.label || ""}" data-name="${d.name || ""}">把配置应用到节点</button>` : "";
      return infoDrawer(`${d.label || d.name} 在线`, H`现场读出的结果`, H`<div class="empty" role="alert"><div class="ill bad">${ic("warn")}</div><h4>读不到在线名单</h4><p>${e.message}</p>${fix}</div>`);
    }
    infoDrawer(`${d.label || r.node} 在线`, H`现场读出的结果`, r.online.length
      ? H`<div class="tw"><table><thead><tr><th>用户</th><th class="num">IP 数</th><th>IP</th></tr></thead><tbody>${r.online.map(e => H`<tr><td><b class="bold">${e.user}</b></td><td class="num">${e.count}</td><td class="mono sub">${(e.ips || []).join(", ")}</td></tr>`)}</tbody></table></div>`
      : emptyBox("users", "没有在线用户", "此刻没有用户连在这台节点上。", ""));
  }),
  "node-config": (d, b) => busy(b, async () => {
    const cfg = await api(nodePath(d.id) + "/config");
    infoDrawer(`${d.label || d.name || "节点"} 的配置预览`, H`「应用到节点」会推这一份`, H`<pre class="code">${typeof cfg === "string" ? cfg : JSON.stringify(cfg, null, 2)}</pre>`);
  }),
  "node-rm": d => confirmBox({ title: `删除节点 ${d.label || d.name}？`, text: H`不可恢复：它上面的连接方式会一起删掉，订阅里不再有它。节点上的代理程序和配置文件会留着；只想让新订阅<span class="nw">别再发它</span>，用<span class="nw">「暂停分配」</span>。`, ok: "删除节点", danger: true, typed: d.name,
    run: async () => { await write("DELETE", nodePath(d.id), undefined, `已删除节点 ${d.label || d.name}`); location.hash = "nodes"; } }),
});

// autoNodeNameFor:直接登记没填内部名时,取 node-1、node-2… 里第一个没被占的。
async function autoNodeNameFor() {
  let names = new Set();
  try { names = new Set((await api("/api/nodes")).map(n => n.name)); } catch { /* 撞名由控制面报 */ }
  for (let i = 1; ; i++) if (!names.has("node-" + i)) return "node-" + i;
}

// 点菜单外或菜单项后收起。
document.addEventListener("click", e => { const m = $("#node-menu"); if (m && !m.hidden && (!e.target.closest(".mw") || e.target.closest("#node-menu button"))) m.hidden = true; });
