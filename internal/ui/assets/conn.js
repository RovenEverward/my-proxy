// 连接组件:按节点分组的「连接」列表 —— 行、展开、名称与节点不符提醒、悬停操作、
// 更多菜单,以及三个抽屉(添加连接 / 再加一个入口 / 编辑连接)。
//
// 谁在用:
//   - 入站与入口页(inbounds.js)只剩页头 + CONN.groups(d);
//   - 节点详情页的「连接方式」以后直接用它,不要再抄一份行/抽屉:
//       const d = await CONN.load([nodeID]);      // 只拉这台节点的入站
//       ... CONN.groups(d, { node: nodeID })      // 不带组头,只有这台的连接行
//     抽屉保存后调用的全局刷新是 load(true, true),所以组件不依赖入站页。
//
// 动作名统一 conn-*;旧的 inb-new / inb-edit / inb-rm / host-new / host-edit / host-rm
// 保留为别名(nodes 页与老测试还在用)。
// 请求与 mpx inbound / mpx host 一致:新增按 model 整体序列化;修改先读后整体回写。
"use strict";

const inbPath = id => "/api/inbounds/" + id;
const inbTag = i => i.tag || `${i.protocol}-${i.port}`;
// 入站的可读写法:「🇯🇵 东京 A · VLESS WS :2087」。tag 是自动生成的(协议-端口)时
// 标题就用它;手起过 tag 的照旧显示 tag,可读写法退到副标题。
const PROTO_NAMES = { vless: "VLESS", vmess: "VMess", trojan: "Trojan", shadowsocks: "SS", wireguard: "WireGuard", hysteria2: "Hysteria2", tuic: "TUIC" };
const NET_NAMES = { ws: "WS", grpc: "gRPC", xhttp: "XHTTP", splithttp: "SplitHTTP", httpupgrade: "HTTPUpgrade" };
const inbAutoTag = i => !i.tag || i.tag === `${i.protocol}-${i.port}`;
const inbKind = i => [PROTO_NAMES[i.protocol] || i.protocol, NET_NAMES[i.network], i.security === "reality" ? "Reality" : i.security === "tls" ? "TLS" : ""].filter(Boolean).join(" ");
// 列表行里的短标签(设计稿只给 64px):Reality / WS / gRPC…,没有传输层名时用协议名;全称放在 title 里。
const inbShortKind = i => i.security === "reality" ? "Reality" : NET_NAMES[i.network] || PROTO_NAMES[i.protocol] || i.protocol;
const inbReadable = (i, node) => `${node ? nodeLabel(node) + " · " : ""}${inbKind(i)} :${i.port}`;
const inbTitle = (i, node) => inbAutoTag(i) ? inbReadable(i, node) : inbTag(i);
// 订阅里的名字:控制面按订阅同一套规则算好(sub_name);没起名的跟随节点显示名。
// inbTitleById:从入站页已拉到的数据里找入站的可读写法;不在那页时退回「入站」。
let INB_CACHE = { inbounds: [], nodes: [] };
let INB_HOSTS = {};
let INB_TOTAL = null;
const inbTitleById = id => { const i = INB_CACHE.inbounds.find(x => x.id === id); return i ? inbTitle(i, INB_CACHE.nodes.find(n => n.id === i.node_id)) : "入站"; };
const splitCSV = s => String(s || "").split(",").map(x => x.trim()).filter(Boolean);
const QUIC = p => p === "hysteria2" || p === "tuic";
const PROTOCOLS = ["vless", "vmess", "trojan", "shadowsocks", "wireguard", "hysteria2", "tuic"];
const NETWORKS = ["tcp", "ws", "grpc", "xhttp", "splithttp", "httpupgrade", "udp"];
const SECURITIES = ["none", "tls", "reality"];
// 下拉里给人看的名字;value 仍是控制面认的小写值。
const OPT_NAME = { vless: "VLESS", vmess: "VMess", trojan: "Trojan", shadowsocks: "Shadowsocks", wireguard: "WireGuard", hysteria2: "Hysteria2", tuic: "TUIC",
  tcp: "TCP", ws: "WebSocket", grpc: "gRPC", xhttp: "XHTTP", splithttp: "SplitHTTP", httpupgrade: "HTTPUpgrade", udp: "UDP", none: "无", tls: "TLS", reality: "REALITY" };
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
  sni: ["SNI", "www.cloudflare.com"], reality_dest: ["REALITY 回落目标", "www.cloudflare.com:443"], short_ids: ["Short ID", "逗号分隔"], alpn: ["ALPN", "h2,http/1.1"], cert_file: ["证书路径(节点上)", "/etc/ssl/fullchain.pem"], key_file: ["私钥路径(节点上)", "/etc/ssl/privkey.pem"], fingerprint: ["uTLS 指纹", "留空 chrome"],
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

/* ---------- 连接与入口的显示规则 ---------- */
const CONN_KIND = { reality: "Reality", ws: "WS" };
const nameOfHost = (i, h, n) => h ? (h.sub_name || h.remark || nodeLabel(n)) : (i.sub_name || nodeLabel(n));
const addrOfHost = (i, h, n) => h ? `${h.effective?.address || n.address}:${h.effective?.port || i.port}` : `${n.address}:${i.port}`;
const customName = h => !!h && !h.auto_name && !!String(h.remark || "").trim();
const customAddr = h => !!h && !!String(h.address || "").trim();
const firstHost = hs => Array.isArray(hs) && hs.length ? hs[0] : null;

