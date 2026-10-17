// 用户页:列表、详情抽屉、新建/编辑、以及 enable / disable / renew / reset-traffic /
// revoke-sub / rm。每个写操作发出的请求与对应的 mpx user 命令一致(读-改-写整体
// 回写、同样的字段与取值),e2e 测试逐条对照 CLI 实际发出的请求。
"use strict";

/* ---- 与 CLI 同一套解析(internal/cli/user.go) ---- */
// parseBytes:B/KB/MB/GB/TB 按 1024 进位,可带小数。
function parseBytes(s) {
  let text = String(s).trim();
  if (!text) throw new Error("大小不能为空");
  let mult = 1;
  for (const [suf, m] of [["TB", 2 ** 40], ["GB", 2 ** 30], ["MB", 2 ** 20], ["KB", 2 ** 10], ["B", 1]]) {
    if (text.toUpperCase().endsWith(suf)) { mult = m; text = text.slice(0, -suf.length).trim(); break; }
  }
  if (!text) throw new Error(`大小缺少数字: "${s}"`);
  if (/^[+-]?\d+$/.test(text)) { const n = Number(text); if (n < 0) throw new Error(`大小不能是负数: "${s}"`); return n * mult; }
  const f = Number(text);
  if (!/^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(text) || !isFinite(f)) throw new Error(`无法解析大小 "${s}",单位用 B/KB/MB/GB/TB`);
  if (f < 0) throw new Error(`大小不能是负数: "${s}"`);
  return Math.trunc(f * mult);
}
// parseExpire:YYYY-MM-DD → 本机时区当天 23:59:59(和 CLI 一样以操作机时区为准)。
function parseExpire(s) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(s).trim());
  const d = m && new Date(+m[1], +m[2] - 1, +m[3], 23, 59, 59);
  if (!d || d.getMonth() !== +m[2] - 1 || d.getDate() !== +m[3]) throw new Error(`到期日要写成 YYYY-MM-DD: "${s}"`);
  return d.toISOString();
}
// renewTarget:从「当前到期日与现在中较晚的那个」往后数整天,落在当天 23:59:59。
function renewTarget(current, days, now = new Date()) {
  const cur = current ? new Date(current) : null;
  const base = cur && cur > now ? cur : now;
  return new Date(base.getFullYear(), base.getMonth(), base.getDate() + days, 23, 59, 59).toISOString();
}
const USERNAME_RE = /^[A-Za-z0-9_.-]{1,64}$/;
// asModelUser:把 GET 回来的 userView 摆成 model.User 的 JSON 形状 —— CLI 把它解进
// model.User 再整体 PUT 回去,这里发出的就是同一份(同样的字段、同样的 omitempty)。
function asModelUser(v) {
  const o = { id: v.id || 0, username: v.username };
  if (v.key) o.key = v.key;
  if (v.sub_token) o.sub_token = v.sub_token;
  o.enabled = !!v.enabled;
  o.data_limit = v.data_limit || 0;
  o.used_traffic = v.used_traffic || 0;
  o.lifetime_used_traffic = v.lifetime_used_traffic || 0;
  if (v.expire_at) o.expire_at = v.expire_at;
  o.reset_strategy = v.reset_strategy || "";
  if (v.traffic_reset_at) o.traffic_reset_at = v.traffic_reset_at;
  if (v.inbound_ids && v.inbound_ids.length) o.inbound_ids = v.inbound_ids;
  if (v.note) o.note = v.note;
  o.created_at = v.created_at || "0001-01-01T00:00:00Z";
  return o;
}
const userPath = n => "/api/users/" + encodeURIComponent(n);
async function readModify(name, change) {
  const u = asModelUser(await api(userPath(name)));
  change(u);
  const res = await api(userPath(name), { method: "PUT", body: JSON.stringify(u), headers: { "Content-Type": "application/json" } });
  afterWrite();
  return res;
}

/* ---- 用户列表的「套餐」列 ---- */
// userPlanCell 显示用户挂的套餐名,并在有额外授权时加 +N —— 只数用户自己的
// inbound_ids 里不在套餐范围内的入站;套餐是 all_nodes 时没有「额外」可言。
// 没挂套餐时显示「全部入站」或「单独授权 · N 个入站」(旧语义)。
function userPlanCell(u, d) {
  const plans = d.plans || [], inbounds = d.inbounds || [];
  const p = plans.find(x => x.id === u.plan_id);
  if (!p) {
    const n = (u.inbound_ids || []).length;
    return n ? H`<span class="pl-ptag"><span class="tag g" title="单独授权的 ${n} 个入站">单独授权 · ${n} 个入站</span></span>`
             : H`<span class="sub">全部入站</span>`;
  }
  const extra = extrasOf(u, p, inbounds);
  return H`<span class="pl-ptag"><span class="tag ${p.is_default ? "g" : ""}">${p.name}</span>${extra.length ? H`<span class="sub" title="额外授权:${extra.map(i => planInboundLabel(i)).join("、")}">+${extra.length}</span>` : ""}</span>`;
}

