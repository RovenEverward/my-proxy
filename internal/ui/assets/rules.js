// 分流规则页:规则表(全局 / 某个用户)、策略组、测试网址、迁移与模式切换。
// 每个写操作对应一条 mpx rule / mpx group 命令,发出的请求与 CLI 一致(见 e2e/rules.spec.mjs)。
"use strict";

const RULE_TYPES = { suffix: "域名后缀", keyword: "域名关键词", set: "规则组", geoip: "IP 所在国家", cidr: "IP 段" };
const RULE_CAPS = { egress: "出口可用", youtube: "能看 YouTube", netflix: "能看 Netflix" };
const RULE_REGIONS = { CN: "🇨🇳 中国", US: "🇺🇸 美国", JP: "🇯🇵 日本", HK: "🇭🇰 香港", SG: "🇸🇬 新加坡", TW: "🇹🇼 台湾", KR: "🇰🇷 韩国", GB: "🇬🇧 英国", DE: "🇩🇪 德国" };
const RP = { tab: "rules", scope: "", edit: null, test: "", testRes: null, testSeq: 0 };
const rulesPath = u => "/api/rules" + (u ? "?user=" + encodeURIComponent(u) : "");
const finalPath = u => "/api/rules/final" + (u ? "?user=" + encodeURIComponent(u) : "");
const groupPath = id => "/api/proxy-groups/" + id;
const groupID = t => /^group:(\d+)$/.test(t || "") ? +t.slice(6) : 0;

