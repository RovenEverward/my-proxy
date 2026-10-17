// 节点页:列表、详情,以及 add / edit / rm / sync / install / drain / undrain / diagnose。
// 每个写操作发出的请求与对应的 mpx node 命令一致;节点状态、在线、配置预览、体检报告
// 只读。下发、安装、体检真去 SSH,失败时把控制面的原话(含 501 没接下发通道)报出来。
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
  return s.version ? `xray ${s.active ? "运行中" : "未运行"}` : "没装核心";
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
      // 体检报告没有就是 404,不算页面错误。
      const diag = api(nodePath(id) + "/diagnostics?from=node").catch(e => e.status === 404 ? null : { error: e.message });
      // 连接方式用入站页同一个组件(conn.js),数据也由它取。
      const [node, conn, report] = await Promise.all([api(nodePath(id)), CONN.load([id]), diag]);
      return { node, inbounds: conn.inbounds, conn, report };
    }
    const nodes = await api("/api/nodes");
    NODES_TOTAL = nodes.length;
    return { nodes };
  },
  render(d, r) { return r[1] ? nodeDetail(d) : nodeList(d); },
};
let NODES_TOTAL = null;

function nodeList(d) {
  const W = can("POST", "/api/nodes");
  const head = H`<tr><th>节点</th><th>状态</th><th class="hide-m">核心</th><th class="hide-m">配置</th><th class="hide-m">SSH</th><th class="num hide-m">检查于</th></tr>`;
  let inner;
  if (!d) inner = H`<table><thead>${head}</thead><tbody>${skRows(5, 6)}</tbody></table>`;
  else if (!d.nodes.length) inner = emptyBox("nodes", "还没有节点", "填 VPS 的地址和 SSH 密码，自动装好并加进订阅。点下面的「添加节点」开始。", "", W ? H`<div style="display:grid;gap:10px;justify-items:center"><button class="btn pri sm" data-act="node-wizard">${ic("plus")}添加节点</button><a class="lnk wlnk" data-act="node-new" href="#">已能免密 SSH，直接登记</a></div>` : "");
  else inner = H`<div class="tw"><table><thead>${head}</thead><tbody>${d.nodes.map(n => H`<tr data-href="#nodes/${n.id}" tabindex="0"><td><b class="bold">${nodeLabel(n)}</b><div class="sub mono">${n.address}</div></td><td>${nodeSt(n)}</td>
    <td class="hide-m">${n.status?.cores?.length ? n.status.cores.map(c => H`<span class="tag">${c.core}</span> `) : n.status?.version ? H`<span class="tag">xray</span> <span class="sub">${n.status.version}</span>` : H`<span class="sub">—</span>`}</td>
    <td class="hide-m">${driftCell(n)}</td><td class="hide-m mono sub">${sshText(n)}</td><td class="num sub hide-m">${n.status ? sinceText(n.status.checked_at) : "—"}</td></tr>`)}</tbody></table></div>`;
  // 「批量识别显示名」只在有实现、且确实还有没显示名的节点时出现。
  const needRelabel = d && META.relabel && d.nodes.some(n => !n.display);
  return H`<div class="ph"><div><h1>节点</h1><p>状态来自控制面的巡检缓存</p></div><div class="sp"></div>
    ${needRelabel ? H`<button class="btn" data-act="relabel-open">${ic("globe")}批量识别显示名</button>` : ""}
    ${W ? H`<button class="btn pri" data-act="node-wizard">${ic("plus")}添加节点</button>` : ""}</div><div class="card">${inner}</div>`;
}
// 配置:节点上读回的哈希与控制面最后一次下发的比对(同 mpx node list 的漂移判定)。
function driftCell(n) {
  if (!n.deployed_hash) return H`<span class="sub">还没应用过</span>`;
  if (!n.status?.observed_hash) return H`<span class="sub">未知</span>`;
  return n.status.observed_hash === n.deployed_hash ? stBadge("ok", "已是最新") : stBadge("warn", "有改动未应用");
}

