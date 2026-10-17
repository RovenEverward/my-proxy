// 体检页:一次 mpx doctor 的结论,走的就是 CLI 那条 GET /api/doctor(uiAllow 的
// "GET /api" 已经放行,不需要 /local 接口)。
//
// 进页面不自动跑:体检会逐台节点现场查一次(服务在不在跑、配置有没有被改过、
// 证书还剩几天),节点多时要几十秒到几分钟,不该在每次切页/轮询时白跑。点了
// 「开始体检」才发请求。结论按 model.DoctorCheck 原样渲染,判定一律以控制面为准。
"use strict";

// 与 CLI 的 doctorTimeout(internal/cli/client.go)同一量级:体检要逐台节点现场查,
// 每次最多 checkBudget,上限给宽到十分钟。
const DOCTOR_TIMEOUT_MS = 10 * 60 * 1000;
// 三种结论的显示:颜色沿用状态点的 ok / warn / bad(绿 / 黄 / 红)。
const DOC_ST = { ok: ["ok", "正常"], warn: ["warn", "警告"], fail: ["warn", "失败"] };

// DOC 是最近一次体检报告;null 表示还没跑过。放模块级而不是 DOM 里:页面每 30 秒
// 会自动重绘一次,结论不能因此丢掉。
let DOC = null, DOC_ERR = "", DOC_RUNNING = false;

PAGES.doctor = {
  title: "体检",
  // 这里故意不调 /api/doctor:体检有副作用(现场连每台节点),要人点了才跑。
  async load() { return {}; },
  render() { return doctorPage(); },
};

const docStat = (checks, k, label) => H`<div class="card stat"><div class="l"><span class="dot ${DOC_ST[k][0]}"></span>${label}</div>
  <div class="v" data-sum="${k}">${checks.filter(c => c.status === k).length}</div>
  <div class="s">${k === "ok" ? "没有毛病" : k === "warn" ? "该看一眼,不算坏" : "要看,系统确实有毛病"}</div></div>`;

function doctorPage() {
  const head = H`<div class="ph"><div><h1>体检</h1><p>控制面、每台节点、六种订阅格式、采样表</p></div><div class="sp"></div>
    ${DOC_RUNNING ? H`<button class="btn pri busy" disabled>开始体检</button>` : H`<button class="btn pri" data-act="doc-run">${ic("check")}开始体检</button>`}</div>`;
  if (DOC_RUNNING) return H`${head}<div class="card pad16"><div class="callout stone">${ic("refresh")}<div>正在体检:会逐台连上节点现场查一次,可能要几十秒。期间别关这个页面。</div></div></div>`;
  if (DOC_ERR) return H`${head}<div class="card pad16"><div class="callout bad" role="alert">${ic("warn", "ico-bad")}<div><b>体检没有跑成</b><div class="mono mt2" data-doc-err>${DOC_ERR}</div></div></div>
    <p class="sub mt">控制面原话在上面。处理完再点一次「开始体检」。</p></div>`;
  if (!DOC) return H`${head}<div class="card pad16">
    <b class="b6">一次体检,只读不改</b>
    <p class="sub mt6 mb14">检查控制面的库能不能读、后台任务还在不在跳;每一台节点现场问一次(服务在不在跑、配置有没有漂移、证书还剩几天);六种订阅格式各渲染一次;采样表有多少行。体检只报告,不动任何东西 —— 发现的问题要不要处置由你决定。</p>
    <p class="sub mb8">它会对每台节点 SSH 一次,<b class="c-ink">要几十秒到几分钟</b>,所以进页面不自动跑。点右上角「开始体检」即可。</p></div>`;
  const checks = DOC.checks || [];
  const rows = checks.map(c => {
    const [cls, text] = DOC_ST[c.status] || ["", c.status];
    // 控制面按内部名给检查项起名(「节点 la-1」);界面上换成显示名,data-check 保留原名。
    const nm = /^节点 (.+)$/.exec(c.name)?.[1], lb = nm ? labelOfName(nm) : "";
    // 控制面给的提示里还是旧按钮名;界面上已改叫「应用到节点」「暂停分配」(控制面接口不改)。
    const hint = (c.ui_hint || c.hint || "").replace(/下发配置/g, "应用到节点").replace(/排水/g, "暂停分配");
    const shown = nm && lb !== nm ? hint.split(`打开 ${nm}`).join(`打开 ${lb}`) : hint;
    return H`<tr data-check="${c.name}"><td><b class="bold">${nm ? "节点 " + lb : c.name}</b></td><td>${stBadge(cls, text)}</td>
      <td class="doc-d"><div>${c.detail}</div>${shown ? H`<div class="sub">建议:${shown}</div>` : ""}</td></tr>`;
  });
  return H`${head}
    <div class="grid g3">${docStat(checks, "ok", "正常")}${docStat(checks, "warn", "警告")}${docStat(checks, "fail", "失败")}</div>
    <div class="card mt"><div class="ch">检查项<small>· 共 ${checks.length} 项</small></div>
      ${checks.length ? H`<div class="tw"><table><thead><tr><th>名称</th><th>结论</th><th>说明</th></tr></thead><tbody>${rows}</tbody></table></div>`
        : H`<div class="row sub">控制面没有给出任何检查项。</div>`}</div>`;
}

Object.assign(ACTS, {
  "doc-run": () => runDoctor(),
});

// runDoctor:发一次请求。先用模块状态摆出「正在体检」再 paint,避免等响应期间
// 按钮还能再点一次(体检不是幂等的便宜操作)。结束不管成没成都重绘整页。
async function runDoctor() {
  DOC_RUNNING = true; DOC_ERR = "";
  paint();
  try {
    DOC = await api("/api/doctor", { signal: AbortSignal.timeout(DOCTOR_TIMEOUT_MS) });
    // 检查项里的节点用显示名展示;还没拉过节点列表时补拉一次(失败就照原名显示)。
    if (!Object.keys(NODE_LABELS).length && can("GET", "/api/nodes")) { try { await api("/api/nodes"); } catch { /* 原名兜底 */ } }
  } catch (e) {
    DOC = null; DOC_ERR = e.message;
  } finally {
    DOC_RUNNING = false;
    paint();
  }
}