// 名称里写到的地名 → 国家码。国旗(区域指示符对)优先,其次看地名。
const PLACE_CODES = [
  ["美国", "US"], ["日本", "JP"], ["香港", "HK"], ["新加坡", "SG"], ["台湾", "TW"], ["韩国", "KR"],
  ["英国", "GB"], ["德国", "DE"], ["法国", "FR"], ["荷兰", "NL"], ["加拿大", "CA"], ["澳大利亚", "AU"],
  ["俄罗斯", "RU"], ["印度", "IN"], ["东京", "JP"], ["大阪", "JP"], ["洛杉矶", "US"], ["圣何塞", "US"],
  ["硅谷", "US"], ["纽约", "US"], ["西雅图", "US"], ["芝加哥", "US"], ["达拉斯", "US"], ["伦敦", "GB"],
  ["法兰克福", "DE"], ["首尔", "KR"], ["台北", "TW"], ["阿姆斯特丹", "NL"],
];
const REGION_ZH = { US: "美国", JP: "日本", HK: "香港", SG: "新加坡", TW: "台湾", KR: "韩国", GB: "英国", DE: "德国", FR: "法国", NL: "荷兰", CA: "加拿大", AU: "澳大利亚", RU: "俄罗斯", IN: "印度" };
function flagRegion(name) {
  const cps = [...String(name || "")].map(c => c.codePointAt(0));
  for (let i = 0; i + 1 < cps.length; i++) {
    if (cps[i] >= 0x1F1E6 && cps[i] <= 0x1F1FF && cps[i + 1] >= 0x1F1E6 && cps[i + 1] <= 0x1F1FF) {
      return String.fromCharCode(cps[i] - 0x1F1E6 + 65, cps[i + 1] - 0x1F1E6 + 65);
    }
  }
  return "";
}
// nameMismatch:名称里写的国家和节点所在地不一样时返回一句提醒,否则 null。
// 纯函数,方便单测;node 只要有 region / city 两个字段。
function nameMismatch(name, node) {
  name = String(name || "").trim();
  const region = String(node?.region || "").trim().toUpperCase();
  if (!name || !region) return null;
  // 国旗是明说的地区,优先;否则取名称里最先出现的地名。
  const flag = flagRegion(name);
  let said = "", code = "";
  if (flag) {
    code = flag;
    said = REGION_ZH[flag] || PLACE_CODES.find(([, c]) => c === flag)?.[0] || flag;
  } else {
    let at = Infinity;
    for (const [word, c] of PLACE_CODES) {
      const k = name.indexOf(word);
      if (k >= 0 && k < at) { at = k; said = word; code = c; }
    }
    if (!said) return null;
  }
  if (code === region) return null;
  return { said, code, region, where: node?.city || REGION_ZH[region] || region };
}
const mismatchText = mm => mm ? `名称写的是${mm.said}，节点在${mm.where}` : "";

/* ---------- 组件入口:CONN ---------- */
// 节点详情页以后这样接(不要再抄行/抽屉):
//   const d = await CONN.load([id]);            // 只要这台节点的入站
//   ... CONN.groups(d, { node: id })            // 不带组头,只有这台的连接行
// 组件里的写操作成功后统一调全局 load(true, true),不依赖入站页。
let INB_EXP = new Set();
let INB_MENU = null;
const inbW = () => can("POST", "/api/inbounds");
const inbWH = () => can("PUT", "/api/hosts/x");

const inbCustom = (txt, on, mono) => on
  ? H`<span class="cx-v on ${mono ? "mono" : ""}" title="已自定义"><span class="t">${txt}</span>${ic("pen", "cx-pen")}</span>`
  : H`<span class="cx-v ${mono ? "mono" : ""}"><span class="t">${txt}</span></span>`;

function inbRow(i, hs, n) {
  const h = firstHost(hs), many = (hs || []).length > 1, open = INB_EXP.has(i.id);
  const mm = customName(h) ? nameMismatch(h.remark || h.sub_name || "", n) : null;
  const name = nameOfHost(i, h, n), addr = addrOfHost(i, h, n);
  const baseName = i.sub_name || nodeLabel(n);
  return H`<div class="cx ${open ? "open" : ""}">
  <div class="cx-row" tabindex="0" data-act="conn-edit" data-id="${i.id}">
    <span class="cx-p"><span class="ptag" title="${inbKind(i)}">${inbShortKind(i)}</span></span>
    <span class="cx-port mono">:${i.port}</span>
    <span class="cx-name">${inbCustom(name, customName(h))}${many ? H`<button type="button" class="cx-more" data-act="conn-exp" data-id="${i.id}" aria-expanded="${open}">${hs.length} 个入口${ic("down")}</button>` : ""}</span>
    <span class="cx-addr">${inbCustom(addr, customAddr(h), true)}</span>
    <span class="cx-st"></span>
    <span class="cx-acts">${can("PUT", "/api/inbounds/x") || inbWH() ? H`<button type="button" class="ib" title="编辑" aria-label="编辑" data-act="conn-edit" data-id="${i.id}">${ic("pen")}</button>` : ""}<button type="button" class="ib" title="更多" aria-label="更多" data-act="conn-menu" data-id="${i.id}">${ic("more")}</button>
    ${INB_MENU === i.id ? H`<div class="menu" role="menu">${inbWH() ? H`<button type="button" data-act="conn-host-new" data-inb="${i.id}">${ic("plus")}再加一个入口</button>` : ""}<div class="msep"></div>${can("DELETE", "/api/inbounds/x") ? H`<button type="button" class="dg" data-act="conn-rm" data-id="${i.id}" data-tag="${inbTag(i)}" data-title="${inbTitle(i, n)}">${ic("trash")}删除</button>` : ""}</div>` : ""}</span>
  </div>
  ${mm ? H`<div class="cx-warn">${ic("warn", "wi")}<span>${mismatchText(mm)}</span><button type="button" class="btn sm" data-act="conn-fix" data-inb="${i.id}" data-id="${h.id}">${ic("undo")}改回跟随节点名</button></div>` : ""}
  ${many && open ? H`<div class="cx-subs">${hs.map(x => H`<div class="cx-sub" data-act="conn-host-edit" data-inb="${i.id}" data-id="${x.id}"><span class="cx-name">${inbCustom(nameOfHost(i, x, n), customName(x))}</span><span class="cx-addr">${inbCustom(addrOfHost(i, x, n), customAddr(x), true)}</span><span class="cx-tg"><span class="ptag lite">${x.address ? "自定义地址" : "节点地址"}</span></span></div>`)}</div>` : ""}
  </div>`;
}