PAGES.rules = {
  title: "分流规则", nav: ["配置", "tpl", 39], closeTo: "rules",
  crumb: () => H`<b>分流规则</b>`,
  async load(r) {
    if (r[1] === "groups" || r[1] === "rules") RP.tab = r[1];
    const [routing, rules, groups, catalog, users] = await Promise.all([
      api("/api/routing"), api(rulesPath(RP.scope)), api("/api/proxy-groups"), api("/api/rule-catalog"), api("/api/users?limit=1000"),
    ]);
    let members = {};
    if (RP.tab === "groups" && groups.length) {
      const pv = await Promise.all(groups.map(g => api("/api/proxy-groups/preview", { method: "POST", body: JSON.stringify(g), headers: { "Content-Type": "application/json" } }).catch(() => null)));
      groups.forEach((g, i) => { members[g.id] = pv[i] ? pv[i].filter(e => e.admitted).map(e => e.name) : null; });
    }
    return { routing, rules, groups, catalog, members, users: (users.users || []).map(u => u.username) };
  },
  render(d) {
    const head = H`<div class="ph"><div><h1>分流规则</h1><p>决定每个网站怎么走:直连、走代理还是拦截。从上往下比对,第一条对上的生效 · 改完客户端更新订阅即可 · 对应 <code>mpx rule</code> / <code>mpx group</code></p></div></div>`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(6, 4)}</tbody></table></div>`;
    RP.d = d;
    const R = d.rules, G = d.groups;
    if (!R.global.length && !G.length && !RP.scope) return H`${head}${modeBanner(d)}${rpEmpty(d)}`;
    const tabs = H`<div class="rp-bar"><div class="seg">${[["rules", "规则", R.global.length + 1], ["groups", "策略组", G.length]].map(([k, t, n]) => H`<button class="${RP.tab === k ? "on" : ""}" data-act="rp-tab" data-k="${k}">${t}<em>${n}</em></button>`)}</div><span class="sp"></span>
      ${RP.tab === "rules" ? H`<span class="sub">作用于</span><select class="in w200" id="rp-scope"><option value="">全部用户(全局)</option>${d.users.map(u => H`<option value="${u}" ${raw(RP.scope === u ? "selected" : "")}>只改 ${u}</option>`)}</select>` : ""}</div>`;
    return H`${head}${modeBanner(d)}${tabs}<div class="rp-main"><div>${RP.tab === "rules" ? rpRulesCard(d) : rpGroupsCard(d)}</div>
      <div class="rp-side">${rpTestCard()}<div class="card mt pad16 sub">等价命令:<code>${RP.tab === "rules" ? `mpx rule list${RP.scope ? " --user " + shq(RP.scope) : ""}` : "mpx group list"}</code> · <code>${RP.tab === "rules" ? "mpx rule test <域名>" : "mpx group preview <名字>"}</code></div></div></div>`;
  },
  after(d) {
    if (!d) return;
    $("#rp-scope")?.addEventListener("change", e => { RP.scope = e.target.value; RP.edit = null; RP.testRes = null; load(true, true); if (RP.test) rpRunTest(); });
    const t = $("#rp-test");
    t?.addEventListener("input", () => { RP.test = t.value; clearTimeout(RP.tt); RP.tt = setTimeout(rpRunTest, 350); });
    $("#rp-final")?.addEventListener("change", e => rpSetFinal(e.target.value));
    $("#re-type")?.addEventListener("change", e => { RP.edit.type = e.target.value; RP.edit.value = e.target.value === "set" ? (d.catalog[0]?.key || "") : e.target.value === "geoip" ? "CN" : ""; RP.edit.target = $("#re-to").value; paint(); });
    if (rpCanWrite()) document.querySelectorAll(".rp-list .rp-row[draggable]").forEach(el => {
      el.addEventListener("dragstart", e => { e.dataTransfer.setData("text/plain", el.dataset.i); el.classList.add("drag"); });
      el.addEventListener("dragend", () => el.classList.remove("drag"));
      el.addEventListener("dragover", e => { e.preventDefault(); el.classList.add("over"); });
      el.addEventListener("dragleave", () => el.classList.remove("over"));
      el.addEventListener("drop", e => { e.preventDefault(); el.classList.remove("over"); rpMove(+e.dataTransfer.getData("text/plain"), +el.dataset.i); });
    });
    $("#re-val")?.focus();
  },
};

const rpCanWrite = () => can("PUT", "/api/rules");
function rpScopeList(d = RP.d) { return RP.scope ? { rules: d.rules.user_rules || [], version: d.rules.user_version || 0 } : { rules: d.rules.global, version: d.rules.global_version }; }
function rpTargetName(t, d = RP.d) {
  if (t === "DIRECT") return "直连"; if (t === "REJECT") return "拦截"; if (!t) return "—";
  const g = d.groups.find(x => x.id === groupID(t)); return g ? g.name : t;
}
function rpPill(t, d = RP.d) {
  const g = d.groups.find(x => x.id === groupID(t)), k = t === "DIRECT" ? "dir" : t === "REJECT" ? "rej" : "grp";
  return H`<span class="rp-to ${k}">${k === "dir" ? "↗ " : k === "rej" ? "⦸ " : g?.icon ? g.icon + " " : ""}${rpTargetName(t, d)}</span>`;
}
const rpSet = (k, d = RP.d) => d.catalog.find(s => s.key === k);
const rpValText = (r, d = RP.d) => r.type === "set" ? rpSet(r.value, d)?.name || r.value : r.type === "geoip" ? RULE_REGIONS[r.value] || r.value : r.value;
function rpTargetOptions(sel, d = RP.d, follow) {
  const o = (v, t) => H`<option value="${v}" ${raw(sel === v ? "selected" : "")}>${t}</option>`;
  return H`${follow ? o("", follow) : ""}<optgroup label="基本">${o("DIRECT", "直连(不走代理)")}${o("REJECT", "拦截")}</optgroup>
    <optgroup label="策略组">${d.groups.map(g => o("group:" + g.id, `${g.icon ? g.icon + " " : ""}${g.name}`))}</optgroup>`;
}

function modeBanner(d) {
  const st = d.routing, rep = st.migration, W = can("PUT", "/api/routing");
  const out = [];
  if (rep && (rep.status === "needs_confirm" || rep.status === "blocked") && st.mode !== "rules") {
    out.push(H`<div class="callout rp-warn">${ic("warn", "ico-warn")}<div><b>${rep.status === "needs_confirm" ? "需要确认后才能改用规则表" : "旧模板里有转换不了的规则,订阅仍按模板生成"}</b>
      ${rep.status === "needs_confirm" ? H`<div>存过自定义 sing-box 模板:迁移后 sing-box 的分流改为跟随规则表(模板里的其他部分照用)。</div>` : H`<div>${(rep.unconvertible || []).slice(0, 5).map(x => H`<code>${x}</code> `)}${rep.unconvertible?.length > 5 ? `等 ${rep.unconvertible.length} 条` : ""}</div>`}
      ${rep.status === "needs_confirm" && can("POST", "/api/routing/migrate") ? H`<div class="mt6"><button class="btn sm" data-act="rp-migrate" data-confirm="1">确认并迁移</button> <span class="sub">mpx rule migrate --confirm</span></div>` : ""}</div></div>`);
  } else if (st.mode !== "rules" && (d.rules.global.length || d.groups.length)) {
    out.push(H`<div class="callout rp-warn">${ic("warn", "ico-warn")}<div><b>订阅现在还按旧模板生成</b>,这里的改动暂不生效。${W ? H` <button class="btn sm" data-act="rp-mode" data-mode="rules">改用规则表</button>` : ""} <span class="sub">mpx rule mode rules</span></div></div>`);
  }
  if (st.warnings?.length) out.push(H`<div class="callout rp-warn">${ic("warn", "ico-warn")}<div>${st.warnings.map(w => H`<div>${w}</div>`)}</div></div>`);
  return out;
}

function rpEmpty(d) {
  const W = can("POST", "/api/routing/migrate");
  return H`<div class="card rp-empty"><div class="rp-flow"><div class="rp-fb"><b>你打开一个网站</b><span class="mono">netflix.com</span></div><span class="rp-arr">→</span>
    <div class="rp-fb wide"><b>按规则从上往下对</b><span>广告 → 拦截</span><span>国内网站 → 直连</span><span class="on">流媒体 → 📺 流媒体</span><span>其他全部 → ⚡ 自动选择</span></div><span class="rp-arr">→</span>
    <div class="rp-fb"><b>走对应的出口</b><span>策略组挑出的节点</span></div></div>
    <h4>还没有分流规则,订阅按旧模板生成</h4><p class="sub">先选一套起步方案,之后随时增删、拖动排序、改去向。</p>
    ${W ? H`<div class="rp-starts">
      <button class="rp-start rec" data-act="rp-migrate"><span class="pill on">推荐</span><b>沿用现在的分流</b><span>把现在的模板${d.routing.migration?.purposes?.length ? "和用途" : ""}转换成规则表:国内直连、拦广告、各类网站走对应分组,其他自动选最快。</span><em>转换 ${ic("arrow")}</em></button>
      <button class="rp-start" data-act="rp-blank" data-k="all"><b>全部走代理</b><span>只建一个「自动选择」组,除了局域网,所有网站都走它。</span><em>用这套 ${ic("arrow")}</em></button>
      <button class="rp-start" data-act="rp-blank" data-k="blank"><b>从空白开始</b><span>只有一条「其他全部 → 自动选择」,你自己一条条加。</span><em>开始 ${ic("arrow")}</em></button></div>
      <p class="sub mt">等价命令:<code>mpx rule migrate</code> · <code>mpx group add 自动选择 --mode auto</code></p>` : H`<div class="cmd mt">mpx rule migrate</div>`}</div>`;
}

function rpRow(r, i, own, editable) {
  if (editable && RP.edit && !RP.edit.isNew && RP.edit.i === i) return rpEditRow();
  const set = r.type === "set" ? rpSet(r.value) : null;
  return H`<div class="rp-row ${r.enabled ? "" : "off"} ${own && RP.scope ? "own" : ""}" ${raw(editable ? "draggable=\"true\"" : "")} data-i="${i}">
    <span class="rp-grip" title="拖动排序">${editable ? "⋮⋮" : ""}</span><span class="rp-n">${i + 1}</span>
    <span class="rp-ty">${RULE_TYPES[r.type] || r.type}</span><span class="rp-v ${r.type === "set" ? "" : "mono"}" title="${rpValText(r)}">${rpValText(r)}${set ? H` <span class="sub">${set.desc}</span>` : ""}${r.note ? H` <span class="sub">· ${r.note}</span>` : ""}</span>
    <span class="rp-a">→</span>${rpPill(r.target)}
    <span class="sp"></span>${own && RP.scope ? H`<span class="pill on">仅 ${RP.scope}</span>` : ""}
    ${editable ? H`<label class="sw" title="${r.enabled ? "停用" : "启用"}"><input type="checkbox" data-act="rp-toggle" data-i="${i}" aria-label="启用第 ${i + 1} 条" ${raw(r.enabled ? "checked" : "")}><i></i></label>
    <button class="ib sm" data-act="rp-edit" data-i="${i}" title="编辑" aria-label="编辑第 ${i + 1} 条">${ic("more")}</button>` : ""}</div>`;
}
function rpEditRow() {
  const e = RP.edit, d = RP.d;
  const valIn = e.type === "set" ? H`<select class="in" id="re-val">${d.catalog.map(s => H`<option value="${s.key}" ${raw(s.key === e.value ? "selected" : "")}>${s.name} · ${s.desc}</option>`)}</select>`
    : e.type === "geoip" ? H`<select class="in" id="re-val">${Object.entries(RULE_REGIONS).map(([k, s]) => H`<option value="${k}" ${raw(k === e.value ? "selected" : "")}>${s}</option>`)}</select>`
    : H`<input class="in mono" id="re-val" value="${e.value}" autocomplete="off" spellcheck="false" placeholder="${{ suffix: "example.com(含子域名)", keyword: "google", cidr: "91.108.4.0/22" }[e.type]}">`;
  return H`<div class="rp-row editing"><span class="rp-grip"></span><span class="rp-n">${e.isNew ? "新" : "✎"}</span>
    <select class="in w140" id="re-type" aria-label="类型">${Object.entries(RULE_TYPES).map(([k, t]) => H`<option value="${k}" ${raw(k === e.type ? "selected" : "")}>${t}</option>`)}</select>
    <div class="grow">${valIn}</div><span class="rp-a">→</span><select class="in w220" id="re-to" aria-label="去向">${rpTargetOptions(e.target)}</select>
    <button class="btn pri sm" data-act="rp-save">${e.isNew ? "添加" : "保存"}</button><button class="btn ghost sm" data-act="rp-cancel">取消</button>${e.isNew ? "" : H`<button class="ib sm c-bad" data-act="rp-del" title="删除" aria-label="删除这条">${ic("x")}</button>`}
    <div class="err" id="re-err" role="alert" style="flex-basis:100%"></div></div>`;
}
function rpRulesCard(d) {
  const W = rpCanWrite(), R = d.rules, sc = RP.scope;
  const own = sc ? H`<div class="rp-sec">只对 ${sc} 生效 · 排在全局规则前面<span class="sp"></span>${W ? H`<button class="btn sm" data-act="rp-add">${ic("plus")}给 ${sc} 加一条</button>` : ""}</div>
    ${RP.edit?.isNew ? rpEditRow() : ""}${(R.user_rules || []).length ? R.user_rules.map((r, i) => rpRow(r, i, true, W)) : H`<div class="rp-row sub">还没有专属规则,${sc} 完全跟随全局。</div>`}
    <div class="rp-sec">全局规则(只读,去「全部用户」里改)</div>` : "";
  const base = sc ? (R.user_rules || []).length : 0;
  const finalSel = sc ? R.user_final || "" : R.final;
  return H`<div class="card">${sc ? "" : H`<div class="ch">从上往下匹配,第一条对上的生效<span class="sp"></span>${W ? H`<button class="btn sm" data-act="rp-builtin">${ic("plus")}内置规则组</button><button class="btn pri sm" data-act="rp-add">${ic("plus")}添加规则</button>` : ""}</div>${RP.edit?.isNew ? rpEditRow() : ""}`}
    <div class="rp-list">${own}${R.global.map((r, i) => sc ? rpRow({ ...r }, base + i, false, false) : rpRow(r, i, true, W))}
    <div class="rp-row final"><span class="rp-grip"></span><span class="rp-n">∞</span><span class="rp-ty">兜底</span><span class="rp-v"><b>其他全部</b> <span class="sub">上面都没对上的</span></span><span class="rp-a">→</span>
      ${W ? H`<select class="in w220" id="rp-final" aria-label="其他全部的去向">${rpTargetOptions(finalSel, d, sc ? `跟随全局(${rpTargetName(R.final)})` : null)}</select>` : rpPill(finalSel || R.final)}<span class="sp"></span></div></div></div>`;
}

function rpTestCard() {
  return H`<div class="card"><div class="ch">测试一个网址</div><div class="pad16"><input class="in mono" id="rp-test" value="${RP.test}" placeholder="输入域名,比如 chatgpt.com" autocomplete="off" spellcheck="false">
    <div class="chips mt6">${["chatgpt.com", "netflix.com", "bilibili.com", "github.com", "baidu.com"].map(x => H`<button class="tag rp-try" data-act="rp-try" data-d="${x}">${x}</button>`)}</div><div id="rp-res">${rpTestOut()}</div></div></div>`;
}
function rpTestOut() {
  const r = RP.testRes;
  if (!RP.test.trim() || !r) return H`<p class="sub mt">看看它会命中哪条规则、最后从哪里出去。${RP.scope ? `按 ${RP.scope} 的规则算。` : ""}</p>`;
  if (r.error) return H`<p class="c-bad mt">${r.error}</p>`;
  const m = r.matched, how = groupID(r.target) ? (r.mode === "auto" ? `自动在 ${r.members?.length || 0} 个入口里挑最快的` : `你在客户端里选的那个(${r.members?.length || 0} 个可选)`) : "";
  return H`<div class="rp-res"><div class="sub">${r.domain}</div><div class="rp-big">${rpPill(r.target)}</div>
    <dl class="kv flat"><dt>命中</dt><dd>${m ? H`${m.scope === "user" ? H`<span class="pill on">仅 ${r.user}</span> ` : ""}第 ${m.index} 条 · ${RULE_TYPES[m.rule.type]} <b>${rpValText(m.rule)}</b>` : "没有规则对上,走「其他全部」"}</dd>
    ${how ? H`<dt>实际出口</dt><dd>${how}${r.members?.length ? H`<div class="chips mt6">${r.members.map(x => H`<span class="tag">${x}</span>`)}</div>` : H` <span class="c-warn">· 组里没有入口,只剩直连</span>`}</dd>` : ""}
    ${r.uncertain?.length ? H`<dt>注意</dt><dd class="c-warn">${r.uncertain.map(u => H`<div>第 ${u.index} 条 ${RULE_TYPES[u.rule.type]} ${rpValText(u.rule)}:${u.reason}</div>`)}</dd>` : ""}
    ${r.disabled_hits?.length ? H`<dt>停用</dt><dd class="sub">有 ${r.disabled_hits.length} 条已停用的规则本来会命中</dd>` : ""}</dl>
    <p class="sub">规则组以 Clash 版上游规则集模拟;客户端实际下载的规则集可能稍有不同。</p></div>`;
}
async function rpRunTest() {
  const seq = ++RP.testSeq, q = RP.test.trim();
  if (!q) { RP.testRes = null; const el = $("#rp-res"); if (el) el.innerHTML = val(rpTestOut()); return; }
  try { RP.testRes = await api("/api/rules/test", { method: "POST", body: JSON.stringify({ domain: q, user: RP.scope }), headers: { "Content-Type": "application/json" } }); }
  catch (e) { RP.testRes = { error: e.message }; }
  if (seq !== RP.testSeq) return;
  const el = $("#rp-res"); if (el) el.innerHTML = val(rpTestOut());
}

function rpGroupsCard(d) {
  const W = can("POST", "/api/proxy-groups");
  const used = id => d.rules.global.filter(r => r.target === "group:" + id).length + (d.rules.final === "group:" + id ? 1 : 0);
  const cond = g => [...(g.regions || []).map(x => RULE_REGIONS[x] || x), ...(g.protocols || []), ...(g.capabilities || []).map(c => RULE_CAPS[c] || c), ...(g.tags || []).map(t => "#" + t)].join(" + ") || "全部入口";
  return H`<div class="card"><div class="ch">策略组:按条件自动收入口,新加的节点符合条件会自己进来<span class="sp"></span>${W ? H`<button class="btn pri sm" data-act="gp-edit" data-id="">${ic("plus")}新建策略组</button>` : ""}</div>
  ${d.groups.length ? d.groups.map(g => { const e = d.members[g.id];
    return H`<div class="row rp-g" data-act="gp-edit" data-id="${g.id}" tabindex="0"><span class="rp-gi">${g.icon || "◎"}</span><div class="grow"><b class="bold">${g.name}</b> <span class="pill">${g.mode === "auto" ? "自动选最快" : "手动选"}</span>
      <div class="sub">${cond(g)} · 被 ${used(g.id)} 条规则使用</div>
      <div class="chips mt6">${e ? e.map(x => H`<span class="tag">${x}</span>`) : ""}${e && !e.length ? H`<span class="c-warn">没有符合条件的入口,订阅里这个组只有「直连」</span>` : ""}</div></div>${ic("arrow")}</div>`; })
    : emptyBox("tpl", "还没有策略组", "策略组是规则的去向:按地区、协议、能力检测挑入口。", "mpx group add 自动选择 --mode auto")}</div>`;
}

async function groupEditor(id) {
  const d = RP.d, g0 = d.groups.find(g => g.id === +id), W = can(g0 ? "PUT" : "POST", "/api/proxy-groups/x");
  const g = g0 ? JSON.parse(JSON.stringify(g0)) : { name: "", icon: "", mode: "auto", regions: [], protocols: [], capabilities: [], tags: [] };
  for (const k of ["regions", "protocols", "capabilities", "tags"]) g[k] ||= [];
  let all = [];
  try { all = await api("/api/proxy-groups/preview", { method: "POST", body: JSON.stringify({ name: "preview", mode: "auto" }), headers: { "Content-Type": "application/json" } }); } catch {}
  const opts = (key, list, label) => { const set = [...new Set([...list, ...g[key]])]; return set.length ? H`<div class="chips" data-key="${key}">${set.map(k => H`<label class="chip"><input type="checkbox" value="${k}" ${raw(g[key].includes(k) ? "checked" : "")}>${label(k)}</label>`)}</div>` : H`<span class="sub">还没有入口带这一项</span>`; };
  const regions = all.map(e => e.region).filter(Boolean), protos = all.map(e => e.protocol).filter(Boolean);
  openDrawer(H`<form id="gp-form" novalidate><div class="dh"><h2>${g0 ? g0.name : "新建策略组"}</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div><div class="db">
    <div class="field"><label for="gp-name">名字</label><div class="flex8"><input class="in" style="width:64px;flex:none;text-align:center" id="gp-icon" value="${g.icon}" placeholder="🌐" aria-label="图标"><input class="in" style="flex:1;min-width:0" id="gp-name" value="${g.name}" placeholder="比如 日本节点" autocomplete="off"></div></div>
    <div class="field"><label>收哪些入口 <span class="sub">· 各类之间要同时满足,不选就不限</span></label></div>
    <div class="field"><label class="sub">地区(任一)</label>${opts("regions", regions, k => RULE_REGIONS[k] || k)}</div>
    <div class="field"><label class="sub">协议(任一)</label>${opts("protocols", protos, k => k)}</div>
    <div class="field"><label class="sub">能力检测(全部通过)</label>${opts("capabilities", Object.keys(RULE_CAPS), k => RULE_CAPS[k])}</div>
    <div class="field"><label class="sub" for="gp-tags">标签(任一,逗号分隔)</label><input class="in mono" id="gp-tags" value="${g.tags.join(",")}" autocomplete="off"></div>
    <div class="field"><label>怎么选</label><div class="seg" id="gp-mode"><button type="button" class="${g.mode === "auto" ? "on" : ""}" data-m="auto">自动选最快</button><button type="button" class="${g.mode === "manual" ? "on" : ""}" data-m="manual">我手动选</button></div><span class="h">自动:客户端定时测网页延迟,挑最快的。手动:用户在客户端里自己选。</span></div>
    <div class="card mt"><div class="ch">现在会收进来<small id="gp-cnt"></small></div><div id="gp-pv"></div></div>
    <div class="field mt"><label>等价命令</label><div class="cmd" id="gp-cmd"></div></div><div class="err" id="gp-err" role="alert"></div>
    ${g0 && can("DELETE", "/api/proxy-groups/x") ? H`<div class="card danger-zone mt22"><div class="row"><div><b class="bold">删除策略组</b><div class="sub">还有规则指向它时删不掉,先改掉那些规则。</div></div><span class="sp"></span><button type="button" class="btn sm c-bad" data-act="gp-rm" data-id="${g0.id}" data-name="${g0.name}">删除</button></div></div>` : ""}</div>
    <div class="df"><button type="button" class="btn" data-act="close">取消</button>${W ? H`<button class="btn pri" id="gp-save">保存</button>` : ""}</div></form>`);
  const f = $("#gp-form");
  const read = () => {
    for (const k of ["regions", "protocols", "capabilities"]) g[k] = [...f.querySelectorAll(`[data-key=${k}] input:checked`)].map(x => x.value);
    g.tags = splitCSV($("#gp-tags").value); g.name = $("#gp-name").value.trim(); g.icon = $("#gp-icon").value.trim();
    const flag = (k, n) => g[k].length ? ` --${n} ${shq(g[k].join(","))}` : "";
    $("#gp-cmd").textContent = g0 ? `mpx group edit ${shq(g0.name)}${g.name !== g0.name ? " --name " + shq(g.name) : ""} --mode ${g.mode}${flag("regions", "region")}${flag("protocols", "protocol")}${flag("capabilities", "capability")}${flag("tags", "tag")}`
      : `mpx group add ${shq(g.name || "<名字>")} --mode ${g.mode}${flag("regions", "region")}${flag("protocols", "protocol")}${flag("capabilities", "capability")}${flag("tags", "tag")}`;
  };
  let seq = 0;
  const sync = async () => {
    read(); const my = ++seq;
    let pv;
    try { pv = await api("/api/proxy-groups/preview", { method: "POST", body: JSON.stringify({ ...g, name: g.name || "preview" }), headers: { "Content-Type": "application/json" } }); }
    catch (e) { if (my === seq) $("#gp-pv").innerHTML = val(H`<div class="pad16 c-bad">${e.message}</div>`); return; }
    if (my !== seq) return;
    const n = pv.filter(x => x.admitted).length;
    $("#gp-cnt").textContent = `· ${n} / ${pv.length} 个入口`;
    $("#gp-pv").innerHTML = val(pv.length ? pv.map(x => H`<div class="row pp-pvr ${x.admitted ? "" : "miss"}"><span class="pp-mk ${x.admitted ? "ok" : ""}">${ic(x.admitted ? "check" : "x")}</span><span class="grow">${x.name}</span><span class="sub">${x.admitted ? (x.capabilities || []).map(c => RULE_CAPS[c] || c).join(" · ") : (x.reasons || []).join("、")}</span></div>`)
      : H`<div class="pad16 sub">还没有入口。</div>`);
  };
  f.addEventListener("change", sync); f.addEventListener("input", e => { if (e.target.id === "gp-tags" || e.target.id === "gp-name" || e.target.id === "gp-icon") read(); });
  $("#gp-tags").addEventListener("change", sync);
  $("#gp-mode").addEventListener("click", e => { const b = e.target.closest("button"); if (!b) return; g.mode = b.dataset.m; f.querySelectorAll("#gp-mode button").forEach(x => x.classList.toggle("on", x === b)); read(); });
  sync();
  f.addEventListener("submit", async e => {
    e.preventDefault(); read();
    const btn = $("#gp-save");
    try {
      if (!g.name) throw new Error("名字不能为空");
      btn.disabled = true; btn.classList.add("busy");
      const body = { ...g }; if (g0) body.id = g0.id;
      await write(g0 ? "PUT" : "POST", g0 ? groupPath(g0.id) : "/api/proxy-groups", body, `策略组「${g.name}」已保存`);
      closeOverlays();
    } catch (e2) { $("#gp-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

function rpBuiltin() {
  const d = RP.d, have = new Set(d.rules.global.filter(r => r.type === "set").map(r => r.value));
  const def = s => s.default === "DIRECT" || s.default === "REJECT" ? s.default : d.rules.final || (d.groups[0] ? "group:" + d.groups[0].id : "DIRECT");
  openDrawer(H`<form id="bi-form"><div class="dh"><h2>内置规则组</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div><div class="db">
    <p class="sub mt0">常用网站已经整理好,勾上就加进规则表,去向可以改。规则内容由客户端定期从公开规则集下载更新。</p>
    ${d.catalog.map(s => H`<div class="row rp-bi"><label class="chk grow"><input type="checkbox" value="${s.key}" ${raw(have.has(s.key) ? "checked disabled" : "")}><div><b class="bold">${s.name}</b> ${have.has(s.key) ? H`<span class="pill on">已添加</span>` : ""}<div class="sub">${s.desc}</div></div></label><select class="in w180" data-to="${s.key}" aria-label="${s.name} 的去向">${rpTargetOptions(def(s))}</select></div>`)}
    <div class="err" id="bi-err" role="alert"></div></div><div class="df"><button type="button" class="btn" data-act="close">取消</button><button class="btn pri">添加所选</button></div></form>`);
  $("#bi-form").addEventListener("submit", async e => {
    e.preventDefault();
    const add = [...document.querySelectorAll("#bi-form input:checked:not(:disabled)")].map(x => ({ type: "set", value: x.value, target: $(`[data-to="${x.value}"]`).value, enabled: true }));
    if (!add.length) { $("#bi-err").textContent = "先勾选要加的规则组"; return; }
    const list = d.rules.global.slice(), at = list.findIndex(r => r.type === "set" && r.value === "cn" || r.type === "geoip");
    list.splice(at < 0 ? list.length : at, 0, ...add);
    try { await rpPut(list, d.rules.global_version, `已添加 ${add.length} 组规则`); closeOverlays(); } catch (e2) { $("#bi-err").textContent = e2.message; }
  });
}

const rpStrip = r => { const o = { type: r.type, value: r.value, target: r.target, enabled: !!r.enabled }; if (r.note) o.note = r.note; return o; };
function rpPut(list, version, okText) { return write("PUT", rulesPath(RP.scope), { rules: list.map(rpStrip), version }, okText); }
async function rpMove(from, to) {
  const { rules, version } = rpScopeList(); if (from === to || !rules[from] || !rules[to]) return;
  const list = rules.slice(), [x] = list.splice(from, 1); list.splice(to, 0, x);
  try { await rpPut(list, version, `已移到第 ${to + 1} 条`); } catch (e) { toast(e.message, true); }
}
async function rpSetFinal(v) {
  try { await write("PUT", finalPath(RP.scope), { target: v }, `其他全部 → ${v ? rpTargetName(v) : "跟随全局"}`); } catch (e) { toast(e.message, true); }
}

Object.assign(ACTS, {
  "rp-tab": d => { RP.tab = d.k; RP.edit = null; location.hash = "rules/" + d.k; },
  "rp-try": d => { RP.test = d.d; const t = $("#rp-test"); if (t) t.value = d.d; rpRunTest(); },
  "rp-toggle": async (d, el) => { const { rules } = rpScopeList(), r = rules[+d.i]; el.disabled = true;
    try { await write("PATCH", "/api/rules/" + r.id, { enabled: !r.enabled }, r.enabled ? "已停用,客户端更新订阅后生效" : "已启用"); } catch (e) { toast(e.message, true); el.checked = r.enabled; el.disabled = false; } },
  "rp-edit": d => { const r = rpScopeList().rules[+d.i]; RP.edit = { ...r, i: +d.i }; paint(); },
  "rp-add": () => { const d = RP.d; RP.edit = { isNew: true, type: "suffix", value: "", target: d.rules.final || (d.groups[0] ? "group:" + d.groups[0].id : "DIRECT"), enabled: true }; paint(); },
  "rp-cancel": () => { RP.edit = null; paint(); },
  "rp-save": async (_, b) => {
    const e = RP.edit, v = $("#re-val").value.trim();
    if (!v) { $("#re-err").textContent = "先填匹配的内容"; return; }
    const { rules, version } = rpScopeList(), list = rules.slice(), o = { ...e, value: v, target: $("#re-to").value, type: $("#re-type").value };
    if (e.isNew) list.unshift(o); else list[e.i] = o;
    b.disabled = true;
    try { await rpPut(list, version, e.isNew ? "已添加,客户端更新订阅后生效" : "已保存"); RP.edit = null; } catch (e2) { $("#re-err").textContent = e2.message; b.disabled = false; }
  },
  "rp-del": async () => { const { rules, version } = rpScopeList(), list = rules.slice(); list.splice(RP.edit.i, 1);
    try { await rpPut(list, version, "已删除"); RP.edit = null; } catch (e) { $("#re-err").textContent = e.message; } },
  "rp-builtin": () => rpBuiltin(),
  "gp-edit": d => groupEditor(d.id),
  "gp-rm": d => confirmBox({ title: `删除策略组 ${d.name}?`, text: "删除后订阅里不再有这个组。", cmd: `mpx group rm ${shq(d.name)}`, ok: "删除", danger: true,
    run: async () => { await write("DELETE", groupPath(d.id), undefined, `策略组「${d.name}」已删除`); closeOverlays(); } }),
  "rp-migrate": d => confirmBox({ title: d.confirm ? "确认迁移到规则表?" : "把现在的分流转换成规则表?", text: "按现在的模板和用途生成策略组与规则,订阅改由规则表生成。原来的模板和用途都保留,随时可以切回(mpx rule mode template)。", cmd: d.confirm ? "mpx rule migrate --confirm" : "mpx rule migrate", ok: "转换",
    run: async () => { const r = await write("POST", "/api/routing/migrate", d.confirm ? { confirm: true } : {}); if (r.status !== "migrated") throw new Error(r.status === "needs_confirm" ? "存过自定义 sing-box 模板,需要确认后迁移" : (r.unconvertible || r.notes || []).join(";") || r.status); toast(`已转换:${r.groups.length} 个策略组、${r.rules} 条规则`); } }),
  "rp-mode": d => confirmBox({ title: d.mode === "rules" ? "改用规则表生成订阅?" : "切回旧模板?", text: d.mode === "rules" ? "Clash Meta 和 sing-box 订阅改由这里的规则生成,客户端更新订阅后生效。" : "订阅回到旧模板与用途。", cmd: `mpx rule mode ${d.mode}`, ok: "切换",
    run: async () => { await write("PUT", "/api/routing", { mode: d.mode }, "已切换"); } }),
  "rp-blank": async (d, b) => busy(b, async () => {
    const g = await write("POST", "/api/proxy-groups", { name: "自动选择", icon: "⚡", mode: "auto" });
    if (d.k === "all") await write("PUT", "/api/rules", { rules: [{ type: "set", value: "lan", target: "DIRECT", enabled: true }], version: 0 });
    await write("PUT", "/api/rules/final", { target: "group:" + g.id });
    await write("PUT", "/api/routing", { mode: "rules" }, "已应用,客户端更新订阅后生效");
  }),
});