function nodeDetail(d) {
  if (!d) return H`<div class="ph"><div><h1><span class="sk" style="width:160px"></span></h1><p><span class="sk" style="width:220px"></span></p></div></div><div class="grid g3"><div class="card stat"><span class="sk w80"></span></div><div class="card stat"><span class="sk w80"></span></div><div class="card stat"><span class="sk w80"></span></div></div>`;
  const n = d.node, s = n.status, id = n.id, W = can("POST", "/api/nodes/x");
  const certs = s?.certificates || [];
  const rep = d.report && !d.report.error ? d.report : null;
  let summary = [];
  try { const j = rep ? (typeof rep.report === "string" ? JSON.parse(rep.report) : rep.report) : null; summary = (j && j.summary) || []; } catch { summary = []; }
  return H`<div class="ph"><div><h1 class="hrow">${nodeLabel(n)} ${nodeSt(n)}</h1><p class="mono">${n.address} · ${sshText(n)}${n.display ? H` · <span title="命令行与数据里用的名字">内部名 ${n.name}</span>` : ""}</p></div><div class="sp"></div>
    <button class="btn" data-act="node-check" data-id="${id}">${ic("refresh")}刷新状态</button>
    ${W ? H`<button class="btn" data-act="node-sync" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}">应用到节点</button>
    <button class="btn" data-act="node-drain" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}" data-on="${n.draining ? "1" : ""}">${n.draining ? "恢复分配" : "暂停分配"}</button>` : ""}
    ${can("PUT", "/api/nodes/x") ? H`<button class="btn" data-act="node-edit" data-id="${id}">编辑</button>` : ""}</div>
  ${s?.error ? H`<div class="callout bad mb" role="alert">${ic("warn", "ico-bad")}<div><b>上次检查${s.reachable ? "有问题" : "连不上"}</b><div class="mono mt2">${s.error}</div></div></div>` : ""}
  ${n.draining ? H`<div class="callout mb">${ic("warn", "ico-warn")}<div><b>已暂停分配</b> · 新订阅不再发这台节点;已连上的用户会在下次刷新订阅时换到别的节点。</div></div>` : ""}
  <div class="grid g3">
    <div class="card stat"><div class="l">核心</div><div class="v f18">${s?.version || "—"}</div><div class="s">${coreText(s)}</div></div>
    <div class="card stat"><div class="l">节点配置</div><div class="v f18" title="${s?.observed_hash || ""}">${driftCell(n)}</div><div class="s">${n.deployed_at ? H`应用于 ${ymd(new Date(n.deployed_at))} ${hm(new Date(n.deployed_at))}` : ""}</div></div>
    <div class="card stat"><div class="l">证书</div><div class="v f18">${certs.length ? certs.map(certText).join(" · ") : "—"}</div><div class="s">${certs.map(c => c.inbound_name).join(" · ") || "没有 TLS 入站"}</div></div>
  </div>
  <div class="card mt conn-card"><div class="ch">连接方式<small>· ${d.inbounds.length}</small><span class="sp"></span>${can("POST", "/api/inbounds") ? H`<button class="btn sm" data-act="conn-new" data-node="${id}">${ic("plus")}添加连接</button>` : ""}</div>
    ${CONN.groups(d.conn, { node: id })}</div>
  <div class="card mt"><div class="ch">维护</div>
    <div class="row"><div><b class="bold">体检</b><div class="sub">${rep ? H`上次 ${ymd(new Date(rep.checked_at))} ${hm(new Date(rep.checked_at))}${rep.measured_address ? H` · 测量地址 ${rep.measured_address}` : ""}` : d.report?.error ? H`<span class="c-bad">${d.report.error}</span>` : "还没有体检报告"}</div>
      ${summary.length ? H`<ul class="summary">${summary.map(x => H`<li>${x}</li>`)}</ul>` : ""}</div><span class="sp"></span>${W ? H`<button class="btn sm" data-act="node-diagnose" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}">体检</button>` : ""}</div>
    ${W ? H`<div class="row"><div><b class="bold">安装核心</b><div class="sub">控制面下载发布包,经 SSH 送到节点,应用当前配置并启动。</div></div><span class="sp"></span><button class="btn sm" data-act="node-install" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}">安装…</button></div>` : ""}
    <div class="row"><div><b class="bold">在线用户</b><div class="sub">现场问节点此刻每个用户连着几个 IP</div></div><span class="sp"></span><button class="btn sm" data-act="node-online" data-id="${id}" data-label="${nodeLabel(n)}">查看</button></div>
    <div class="row"><div><b class="bold">配置预览</b><div class="sub">「应用到节点」会推的 config.json,不碰节点</div></div><span class="sp"></span><button class="btn sm" data-act="node-config" data-id="${id}" data-label="${nodeLabel(n)}">查看</button></div>
  </div>
  ${can("DELETE", "/api/nodes/x") ? H`<div class="card mt danger-zone"><div class="ch c-bad">危险操作</div><div class="row"><div><b class="bold">删除节点</b><div class="sub">它上面的入站和入口一起删掉,订阅里不再有它。机器上的核心和配置不会被清理。</div></div><span class="sp"></span><button class="btn sm c-bad" data-act="node-rm" data-id="${id}" data-name="${n.name}" data-label="${nodeLabel(n)}">删除</button></div></div>` : ""}`;
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
    <div class="field"><label for="n-address">对外地址</label><input class="in mono" id="n-address" name="address" value="${n.address}" placeholder="203.0.113.10"><span class="h">入口没写地址时订阅里用它${isNew ? ";显示名按它自动识别" : ""}</span></div>
    <div class="two"><div class="field"><label for="n-ssh-port">SSH 端口</label><input class="in mono" id="n-ssh-port" name="ssh_port" value="${num(n.ssh_port)}" placeholder="22" inputmode="numeric"></div>
    <div class="field"><label for="n-ssh-user">SSH 用户</label><input class="in mono" id="n-ssh-user" name="ssh_user" value="${n.ssh_user}" placeholder="root"></div></div>
    ${isNew ? "" : H`<div class="field"><label>显示名<span class="sub" id="n-display"></span></label><div class="three">
      <input class="in mono" id="n-region" name="region" value="${meta.region || ""}" placeholder="地区 US" aria-label="地区" autocomplete="off">
      <input class="in" id="n-city" name="city" value="${meta.city || ""}" placeholder="城市,留空用国家名" aria-label="城市" autocomplete="off">
      <input class="in mono" id="n-letter" name="letter" value="${meta.letter || ""}" placeholder="字母,留空自动" aria-label="字母" autocomplete="off"></div>
      <div class="flex8 mt"><button type="button" class="btn sm" id="n-locate">重新识别</button><span class="h" id="n-locate-msg">按节点 IP 问 ipwho.is 给建议,只填进表单,保存才写入;节点 IP 会发给这个第三方。</span></div></div>`}
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
    ${isNew ? H`<p class="sub">这里只登记已能免密 SSH 的机器。要用 SSH 密码添加，请用「添加节点」向导。</p>` : H`<p class="sub">改地址会改掉订阅里没写地址的入口;改 SSH 参数只影响控制面以后怎么连它。都不会自动下发。</p>`}
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
    $("#n-locate-msg").textContent = `建议:${sg.display}(${sg.source},依据${sg.via === "node" ? "节点出口" : "节点"} IP ${sg.ip})${!sg.city && sg.city_en ? `;${sg.city_en} 不在对照表里,可手填城市` : ""}`;
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
          if (!nodeChanged) { toast(`显示名已改为 ${previewDisplay(saved) || "(没有地区,不显示)"}`); closeOverlays(); load(true, true); return; }
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
  m.innerHTML = val(H`<div class="mbody"><h2>批量识别显示名</h2>
  <p>对还没有显示名的节点各做一次识别(控制面按节点 IP 问 ipwho.is,<b>节点 IP 会发给这个第三方</b>),先出「旧名 → 新名」对照表。已经有显示名的节点会跳过,用户写过的值不会被覆盖。</p>
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