// extrasOf 返回用户额外授权里不在套餐范围内的入站对象;all_nodes 套餐固定为空。
function extrasOf(u, p, inbounds) {
  if (p.all_nodes) return [];
  const set = planEffectiveSet(p, inbounds);
  return (u.inbound_ids || []).filter(id => !set.has(id)).map(id => inbounds.find(i => i.id === id)).filter(Boolean);
}

// inboundDisplay 把入站 id 摆成入站标签;找不到时退回 #id。
function inboundDisplay(id) { return planInboundLabel((PLAN_CACHE.inbounds || []).find(i => i.id === id) || { id }); }

// userPlanDetail 是用户详情里的套餐描述。
function userPlanDetail(u) {
  const p = (PLAN_CACHE.plans || []).find(x => x.id === u.plan_id);
  if (!p) return (u.inbound_ids || []).length ? H`单独授权 · ${u.inbound_ids.length} 个入站` : H`全部入站`;
  return p.all_nodes ? H`${p.name} · 全部节点` : H`${p.name} · ${(p.node_ids || []).length} 个节点`;
}

/* ---- 列表状态 ---- */
const PAGE_SIZE = 50;
const US = { status: "", search: "", plan: "", offset: 0 };
const STATUS_TABS = [["", "全部"], ["active", "正常"], ["limited", "已超限"], ["expired", "已过期"], ["disabled", "已停用"]];

// 用户页顶部的「用户 | 套餐」页签:套餐是用户的属性,不再单独占侧栏一级(#users/plans)。
const userTabs = plans => PAGES.plans ? H`<div class="tabs"><a class="${plans ? "" : "on"}" href="#users">用户</a><a class="${plans ? "on" : ""}" href="#users/plans">套餐</a></div>` : "";