function inbGroup(n, list) {
  const W = inbW(), WH = inbWH();
  return H`<section class="ng">
  <div class="ng-h"><b class="ng-n">${nodeLabel(n)}</b><span class="mono ng-a">${n.address}</span><span class="ng-c">${list.length} 个连接</span><span class="sp"></span>${connPending(n)}${list.length && W ? H`<button class="btn sm" data-act="conn-new" data-node="${n.id}">${ic("plus")}连接</button>` : ""}</div>
  ${list.length ? H`<div class="cx-list">${list.map(i => inbRow(i, INB_HOSTS[i.id] || [], n))}</div>`
    : H`<div class="ng-empty"><div class="ill">${ic("inb")}</div><div><b>这台节点上还没有连接</b><span>加一条，用户刷新订阅后就能用这台节点</span></div><span class="sp"></span>${W || WH ? H`<button class="btn sm pri" data-act="conn-new" data-node="${n.id}">${ic("plus")}添加连接</button>` : ""}</div>`}
  </section>`;
}

// connLoad(nodeIds):拉组件要的全部数据。nodeIds 给了就只留这些节点的入站,
// 但节点列表里没有连接的节点也照样返回(节点详情要显示「还没有连接」)。
let INB_PENDING = new Set();
// 节点组头 / 节点详情里的「有改动未应用 · 应用」。「应用」走节点页同一个 node-sync(确认后 POST /sync)。
function connPending(n) {
  if (!INB_PENDING.has(n.id)) return "";
  return H`<span class="tag pend" data-pending="${n.id}">有改动未应用</span>${can("POST", "/api/nodes/x/sync") ? H`<button type="button" class="btn sm" data-act="node-sync" data-id="${n.id}" data-name="${n.name || ""}" data-label="${nodeLabel(n)}">应用</button>` : ""}`;
}
// 给灰字说明用的节点名:不带国旗。
const plainNodeName = n => String(nodeLabel(n) || n?.name || "节点").replace(/[\u{1F1E6}-\u{1F1FF}]{2}\s*/gu, "").trim() || "节点";
const restartNote = n => `会重启${plainNodeName(n)}上的代理，已连着的用户会断开几秒`;
// 抽屉底部:次要「只…，稍后应用」+ 主按钮「…并应用到节点」,主按钮下面一行灰字。
function applyFoot(n, main, later, id = "i-save") {
  return H`<div class="df df-apply"><div class="df-row"><button type="button" class="btn" id="x-later">${later}</button><button type="submit" class="btn pri" id="${id}">${main}</button></div><p class="df-note" id="x-note">${restartNote(n)}</p></div>`;
}
// 和控制面的判断一致(server/nodepending.go):只改用户数上限、客户端指纹不用应用。
function inbNeedsApply(a, b) {
  const strip = v => { const o = asModelInbound(v); delete o.max_users; delete o.fingerprint; return JSON.stringify(o); };
  return strip(a) !== strip(b);
}
// 写完之后应用到节点;失败不回滚,节点组头会留着「有改动未应用」可重试。
async function applyAfterWrite(n, done) {
  try {
    await write("POST", `/api/nodes/${n.id}/sync`);
    toast(`${done}，已应用到 ${plainNodeName(n)}`);
  } catch (e) {
    toast(`${done}，但应用到 ${plainNodeName(n)} 失败:${e.message}`, true);
  }
}
async function connLoad(nodeIds) {
  const all = await api("/api/inbounds");
  const want = Array.isArray(nodeIds) && nodeIds.length ? new Set(nodeIds.map(Number)) : null;
  const inbounds = want ? all.filter(i => want.has(i.node_id)) : all;
  const rawNodes = await api("/api/nodes");
  const picked = want ? rawNodes.filter(n => want.has(n.id)) : rawNodes;
  // 入口按入站各取一次:控制面没有「全部入口」的接口。
  // 节点的 region / city 只在 metadata 里(节点 JSON 只给拼好的显示名),名称与
  // 节点所在地的对照要用它们;读不到就当没有地区,不提醒。
  // 有未应用改动的节点(老控制面没有这个接口,当作没有)。
  const [hosts, metas, pend] = await Promise.all([
    Promise.all(inbounds.map(i => api(inbPath(i.id) + "/hosts"))),
    Promise.all(picked.map(n => api(`/api/nodes/${n.id}/metadata`).catch(() => null))),
    api("/api/nodes/pending").catch(() => []),
  ]);
  INB_PENDING = new Set((pend || []).map(p => p.node_id));
  const nodes = picked.map((n, k) => ({ ...n, region: metas[k]?.region || "", city: metas[k]?.city || "" }));
  INB_TOTAL = all.length;
  INB_CACHE = { inbounds, nodes, all };
  INB_HOSTS = Object.fromEntries(inbounds.map((i, k) => [i.id, hosts[k]]));
  return { inbounds, nodes, hosts: INB_HOSTS, total: all.length };
}

function connGroupEmpty(n, opts) {
  if (opts && opts.empty != null) return opts.empty;
  return H`<div class="ng-empty"><div class="ill">${ic("inb")}</div><div><b>这台节点上还没有连接</b><span>加一条，用户刷新订阅后就能用这台节点</span></div><span class="sp"></span>${inbW() || inbWH() ? H`<button class="btn sm pri" data-act="conn-new" data-node="${n.id}">${ic("plus")}添加连接</button>` : ""}</div>`;
}

// connGroups(d, opts):所有节点分组。opts.node = 某节点 id 时只渲染那台的连接行,
// 不带组头(节点详情页用);opts.empty 自定义这台节点没有连接时的空状态。
function connGroups(d, opts = {}) {
  if (!d) return H`<div class="card" data-conn-groups><table><tbody>${skRows(6, 4)}</tbody></table></div>`;
  const nodes = d.nodes || [], hosts = d.hosts || INB_HOSTS || {};
  if (opts.node != null) {
    const id = +opts.node;
    const n = nodes.find(x => x.id === id) || { id, address: "" };
    const list = (d.inbounds || []).filter(i => i.node_id === id);
    return H`<section class="ng conn-node" data-conn-groups data-node="${id}"><div class="cx-list">${list.map(i => inbRow(i, hosts[i.id] || [], n))}</div>${list.length ? "" : connGroupEmpty(n, opts)}</section>`;
  }
  const byNode = new Map(nodes.map(n => [n.id, []]));
  const orphan = [];
  for (const i of d.inbounds || []) (byNode.has(i.node_id) ? byNode.get(i.node_id) : orphan).push(i);
  return H`<div class="ngs" data-conn-groups>${nodes.map(n => inbGroup(n, byNode.get(n.id) || []))}${orphan.length ? H`<section class="ng"><div class="ng-h"><b class="ng-n">未知节点</b><span class="ng-c">${orphan.length} 个连接</span></div><div class="cx-list">${orphan.map(i => inbRow(i, hosts[i.id] || [], { address: "", id: i.node_id }))}</div></section>` : ""}</div>`;
}

