// 套餐页:列表、新建/编辑抽屉、删除时改挂去向。每个写操作对应一条 mpx plan
// 命令,发出的请求与 CLI 一致(见 e2e/plans.spec.mjs)。
//
// 判定一律以控制面为准:套餐的授权范围由 node_ids / inbound_ids / all_nodes 三个
// 字段表达,页面只负责把它们摆成节点显示名与入站。用户数、活跃数与额外授权数来自
// GET /api/plans(active_users 由存储层按 User.Status 统计)。
"use strict";

/* ---- 共享给用户页的套餐解析 ---- */
// PLAN_CACHE 是页面之间共用的一份原始数据:套餐、节点、入站、用户(≤1000)。
// 用户页与套餐页各自 load 时刷新它,渲染只读这里的值。loaded 用来让用户页在
// 进入时按需取一次套餐/入站(控制面 /api 按 IP 限流,能省一次是一次)。
const PLAN_CACHE = { plans: [], nodes: [], inbounds: [], users: [], loaded: false };

const planNodeName = n => (n && (n.display || n.name)) || "";
const planInboundLabel = i => i.tag || i.protocol || ("#" + i.id);

// planEffectiveSet 返回套餐当前能用的入站 id 集合;all_nodes 时返回 null 表示「全部」。
function planEffectiveSet(p, inbounds) {
  if (p.all_nodes) return null;
  const set = new Set(p.inbound_ids || []);
  const nodes = new Set(p.node_ids || []);
  for (const i of inbounds) if (nodes.has(i.node_id)) set.add(i.id);
  return set;
}

// planExtraCount 数用户自己的额外授权里,不在套餐范围内的入站数。套餐是
// all_nodes 时没有「额外」可言,固定为 0(不显示 +N)。
function planExtraCount(u, p, inbounds) {
  if (!p || p.all_nodes) return 0;
  const set = planEffectiveSet(p, inbounds);
  return (u.inbound_ids || []).filter(id => !set.has(id)).length;
}

// userEffectiveSet 返回一个用户实际能用的入站 id 集合;返回 null 表示「全部」。
function userEffectiveSet(u, plans, inbounds) {
  const p = (plans || []).find(x => x.id === u.plan_id);
  if (!p) return (u.inbound_ids || []).length ? new Set(u.inbound_ids) : null;
  if (p.all_nodes) return null;
  const set = planEffectiveSet(p, inbounds);
  for (const id of u.inbound_ids || []) set.add(id);
  return set;
}

// activeOccupancy 按与订阅渲染相同的规则,数各入站此刻占着几个名额:只数状态为
// active 的用户,授权范围 = 套餐 ∪ 额外授权(没挂套餐且没有额外授权即全部)。
function activeOccupancy(inbounds, users, plans) {
  const occ = {};
  for (const i of inbounds) occ[i.id] = 0;
  for (const u of users || []) {
    if (u.status !== "active") continue;
    const set = userEffectiveSet(u, plans, inbounds);
    for (const i of inbounds) if (!set || set.has(i.id)) occ[i.id]++;
  }
  return occ;
}

const planCountText = (users, active) => `${users}（活跃 ${active}）`;