Object.assign(ACTS, {
  "node-new": () => nodeEditor(null),
  "node-wizard": () => openNodeWizard(),
  "relabel-open": () => relabelModal(),
  "node-edit": d => nodeEditor(+d.id),
  // 现场检查 = mpx node status <id>:GET ?fresh=1,结果由控制面落库。
  "node-check": (d, b) => busy(b, async () => { await api(nodePath(d.id) + "?fresh=1"); toast("状态已刷新"); await load(true, true); }),
  "node-sync": d => confirmBox({ title: `把配置应用到 ${d.label || d.name}?`, text: "把当前配置推到节点并重启核心:这台节点上所有人的连接会断一次。加人、删人、到期、超限不用手动应用,控制面每 30 秒自动跟上。", ok: "应用到节点并重启",
    run: async () => { await write("POST", nodePath(d.id) + "/sync", undefined, `已应用到 ${d.label || d.name},核心已重启`); await load(true, true); } }),
  "node-drain": (d, b) => {
    if (d.on) return busy(b, async () => { await write("POST", nodePath(d.id) + "/undrain", undefined, `${d.label || d.name} 已恢复分配`); await load(true, true); });
    confirmBox({ title: `暂停给 ${d.label || d.name} 分配?`, text: "新订阅不再发这台节点的入口;已连上的用户不会立刻断线,会在下次刷新订阅时换到别的节点。随时可以恢复分配。", ok: "开始排水",
      run: async () => { await write("POST", nodePath(d.id) + "/drain", undefined, `${d.label || d.name} 已暂停分配`); await load(true, true); } });
  },
  "node-install": d => confirmBox({ title: `在 ${d.label || d.name} 上安装核心?`, text: "控制面下载发布包(有缓存),经 SSH 送到节点,应用当前配置并启动。慢链路上要几分钟,期间别关这个页面。", ok: "安装",
    extra: H`<div class="two"><div class="field"><label for="i-core">核心</label><select class="in" id="i-core"><option value="xray">xray</option><option value="sing-box">sing-box(hysteria2 / tuic)</option></select></div>
      <div class="field"><label for="i-ver">版本</label><input class="in mono" id="i-ver" placeholder="留空用最新版" autocomplete="off"></div></div>`,
    run: async () => {
      const core = $("#i-core").value, v = $("#i-ver").value.trim();
      let path = nodePath(d.id) + "/install?core=" + encodeURIComponent(core);
      if (v) path += "&version=" + encodeURIComponent(v);
      const res = await write("POST", path);
      toast(`已在 ${d.label || d.name} 上装好并启动 ${core} ${res?.version || ""}`);
      await load(true, true);
    } }),
  // 体检 = mpx node diagnose <id> 的默认参数:Fusion、测速开、硬件测试关。
  "node-diagnose": d => confirmBox({ title: `体检 ${d.label || d.name}?`, text: "在节点上执行健康、入口能力和 Fusion 完整体检(测速开、硬件测试关)。最长要十几分钟,期间别关这个页面。", ok: "开始体检",
    run: async () => { await write("POST", nodePath(d.id) + "/diagnostics", { engine: "fusion", position: "node", node_id: String(d.id), speed: true }, "体检完成"); await load(true, true); } }),
  "node-online": (d, b) => busy(b, async () => {
    const r = await api(nodePath(d.id) + "/online");
    infoDrawer(`${d.label || r.node} 在线`, H`现场读出的结果`, r.online.length
      ? H`<div class="tw"><table><thead><tr><th>用户</th><th class="num">IP 数</th><th>IP</th></tr></thead><tbody>${r.online.map(e => H`<tr><td><b class="bold">${e.user}</b></td><td class="num">${e.count}</td><td class="mono sub">${(e.ips || []).join(", ")}</td></tr>`)}</tbody></table></div>`
      : emptyBox("users", "没有在线用户", "此刻没有用户连在这台节点上。", ""));
  }),
  "node-config": (d, b) => busy(b, async () => {
    const cfg = await api(nodePath(d.id) + "/config");
    infoDrawer(`${d.label || d.name || "节点"} 的配置预览`, H`「应用到节点」会推这一份`, H`<pre class="code">${typeof cfg === "string" ? cfg : JSON.stringify(cfg, null, 2)}</pre>`);
  }),
  "node-rm": d => confirmBox({ title: `删除节点 ${d.label || d.name}?`, text: "不可恢复:它上面的入站和入口一起删掉,订阅里不再有它。机器上的核心还在跑,要清理得自己登上去。只想让新订阅别再发它,用「暂停分配」。", ok: "删除节点", danger: true, typed: d.name,
    run: async () => { await write("DELETE", nodePath(d.id), undefined, `已删除节点 ${d.label || d.name}`); location.hash = "nodes"; } }),
});

// autoNodeNameFor:直接登记没填内部名时,取 node-1、node-2… 里第一个没被占的。
async function autoNodeNameFor() {
  let names = new Set();
  try { names = new Set((await api("/api/nodes")).map(n => n.name)); } catch { /* 撞名由控制面报 */ }
  for (let i = 1; ; i++) if (!names.has("node-" + i)) return "node-" + i;
}