const CONN = { load: connLoad, groups: connGroups, pending: connPending, count: () => INB_TOTAL ?? STATUS?.counts?.inbounds ?? "" };
window.CONN = CONN;

/* ---------- 入站字段:完整编辑器与「高级」共用 ---------- */
function inbFieldInput(base, k) {
  const [l, ph] = INB_LABEL[k], t = INB_FIELDS.find(f => f[0] === k)[2];
  const fv = Array.isArray(base[k]) ? base[k].join(",") : base[k] ? String(base[k]) : "";
  return t === "b"
    ? H`<label class="chk" data-f="${k}"><input type="checkbox" name="${k}" ${base[k] ? raw("checked") : ""}> ${l}</label>`
    : H`<div class="field" data-f="${k}"><label for="i-${k}">${l}</label><input class="in mono" id="i-${k}" name="${k}" value="${fv}" placeholder="${ph}" autocomplete="off" ${t === "i" ? raw('inputmode="numeric"') : ""}></div>`;
}
function inbFormFields(base, nodes, isNew) {
  const opt = (list, cur) => list.map(x => H`<option value="${x}" ${x === cur ? raw("selected") : ""}>${OPT_NAME[x] || x}</option>`);
  const more = ["tag", "listen", "max_users"].map(k => inbFieldInput(base, k));
  return H`<div class="two"><div class="field"><label for="i-node">节点</label><select class="in" id="i-node" name="node_id" ${isNew ? "" : raw("disabled")}>${nodes.map(n => H`<option value="${n.id}" ${n.id === base.node_id ? raw("selected") : ""}>${nodeLabel(n)}</option>`)}</select></div>
    <div class="field"><label for="i-port">端口</label><input class="in mono" id="i-port" name="port" value="${base.port || ""}" placeholder="443" inputmode="numeric"></div></div>
    <div class="field"><label for="i-protocol">协议</label><select class="in" id="i-protocol" name="protocol">${opt(PROTOCOLS, base.protocol)}</select></div>
    <div class="two"><div class="field"><label for="i-network">传输</label><select class="in" id="i-network" name="network">${opt(NETWORKS, base.network)}</select></div>
    <div class="field"><label for="i-security">安全层</label><select class="in" id="i-security" name="security">${opt(SECURITIES, base.security)}</select></div></div>
    ${["sni", "reality_dest", "short_ids", "cert_file", "key_file", "alpn", "fingerprint", "flow", "ws_path", "grpc_service", "xhttp_path", "xhttp_host", "xhttp_mode", "wg_address", "wg_mtu", "congestion_control"].map(k => inbFieldInput(base, k))}
    <details class="adv"><summary>更多</summary>${more}<div class="stack8 mb">${inbFieldInput(base, "mux")}${inbFieldInput(base, "fragment")}</div></details>`;
}
function inbCollect(form, base, isNew) {
  const f = new FormData(form), err = [], out = {};
  out.node_id = isNew ? +(f.get("node_id") || base.node_id) : base.node_id;
  out.protocol = f.get("protocol"); out.network = f.get("network"); out.security = f.get("security");
  const portS = String(f.get("port") || "").trim();
  out.port = /^\d+$/.test(portS) ? +portS : NaN;
  if (!(out.port >= 1 && out.port <= 65535)) err.push(`端口必须在 1-65535 之间`);
  for (const [k, , t] of INB_FIELDS) {
    const s = t === "b" ? !!f.get(k) : String(f.get(k) ?? "").trim();
    if (t === "i") { if (s && !/^\d+$/.test(s)) err.push(`${INB_LABEL[k]?.[0] || k} 要是整数`); out[k] = s ? +s : 0; }
    else if (t === "l") out[k] = splitCSV(s);
    else out[k] = s;
  }
  return { out, err };
}
function inbBind(form, base, isNew) {
  let lastProto = base.protocol;
  const sync = () => {
    const p = form.querySelector("#i-protocol").value;
    // 与 CLI 一样:选了 QUIC 协议而没另选传输与安全层时,默认 udp + tls。
    if (QUIC(p) && !QUIC(lastProto)) { form.querySelector("#i-network").value = "udp"; form.querySelector("#i-security").value = "tls"; }
    lastProto = p;
    const c = inbCollect(form, base, isNew);
    for (const [k, , , show] of INB_FIELDS) { const el = form.querySelector(`[data-f="${k}"]`); if (el && show) el.hidden = !show(c.out); }
  };
  form.oninput = form.onchange = sync; sync();
  return sync;
}

