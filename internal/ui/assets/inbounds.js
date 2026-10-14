// 入站与入口:列表、inbound add / edit / rm、host add / edit / rm。
// 请求与 mpx inbound / mpx host 一致:新增按 model 整体序列化;修改先读后整体回写。
"use strict";

const inbPath = id => "/api/inbounds/" + id;
const inbTag = i => i.tag || `${i.protocol}-${i.port}`;
const splitCSV = s => String(s || "").split(",").map(x => x.trim()).filter(Boolean);
const QUIC = p => p === "hysteria2" || p === "tuic";
const PROTOCOLS = ["vless", "vmess", "trojan", "shadowsocks", "wireguard", "hysteria2", "tuic"];
const NETWORKS = ["tcp", "ws", "grpc", "xhttp", "splithttp", "httpupgrade", "udp"];
const SECURITIES = ["none", "tls", "reality"];
// 入站的可编辑字段:[JSON 键, CLI flag, 种类, 什么时候显示]。种类 s=字符串 i=整数 l=逗号列表 b=开关。
const INB_FIELDS = [
  ["tag", "--tag", "s"], ["listen", "--listen", "s"],
  ["ws_path", "--ws-path", "s", f => f.network === "ws"],
  ["grpc_service", "--grpc-service", "s", f => f.network === "grpc"],
  ["xhttp_path", "--xhttp-path", "s", f => ["xhttp", "splithttp", "httpupgrade"].includes(f.network)],
  ["xhttp_host", "--xhttp-host", "s", f => ["xhttp", "splithttp", "httpupgrade"].includes(f.network)],
  ["xhttp_mode", "--xhttp-mode", "s", f => f.network === "xhttp"],
  ["sni", "--sni", "s", f => f.security !== "none"],
  ["reality_dest", "--reality-dest", "s", f => f.security === "reality"],
  ["short_ids", "--short-ids", "l", f => f.security === "reality"],
  ["alpn", "--alpn", "l", f => f.security === "tls"],
  ["cert_file", "--cert-file", "s", f => f.security === "tls"],
  ["key_file", "--key-file", "s", f => f.security === "tls"],
  ["fingerprint", "--fingerprint", "s", f => f.security !== "none"],
  ["flow", "--flow", "s", f => f.protocol === "vless"],
  ["wg_address", "--wg-address", "s", f => f.protocol === "wireguard"],
  ["wg_mtu", "--wg-mtu", "i", f => f.protocol === "wireguard"],
  ["congestion_control", "--congestion-control", "s", f => f.protocol === "tuic"],
  ["max_users", "--max-users", "i"],
  ["mux", "--mux", "b"], ["fragment", "--fragment", "b"],
];
const INB_LABEL = { tag: ["标签", "留空按「协议-端口」生成"], listen: ["监听地址", "留空 0.0.0.0"], ws_path: ["WS 路径", "/ws"], grpc_service: ["gRPC serviceName", ""], xhttp_path: ["xhttp 路径", ""], xhttp_host: ["xhttp Host", ""], xhttp_mode: ["xhttp mode", "auto / packet-up / stream-up / stream-one"],
  sni: ["SNI", "www.cloudflare.com"], reality_dest: ["REALITY 回落目标", "www.cloudflare.com:443"], short_ids: ["shortId", "逗号分隔"], alpn: ["ALPN", "h2,http/1.1"], cert_file: ["证书路径(节点上)", "/etc/ssl/fullchain.pem"], key_file: ["私钥路径(节点上)", "/etc/ssl/privkey.pem"], fingerprint: ["uTLS 指纹", "留空 chrome"],
  flow: ["VLESS flow", "xtls-rprx-vision"], wg_address: ["wireguard 隧道地址", "10.0.0.1/24"], wg_mtu: ["wireguard MTU", "1420"], congestion_control: ["TUIC 拥塞控制", "bbr / cubic / new_reno"], max_users: ["用户数上限", "0 不限"], mux: ["订阅里开 mux"], fragment: ["订阅里开分片(仅 xray)"] };
// asModelInbound:摆成 model.Inbound 的 JSON 形状(同样的 omitempty)。
function asModelInbound(v) {
  const o = { id: v.id || 0, node_id: v.node_id, tag: v.tag || "", protocol: v.protocol, listen: v.listen || "", port: v.port || 0, network: v.network || "", security: v.security || "" };
  for (const k of ["ws_path", "grpc_service", "xhttp_path", "xhttp_host", "xhttp_mode", "wg_address", "wg_mtu", "sni", "alpn", "fingerprint", "cert_file", "key_file", "reality_dest", "reality_pub", "short_ids", "congestion_control", "flow", "mux", "fragment", "max_users"]) {
    const x = v[k];
    if (Array.isArray(x) ? x.length : x) o[k] = x;
  }
  return o;
}
function asModelHost(v) {
  const o = { id: v.id || 0, inbound_id: v.inbound_id, remark: v.remark || "", address: v.address || "", port: v.port || 0 };
  for (const k of ["sni", "host", "path", "alpn"]) { const x = v[k]; if (Array.isArray(x) ? x.length : x) o[k] = x; }
  return o;
}