PAGES.users = {
  title: "用户", closeTo: "users",
  count: () => USERS_TOTAL ?? STATUS?.counts?.users ?? "",
  crumb: r => r[1] ? H`<a href="#users">用户</a><i>/</i><b>${r[1]}</b>` : H`<b>用户</b>`,
  async load(r) {
    const q = new URLSearchParams({ limit: PAGE_SIZE, offset: US.offset });
    if (US.status) q.set("status", US.status);
    if (US.search) q.set("search", US.search);
    if (US.plan) q.set("plan", US.plan);
    // 各状态的人数:一次取回(≤1000 人)按控制面给的 status 数,不再为每个标签各打一次
    // 请求 —— /api 按 IP 限流,一页五六个请求很容易撞上。人更多时不显示数字。
    const counts = api("/api/users?limit=1000").then(p => {
      const c = { "": p.total };
      if (p.total <= p.users.length) for (const [s] of STATUS_TABS.slice(1)) c[s] = p.users.filter(u => u.status === s).length;
      return c;
    });
    const plans = PLAN_CACHE.loaded ? Promise.resolve(PLAN_CACHE.plans) : api("/api/plans");
    const inbounds = PLAN_CACHE.loaded ? Promise.resolve(PLAN_CACHE.inbounds) : api("/api/inbounds");
    // 详情还要:拉取记录(「发给他」标题右边的状态;老控制面没有这个接口就当没有)、
    // 订阅前缀(没设时提示链接里是 IP)、节点数(套餐「N 个节点」)。
    const detail = r[1] ? Promise.all([api(userPath(r[1])), api(userPath(r[1]) + "/links"),
      api("/api/sub-pulls").catch(() => null), api("/api/settings").catch(() => null), api("/api/nodes").catch(() => [])]) : null;
    const [page, cs, pl, ib, d] = await Promise.all([api("/api/users?" + q), counts, plans, inbounds, detail]);
    USERS_TOTAL = cs[""];
    PLAN_CACHE.plans = pl || [];
    PLAN_CACHE.inbounds = ib || [];
    PLAN_CACHE.loaded = true;
    return { page, counts: cs, plans: PLAN_CACHE.plans, inbounds: PLAN_CACHE.inbounds, user: d && d[0], links: d && d[1],
      pull: d && d[2] ? d[2].find(p => p.username === r[1]) || null : null, pullsKnown: !!(d && d[2]), settings: d && d[3], nodes: d ? d[4] : [] };
  },
  render(d) {
    const head = H`<tr><th>用户</th><th>状态</th><th>套餐</th><th>本期用量</th><th class="hide-m">到期</th><th class="hide-m">重置</th></tr>`;
    let inner;
    if (!d) inner = H`<table><thead>${head}</thead><tbody>${skRows(8, 6)}</tbody></table>`;
    else if (!d.page.users.length) inner = US.status || US.search
      ? emptyBox("search", "没有匹配的用户", "换个筛选条件或搜索词试试。", "")
      : emptyBox("users", "还没有用户", "加一个用户,他就会拿到一条订阅地址。之后续期、停用、重置流量都在这里。", "", can("POST", "/api/users") ? H`<button class="btn pri sm" data-act="user-new">${ic("plus")}添加用户</button>` : "");
    else {
      const P = d.page, end = P.offset + P.users.length;
      inner = H`<div class="tw"><table><thead>${head}</thead><tbody>${P.users.map(u => H`<tr data-href="#users/${encodeURIComponent(u.username)}" tabindex="0"><td><div class="u">${avatar(u.username)}<div><b class="bold">${u.username}</b>${u.note ? H`<div class="sub">${u.note}</div>` : ""}</div></div></td>
        <td>${userSt(u.status)}</td><td>${userPlanCell(u, d)}</td><td>${usageBar(u)}</td><td class="hide-m">${expireCell(u)}</td><td class="hide-m sub">${RESET[u.reset_strategy] || u.reset_strategy}</td></tr>`)}</tbody></table></div>
        <div class="pager">显示 ${P.offset + 1}–${end},共 ${P.total} 个<span class="sp"></span><button class="btn sm" data-act="user-page" data-dir="-1" ${P.offset ? "" : raw("disabled")}>上一页</button><button class="btn sm" data-act="user-page" data-dir="1" ${end < P.total ? "" : raw("disabled")}>下一页</button></div>`;
    }
    const c = d ? d.counts : {};
    const planOpts = d ? d.plans.slice().sort((a, b) => (b.is_default - a.is_default) || (a.id - b.id)) : [];
    return H`<div class="ph"><div><h1>用户</h1><p>每个用户一条自己的订阅</p></div><div class="sp"></div>
      <button class="btn hide-m" data-act="user-export">${ic("dl")}导出 CSV</button>
      ${can("POST", "/api/users") ? H`<button class="btn pri" data-act="user-new">${ic("plus")}添加用户</button>` : ""}</div>
      ${userTabs(false)}
      <div class="card"><div class="tb"><div class="seg" role="tablist">${STATUS_TABS.map(([k, t]) => H`<button role="tab" class="${US.status === k ? "on" : ""}" data-act="user-filter" data-status="${k}">${t}<em>${c[k] ?? ""}</em></button>`)}</div>
      <span class="sp" style="flex:1"></span><select class="in w180" id="uf-plan" aria-label="按套餐筛选"><option value="">全部套餐</option>${planOpts.map(p => H`<option value="${p.id}" ${raw(US.plan == p.id ? "selected" : "")}>${p.name}</option>`)}</select>
      <label class="search in-card">${ic("search")}<input type="search" id="user-search" placeholder="搜索用户名" value="${US.search}" autocomplete="off"></label></div>${inner}</div>`;
  },
  after(d, r) {
    const s = $("#user-search");
    if (s) { let t; s.addEventListener("input", () => { clearTimeout(t); t = setTimeout(() => { US.search = s.value.trim(); US.offset = 0; load(true).then(() => { const n = $("#user-search"); n?.focus(); n?.setSelectionRange(n.value.length, n.value.length); }); }, 250); }); }
    const pf = $("#uf-plan");
    if (pf) pf.addEventListener("change", () => { US.plan = pf.value; US.offset = 0; load(true); });
    if (d && r[1] && d.user) userDrawer(d.user, d.links, d);
  },
};
let USERS_TOTAL = null;