/* ---- 套餐列表 ---- */
PAGES.plans = {
  title: "套餐", nav: ["管理", "plan", 15], closeTo: "plans",
  count: () => PLAN_CACHE.plans.length || "",
  async load() {
    const [plans, nodes, inbounds, users] = await Promise.all([
      api("/api/plans"), api("/api/nodes"), api("/api/inbounds"), api("/api/users?limit=1000"),
    ]);
    PLAN_CACHE.plans = plans || [];
    PLAN_CACHE.nodes = nodes || [];
    PLAN_CACHE.inbounds = inbounds || [];
    PLAN_CACHE.users = users.users || [];
    PLAN_CACHE.loaded = true;
    return { plans: PLAN_CACHE.plans, nodes: PLAN_CACHE.nodes, inbounds: PLAN_CACHE.inbounds, users: PLAN_CACHE.users };
  },
  render(d) {
    const W = can("POST", "/api/plans");
    const head = H`<div class="ph"><div><h1>套餐</h1><p>决定用户能用哪些节点。挂了套餐的用户只拿得到套餐里的节点</p></div><span class="sp"></span>
      ${W ? H`<button class="btn pri" data-act="pl-edit" data-id="">${ic("plus")}新建套餐</button>` : ""}</div>`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(4, 3)}</tbody></table></div>`;
    const plans = d.plans.slice().sort((a, b) => (b.is_default - a.is_default) || (a.id - b.id));
    const list = H`<div class="card">${plans.map(p => plRow(p, d))}</div>`;
    const only = plans.length === 1 && plans[0].is_default;
    const p0 = plans[0];
    return H`${head}${list}${only ? H`<div class="callout stone mt">${ic("globe")}<div>所有 ${planCountText(p0.users, p0.active_users)} 个用户都在默认套餐,能用全部节点。要分开,就新建套餐。</div></div>` : ""}`;
  },
  after(d) {
    if (d) document.querySelectorAll("#pl-nodes [data-ind]").forEach(x => { x.indeterminate = true; });
  },
};

function plRow(p, d) {
  return H`<div class="pl-row" data-act="pl-edit" data-id="${p.id}">
    <div class="pl-name"><b title="${p.name}">${p.name}${p.is_default ? H` <span class="pl-def">默认</span>` : ""}</b>${p.note ? H`<span title="${p.note}">${p.note}</span>` : ""}</div>
    <div class="pl-inc">${plIncTags(p, d)}${plWarnTags(p, d)}</div>
    <a class="pl-uc ${p.users ? "" : "zero"}" href="#users" data-act="pl-users" data-id="${p.id}">${ic("users")}${planCountText(p.users, p.active_users)}</a>${ic("arrow")}</div>`;
}

// plIncTags 把套餐包含的节点/入站摆成标签:整节点用显示名;只勾部分入站时用
// 「显示名 · 仅 协议」。all_nodes 与空套餐各有一个专门的标签。
function plIncTags(p, d) {
  if (p.all_nodes) return H`<span class="tag all">${ic("globe")}全部节点(含以后新加的)</span>`;
  const covered = new Set(p.node_ids || []);
  if (!covered.size && !(p.inbound_ids || []).length) return H`<span class="tag none">${ic("ban")}无可用节点</span>`;
  const byNode = new Map();
  const nodeByID = new Map((d.nodes || []).map(n => [n.id, n]));
  for (const nid of p.node_ids || []) byNode.set(nid, "all");
  for (const iid of p.inbound_ids || []) {
    const inb = (d.inbounds || []).find(i => i.id === iid);
    if (!inb) continue;
    if (byNode.get(inb.node_id) === "all") continue;
    if (!byNode.has(inb.node_id)) byNode.set(inb.node_id, []);
    byNode.get(inb.node_id).push(iid);
  }
  const tags = [];
  for (const [nid, sel] of byNode) {
    const name = planNodeName(nodeByID.get(nid)) || ("#" + nid);
    if (sel === "all") tags.push(H`<span class="tag">${name}</span>`);
    else {
      const protos = sel.map(id => { const inb = (d.inbounds || []).find(i => i.id === id); return inb ? planInboundLabel(inb) : "#" + id; }).join("、");
      tags.push(H`<span class="tag">${name} · 仅 ${protos}</span>`);
    }
  }
  return tags;
}

// plWarnTags 是列表里的警告徽标:空套餐还挂着人(红)、含排水中节点(灰)。
function plWarnTags(p, d) {
  const out = [];
  const empty = !p.all_nodes && !(p.node_ids || []).length && !(p.inbound_ids || []).length;
  if (empty && p.users) out.push(H`<span class="tag none">${ic("warn")}挂着的 ${p.users} 个用户什么都用不了</span>`);
  if (!p.all_nodes) {
    const nodes = new Set(p.node_ids || []);
    for (const inb of d.inbounds || []) if ((p.inbound_ids || []).includes(inb.id)) nodes.add(inb.node_id);
    const draining = (d.nodes || []).some(n => n.draining && nodes.has(n.id));
    if (draining) out.push(H`<span class="tag g">含排水中的节点</span>`);
  }
  return out;
}

/* ---- 新建 / 编辑抽屉 ---- */
// ED 是当前编辑中的套餐草稿;sel 把节点映射到 "all"(整节点)或入站 id 数组。
let ED = null, PL_EXP = new Set();

function planEditor(id) {
  const p0 = (PLAN_CACHE.plans || []).find(p => p.id === +id) || null;
  ED = p0
    ? { id: p0.id, name: p0.name, note: p0.note || "", all_nodes: !!p0.all_nodes, is_default: !!p0.is_default, sel: {} }
    : { id: 0, name: "", note: "", all_nodes: false, is_default: false, sel: {} };
  if (p0) {
    for (const nid of p0.node_ids || []) ED.sel[nid] = "all";
    for (const iid of p0.inbound_ids || []) {
      const inb = PLAN_CACHE.inbounds.find(i => i.id === iid);
      if (!inb) continue;
      if (ED.sel[inb.node_id] === "all") continue;
      if (!Array.isArray(ED.sel[inb.node_id])) ED.sel[inb.node_id] = [];
      ED.sel[inb.node_id].push(iid);
    }
  }
  PL_EXP = new Set(Object.entries(ED.sel).filter(([, v]) => Array.isArray(v)).map(([k]) => +k));
  const used = p0 ? PLAN_CACHE.users.filter(u => u.plan_id === p0.id) : [];
  const W = can(p0 ? "PUT" : "POST", "/api/plans");
  openDrawer(H`<form id="pl-form"><div class="dh"><h2 class="ell">${p0 ? p0.name : "新建套餐"}</h2>${p0?.is_default ? H`<span class="pl-def">默认</span>` : ""}${p0 ? H`<span class="pill ${used.length ? "on" : ""}">${planCountText(p0.users, p0.active_users)} 个用户在用</span>` : ""}<span style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db"><div id="pl-warn"></div>
    <div class="pl-ed"><div>
      <div class="two">
        <div class="field"><label for="pl-name">名字</label><input class="in" id="pl-name" value="${ED.name}" ${ED.is_default ? raw("readonly") : ""} placeholder="美日" autocomplete="off" ${W ? "" : raw("disabled")}></div>
        <div class="field"><label for="pl-note">备注</label><input class="in" id="pl-note" value="${ED.note}" placeholder="可选" autocomplete="off" ${W ? "" : raw("disabled")}></div>
      </div>
      <div class="pl-all"><div class="grow"><b>全部节点</b><span>包括以后新加的节点</span></div>
        <label class="sw"><input type="checkbox" id="pl-all" ${ED.all_nodes ? raw("checked") : ""} ${W ? "" : raw("disabled")}><i></i></label></div>
      <div class="pl-sec">节点<small id="pl-cnt"></small></div><div id="pl-nodes"></div>
    </div><div id="pl-pv"></div></div></div>
    <div class="err" id="pl-err" role="alert"></div></div>
  <div class="df">${p0?.is_default ? H`<span class="sub">新用户自动进这里 · 不能删除</span>` : p0 && can("DELETE", "/api/plans") ? H`<button type="button" class="btn ghost c-bad" data-act="pl-del" data-id="${p0.id}">${ic("trash")}删除</button>` : ""}<span style="flex:1"></span>
    <div class="cmd hide-m" id="pl-cmd" style="max-width:420px"></div>${W ? H`<button type="button" class="btn ghost" data-act="close">取消</button><button class="btn pri" id="pl-save">保存</button>` : H`<button type="button" class="btn" data-act="close">关闭</button>`}</div></form>`, "wide");
  const f = $("#pl-form");
  if (!f) return;
  f.addEventListener("input", e => {
    if (e.target.id === "pl-name") { ED.name = e.target.value; plSync(); }
    // 备注也进等价命令(edit 的 --note),跟着刷。
    if (e.target.id === "pl-note") { ED.note = e.target.value; plSync(); }
  });
  f.addEventListener("change", e => {
    const t = e.target;
    if (t.id === "pl-all") ED.all_nodes = t.checked;
    else if (t.dataset.node != null) { if (t.checked) ED.sel[t.dataset.node] = "all"; else delete ED.sel[t.dataset.node]; }
    else if (t.dataset.ib != null) {
      const inb = PLAN_CACHE.inbounds.find(i => i.id === +t.dataset.ib);
      if (!inb) return;
      const cur = ED.sel[inb.node_id] === "all" ? PLAN_CACHE.inbounds.filter(i => i.node_id === inb.node_id).map(i => i.id) : (ED.sel[inb.node_id] || []);
      const nx = t.checked ? [...cur, inb.id] : cur.filter(x => x !== inb.id);
      if (nx.length) ED.sel[inb.node_id] = PLAN_CACHE.inbounds.filter(i => i.node_id === inb.node_id && nx.includes(i.id)).map(i => i.id);
      else delete ED.sel[inb.node_id];
    } else return;
    plSync();
  });
  f.addEventListener("submit", async e => {
    e.preventDefault();
    const name = ED.name.trim(), btn = $("#pl-save");
    if (!name) { $("#pl-name").classList.add("bad"); $("#pl-err") && ($("#pl-err").textContent = "名字不能为空"); return; }
    $("#pl-name").classList.remove("bad");
    btn.disabled = true; btn.classList.add("busy");
    try {
      const body = plBody(p0);
      await write(p0 ? "PUT" : "POST", p0 ? "/api/plans/" + p0.id : "/api/plans", body, `套餐「${name}」已保存`);
      closeOverlays();
    } catch (e2) { $("#pl-name").classList.remove("bad"); $("#pl-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
  plSync();
}

// plBody 造出与 CLI 相同的 model.Plan JSON:新建带 id:0 / is_default:false /
// users:0 / extra_auth:0;编辑沿用读取到的计数与 is_default,只改可编辑字段。
// omitempty 的字段(空备注、空数组)两边都不发。
function plBody(p0) {
  const sel = ED.all_nodes ? {} : ED.sel;
  const nodeIDs = Object.keys(sel).filter(k => sel[k] === "all").map(Number).sort((a, b) => a - b);
  const inbIDs = Object.keys(sel).filter(k => Array.isArray(sel[k])).flatMap(k => sel[k]).sort((a, b) => a - b);
  const body = p0
    ? { id: p0.id, name: ED.name.trim(), all_nodes: !!ED.all_nodes, is_default: !!p0.is_default, users: p0.users, active_users: p0.active_users, extra_auth: p0.extra_auth }
    : { id: 0, name: ED.name.trim(), all_nodes: !!ED.all_nodes, is_default: false, users: 0, active_users: 0, extra_auth: 0 };
  if (ED.note.trim()) body.note = ED.note.trim();
  if (nodeIDs.length) body.node_ids = nodeIDs;
  if (inbIDs.length) body.inbound_ids = inbIDs;
  return body;
}

function plSync() {
  const e = ED, p0 = (PLAN_CACHE.plans || []).find(p => p.id === e.id);
  const users = p0 ? PLAN_CACHE.users.filter(u => u.plan_id === p0.id) : [];
  const empty = !e.all_nodes && !Object.keys(e.sel).length;
  const warn = $("#pl-warn");
  if (warn) warn.innerHTML = val(empty ? H`<div class="callout mb">${ic("warn", "ico-warn")}<div><b>空套餐</b>:${e.is_default ? `默认套餐里的用户和以后新建的用户都将无法使用任何节点` : "挂着的用户将无法使用任何节点"}</div></div>` : "");
  const cntEl = $("#pl-cnt");
  if (cntEl) cntEl.textContent = e.all_nodes ? "" : `· 已选 ${Object.keys(e.sel).length} / ${PLAN_CACHE.nodes.length}`;
  const nodesEl = $("#pl-nodes");
  if (nodesEl) nodesEl.innerHTML = val(plNodes());
  document.querySelectorAll("#pl-nodes [data-ind]").forEach(x => { x.indeterminate = true; });
  const pvEl = $("#pl-pv");
  if (pvEl) pvEl.innerHTML = val(plPreview(users));
  const cmdEl = $("#pl-cmd");
  if (cmdEl) cmdEl.textContent = plCmd(!p0);
}

function plNodes() {
  const e = ED, off = e.all_nodes;
  const occ = activeOccupancy(PLAN_CACHE.inbounds, PLAN_CACHE.users, PLAN_CACHE.plans);
  return H`<div class="pl-nodes ${off ? "off" : ""}">${PLAN_CACHE.nodes.map(n => {
    const sel = off ? "all" : e.sel[n.id], part = Array.isArray(sel), exp = PL_EXP.has(n.id);
    const tail = !sel ? "" : part ? `已选 ${sel.length} / ${PLAN_CACHE.inbounds.filter(i => i.node_id === n.id).length} 个入站` : "含以后新加的入站";
    return H`<div class="pl-n ${exp ? "exp" : ""}">
      <label><input type="checkbox" data-node="${n.id}" ${sel ? raw("checked") : ""} ${part ? raw("data-ind=1") : ""} ${off || !can("PUT", "/api/plans") ? raw("disabled") : ""}><b>${planNodeName(n)}</b>${nodeSt(n)}</label>
      <span class="tail">${tail}</span><button type="button" class="ib sm" data-act="pl-exp" data-id="${n.id}" aria-label="展开入站">${ic("arrow")}</button></div>
      ${exp ? H`<div class="pl-ibs">${PLAN_CACHE.inbounds.filter(i => i.node_id === n.id).map(i => H`<label class="pl-i"><input type="checkbox" data-ib="${i.id}" ${sel === "all" || (part && sel.includes(i.id)) ? raw("checked") : ""} ${off || !can("PUT", "/api/plans") ? raw("disabled") : ""}><span>${planInboundLabel(i)}</span><span class="mono">:${i.port}</span>${plFullTag(i, occ)}</label>`)}
        ${PLAN_CACHE.inbounds.some(i => i.node_id === n.id) ? "" : H`<div class="pl-i sub">这台节点还没有入站</div>`}</div>` : ""}`;
  })}</div>`;
}

function plFullTag(i, occ) {
  if (!i.max_users) return "";
  return occ[i.id] >= i.max_users ? H`<span class="tag w">满员</span>` : "";
}

function plPreview(users) {
  const e = ED;
  const auth = e.all_nodes ? PLAN_CACHE.inbounds.slice() : PLAN_CACHE.inbounds.filter(i => e.sel[i.node_id] === "all" || (Array.isArray(e.sel[i.node_id]) && e.sel[i.node_id].includes(i.id)));
  const occ = activeOccupancy(PLAN_CACHE.inbounds, PLAN_CACHE.users, PLAN_CACHE.plans);
  const nodeSet = [...new Set(auth.map(i => i.node_id))];
  const why = i => { const n = PLAN_CACHE.nodes.find(x => x.id === i.node_id); if (n && n.draining) return "排水中"; if (i.max_users && occ[i.id] >= i.max_users) return "满员"; return ""; };
  const ulist = users.length ? H`<div class="pl-sub"><div class="pl-sec">在用的用户</div><div class="pl-users">${users.slice(0, 12).map(u => H`<span class="tag">${u.username}</span>`)}${users.length > 12 ? H`<span class="tag g">+${users.length - 12}</span>` : ""}</div></div>` : "";
  return H`<div class="pl-pv"><h5>挂这个套餐的用户会拿到<small>${auth.length ? `· ${auth.length} 个入口` : ""}</small></h5>
    ${auth.length ? nodeSet.map(nid => { const n = PLAN_CACHE.nodes.find(x => x.id === nid); return H`<div class="pl-g"><b>${planNodeName(n) || "#" + nid}</b>${auth.filter(i => i.node_id === nid).map(i => { const w = why(i); return H`<div class="pl-e ${w ? "dim" : ""}"><span class="pp-mk ${w ? "" : "ok"}">${ic(w ? "ban" : "check")}</span>${planInflightName(n, i)}<span class="sp"></span><span class="why">${w}</span></div>`; })}</div>`; })
      : H`<div class="pl-none"><span class="ill">${ic("ban")}</span><b>无可用节点</b><span>${users.length ? `${planCountText(users.length, users.filter(u => u.status === "active").length)} 个用户将断开` : "挂上的用户什么都用不了"}</span></div>`}
    ${ulist}</div>`;
}

const planInflightName = (n, i) => `${planNodeName(n) || "#" + i.node_id} · ${planInboundLabel(i)}`;

// planNodeRef / planInboundRef 把 id 摆成 CLI 认得的参数:节点用显示名
// (resolveNodeID 收 id、名字、显示名或唯一前缀),入站用「节点名/tag」;tag 缺失
// 或节点对不上时退回入站 id(resolveInboundID 两种都收,见 internal/cli/resolve.go)。
const planNodeRef = nid => { const n = PLAN_CACHE.nodes.find(x => x.id === +nid); return planNodeName(n) || String(nid); };
const planInboundRef = iid => {
  const i = PLAN_CACHE.inbounds.find(x => x.id === +iid);
  const n = i && PLAN_CACHE.nodes.find(x => x.id === i.node_id);
  return i && i.tag && n ? `${planNodeName(n)}/${i.tag}` : String(iid);
};

// plCmd 生成等价命令,只作展示;真实请求是 POST/PUT /api/plans 的整体模型。
// add 直接摆最终状态;edit 按「改前 → 改后」的差异给 flag,与 mpx plan edit 的
// 选项一一对应,没有任何变化时就只剩 mpx plan edit 名字 —— 第一个参数是改前的
// 名字,新名字放在 --name。
function plCmd(isNew) {
  const e = ED;
  // all_nodes 时请求体不带节点/入站(见 plBody),差异按清空了算。
  const sel = e.all_nodes ? {} : e.sel;
  const nodes = Object.keys(sel).filter(k => sel[k] === "all").map(Number).sort((a, b) => a - b);
  const inbs = Object.keys(sel).filter(k => Array.isArray(sel[k])).flatMap(k => sel[k]).sort((a, b) => a - b);
  if (isNew) {
    let s = `mpx plan add ${shq(e.name.trim() || "名字")}`;
    for (const nid of nodes) s += ` --node ${shq(planNodeRef(nid))}`;
    for (const iid of inbs) s += ` --inbound ${shq(planInboundRef(iid))}`;
    if (e.note.trim()) s += ` --note ${shq(e.note.trim())}`;
    if (e.all_nodes) s += " --all-nodes";
    return s;
  }
  const p0 = (PLAN_CACHE.plans || []).find(p => p.id === e.id);
  if (!p0) return `mpx plan edit ${shq(e.name.trim() || "名字")}`;
  const oldNodes = (p0.node_ids || []).slice().sort((a, b) => a - b);
  const oldInbs = (p0.inbound_ids || []).slice().sort((a, b) => a - b);
  const name = e.name.trim();
  let s = `mpx plan edit ${shq(p0.name)}`;
  for (const nid of nodes.filter(x => !oldNodes.includes(x))) s += ` --add-node ${shq(planNodeRef(nid))}`;
  for (const nid of oldNodes.filter(x => !nodes.includes(x))) s += ` --rm-node ${shq(planNodeRef(nid))}`;
  for (const iid of inbs.filter(x => !oldInbs.includes(x))) s += ` --add-inbound ${shq(planInboundRef(iid))}`;
  for (const iid of oldInbs.filter(x => !inbs.includes(x))) s += ` --rm-inbound ${shq(planInboundRef(iid))}`;
  if (name && name !== p0.name) s += ` --name ${shq(name)}`;
  if (e.note.trim() !== (p0.note || "")) s += ` --note ${e.note.trim() ? shq(e.note.trim()) : '""'}`;
  if (!!e.all_nodes !== !!p0.all_nodes) s += e.all_nodes ? " --all-nodes" : " --all-nodes=false";
  return s;
}

/* ---- 删除:有用户时必须选改挂去向,没有「移出套餐」 ---- */
function planDelete(id) {
  const p0 = PLAN_CACHE.plans.find(p => p.id === +id);
  if (!p0) return;
  const used = PLAN_CACHE.users.filter(u => u.plan_id === p0.id);
  const others = PLAN_CACHE.plans.filter(x => x.id !== p0.id).sort((a, b) => (b.is_default - a.is_default) || (a.id - b.id));
  const def = others.find(x => x.is_default) || others[0];
  confirmBox({
    title: `删除「${p0.name}」`, danger: true, ok: "删除",
    text: used.length ? `${planCountText(used.length, used.filter(u => u.status === "active").length)} 个用户挂着这个套餐。` : "没有用户挂着这个套餐。",
    extra: used.length ? H`<div class="field"><label for="pl-to">这些用户改挂到</label><select class="in" id="pl-to">${others.map(x => H`<option value="${x.id}" ${raw(x.id === def?.id ? "selected" : "")}>「${x.name}」</option>`)}</select></div>` : "",
    cmdOf: m => {
      const v = $("#pl-to", m)?.value;
      return `mpx plan rm ${shq(p0.name)}${used.length && v ? ` --reassign ${shq((PLAN_CACHE.plans.find(x => x.id === +v) || {}).name || "")}` : ""}`;
    },
    run: async () => {
      const v = used.length ? $("#pl-to")?.value : "";
      const path = "/api/plans/" + p0.id + (v ? "?reassign=" + v : "");
      await write("DELETE", path, undefined, `已删除「${p0.name}」`);
      closeOverlays();
    },
  });
}

Object.assign(ACTS, {
  "pl-edit": d => planEditor(d.id),
  "pl-exp": d => { const id = +d.id; if (PL_EXP.has(id)) PL_EXP.delete(id); else PL_EXP.add(id); plSync(); },
  "pl-del": d => planDelete(d.id),
  "pl-users": (d, a, e) => { e.stopPropagation(); US.plan = String(d.id); US.offset = 0; US.status = ""; US.search = ""; location.hash = "users"; },
});