PAGES.inbounds = {
  title: "入站与入口", short: "入站", nav: ["管理", "inb", 30],
  count: () => INB_TOTAL ?? STATUS?.counts?.inbounds ?? "",
  async load() {
    const [inbounds, nodes] = await Promise.all([api("/api/inbounds"), api("/api/nodes")]);
    // 入口按入站各取一次:控制面没有「全部入口」的接口。
    const hosts = await Promise.all(inbounds.map(i => api(inbPath(i.id) + "/hosts")));
    INB_TOTAL = inbounds.length;
    return { inbounds, nodes, hosts: Object.fromEntries(inbounds.map((i, k) => [i.id, hosts[k]])) };
  },
  render(d) {
    const W = can("POST", "/api/inbounds"), WH = can("PUT", "/api/hosts/x");
    const head = H`<div class="ph"><div><h1>入站与入口</h1><p>入站是节点上监听的端口;入口决定订阅里发什么地址 · 对应 <code>mpx inbound list</code> / <code>mpx host list</code></p></div><div class="sp"></div>${W && d?.nodes.length ? H`<button class="btn pri" data-act="inb-new">${ic("plus")}添加入站</button>` : ""}</div>`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(6, 4)}</tbody></table></div>`;
    if (!d.inbounds.length) return H`${head}<div class="card">${d.nodes.length
      ? emptyBox("inb", "还没有入站", "入站决定节点上监听什么协议和端口。加好后要下发一次节点配置才生效。", `mpx inbound add --node ${d.nodes[0].id} --protocol vless --port 443 --security reality`, W ? H`<button class="btn pri sm" data-act="inb-new">${ic("plus")}添加入站</button>` : "")
      : emptyBox("nodes", "还没有节点", "先有节点,再在节点上加入站。", "mpx node add hk --address 203.0.113.10")}</div>`;
    const nodeName = id => d.nodes.find(n => n.id === id)?.name || "#" + id;
    return H`${head}${d.inbounds.map(i => { const hs = d.hosts[i.id] || []; return H`<div class="card mb14"><div class="ch wrap"><b class="b6">${inbTag(i)}</b><span class="tag">${i.protocol}</span><span class="sub mono fw4">#${i.id} · ${nodeName(i.node_id)} :${i.port} · ${i.network} · ${i.security}${i.max_users ? ` · 上限 ${i.max_users} 人` : ""}</span><span class="sp"></span>
      ${can("PUT", "/api/inbounds/x") ? H`<button class="btn ghost sm" data-act="inb-edit" data-id="${i.id}">编辑</button>` : ""}${WH ? H`<button class="btn sm" data-act="host-new" data-inb="${i.id}">${ic("plus")}入口</button>` : ""}</div>
      ${hs.length ? hs.map(h => H`<div class="row"><span class="dot ok"></span><b class="bold">${h.remark || "(无名)"}</b><span class="sub mono ell">${h.address ? `${h.address}:${h.effective.port}` : H`(节点地址) ${h.effective.address}:${h.effective.port}`}</span>${h.alpn?.length ? H`<span class="tag hide-m">${h.alpn.join(",")}</span>` : ""}<span class="sp"></span><span class="sub hide-m">#${h.id}</span>${WH ? H`<button class="btn ghost sm" data-act="host-edit" data-inb="${i.id}" data-id="${h.id}">编辑</button>` : ""}</div>`)
        : H`<div class="row sub">没有对外入口,订阅里用入站自己的地址与端口。<span class="sp"></span><code class="hide-m">mpx host add --inbound ${i.id}</code></div>`}</div>`; })}`;
  },
};
let INB_TOTAL = null;

/* ---- 入站编辑器 ---- */
async function inboundEditor(id, presetNode) {
  let v = null, nodes = [];
  try { [v, nodes] = await Promise.all([id ? api(inbPath(id)) : null, api("/api/nodes")]); } catch (e) { toast(e.message, true); return; }
  const isNew = !v;
  const base = v ? asModelInbound(v) : { node_id: presetNode || nodes[0]?.id, protocol: "vless", port: 0, network: "tcp", security: "none", wg_mtu: 1420 };
  const fv = k => { const x = base[k]; return Array.isArray(x) ? x.join(",") : x ? String(x) : ""; };
  const opt = (list, cur) => list.map(x => H`<option value="${x}" ${x === cur ? raw("selected") : ""}>${x}</option>`);
  const input = k => { const [l, ph] = INB_LABEL[k]; const t = INB_FIELDS.find(f => f[0] === k)[2];
    return t === "b" ? H`<label class="chk" data-f="${k}"><input type="checkbox" name="${k}" ${base[k] ? raw("checked") : ""}> ${l}</label>`
      : H`<div class="field" data-f="${k}"><label for="i-${k}">${l}</label><input class="in mono" id="i-${k}" name="${k}" value="${fv(k)}" placeholder="${ph}" autocomplete="off" ${t === "i" ? raw('inputmode="numeric"') : ""}></div>`; };
  openDrawer(H`<form id="inb-form" novalidate><div class="dh"><h2>${isNew ? "添加入站" : "编辑入站 " + inbTag(v)}</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    <div class="two"><div class="field"><label for="i-node">节点</label><select class="in" id="i-node" name="node_id" ${isNew ? "" : raw("disabled")}>${nodes.map(n => H`<option value="${n.id}" ${n.id === base.node_id ? raw("selected") : ""}>#${n.id} ${n.name}</option>`)}</select></div>
    <div class="field"><label for="i-port">端口</label><input class="in mono" id="i-port" name="port" value="${fv("port")}" placeholder="443" inputmode="numeric"></div></div>
    <div class="field"><label for="i-protocol">协议</label><select class="in" id="i-protocol" name="protocol">${opt(PROTOCOLS, base.protocol)}</select></div>
    <div class="two"><div class="field"><label for="i-network">传输</label><select class="in" id="i-network" name="network">${opt(NETWORKS, base.network)}</select></div>
    <div class="field"><label for="i-security">安全层</label><select class="in" id="i-security" name="security">${opt(SECURITIES, base.security)}</select></div></div>
    ${["sni", "reality_dest", "short_ids", "cert_file", "key_file", "alpn", "fingerprint", "flow", "ws_path", "grpc_service", "xhttp_path", "xhttp_host", "xhttp_mode", "wg_address", "wg_mtu", "congestion_control"].map(input)}
    <details class="adv"><summary>更多</summary>${["tag", "listen", "max_users"].map(input)}<div class="stack8 mb">${input("mux")}${input("fragment")}</div></details>
    <p class="sub">${isNew ? "REALITY 的密钥对由控制面生成。" : "只改用户数上限不用下发;"}改端口、协议、传输、安全层、证书后要下发节点配置才生效,下发会重启核心。</p>
    <div class="field mt"><label>等价命令</label><div class="cmd" id="i-cmd"></div></div>
    <div class="err" id="i-err" role="alert"></div>
    ${!isNew && can("DELETE", "/api/inbounds/x") ? H`<div class="card danger-zone mt"><div class="row"><div><b class="bold">删除入站</b><div class="sub">挂在它上面的入口一起删掉。</div></div><span class="sp"></span><button type="button" class="btn sm c-bad" data-act="inb-rm" data-id="${id}" data-tag="${inbTag(v)}">删除</button></div></div>` : ""}
  </div><div class="df"><button type="button" class="btn" data-act="close">取消</button><button type="submit" class="btn pri" id="i-save">${isNew ? "创建" : "保存"}</button></div></form>`);
  const form = $("#inb-form");
  let lastProto = base.protocol;
  const collect = () => {
    const f = new FormData(form), err = [], out = {};
    out.node_id = isNew ? +f.get("node_id") : base.node_id;
    out.protocol = f.get("protocol"); out.network = f.get("network"); out.security = f.get("security");
    const portS = String(f.get("port") || "").trim();
    out.port = /^\d+$/.test(portS) ? +portS : NaN;
    if (!(out.port >= 1 && out.port <= 65535)) err.push(`端口必须在 1-65535 之间`);
    for (const [k, flag, t] of INB_FIELDS) {
      const s = t === "b" ? !!f.get(k) : String(f.get(k) ?? "").trim();
      if (t === "i") { if (s && !/^\d+$/.test(s)) err.push(`${flag} 要是整数`); out[k] = s ? +s : 0; }
      else if (t === "l") out[k] = splitCSV(s);
      else out[k] = s;
    }
    // 等价命令:新增列出非默认值;修改列出改动的字段(CLI 只认给出来的 flag)。
    const flags = [], cur = isNew ? { network: QUIC(out.protocol) ? "udp" : "tcp", security: QUIC(out.protocol) ? "tls" : "none", wg_mtu: 1420 } : base;
    const same = (k, x) => { const o = cur[k]; return Array.isArray(x) ? x.join(",") === (o || []).join(",") : x === (o ?? (typeof x === "number" ? 0 : typeof x === "boolean" ? false : "")); };
    if (isNew) flags.push("--node", out.node_id, "--protocol", out.protocol, "--port", portS || "<端口>");
    else { if (out.protocol !== base.protocol) flags.push("--protocol", out.protocol); if (out.port !== base.port) flags.push("--port", portS); }
    for (const k of ["network", "security"]) if (!same(k, out[k])) flags.push("--" + k, out[k]);
    for (const [k, flag, t] of INB_FIELDS) {
      const x = out[k];
      if (same(k, x)) continue;
      if (t === "b") flags.push(x ? flag : flag + "=false");
      else { const s = t === "l" ? x.join(",") : String(x); flags.push(flag, s === "" ? '""' : shq(s)); }
    }
    const cmd = isNew ? `mpx inbound add ${flags.join(" ")}` : `mpx inbound edit ${id} ${flags.join(" ") || "(没有改动)"}`;
    return { out, err, cmd };
  };
  const sync = () => {
    const p = $("#i-protocol").value;
    // 与 CLI 一样:选了 QUIC 协议而没另选传输与安全层时,默认 udp + tls。
    if (QUIC(p) && !QUIC(lastProto)) { $("#i-network").value = "udp"; $("#i-security").value = "tls"; }
    lastProto = p;
    const c = collect();
    for (const [k, , , show] of INB_FIELDS) { const el = form.querySelector(`[data-f="${k}"]`); if (el && show) el.hidden = !show(c.out); }
    $("#i-cmd").textContent = c.cmd;
  };
  form.oninput = form.onchange = sync; sync();
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const c = collect(), btn = $("#i-save");
    $("#i-err").textContent = c.err.join("\n");
    if (c.err.length) return;
    btn.disabled = true; btn.classList.add("busy");
    try {
      if (isNew) {
        const created = await write("POST", "/api/inbounds", asModelInbound({ ...c.out, id: 0 }));
        toast(`已创建入站 ${created.id}(${inbTag(created)})`);
      } else {
        const fresh = asModelInbound(await api(inbPath(id)));
        const updated = await write("PUT", inbPath(id), asModelInbound({ ...fresh, ...c.out, node_id: fresh.node_id }));
        const structural = updated.port !== base.port || updated.network !== base.network || updated.security !== base.security;
        toast(structural ? `已更新入站 ${updated.id};要下发节点 ${updated.node_id} 才生效` : `已更新入站 ${updated.id}(${inbTag(updated)})`);
      }
      closeOverlays(); load(true, true);
    } catch (e2) { $("#i-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

/* ---- 入口编辑器 ---- */
async function hostEditor(inbID, hostID) {
  let hosts = [];
  try { hosts = await api(inbPath(inbID) + "/hosts"); } catch (e) { toast(e.message, true); return; }
  const found = hostID ? hosts.find(h => h.id === hostID) : null;
  if (hostID && !found) { toast(`入站 ${inbID} 上没有 id 为 ${hostID} 的对外入口`, true); return; }
  const isNew = !found, h = found ? asModelHost(found) : { inbound_id: inbID, remark: "", address: "", port: 0 };
  const F = [["remark", "--remark", "客户端看到的名字", "香港 · 直连", "可用 {SERVER_IP} {NODE_NAME} {PROTOCOL} {TRANSPORT}"], ["address", "--address", "对外地址", "留空用节点地址", ""], ["port", "--port", "对外端口", "留空用入站端口", ""],
    ["sni", "--sni", "SNI", "", ""], ["host", "--host", "Host 头", "ws / grpc", ""], ["path", "--path", "路径", "ws", ""], ["alpn", "--alpn", "ALPN", "逗号分隔", ""]];
  const fv = k => Array.isArray(h[k]) ? h[k].join(",") : h[k] ? String(h[k]) : "";
  openDrawer(H`<form id="host-form" novalidate><div class="dh"><h2>${isNew ? `给入站 #${inbID} 加入口` : `编辑入口 #${hostID}`}</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    ${F.map(([k, , l, ph, hint]) => H`<div class="field"><label for="h-${k}">${l}</label><input class="in ${k === "remark" ? "" : "mono"}" id="h-${k}" name="${k}" value="${fv(k)}" placeholder="${ph}" autocomplete="off">${hint ? H`<span class="h">${hint}</span>` : ""}</div>`)}
    ${found ? H`<p class="sub">现在发出去的是 <code>${found.effective.address}:${found.effective.port}</code>。改地址或端口,用户下次刷新订阅就跟着变;不用下发。</p>` : H`<p class="sub">入口只影响订阅里写什么,不用下发节点配置。</p>`}
    <div class="field mt"><label>等价命令</label><div class="cmd" id="h-cmd"></div></div>
    <div class="err" id="h-err" role="alert"></div>
    ${found && can("DELETE", "/api/hosts/x") ? H`<div class="card danger-zone mt"><div class="row"><div><b class="bold">删除入口</b><div class="sub">只影响订阅;删掉最后一个入口,订阅回落到入站自己的地址。</div></div><span class="sp"></span><button type="button" class="btn sm c-bad" data-act="host-rm" data-id="${hostID}" data-remark="${h.remark}">删除</button></div></div>` : ""}
  </div><div class="df"><button type="button" class="btn" data-act="close">取消</button><button type="submit" class="btn pri" id="h-save">${isNew ? "创建" : "保存"}</button></div></form>`);
  const form = $("#host-form");
  const collect = () => {
    const f = new FormData(form), out = {}, err = [], flags = [];
    for (const [k, flag] of F) {
      const s = String(f.get(k) || "").trim();
      if (k === "port") { if (s && !/^\d+$/.test(s)) err.push("对外端口要是整数"); out.port = s ? +s : 0; }
      else if (k === "alpn") out.alpn = splitCSV(s);
      else out[k] = s;
      const cur = k === "alpn" ? out.alpn.join(",") : String(out[k] || ""), old = fv(k);
      if (isNew ? cur !== "" : cur !== old) flags.push(flag, cur === "" ? '""' : shq(cur));
    }
    const cmd = isNew ? `mpx host add --inbound ${inbID} ${flags.join(" ")}` : `mpx host edit ${hostID} --inbound ${inbID} ${flags.join(" ") || "(没有改动)"}`;
    return { out, err, cmd };
  };
  const sync = () => { $("#h-cmd").textContent = collect().cmd; };
  form.oninput = form.onchange = sync; sync();
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const c = collect(), btn = $("#h-save");
    $("#h-err").textContent = c.err.join("\n");
    if (c.err.length) return;
    btn.disabled = true; btn.classList.add("busy");
    try {
      if (isNew) {
        const created = await write("POST", inbPath(inbID) + "/hosts", asModelHost({ ...c.out, id: 0, inbound_id: inbID }));
        toast(`已创建对外入口 ${created.id}`);
      } else {
        // 与 host edit 一致:先从所属入站的列表里找到它,再整体 PUT。
        const now = (await api(inbPath(inbID) + "/hosts")).find(x => x.id === hostID);
        if (!now) throw new Error(`入站 ${inbID} 上没有 id 为 ${hostID} 的对外入口`);
        await write("PUT", "/api/hosts/" + hostID, asModelHost({ ...asModelHost(now), ...c.out }));
        toast(`已更新对外入口 ${hostID}`);
      }
      closeOverlays(); load(true, true);
    } catch (e2) { $("#h-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

Object.assign(ACTS, {
  "inb-new": d => inboundEditor(null, d.node ? +d.node : 0),
  "inb-edit": d => inboundEditor(+d.id),
  "inb-rm": d => confirmBox({ title: `删除入站 ${d.tag}?`, text: "不可恢复:挂在它上面的入口一起删掉,用它的客户端下次刷新订阅后就没了这个入口。节点上的核心要等下次下发才停止监听。", cmd: `mpx inbound rm ${d.id}`, ok: "删除入站", danger: true, typed: d.tag,
    run: async () => { await write("DELETE", inbPath(d.id), undefined, `已删除入站 ${d.id}`); closeOverlays(); if (route[0] === "inbounds") load(true, true); else location.hash = "inbounds"; } }),
  "host-new": d => hostEditor(+d.inb, 0),
  "host-edit": d => hostEditor(+d.inb, +d.id),
  "host-rm": d => confirmBox({ title: `删除入口 ${d.remark || "#" + d.id}?`, text: "客户端下次刷新订阅后这个地址就没了;节点和已连着的用户不受影响。", cmd: `mpx host rm ${d.id}`, ok: "删除入口", danger: true,
    run: async () => { await write("DELETE", "/api/hosts/" + d.id, undefined, `已删除对外入口 ${d.id}`); closeOverlays(); load(true, true); } }),
});