/* ---- 详情抽屉 ---- */
// 套餐里有几台节点:全部节点套餐 = 现有节点数;否则是勾选节点和单独勾选入站所在节点的并集。
function planNodeCount(p, nodes, inbounds) {
  if (!p) return 0;
  const ids = new Set((nodes || []).map(n => n.id));
  if (p.all_nodes) return ids.size;
  const got = new Set((p.node_ids || []).filter(id => ids.has(id)));
  for (const iid of p.inbound_ids || []) { const i = (inbounds || []).find(x => x.id === iid); if (i && ids.has(i.node_id)) got.add(i.node_id); }
  return got.size;
}
// 控制面记的是 User-Agent 第一段,常见的换成客户端自己的写法;认不出的照原样。
const CLIENT_NAMES = { "clash-verge": "Clash Verge", "clash.meta": "Clash Meta", mihomo: "Mihomo", clash: "Clash", shadowrocket: "Shadowrocket", stash: "Stash", "sing-box": "sing-box", sfa: "sing-box", sfi: "sing-box", sfm: "sing-box", v2rayn: "v2rayN", v2rayng: "v2rayNG", karing: "Karing", hiddify: "Hiddify", nekobox: "NekoBox", quantumult: "Quantumult X", surge: "Surge", loon: "Loon" };
const clientLabel = c => !c || c === "unknown" ? "客户端" : CLIENT_NAMES[c.toLowerCase()] || c;
// 「sing-box 在 14:37 拉取过」;一分钟内写「刚刚」,一小时内写「N 分钟前」。
function pullText(p) {
  const d = new Date(p.last_pulled_at), sec = (Date.now() - d) / 1e3, who = clientLabel(p.client);
  if (sec < 60) return `${who} 刚刚拉取过`;
  if (sec < 3600) return `${who} ${Math.floor(sec / 60)} 分钟前拉取过`;
  return `${who} 在 ${sec < 86400 ? hm(d) : ymd(d)} 拉取过`;
}
const SEND_FORMATS = [["links", "分享链接", "一行一个节点,粘贴进 v2rayN、Shadowrocket"], ["base64-links", "Base64", "老客户端认的订阅格式"], ["clash-meta", "Clash Meta", "Clash Verge、Mihomo"], ["clash", "Clash", "老版 Clash 内核"], ["sing-box", "sing-box", "sing-box 官方客户端、Karing"], ["xray", "Xray", "Xray 内核的 JSON 配置"]];
// 「发给他」:创建后和用户详情共用。二维码由本机 /local/qr.svg 画,永远放在白底上(暗色也一样),保证能扫。
function sendBlock(u, links, d) {
  const name = u.username, L = (links && links.auto) || "";
  // 按稿子用 ?u=<名字>,由本机进程向控制面取链接再画;v 让换了新链接后不吃旧图。
  // 本机取不到(比如撞上控制面限流)时,改用页面手里现成的链接 ?d= 画,见 userDrawer。
  // 「保存二维码」直接用 ?d=:点的时候不必再问一次控制面。
  const qr = `/local/qr.svg?u=${encodeURIComponent(name)}&v=${encodeURIComponent(L.slice(-10))}`, qrD = `/local/qr.svg?d=${encodeURIComponent(L)}`;
  const noPrefix = d && d.settings && !d.settings.sub_url_prefix;
  const pull = d && d.pull;
  return H`<section class="send" id="send">
    <div class="send-h"><h3>发给他</h3><span class="sub" id="send-pull">${pull ? pullText(pull) : d && d.pullsKnown ? "还没有客户端导入过" : ""}</span></div>
    <div class="send-b"><div class="qr-tile"><img class="qrs" src="${qr}" data-fb="${qrD}" alt="${name} 的订阅二维码"></div>
      <div class="send-r">
        <button type="button" class="btn pri send-copy" data-act="copy" data-text="${L}">${ic("copy")}复制订阅链接</button>
        <div class="send-link mono" title="${L}">${L.replace(/^https?:\/\//, "")}</div>
        <a class="btn" href="${qrD}" download="${name}-订阅二维码.svg">${ic("dl")}保存二维码</a>
        <p class="send-hint">Clash Verge、Shadowrocket、sing-box、v2rayN 都能直接扫码或粘贴导入,会自动拿到适合它的格式。</p>
      </div></div>
    ${noPrefix ? H`<div class="send-ip"><span class="send-ip-t" title="链接里是服务器 IP，设好订阅地址后新旧链接都能用">${ic("warn")}链接里是服务器 IP，设好订阅地址后新旧链接都能用</span><a class="lnk u-link" href="#sub">去设置</a></div>` : ""}
    <details class="fmts"><summary>${ic("arrow", "car")}其他格式<span class="sub">客户端不认自动识别时再用</span></summary>
      <div class="fmt-l">${SEND_FORMATS.filter(([k]) => links && links[k]).map(([k, n, t]) => H`<div class="fmt"><div class="fmt-t"><b>${n}</b><span class="sub">${t}</span></div><button type="button" class="ib" data-act="copy" data-text="${links[k]}" title="复制 ${n} 链接" aria-label="复制 ${n} 链接">${ic("copy")}</button></div>`)}</div>
    </details></section>`;
}
let JUST_CREATED = "";  // 刚建好的用户:详情顶上多一行「已创建 X」
function userDrawer(u, links, d) {
  const W = can("PUT", "/api/users/x"), name = u.username;
  const p = (d && d.plans || PLAN_CACHE.plans || []).find(x => x.id === u.plan_id);
  const nn = p ? planNodeCount(p, d && d.nodes, d && d.inbounds || PLAN_CACHE.inbounds) : 0;
  const created = JUST_CREATED === name; JUST_CREATED = "";
  const pull = d && d.pull;
  const use = u.data_limit ? H`<div class="bar w100 tall"><i class="${pct(u) >= 100 ? "bad" : pct(u) >= 80 ? "warn" : ""}" style="width:${pct(u).toFixed(1)}%"></i></div><div class="sub mt8">${fmtBytes(u.used_traffic)} / ${fmtBytes(u.data_limit)}${u.reset_strategy && u.reset_strategy !== "none" ? ` · ${RESET[u.reset_strategy]}清零` : ""}</div>` : H`<div class="sub mt8">不限流量</div>`;
  openDrawer(H`<div class="dh ud-h">${avatar(name)}<div class="ud-t"><h2 class="ell">${name}</h2>${userSt(u.status)}</div><span class="sp" style="flex:1"></span>
    ${W ? H`<button class="ib" title="编辑" aria-label="编辑" data-act="user-edit" data-name="${name}">${ic("pen")}</button>
    <span class="mw"><button class="ib" title="更多" aria-label="更多" aria-haspopup="menu" data-act="user-menu">${ic("more")}</button><div class="menu" role="menu" id="user-menu" hidden>
      <button type="button" data-act="user-renew" data-name="${name}">${ic("clock")}续期 30 天</button>
      <button type="button" data-act="user-reset" data-name="${name}">${ic("undo")}清零本期用量</button>
      <button type="button" data-act="user-toggle" data-name="${name}" data-on="${u.enabled ? "1" : ""}">${ic("pause")}${u.enabled ? "停用" : "启用"}</button>
      <div class="msep"></div>
      <button type="button" data-act="user-revoke" data-name="${name}">${ic("key")}换一条新链接</button>
      <button type="button" class="dg" data-act="user-rm" data-name="${name}">${ic("trash")}删除用户</button></div></span>` : ""}
    <button class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db ud-b">${created ? H`<div class="created">${ic("check")}<span>已创建 <b>${name}</b>。把下面的二维码或链接发给他就行。</span></div>` : ""}
    ${sendBlock(u, links, d)}
    <section class="ud-use"><div class="sub">本期用量</div><div class="ud-v">${fmtBytes(u.used_traffic)}</div>${use}</section>
    <dl class="kv flat ud-kv"><dt>套餐</dt><dd>${p ? H`${p.name} <span class="sub">· ${nn ? `${nn} 个节点` : "没有节点"}</span>` : userPlanDetail(u)}</dd>
      ${u.inbound_ids?.length ? H`<dt>额外授权</dt><dd>${u.inbound_ids.map(i => H`<span class="tag">${inboundDisplay(i)}</span> `)}</dd>` : ""}
      <dt>到期</dt><dd>${expireCell(u)}</dd>
      <dt>最近导入</dt><dd>${pull ? pullText(pull) : "还没有"}</dd>
      <dt>创建于</dt><dd>${ymd(new Date(u.created_at))}</dd>${u.note ? H`<dt>备注</dt><dd>${u.note}</dd>` : ""}</dl>
  </div>`);
  const img = $("#send .qrs");
  img?.addEventListener("error", () => { if (img.dataset.fb && img.src.indexOf("?u=") >= 0) img.src = img.dataset.fb; });
}
async function refreshDrawer(name) {
  await load(true, true);
  const d = current.data;
  if (route[0] === "users" && route[1] === name && d?.user) userDrawer(d.user, d.links, d);
}