/* ---------- 完整入站编辑器(节点页 / 其他协议) ---------- */
// applyMode(form, probe, verb):管底部两个按钮。probe() 回 { need, node }:need 为假(比如只改了名称)
// 时只剩一个「保存」,灰字也藏起来。点「稍后应用」= 只写不应用;回车和主按钮 = 写完应用。
function applyMode(form, probe, verb) {
  let now = true, st = { need: true, node: {} };
  const later = form.querySelector("#x-later"), note = form.querySelector("#x-note"), main = form.querySelector(".df-apply button[type=submit]");
  const mainText = main.textContent, laterText = later.textContent;
  const sync = () => {
    try { st = probe(); } catch { /* 表单还没填完 */ }
    // .btn 自带 display,hidden 属性压不住,直接改 display。
    later.style.display = st.need ? "" : "none"; note.style.display = st.need ? "" : "none";
    main.textContent = st.need ? mainText : verb;
    later.textContent = laterText; note.textContent = restartNote(st.node);
  };
  form.addEventListener("input", sync); form.addEventListener("change", sync);
  later.addEventListener("click", () => { now = false; form.requestSubmit(main); });
  main.addEventListener("click", () => { now = true; });
  sync();
  return { now: () => { const v = now; now = true; return v; }, state: () => (sync(), st), btn: () => now ? main : later };
}
async function inboundEditor(id, presetNode) {
  let v = null, nodes = [];
  try { [v, nodes] = await Promise.all([id ? api(inbPath(id)) : null, api("/api/nodes")]); } catch (e) { toast(e.message, true); return; }
  const isNew = !v;
  const base = v ? asModelInbound(v) : { node_id: presetNode || nodes[0]?.id, protocol: "vless", port: 0, network: "tcp", security: "none", wg_mtu: 1420 };
  openDrawer(H`<form id="inb-form" novalidate><div class="dh"><h2>${isNew ? "添加入站" : "编辑入站 " + inbTitle(v, nodes.find(n => n.id === v.node_id))}</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    ${inbFormFields(base, nodes, isNew)}
    <p class="sub">${isNew ? "REALITY 的密钥对由控制面生成。" : "只改用户数上限不用应用到节点。"}</p>
    <div class="err" id="i-err" role="alert"></div>
    ${!isNew && can("DELETE", "/api/inbounds/x") ? H`<div class="card danger-zone mt"><div class="row"><div><b class="bold">删除入站</b><div class="sub">挂在它上面的入口一起删掉。</div></div><span class="sp"></span><button type="button" class="btn sm c-bad" data-act="conn-rm" data-id="${id}" data-tag="${inbTag(v)}" data-title="${inbTitle(v, nodes.find(n => n.id === v.node_id))}">删除</button></div></div>` : ""}
  </div>${applyFoot(nodes.find(n => n.id === base.node_id) || {}, isNew ? "创建并应用到节点" : "保存并应用到节点", isNew ? "只创建，稍后应用" : "只保存，稍后应用")}</form>`);
  const form = $("#inb-form");
  inbBind(form, base, isNew);
  const nodeOf = c => nodes.find(n => n.id === (isNew ? +c.out.node_id || base.node_id : base.node_id)) || { id: base.node_id };
  // 和刚打开时的表单比(表单取值和库里的原值写法不完全一样,直接和 base 比会误报)。
  const c0 = inbCollect(form, base, isNew).out;
  const mode = applyMode(form, () => { const c = inbCollect(form, base, isNew); return { need: isNew || inbNeedsApply({ ...base, ...c0 }, { ...base, ...c.out }), node: nodeOf(c) }; }, isNew ? "创建" : "保存");
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const c = inbCollect(form, base, isNew), btn = mode.btn();
    $("#i-err").textContent = c.err.join("\n");
    if (c.err.length) return;
    btn.disabled = true; btn.classList.add("busy");
    try {
      const st = mode.state(), now = mode.now();
      let done;
      if (isNew) {
        const created = await write("POST", "/api/inbounds", asModelInbound({ ...c.out, id: 0 }));
        done = `已创建入站 ${inbTitle(created, nodes.find(n => n.id === created.node_id))}`;
      } else {
        const fresh = asModelInbound(await api(inbPath(id)));
        const updated = await write("PUT", inbPath(id), asModelInbound({ ...fresh, ...c.out, node_id: fresh.node_id }));
        done = `已更新入站 ${inbTitle(updated, nodes.find(n => n.id === updated.node_id))}`;
      }
      if (st.need && now) await applyAfterWrite(st.node, done);
      else toast(st.need ? `${done}；${plainNodeName(st.node)} 显示「有改动未应用」，随时可以应用` : done);
      closeOverlays(); load(true, true);
    } catch (e2) { $("#i-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

/* ---------- 编辑连接:名称、地址在顶上,入站细节收进「高级」 ---------- */
async function connectionEditor(inbID) {
  let inb, hosts, nodes;
  const seq = ++CONN_OPEN_SEQ;
  try {
    [inb, hosts, nodes] = await Promise.all([api(inbPath(inbID)), api(inbPath(inbID) + "/hosts"), api("/api/nodes")]);
  } catch (e) { toast(e.message, true); return; }
  if (seq !== CONN_OPEN_SEQ) return;
  const n = nodes.find(x => x.id === inb.node_id) || { address: "", id: inb.node_id };
  const h = firstHost(hosts), base = asModelInbound(inb);
  const defName = nameOfHost(inb, h, n);
  const nameCustom = customName(h), addrCustom = customAddr(h);
  const inbName = inbTitle(inb, n);
  openDrawer(H`<form id="conn-form" novalidate><div class="dh"><div class="dt"><h2>${defName}</h2><span class="sub">${inbKind(inb)} · 端口 ${inb.port} · 在 ${nodeLabel(n)} 上</span></div><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    <div class="field"><label for="d-name">名称</label><input class="in" id="d-name" name="d_name" value="${h?.remark || ""}" placeholder="${inb.sub_name || nodeLabel(n)}" autocomplete="off"><span class="h">${nameCustom && h ? H`已自定义 · <a class="lnk" data-act="conn-fix" data-inb="${inb.id}" data-id="${h.id}">改回跟随节点名</a>` : "留空就跟着节点名走"}</span></div>
    <div class="field"><label for="d-addr">地址</label><input class="in mono" id="d-addr" name="d_addr" value="${h?.address || ""}" placeholder="${n.address}" autocomplete="off"><span class="h">${addrCustom ? "已自定义 · 留空改回节点地址" : "留空用节点地址"}</span></div>
    <details class="adv2"><summary>${ic("arrow")}高级<span>协议、端口、${(hosts || []).length > 1 ? `${hosts.length} 个入口` : "入口"}</span></summary>
      <div class="adv-b">
        ${inbFormFields(base, nodes, false)}
        ${h ? H`<div class="ae-h">入口<span>${hosts.length} 个 · 订阅里每个入口一行</span><span class="sp"></span>${inbWH() ? H`<button type="button" class="btn sm ghost" data-act="conn-host-new" data-inb="${inb.id}">${ic("plus")}入口</button>` : ""}</div>
          <div class="ae">${hosts.map(x => H`<div class="ae-r"><div><b class="${customName(x) ? "" : "dim"}">${nameOfHost(inb, x, n)}</b><span class="mono">${addrOfHost(inb, x, n)}</span></div><span class="ptag lite">${x.address ? "自定义地址" : "节点地址"}</span><button type="button" class="ib" title="编辑这个入口" data-act="conn-host-edit" data-inb="${inb.id}" data-id="${x.id}">${ic("pen")}</button></div>`)}</div>`
          : H`<div class="ae-h">入口<span>还没有入口 · 订阅会用入站自己的地址</span><span class="sp"></span>${inbWH() ? H`<button type="button" class="btn sm ghost" data-act="conn-host-new" data-inb="${inb.id}">${ic("plus")}入口</button>` : ""}</div>`}
        ${can("DELETE", "/api/inbounds/x") ? H`<div class="dz"><div><b>删除连接</b><span>它的 ${hosts.length} 个入口一起删掉</span></div><span class="sp"></span><button type="button" class="btn sm c-bad" data-act="conn-rm" data-id="${inb.id}" data-tag="${inbTag(inb)}" data-title="${inbName}">删除</button></div>` : ""}
        <div class="err" id="i-err" role="alert"></div>
      </div></details>
  </div>${applyFoot(n, "保存并应用到节点", "只保存，稍后应用")}</form>`);
  const form = $("#conn-form");
  inbBind(form, base, false);
  // 只改名称、地址(入口)不碰节点;改了协议、端口这些才要应用。
  const c0 = inbCollect(form, base, false).out;
  const mode = applyMode(form, () => ({ need: inbNeedsApply({ ...base, ...c0 }, { ...base, ...inbCollect(form, base, false).out }), node: n }), "保存");
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const c = inbCollect(form, base, false), btn = mode.btn();
    $("#i-err").textContent = c.err.join("\n");
    if (c.err.length) return;
    const name = String(new FormData(form).get("d_name") || "").trim();
    const addr = String(new FormData(form).get("d_addr") || "").trim();
    btn.disabled = true; btn.classList.add("busy");
    const msgs = [];
    try {
      const next = asModelInbound({ ...base, ...c.out, node_id: base.node_id });
      let updated = null;
      if (JSON.stringify(next) !== JSON.stringify(asModelInbound(base))) {
        const fresh = asModelInbound(await api(inbPath(inb.id)));
        updated = await write("PUT", inbPath(inb.id), asModelInbound({ ...fresh, ...c.out, node_id: fresh.node_id }));
        msgs.push(`已更新入站 ${inbTitle(updated, n)}`);
      }
      const cur = asModelHost(h || { inbound_id: inb.id });
      const changed = h ? (name !== (h.remark || "") || addr !== (h.address || "")) : !!(name || addr);
      if (changed && (h || name || addr)) {
        if (h) {
          await write("PUT", "/api/hosts/" + h.id, asModelHost({ ...cur, remark: name, address: addr }));
          msgs.push(`已更新入口「${name || inb.sub_name || nodeLabel(n)}」`);
        } else {
          await write("POST", inbPath(inb.id) + "/hosts", asModelHost({ ...cur, id: 0, inbound_id: inb.id, remark: name, address: addr }));
          msgs.push(`已${name ? "创建入口「" + name + "」" : "创建入口,名字跟随节点名"}`);
        }
      }
      const st = mode.state(), done = msgs.length ? msgs.join("；") : "已保存";
      if (st.need && mode.now()) await applyAfterWrite(n, done);
      else toast(st.need ? `${done}；${plainNodeName(n)} 显示「有改动未应用」，随时可以应用` : done);
      closeOverlays(); load(true, true);
    } catch (e2) { $("#i-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

/* ---------- 添加连接 ---------- */
const PROTO_DEF = { reality: 8443, ws: 2087 };
let INB_ADD = null;
const inbFreePort = (nodeId, want) => {
  const used = new Set((INB_CACHE.inbounds || []).filter(i => i.node_id === nodeId).map(i => i.port));
  let x = want; while (used.has(x)) x++; return x;
};
function drawAddConnection() {
  const nodes = INB_CACHE.nodes || [];
  if (!nodes.length) { toast("先添加一台节点", true); return; }
  let node = nodes.find(n => n.id === INB_ADD.node) || nodes[0];
  INB_ADD.node = node.id;
  const want = PROTO_DEF[INB_ADD.proto], auto = inbFreePort(node.id, want);
  if (!INB_ADD.portTouched) INB_ADD.port = auto;
  const opt = (k, t, h, rec) => H`<div class="opt pcard ${INB_ADD.proto === k ? "on" : ""}" data-act="conn-add-proto" data-k="${k}" role="radio" aria-checked="${INB_ADD.proto === k}" tabindex="0"><span class="rd"></span><div><b>${t}${rec ? H`<span class="rec">推荐</span>` : ""}</b><span class="h">${h}</span></div></div>`;
  openDrawer(H`<form id="add-form" novalidate><div class="dh"><div class="dt"><h2>添加连接</h2><span class="sub">加好后会出现在订阅里</span></div><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    <div class="field"><label for="a-node">节点</label><select class="in" id="a-node">${nodes.map(x => H`<option value="${x.id}" ${x.id === node.id ? raw("selected") : ""}>${nodeLabel(x)}　${x.address}</option>`)}</select></div>
    <div class="field"><label>协议</label><div class="opts">${opt("reality", "VLESS Reality", "不需要域名和证书，伪装成访问普通网站，最省心", true)}${opt("ws", "VLESS WS", "可以套 CDN 隐藏节点地址，需要一个域名")}</div></div>
    <div class="field"><label for="a-port">端口</label><input class="in mono" id="a-port" value="${INB_ADD.port}" inputmode="numeric">
      <span class="h hi" id="a-port-h">${ic("info")}<span>${INB_ADD.portTouched ? "已手动修改" : auto !== want ? `这台节点上 ${want} 已经在用，自动换成 ${auto}` : "自动选的常用端口，可以改"}</span></span></div>
    <div class="pv"><span class="pv-l">订阅里会显示成</span><div class="pv-r"><b id="pv-n">${nodeLabel(node)} · ${CONN_KIND[INB_ADD.proto]}</b><span class="mono" id="pv-a">${node.address}:${INB_ADD.port}</span></div><span class="pv-h">名称跟着节点名走，节点改名这里一起变</span></div>
    <p class="sub"><a class="lnk" data-act="conn-other" data-node="${node.id}">其他协议…</a></p>
    <div class="err" id="add-err" role="alert"></div>
  </div>${applyFoot(node, "创建并应用到节点", "只创建，稍后应用", "a-save")}</form>`);
  const form = $("#add-form");
  const mode = applyMode(form, () => ({ need: true, node }), "创建");
  form.querySelector("#a-node").onchange = e => { INB_ADD.node = +e.target.value; INB_ADD.portTouched = false; drawAddConnection(); };
  const port = form.querySelector("#a-port");
  port.oninput = () => {
    INB_ADD.port = +port.value || ""; INB_ADD.portTouched = true;
    form.querySelector("#a-port-h span").textContent = "已手动修改";
    form.querySelector("#pv-a").textContent = `${node.address}:${INB_ADD.port}`;
  };
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const err = $("#add-err"), btn = mode.btn();
    const p = +port.value;
    if (!(p >= 1 && p <= 65535)) { err.textContent = "端口必须在 1-65535 之间"; return; }
    btn.disabled = true; btn.classList.add("busy");
    const body = asModelInbound({
      id: 0, node_id: node.id, protocol: "vless", port: p,
      network: INB_ADD.proto === "reality" ? "tcp" : "ws",
      security: INB_ADD.proto === "reality" ? "reality" : "none",
      flow: INB_ADD.proto === "reality" ? "xtls-rprx-vision" : "",
      ws_path: INB_ADD.proto === "ws" ? "/ws" : "",
      // REALITY 服务端必须有回落目标与 SNI;与 init/bootstrap 的默认值一致。
      ...(INB_ADD.proto === "reality" ? { sni: "www.cloudflare.com", reality_dest: "www.cloudflare.com:443" } : {}),
    });
    try {
      const created = await write("POST", "/api/inbounds", body);
      const done = `已添加 ${inbTitle(created, node)}`;
      if (mode.now()) await applyAfterWrite(node, done);
      else toast(`${done}；${plainNodeName(node)} 显示「有改动未应用」，随时可以应用`);
      closeOverlays(); load(true, true);
    } catch (e2) { err.textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

/* ---------- 入口(host)编辑器 ---------- */
// 抽屉要先拉数据再打开:连点两下或在一个抽屉里打开另一个时,只让最后一次打开的生效,
// 先发出去的那个请求回来后不再把抽屉换掉。
let CONN_OPEN_SEQ = 0;
async function hostEditor(inbID, hostID) {
  let hosts = [];
  const seq = ++CONN_OPEN_SEQ;
  try { hosts = await api(inbPath(inbID) + "/hosts"); } catch (e) { toast(e.message, true); return; }
  if (seq !== CONN_OPEN_SEQ) return;
  const found = hostID ? hosts.find(h => h.id === hostID) : null;
  if (hostID && !found) { toast(`入站 ${inbID} 上没有 id 为 ${hostID} 的对外入口`, true); return; }
  const isNew = !found, h = found ? asModelHost(found) : { inbound_id: inbID, remark: "", address: "", port: 0 };
  const F = [["remark", "--remark", "客户端看到的名字", "香港 · 直连", "留空用节点显示名;可用 {SERVER_IP} {NODE_NAME} {NODE_DISPLAY} {PROTOCOL} {TRANSPORT}"], ["address", "--address", "对外地址", "留空用节点地址", ""], ["port", "--port", "对外端口", "留空用入站端口", ""],
    ["sni", "--sni", "SNI", "", ""], ["host", "--host", "Host 头", "ws / grpc", ""], ["path", "--path", "路径", "ws", ""], ["alpn", "--alpn", "ALPN", "逗号分隔", ""]];
  const fv = k => Array.isArray(h[k]) ? h[k].join(",") : h[k] ? String(h[k]) : "";
  const hostName = found ? (found.sub_name || found.remark || "入口") : "";
  openDrawer(H`<form id="host-form" novalidate><div class="dh"><h2>${isNew ? `给「${inbTitleById(inbID)}」加入口` : `编辑入口「${hostName}」`}</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    ${F.map(([k, , l, ph, hint]) => H`<div class="field"><label for="h-${k}">${l}</label><input class="in ${k === "remark" ? "" : "mono"}" id="h-${k}" name="${k}" value="${fv(k)}" placeholder="${ph}" autocomplete="off">${hint ? H`<span class="h">${hint}</span>` : ""}</div>`)}
    ${found ? H`<p class="sub">现在发出去的是 <code>${found.effective.address}:${found.effective.port}</code>。改地址或端口,用户下次刷新订阅就跟着变;不用应用到节点。</p>` : H`<p class="sub">入口只影响订阅里写什么,不用下发节点配置。</p>`}
    <div class="err" id="h-err" role="alert"></div>
    ${found && can("DELETE", "/api/hosts/x") ? H`<div class="card danger-zone mt"><div class="row"><div><b class="bold">删除入口</b><div class="sub">只影响订阅;删掉最后一个入口,订阅回落到入站自己的地址。</div></div><span class="sp"></span><button type="button" class="btn sm c-bad" data-act="conn-host-rm" data-id="${hostID}" data-remark="${hostName}">删除</button></div></div>` : ""}
  </div><div class="df"><button type="button" class="btn" data-act="close">取消</button><button type="submit" class="btn pri" id="h-save">${isNew ? "创建" : "保存"}</button></div></form>`);
  const form = $("#host-form");
  const collect = () => {
    const f = new FormData(form), out = {}, err = [];
    for (const [k] of F) {
      const s = String(f.get(k) || "").trim();
      if (k === "port") { if (s && !/^\d+$/.test(s)) err.push("对外端口要是整数"); out.port = s ? +s : 0; }
      else if (k === "alpn") out.alpn = splitCSV(s);
      else out[k] = s;
    }
    return { out, err };
  };
  form.oninput = form.onchange = () => collect();
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const c = collect(), btn = $("#h-save");
    $("#h-err").textContent = c.err.join("\n");
    if (c.err.length) return;
    btn.disabled = true; btn.classList.add("busy");
    try {
      if (isNew) {
        const created = await write("POST", inbPath(inbID) + "/hosts", asModelHost({ ...c.out, id: 0, inbound_id: inbID }));
        toast(c.out.remark ? `已创建入口「${c.out.remark}」` : "已创建入口,名字跟随节点名");
      } else {
        // 与 host edit 一致:先从所属入站的列表里找到它,再整体 PUT。
        const now = (await api(inbPath(inbID) + "/hosts")).find(x => x.id === hostID);
        if (!now) throw new Error(`入站 ${inbID} 上没有 id 为 ${hostID} 的对外入口`);
        await write("PUT", "/api/hosts/" + hostID, asModelHost({ ...asModelHost(now), ...c.out }));
        toast(`已更新入口「${c.out.remark || hostName}」`);
      }
      closeOverlays(); load(true, true);
    } catch (e2) { $("#h-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

/* ---------- 动作 ---------- */
Object.assign(ICONS, {
  pen: '<path d="M4 20h4L19 9a2.8 2.8 0 0 0-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>',
  undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10a6 6 0 0 1 0 12h-3"/>',
});

// 组件自己的动作都用 conn-*。inb-new 是节点页的「添加入站」(完整入站表单),
// 其余旧名字保留成别名,给 nodes 页和还没改过来的测试用。
const CONN_ACTS = {
  // 组件里的「添加连接」:打开添加连接抽屉,并预选这台节点。
  "conn-new": d => {
    const nid = d.node ? +d.node : 0;
    INB_ADD = { node: nid || INB_CACHE.nodes?.[0]?.id, proto: "reality", portTouched: false };
    drawAddConnection();
  },
  "conn-other": d => inboundEditor(null, +d.node || 0),
  "conn-edit": (d, el, ev) => {
    // 整行可点;点到行里别的按钮(展开入口、更多、改回)不算。行尾的铅笔按钮本身就是 conn-edit,要放行。
    const b = ev && ev.target.closest("button,.cx-more,.menu,.cx-warn");
    if (b && b !== el) return;
    connectionEditor(+d.id);
  },
  "conn-menu": (d, el, ev) => { ev.stopPropagation(); INB_MENU = INB_MENU === +d.id ? null : +d.id; paint(); },
  "conn-exp": (d, el, ev) => { ev.stopPropagation(); const id = +d.id; INB_EXP.has(id) ? INB_EXP.delete(id) : INB_EXP.add(id); paint(); },
  "conn-add-proto": d => { INB_ADD.proto = d.k; INB_ADD.portTouched = false; drawAddConnection(); },
  // 改回跟随节点名:整体读回当前入口,只把 remark 置空再 PUT。
  "conn-fix": async d => {
    const inbID = +d.inb, hostID = +d.id;
    try {
      let h = (INB_HOSTS[inbID] || []).find(x => x.id === hostID);
      if (!h) h = (await api(inbPath(inbID) + "/hosts")).find(x => x.id === hostID);
      if (!h) throw new Error("入口已经不存在了");
      await write("PUT", "/api/hosts/" + hostID, asModelHost({ ...asModelHost(h), remark: "" }));
      toast("已改回跟随节点名");
      closeOverlays(); load(true, true);
    } catch (e) { toast(e.message, true); }
  },
  "conn-rm": d => {
    const inb = (INB_CACHE.all || INB_CACHE.inbounds || []).find(i => i.id === +d.id);
    const n = (INB_CACHE.nodes || []).find(x => x.id === inb?.node_id) || (inb ? { id: inb.node_id } : null);
    const title = d.title || inbTitleById(+d.id), done = `已删除入站「${title}」`;
    const del = async now => {
      await write("DELETE", inbPath(d.id));
      if (n && now) await applyAfterWrite(n, done);
      else toast(n ? `${done}；${plainNodeName(n)} 显示「有改动未应用」，应用后节点才停止监听` : done);
      closeOverlays(); load(true, true);
    };
    confirmBox({ title: `删除连接「${title}」?`, text: H`不可恢复:挂在它上面的入口一起删掉,用它的客户端下次刷新订阅后就没了这个入口。${n ? H`<br><span class="sub">${restartNote(n)}</span>` : ""}`,
      ok: n ? "删除并应用到节点" : "删除连接", danger: true, typed: d.tag,
      alt: n ? { label: "只删除，稍后应用", run: () => del(false) } : null,
      run: () => del(true) });
  },
  "conn-host-new": d => hostEditor(+d.inb, 0),
  "conn-host-edit": d => hostEditor(+d.inb, +d.id),
  "conn-host-rm": d => confirmBox({ title: `删除入口「${d.remark || "入口"}」?`, text: "客户端下次刷新订阅后这个地址就没了;节点和已连着的用户不受影响。", ok: "删除入口", danger: true,
    run: async () => { await write("DELETE", "/api/hosts/" + d.id, undefined, `已删除入口「${d.remark || "入口"}」`); closeOverlays(); load(true, true); } }),
};
Object.assign(ACTS, CONN_ACTS, {
  // 旧动作名 → 组件动作(别名)。nodes 页与老测试还在用。
  "inb-edit": CONN_ACTS["conn-edit"], "inb-exp": CONN_ACTS["conn-exp"], "inb-menu": CONN_ACTS["conn-menu"],
  "inb-fix": CONN_ACTS["conn-fix"], "inb-rm": CONN_ACTS["conn-rm"], "inb-add-proto": CONN_ACTS["conn-add-proto"],
  "inb-other": CONN_ACTS["conn-other"],
  "host-new": CONN_ACTS["conn-host-new"], "host-edit": CONN_ACTS["conn-host-edit"], "host-rm": CONN_ACTS["conn-host-rm"],
  // 节点页的「添加入站」仍然打开完整的入站表单。
  "inb-new": d => {
    const nid = d.node ? +d.node : 0;
    if (route[0] === "inbounds") CONN_ACTS["conn-new"](d);
    else inboundEditor(null, nid);
  },
});

// 行上的键盘:Enter 打开编辑。
document.addEventListener("keydown", e => {
  if (e.key === "Enter" && e.target.matches?.(".cx-row")) { e.preventDefault(); connectionEditor(+e.target.dataset.id); }
});
// 点空白处收起「更多」菜单。
document.addEventListener("click", e => {
  if (INB_MENU !== null && !e.target.closest(".menu,[data-act=conn-menu],[data-act=inb-menu]")) { INB_MENU = null; paint(); }
}, true);