/* ---- 编辑器 ---- */
async function userEditor(name) {
  let u = null, inbounds = [], nodes = [], plans = [];
  try { [u, inbounds, nodes, plans] = await Promise.all([name ? api(userPath(name)) : null, api("/api/inbounds"), api("/api/nodes"), api("/api/plans")]); }
  catch (e) { toast(e.message, true); return; }
  const isNew = !u;
  u = u || { username: "", data_limit: 0, expire_at: null, reset_strategy: "none", inbound_ids: [], note: "", enabled: true, plan_id: null };
  const nodeName = id => { const n = nodes.find(x => x.id === id); return n ? nodeLabel(n) : "节点"; };
  const planList = plans.slice().sort((a, b) => (b.is_default - a.is_default) || (a.id - b.id));
  const defPlan = planList.find(p => p.is_default);
  const currentPlan = u.plan_id != null ? String(u.plan_id) : "";
  let plan = isNew ? (defPlan ? String(defPlan.id) : "") : currentPlan;
  const sel = new Set(u.inbound_ids || []);
  const allPlan = planList.find(p => p.all_nodes && planNodeCount(p, nodes, inbounds));
  const limitText = u.data_limit ? (u.data_limit % 2 ** 30 === 0 ? u.data_limit / 2 ** 30 + "GB" : u.data_limit + "B") : "";
  openDrawer(H`<form id="user-form" novalidate><div class="dh"><h2>${isNew ? "添加用户" : "编辑 " + u.username}</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    <div class="field"><label for="f-name">用户名</label><input class="in" id="f-name" name="username" value="${u.username}" ${isNew ? raw("autofocus") : raw("disabled")} placeholder="alice" autocomplete="off"><span class="h">字母、数字、下划线、点和减号,1–64 位${isNew ? "" : ";建好后不能改"}</span></div>
    <div class="field"><label id="f-plan-l">套餐</label><div class="plans" role="radiogroup" aria-labelledby="f-plan-l" id="f-plans">
      ${!isNew && u.plan_id == null ? H`<label class="plan"><input type="radio" name="plan" value="" checked disabled><span class="rd"></span><span class="plan-t"><b>未挂套餐</b><span class="sub">单独授权;选一个套餐后不能再移出</span></span></label>` : ""}
      ${planList.map(p => { const n = planNodeCount(p, nodes, inbounds); return H`<label class="plan"><input type="radio" name="plan" value="${p.id}" ${raw(plan === String(p.id) ? "checked" : "")}><span class="rd"></span><span class="plan-t"><b>${p.name}</b>${p.note || p.is_default ? H`<span class="sub">${p.note || "新用户默认挂这个"}</span>` : ""}</span><span class="plan-n ${n ? "" : "zero"}">${n ? `${n} 个节点` : "没有节点"}</span></label>`; })}</div>
      ${planList.filter(p => !planNodeCount(p, nodes, inbounds)).map(p => H`<div class="block" role="alert" data-plan="${p.id}" hidden>${ic("warn")}<div><b>「${p.name}」里还没有节点</b><p>挂在它上面的用户什么都连不上。先给套餐加节点,或者换一个套餐。</p><div class="block-a"><a class="btn sm" href="#users/plans">${ic("plus")}给「${p.name}」加节点</a>${allPlan ? H`<button type="button" class="btn sm ghost" data-act="f-plan-all">换成「${allPlan.name}」</button>` : ""}</div></div></div>`)}</div>
    <div class="two"><div class="field"><label for="f-limit">流量上限</label><input class="in" id="f-limit" name="limit" value="${limitText}" placeholder="100GB,留空不限"><span class="h">B / KB / MB / GB / TB,按 1024 进位</span></div>
    <div class="field"><label for="f-expire">到期日</label><input class="in" id="f-expire" name="expire" value="${u.expire_at ? ymd(new Date(u.expire_at)) : ""}" placeholder="YYYY-MM-DD,留空永不"><span class="h">本机时区当天 23:59:59</span></div></div>
    <details class="fmts more" id="f-more"><summary>${ic("arrow", "car")}更多选项<span class="sub">清零周期、额外授权、备注</span></summary><div class="more-b">
    <div class="field"><label>流量重置</label><div class="chips">${Object.entries(RESET).map(([k, t]) => H`<label class="chip"><input type="radio" name="reset" value="${k}" ${(u.reset_strategy || "none") === k ? raw("checked") : ""}>${t}</label>`)}</div></div>
    <div class="field"><label id="f-inb-label">额外授权</label><div class="chips">${inbounds.length ? inbounds.map(i => H`<label class="chip"><input type="checkbox" name="inb" value="${i.id}" ${sel.has(i.id) ? raw("checked") : ""}>${nodeName(i.node_id)} · ${inbTitle(i, null)}</label>`) : H`<span class="sub">还没有入站</span>`}</div><span class="h" id="f-inb-h"></span></div>
    <div class="field"><label for="f-note">备注</label><input class="in" id="f-note" name="note" value="${u.note || ""}" placeholder="可选"></div>
    ${isNew ? H`<label class="chk"><input type="checkbox" name="disabled"> 先不启用(建好后手动启用)</label>` : ""}</div></details>
    <div class="err" id="f-err" role="alert"></div>
  </div><div class="df"><span class="sub df-why" id="f-why" hidden>${ic("warn")}选的套餐没有节点</span><span class="sp" style="flex:1"></span><button type="button" class="btn" data-act="close">取消</button><button type="submit" class="btn pri" id="f-save">${isNew ? "创建" : "保存"}</button></div></form>`);
  const form = $("#user-form");
  // 表单 → { ... }:collect 同时把套餐三态收出来 —— 没变就不发 plan_id,变了才发具体 id。
  // 挂的是 all_nodes 套餐时额外授权没有意义:勾了和它一起提交会被控制面摘掉
  // 套餐(store.UpdateUser 的规则),于是固定按空处理 —— 新建不带 inbound_ids,
  // 编辑清空,命令里也不出现 --inbounds(编辑时原本有授权才用 --inbounds "" 清掉)。
  const collect = () => {
    const f = new FormData(form), err = [];
    const username = isNew ? String(f.get("username") || "").trim() : u.username;
    const limitS = String(f.get("limit") || "").trim(), expS = String(f.get("expire") || "").trim();
    const reset = String(f.get("reset") || "none"), note = String(f.get("note") || "");
    const planVal = String(f.get("plan") || "");
    const allNodes = !!planList.find(p => String(p.id) === planVal)?.all_nodes;
    const inb = allNodes ? [] : f.getAll("inb").map(Number);
    const planChanged = isNew ? planVal !== "" : (planVal !== "" && planVal !== currentPlan);
    const planName = planList.find(p => String(p.id) === planVal)?.name || "";
    let limit = 0, exp = null;
    if (isNew && !USERNAME_RE.test(username)) err.push("用户名只能用字母、数字、下划线、点和减号,长度 1-64");
    try { limit = limitS ? parseBytes(limitS) : 0; } catch (e) { err.push(e.message); }
    try { exp = expS ? parseExpire(expS) : null; } catch (e) { err.push(e.message); }
    return { err, username, limit, exp, reset, inb, note, disabled: !!f.get("disabled"), planVal, planChanged };
  };
  const sync = () => {
    const c = collect();
    const p = planList.find(x => String(x.id) === c.planVal);
    const allNodes = !!p && !!p.all_nodes;
    // all_nodes 套餐下额外授权不可选(见 collect),切回普通套餐恢复。
    document.querySelectorAll('#user-form input[name="inb"]').forEach(el => { el.disabled = allNodes; });
    // 选了没有节点的套餐:灰色提示块 + 创建按钮置灰(原因写在底栏)。编辑老用户不拦保存。
    const empty = !!p && !planNodeCount(p, nodes, inbounds);
    form.querySelectorAll(".block[data-plan]").forEach(b => { b.hidden = b.dataset.plan !== c.planVal; });
    if (isNew) { $("#f-why").hidden = !empty; $("#f-save").disabled = empty; }
    const label = $("#f-inb-label"), hint = $("#f-inb-h");
    if (label && hint) {
      if (!p) { label.textContent = "可用入站"; hint.textContent = "一个都不选 = 全部入站,含以后新加的"; }
      else if (allNodes) { label.textContent = "额外授权"; hint.textContent = "全部节点套餐不需要额外授权"; }
      else { label.textContent = "额外授权(可选)"; hint.textContent = "在套餐之外再给这个用户开放的入站"; }
    }
  };
  form.addEventListener("input", sync); form.addEventListener("change", sync); sync();
  // 有已填的低频字段时「更多选项」默认展开,免得编辑时看不到。
  if (!isNew && (u.reset_strategy && u.reset_strategy !== "none" || u.inbound_ids?.length || u.note)) $("#f-more").open = true;
  form.querySelector('[data-act="f-plan-all"]') && form.addEventListener("click", e => {
    if (!e.target.closest('[data-act="f-plan-all"]')) return;
    const r = form.querySelector(`input[name="plan"][value="${allPlan.id}"]`); r.checked = true; r.focus(); sync();
  });
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const c = collect(), btn = $("#f-save");
    if (btn.disabled) return;
    $("#f-err").textContent = c.err.join("\n");
    if (c.err.length) return;
    btn.disabled = true; btn.classList.add("busy");
    try {
      if (isNew) {
        // 与 user add 一致:没给的字段保持零值,model.User 整体序列化;选中的套餐
        // 显式带上 plan_id。
        const body = asModelUser({ username: c.username, enabled: !c.disabled, reset_strategy: c.reset, note: c.note, data_limit: c.limit, expire_at: c.exp, inbound_ids: c.inb });
        if (c.planChanged) body.plan_id = +c.planVal;
        await write("POST", "/api/users", body);
        // 同一个抽屉直接切到这个用户的详情,顶上写「已创建」,下面就是「发给他」。
        JUST_CREATED = c.username;
        location.hash = "users/" + encodeURIComponent(c.username);
      } else {
        await readModify(u.username, m => {
          m.data_limit = c.limit;
          if (c.exp) m.expire_at = c.exp; else delete m.expire_at;
          m.reset_strategy = c.reset;
          if (c.inb.length) m.inbound_ids = c.inb; else delete m.inbound_ids;
          if (c.note) m.note = c.note; else delete m.note;
          if (c.planChanged) m.plan_id = +c.planVal;
        });
        toast(`已更新用户 ${u.username}`);
        await refreshDrawer(u.username);
      }
    } catch (e2) { $("#f-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

/* ---- 动作 ---- */
Object.assign(ACTS, {
  "user-filter": d => { US.status = d.status; US.offset = 0; load(true); },
  "user-page": d => { US.offset = Math.max(0, US.offset + PAGE_SIZE * +d.dir); load(true); },
  "user-new": () => userEditor(null),
  "user-edit": d => userEditor(d.name),
  "user-export": () => confirmBox({ title: "导出 CSV", text: "导出用户清单暂不支持在窗口里操作。", ok: "知道了", run: async () => {} }),
  "user-renew": async d => {
    try {
      let target;
      await readModify(d.name, m => { target = renewTarget(m.expire_at, 30); m.expire_at = target; });
      toast(`已把用户 ${d.name} 的到期日设为 ${ymd(new Date(target))}`);
      await refreshDrawer(d.name);
    } catch (e) { toast(e.message, true); }
  },
  "user-toggle": d => {
    const off = !!d.on, verb = off ? "停用" : "启用";
    const run = async () => { await readModify(d.name, m => { m.enabled = !off; }); toast(`已${verb}用户 ${d.name}`); await refreshDrawer(d.name); };
    if (!off) return run().catch(e => toast(e.message, true));
    confirmBox({ title: `停用 ${d.name}?`, text: "下一轮收敛(最多 30 秒)把他从所有节点上摘掉,订阅里只剩「已停用」占位节点。记录保留,随时可以再启用。", ok: "停用", run });
  },
  "user-reset": d => confirmBox({ title: `重置 ${d.name} 的本期流量?`, text: "本期用量清零,累计用量不变。超限的用户会在下一轮收敛时恢复。", ok: "重置流量",
    run: async () => { await write("POST", userPath(d.name) + "/reset-traffic", undefined, `已重置用户 ${d.name} 的流量`); await refreshDrawer(d.name); } }),
  // 换链接不是删除:不用红色,但要确认 —— 旧链接马上失效,他得重新导入。
  "user-revoke": d => confirmBox({ title: `给 ${d.name} 换一条新链接?`, text: "旧链接马上失效,他的客户端要重新扫码或粘贴新链接才能更新订阅。已经连着的不会断。", ok: "换一条新链接",
    run: async () => { await write("POST", userPath(d.name) + "/revoke-sub", undefined, "已换成新链接,旧链接已失效"); await refreshDrawer(d.name); } }),
  // ⋯ 菜单:点按钮开合;点菜单里的项或别处就收起。
  "user-menu": () => { const m = $("#user-menu"); if (m) m.hidden = !m.hidden; },
  "user-rm": d => confirmBox({ title: `删除 ${d.name}?`, text: "不可恢复:协议凭证与订阅 token 一起删掉,已经连上的客户端会断开,订阅地址立刻 404。只想让人连不上,用「停用」。", ok: "删除用户", danger: true, typed: d.name,
    run: async () => { await write("DELETE", userPath(d.name), undefined, `已删除用户 ${d.name}`); closeOverlays(); location.hash = "users"; } }),
});
document.addEventListener("keydown", e => { if (e.key === "Enter") { const tr = e.target.closest?.("tr[data-href]"); if (tr) location.hash = tr.dataset.href; } });
document.addEventListener("click", e => { const m = $("#user-menu"); if (m && !m.hidden && (!e.target.closest(".mw") || e.target.closest("#user-menu button"))) m.hidden = true; });
