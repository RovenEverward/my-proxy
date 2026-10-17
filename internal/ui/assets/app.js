// mpx ui 的页面。不用框架:每个页面是一个 load() + render(),渲染成字符串交给
// innerHTML;所有插进去的数据都经过 H`` 转义。交互用 data-act 委托,不写行内脚本
// (CSP 不允许)。页面只认控制面 /api 的原始 JSON,判定一律以控制面为准。
"use strict";

/* ---------- 基础 ---------- */
// 区域指示符国旗的识别与本地 SVG 名字,替换逻辑见文件末尾的「国旗」一节。
const RI = /[\u{1F1E6}-\u{1F1FF}]/u;
const RI_PAIR = /([\u{1F1E6}-\u{1F1FF}])([\u{1F1E6}-\u{1F1FF}])/gu;
const riCode = (a, b) => String.fromCharCode(97 + a.codePointAt(0) - 0x1F1E6, 97 + b.codePointAt(0) - 0x1F1E6);
const $ = (s, el = document) => el.querySelector(s);
class Raw { constructor(s) { this.s = s; } toString() { return this.s; } }
const raw = s => new Raw(s);
const esc = v => String(v ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const val = v => v instanceof Raw ? v.s : Array.isArray(v) ? v.map(val).join("") : esc(v);
function H(strings, ...vals) { let s = strings[0]; vals.forEach((v, i) => { s += val(v) + strings[i + 1]; }); return raw(s); }

let writeGen = 0; // 写请求的代数,见 pageData
// 界面里指代节点一律用显示名(🇺🇸 洛杉矶 A);内部名(la-1)只在节点详情与编辑里作次要信息。
// NODE_LABELS:内部名 → 显示名,每次拉到节点列表时记下,给只带内部名的数据(更新弹窗、退避)用。
const nodeLabel = n => (n && (n.display || n.name)) || "";
const NODE_LABELS = {};
const labelOfName = name => NODE_LABELS[name] || name;
// shortVer:版本号只取开头的 vX.Y.Z(伪版本、dirty 尾巴都舍掉),匹配不到就原样显示。
const shortVer = v => { const m = /^v?\d+\.\d+\.\d+/.exec(String(v || "")); return m ? (m[0][0] === "v" ? m[0] : "v" + m[0]) : (v ?? ""); };
class APIError extends Error { constructor(status, msg) { super(msg); this.status = status; } }
async function api(path, init = {}) {
  for (let attempt = 0; ; attempt++) {
    let res;
    try {
      res = await fetch(path, { credentials: "same-origin", ...init, headers: { Accept: "application/json", ...(init.headers || {}) } });
    } catch (e) { throw new APIError(0, "连不上本地服务:MPX 可能已经退出,请重新打开"); }
    // 写请求回来之后,之前发出的读都可能是旧的:换一代,load 不再复用它们(见 pageData)。
    if ((init.method || "GET") !== "GET") writeGen++;
    // 控制面 /api 按 IP 限流,被拒的请求没有执行过,等一下再试是安全的(同 CLI)。
    // 只重试限流闸门的 429(带 Retry-After);体检自己的频率限制也回 429,但不带这个头,
    // 那是「这台刚测过」,重试只会再被拒一次。
    if (res.status === 429 && res.headers.get("Retry-After") && attempt < 3) { await sleep(1000 * (+res.headers.get("Retry-After") || 2)); continue; }
    const text = await res.text();
    // asText:要原文(模板),不要把看着像 JSON 的正文解析掉。
    if (init.asText && res.ok) return text;
    if (res.ok && (init.method || "GET") === "GET" && /^\/api\/nodes(\?|$)/.test(path)) { try { for (const n of JSON.parse(text) || []) NODE_LABELS[n.name] = nodeLabel(n); } catch {} }
    let body = null;
    try { body = text ? JSON.parse(text) : null; } catch { body = text; }
    // 控制面给界面的说法(ui_hint,不提命令)优先;没有才用 error 原话。
    if (!res.ok) throw new APIError(res.status, (body && (body.ui_hint || body.error)) || text || res.statusText);
    return body;
  }
}
const sleep = ms => new Promise(r => setTimeout(r, ms));

const ICONS = {
  home: '<path d="M3 10.5 12 4l9 6.5V20a1 1 0 0 1-1 1h-5v-6h-6v6H4a1 1 0 0 1-1-1z"/>',
  users: '<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20c.6-3.6 3.3-5.5 6.5-5.5s5.9 1.9 6.5 5.5"/><path d="M16 4.6a3.5 3.5 0 0 1 0 6.8M18 14.8c2 .7 3.2 2.5 3.5 5.2"/>',
  nodes: '<rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/><path d="M7 7h.01M7 17h.01"/>',
  inb: '<path d="M4 12h10M10 8l4 4-4 4"/><path d="M14 4h5a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-5"/>',
  set: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M2 12h3M19 12h3M4.9 19.1 7 17M17 7l2.1-2.1"/>',
  moon: '<path d="M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z"/>',
  sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
  plus: '<path d="M12 5v14M5 12h14"/>', up: '<path d="M12 21V9M7 14l5-5 5 5M4 3h16"/>', check: '<path d="m5 12 5 5 9-10"/>', search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
  refresh: '<path d="M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7"/>', x: '<path d="M6 6l12 12M18 6 6 18"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>', undo: '<path d="M9 14 4 9l5-5"/><path d="M4 9h10a6 6 0 0 1 0 12h-3"/>',
  pause: '<rect x="7" y="5" width="3.5" height="14" rx="1"/><rect x="13.5" y="5" width="3.5" height="14" rx="1"/>', key: '<circle cx="8" cy="15" r="4"/><path d="m11 12 9-9M17 6l3 3"/>',
  qr: '<rect x="4" y="4" width="6" height="6" rx="1"/><rect x="14" y="4" width="6" height="6" rx="1"/><rect x="4" y="14" width="6" height="6" rx="1"/><path d="M14 14h2v2h-2zM18 18h2v2h-2zM14 18h2M18 14h2"/>',
  bell: '<path d="M6 16V11a6 6 0 1 1 12 0v5l1.5 2h-15z"/><path d="M10 20a2 2 0 0 0 4 0"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
  warn: '<path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/>',
  copy: '<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>',
  dl: '<path d="M12 3v12M7 10l5 5 5-5M4 21h16"/>', term: '<path d="m4 17 6-5-6-5M12 19h8"/>',
  more: '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
  arrow: '<path d="M9 6l6 6-6 6"/>', chevup: '<path d="m6 15 6-6 6 6"/>', check: '<path d="M5 12.5 10 17 19 7"/>',
  tpl: '<path d="M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8z"/><path d="M14 3v5h5M9 13h6M9 17h4"/>',
  plan: '<path d="m12 3 9 5-9 5-9-5z"/><path d="m3 13 9 5 9-5"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/>',
  ban: '<circle cx="12" cy="12" r="9"/><path d="m5.6 5.6 12.8 12.8"/>',
  trash: '<path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13"/>',
  down: '<path d="m6 9 6 6 6-6"/>',
  sub: '<path d="M5 11a8 8 0 0 1 8 8M5 5a14 14 0 0 1 14 14"/><circle cx="6" cy="18" r="1.4"/>',
  split: '<path d="M6 3v6a4 4 0 0 0 4 4h4a4 4 0 0 1 4 4v4M18 3v6a4 4 0 0 1-4 4"/><path d="m3 6 3-3 3 3M15 6l3-3 3 3"/>',
  pulse: '<path d="M3 12h4l2.5-6 5 12 2.5-6H21"/>',
  swap: '<path d="M7 7h12l-3-3M17 17H5l3 3"/>',
};
const ic = (n, cls = "") => raw(`<svg class="${cls}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICONS[n]}</svg>`);

/* ---------- 格式 ---------- */
const GiB = 1 << 30, TiB = 2 ** 40;
const fmtBytes = b => { b = +b || 0; if (b >= TiB) return (b / TiB).toFixed(2) + " TB"; if (b >= GiB) return (b / GiB).toFixed(1) + " GB"; if (b >= 1 << 20) return (b / (1 << 20)).toFixed(1) + " MB"; if (b >= 1024) return (b / 1024).toFixed(1) + " KB"; return b + " B"; };
const pad = n => String(n).padStart(2, "0");
const ymd = d => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
const hm = d => `${pad(d.getHours())}:${pad(d.getMinutes())}`;
const hms = d => `${hm(d)}:${pad(d.getSeconds())}`;
const DAY = 864e5;
// 到期日以「当地日历日」计:23:59:59 到期的那天还算剩 0 天。
const daysLeft = iso => { if (!iso) return null; const e = new Date(iso), n = new Date(); const a = new Date(e.getFullYear(), e.getMonth(), e.getDate()), b = new Date(n.getFullYear(), n.getMonth(), n.getDate()); return Math.round((a - b) / DAY); };
const fmtDur = s => { s = Math.floor(+s || 0); const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60); return d ? `${d} 天 ${h} 小时` : h ? `${h} 小时 ${m} 分` : `${m} 分钟`; };
// fmtSecs:秒数写成中文单位,不到 0.01 秒不写成「0.00 秒」。
const fmtSecs = v => { v = +v || 0; if (v < 0.01) return "不到 0.01 秒"; if (v < 60) return `${v.toFixed(v < 10 ? 2 : 1)} 秒`; return fmtDur(v); };
const sinceText = iso => { if (!iso) return "—"; const d = new Date(iso), s = (Date.now() - d) / 1e3; if (s < 60) return "刚刚"; if (s < 3600) return `${Math.floor(s / 60)} 分钟前`; if (s < 86400) return hm(d); return ymd(d); };

const USER_ST = { active: ["ok", "正常"], disabled: ["", "已停用"], expired: ["warn", "已过期"], limited: ["warn", "已超限"] };
const stBadge = (cls, text) => H`<span class="st"><span class="dot ${cls}"></span>${text}</span>`;
const userSt = s => stBadge(...(USER_ST[s] || ["", s]));
const RESET = { none: "不重置", daily: "每天", weekly: "每周", monthly: "每月" };
const pct = u => u.data_limit ? Math.min(100, u.used_traffic / u.data_limit * 100) : 0;
function usageBar(u, wide) {
  if (!u.data_limit) return H`<div class="use"><span class="keep">${fmtBytes(u.used_traffic)} <span class="c-faint">· 不限</span></span></div>`;
  const p = pct(u), cls = p >= 100 ? "bad" : p >= 80 ? "warn" : "";
  return H`<div class="use"><div class="bar${wide ? " w100" : ""}"><i class="${cls}" style="width:${p.toFixed(1)}%"></i></div><span>${fmtBytes(u.used_traffic)} / ${fmtBytes(u.data_limit)}</span></div>`;
}
function expireCell(u) {
  if (!u.expire_at) return H`<span class="sub">永不过期</span>`;
  const d = daysLeft(u.expire_at), cls = d < 0 ? "c-bad" : d <= EXPIRE_SOON_DAYS ? "c-warn" : "";
  return H`${ymd(new Date(u.expire_at))}<div class="sub ${cls}">${d < 0 ? `已过 ${-d} 天` : d === 0 ? "今天到期" : `剩 ${d} 天`}</div>`;
}
const avatar = n => H`<span class="av">${[...String(n)][0]?.toUpperCase() || "?"}</span>`;
function nodeSt(n) {
  if (!n.status) return n.draining ? stBadge("info", "已暂停分配") : stBadge("", "未巡检");
  if (!n.status.reachable) return stBadge("bad", "连不上");
  if (n.draining) return stBadge("info", "已暂停分配");
  if (!n.status.active) return stBadge("warn", "核心未运行");
  return stBadge("ok", "运行中");
}
// 快到期:剩 7 天以内(与原型确认的阈值一致)。快用完:按控制面的通知阈值。
const EXPIRE_SOON_DAYS = 7;

/* ---------- 共享的块 ---------- */
const skRows = (n, c) => raw(Array.from({ length: n }, () => `<tr>${Array.from({ length: c }, (_, i) => `<td><span class="sk" style="width:${[70, 50, 90, 40, 60][i % 5]}%"></span></td>`).join("")}</tr>`).join(""));
function errorBox(what, e) {
  const auth = e && e.status === 401;
  return H`<div class="empty" role="alert"><div class="ill bad">${ic("warn")}</div><h4>读不到${what}</h4>
  <p>${e ? e.message : ""}</p>${auth ? H`<p class="sub mt">登录状态已失效,请重新打开 MPX 并登录。</p>` : ""}
  <div class="mt"><button class="btn sm" data-act="reload">${ic("refresh")}重试</button></div></div>`;
}
const emptyBox = (icon, title, text, cmd, btn = "") => H`<div class="empty"><div class="ill">${ic(icon)}</div><h4>${title}</h4><p>${text}</p>${btn}${cmd ? H`<div class="cmd mt">${cmd}</div>` : ""}</div>`;

/* ---------- 页面注册 ---------- */
// 每个页面:{ title, nav: [组, 图标, 排序], load(route) → data, render(data, route) → H }
const PAGES = {};
let META = { server: "", profile: "", allow: ["GET /api"] };
// 页面只在转发层放行时才显示写操作(放行规则见 internal/cli/ui.go)。
const can = (method, path) => META.allow.some(a => { const [m, p] = a.split(" "); return m === method && (path === p || path.startsWith(p.replace(/\/$/, "") + "/")); });
let route = [], current = { page: null, data: null, error: null, loading: true, at: null };

/* ---------- 更新 pill ---------- */
// GET /local/update 的最近一次结论;null 表示还没问过。弹窗读它,不重复请求。
let UPDATE = null;
const UPD_SKIP_KEY = "mpx-upd-skip";
// 本机那一层与「正在运行的程序」的叫法:桌面构建(/local/update 的 desktop 为
// true)都叫「桌面程序」,浏览器模式是「本机程序」与「本机界面」;还没问到结论时
// 按浏览器模式叫。
const updSelfName = () => UPDATE && UPDATE.desktop ? "桌面程序" : "本机程序";
const updProgName = () => UPDATE && UPDATE.desktop ? "桌面程序" : "本机界面";

// cmpVer 给版本号做粗略排序:按数字段比,段数不够的算小。"v" 前缀与预发布后缀不计。
function cmpVer(a, b) {
  const seg = v => String(v || "").replace(/^v/i, "").split(/[.\-+]/).map(x => /^\d+$/.test(x) ? +x : 0);
  const A = seg(a), B = seg(b);
  for (let i = 0; i < Math.max(A.length, B.length); i++) { const d = (A[i] || 0) - (B[i] || 0); if (d) return d; }
  return 0;
}
// isDevVer:开发版(v0.0.0-…、带 dev 的、空的)没有可比的发布号,不提示「有新版本」。
function isDevVer(v) { v = String(v || "").trim().toLowerCase(); return !v || /^v?0\.0\.0(\b|[-+.]|$)/.test(v) || /(^|[^a-z])dev([^a-z]|$)/.test(v); }
// updLatest 取 cli/control 里较新且非空的 latest;都空时返回 ""。skip 以它为准。
function updLatest(u) {
  let out = "";
  for (const p of [u && u.control, u && u.cli]) if (p && p.latest && (!out || cmpVer(p.latest, out) > 0)) out = p.latest;
  return out;
}
function updSkipped() { try { return sessionStorage.getItem(UPD_SKIP_KEY) || ""; } catch { return ""; } }
// sameVer:同一版本的粗判,忽略空白与打头的 v —— 节点核心报的 current / target
// 一个带 v 一个不带时,直接字符串比会把同一版判成两版。
function sameVer(a, b) {
  a = String(a || "").trim().replace(/^v/, "");
  b = String(b || "").trim().replace(/^v/, "");
  return a !== "" && a === b;
}
// 节点核心这一行有没有得升:target 为空(没查到或没钉版本)、还不知道节点上是哪一版(没巡检过)、
// 或已等于 target 都算没有 —— 不知道现状就不催人更新。
function nodeUpgradable(n) { return !!n.target && !!String(n.current || "").trim() && !sameVer(n.current, n.target); }
// 有可升级项、且本会话没对这个版本点过「稍后」时才显示 pill。可升级 = 本机 /
// 控制面有新发布,或任一节点核心不在推荐版本上。
function showUpdatePill(u) {
  if (!u) return false;
  const c = u.control || {}, l = u.cli || {};
  const nodes = Array.isArray(u.nodes) ? u.nodes : [];
  const cAvail = c.available && !isDevVer(c.current), lAvail = l.available && !isDevVer(l.current);
  if (!(cAvail || lAvail || nodes.some(nodeUpgradable))) return false;
  const latest = updLatest(u);
  return !(latest && updSkipped() === latest);
}
// 失败静默:保留上次状态,不打扰用户。同一轮里多个 load 只真问一次。
let updAt = 0, updBusy = false;
async function refreshUpdatePill(force) {
  if (updBusy) return;
  if (!force && Date.now() - updAt < 30000) return;
  updBusy = true;
  let u;
  try { u = await api("/local/update"); } catch { updBusy = false; return; }
  updAt = Date.now(); updBusy = false;
  UPDATE = u;
  const b = $("#upd-pill");
  if (b) b.hidden = !showUpdatePill(u);
}
function closeModal() { $("#modal").classList.remove("open"); if (!$("#drawer.open")) $("#scrim").classList.remove("open"); updPollStop(); }
function updTitle(u) {
  const c = u.control || {}, l = u.cli || {};
  const ver = c.latest && l.latest && c.latest !== l.latest ? `控制面 ${c.latest} / 本机 ${l.latest}` : (c.latest || l.latest || "");
  return `更新到 ${ver}`.trim();
}
function updRow(name, p) {
  p = p || {};
  const to = p.latest || "—";
  return H`<div class="upd-row"><b>${name}</b><span class="mono">${p.current || "未知"}</span><span class="upd-arrow">→</span><span class="mono">${to}</span><span class="sp"></span>${p.available ? H`<span class="tag g">可升级</span>` : H`<span class="sub">已是最新</span>`}</div>`;
}
// 节点核心一行:「🇺🇸 洛杉矶 A · xray 26.3.27 → 26.4.1」;已在推荐版本上的只报现版本。
function nodeUpdRow(n) {
  const up = nodeUpgradable(n);
  return H`<div class="upd-row"><b>${labelOfName(n.name)}</b><span class="sub">${n.core}</span>
  ${up ? H`<span class="mono">${n.current || "未知"}</span><span class="upd-arrow">→</span><span class="mono">${n.target}</span>` : H`<span class="mono">${n.current || "未知"}</span>`}
  <span class="sp"></span>${up ? H`<span class="tag g">可升级</span>` : H`<span class="sub">已是推荐版本</span>`}</div>`;
}
// openUpdateModal 直接写 #modal,与 confirmBox 同一套 .mbody / .mf 结构。
function openUpdateModal() {
  if (updAll) { renderUpdAll(); return; } // 一轮正在进行:直接回进度视图
  const u = UPDATE;
  if (!u) return;
  const c = u.control || {}, l = u.cli || {};
  const nodes = Array.isArray(u.nodes) ? u.nodes : [];
  const m = $("#modal");
  m.innerHTML = val(H`<div class="mbody"><h2>${updTitle(u)}</h2>
  ${updRow(updSelfName(), l)}${updRow("控制面", c)}${nodes.map(nodeUpdRow)}
  ${releaseHighlights(u.notes).length ? H`<div class="upd-notes"><div class="upd-hl-t">这次更新</div><ul class="upd-hl">${releaseHighlights(u.notes).map(t => H`<li>${raw(inlineMd(t))}</li>`)}</ul></div>` : ""}
  ${(c.release_url || l.release_url) ? H`<p><a class="u-link" target="_blank" rel="noopener" href="${c.release_url || l.release_url}">查看发布说明</a></p>` : ""}
  <p class="sub mt">${u.desktop ? "将依次升级控制面、节点、桌面程序,完成后自动重启。" : "将依次升级控制面、节点、本机程序,完成后自动重启。"}</p>
  <div class="err" id="upd-err" role="alert"></div></div>
  ${updFooter(u, false)}`);
  $("#scrim").classList.add("open"); m.classList.add("open");
  updPollStop();
  updPollStart();
  if (!(u.prefetch && u.prefetch.state === "downloading")) setTimeout(() => $("#upd-ok")?.focus(), 50);
}
// 弹窗脚下那一排:有 prefetch 字段时主按钮跟着后台下载状态走;没有这个字段
// (老 mpx ui)保持原来的「重启以更新」。kept 为真时省略「稍后」(轮询只换按钮排)。
function updFooter(u, kept) {
  const p = u && u.prefetch;
  const skip = kept ? "" : H`<button class="btn" data-act="update-skip">稍后</button>`;
  if (!p) return H`<div class="mf">${skip}<button class="btn pri" id="upd-ok" data-act="update-apply">重启以更新</button></div>`;
  if (p.state === "downloading") {
    const pct = p.total > 0 ? " " + Math.min(100, Math.floor((p.bytes || 0) * 100 / p.total)) + "%" : "";
    return H`<div class="mf">${skip}<button class="btn pri" id="upd-ok" data-act="update-apply" disabled>正在后台下载…${pct}</button></div>`;
  }
  if (p.state === "failed") {
    return H`<div class="mf">${skip}${H`<span class="sub">下载失败，稍后自动重试</span><button class="btn ghost sm" id="upd-retry" data-act="update-prefetch-retry">立即重试</button>`}<button class="btn pri" id="upd-ok" data-act="update-apply" disabled>重启以更新</button></div>`;
  }
  return H`<div class="mf">${skip}<button class="btn pri" id="upd-ok" data-act="update-apply">重启以更新</button></div>`;
}
// 弹窗开着时每 2 秒问一次 /local/update,只更新按钮那一排,不打断正文滚动。
// 进入下载中以外的状态就停;关掉弹窗也停。
let updPollTimer = 0;
function updPollStop() { clearTimeout(updPollTimer); updPollTimer = 0; }
function updPollStart() {
  updPollStop();
  const p = UPDATE && UPDATE.prefetch;
  if (!p || p.state !== "downloading") return;
  updPollTimer = setTimeout(updPollTick, 2000);
}
async function updPollTick() {
  updPollTimer = 0;
  if (!$("#modal.open") || updAll) return;
  let u;
  try { u = await api("/local/update"); } catch { updPollTimer = setTimeout(updPollTick, 2000); return; }
  UPDATE = u; updAt = Date.now();
  const p = u.prefetch;
  if (p && p.state === "downloading") updAdjustDownloading(p);
  else { const mf = $("#modal .mf"); if (mf) mf.replaceWith(dom(updFooter(u, true))); }
  if (p && p.state === "downloading") updPollTimer = setTimeout(updPollTick, 2000);
}
// 下载中只改文字,不重画整排按钮,免得每 2 秒把焦点/布局抖一下。
function updAdjustDownloading(p) {
  const b = $("#upd-ok");
  if (!b) return;
  const pct = p.total > 0 ? " " + Math.min(100, Math.floor((p.bytes || 0) * 100 / p.total)) + "%" : "";
  b.textContent = "正在后台下载…" + pct;
  b.disabled = true;
}
// dom 把 H 片段变成可以 replaceWith 的节点。
function dom(html) { const t = document.createElement("template"); t.innerHTML = val(html).trim(); return t.content.firstElementChild; }
// 一键更新:POST /local/update/all 启动编排,GET ?since= 增量拉进度直到
// done / failed / restarting。409 表示已有一轮在跑,直接进轮询。
let updAll = null;
const UPD_STEP_NAMES = { control: "控制面", nodes: "节点", cli: "本机", restart: "重启" };
// 进度视图里步骤组的名字;本机那一组在桌面构建下叫「桌面程序」。
const updStepName = s => s === "cli" && UPDATE && UPDATE.desktop ? "桌面程序" : (UPD_STEP_NAMES[s] || s);
const UPD_ALL_MIN = 1000, UPD_ALL_MAX = 5000;
async function applyUpdate(el) {
  el.disabled = true; el.classList.add("busy");
  try {
    try {
      await api("/local/update/all", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
    } catch (e) {
      if (e.status !== 409) throw e;
    }
  } catch (e) {
    const box = $("#upd-err");
    if (box) box.textContent = e.message;
    el.disabled = false; el.classList.remove("busy");
    return;
  }
  updAll = { since: 0, events: [], delay: UPD_ALL_MIN, timer: 0, state: "" };
  renderUpdAll();
  pollUpdateAll();
}
// 轮询节奏:有新事件回到 1 秒,没有就翻倍,上限 5 秒;页面隐藏时一拍都不发,
// 回到可见时由 visibilitychange 立刻补一次。游标用最后一条事件的 seq 而不是
// 响应里的 seq 总数:单次响应有条数 / 大小上限,总数会跳过被截掉的事件。
async function pollUpdateAll() {
  const a = updAll;
  if (!a || document.hidden) return;
  let snap = null;
  try { snap = await api("/local/update/all?since=" + a.since); }
  catch (e) {
    // 进度接口连不上:本机二进制正在被换,视同进入重启。
    if (e.status === 0) { updRestartWait(); return; }
  }
  if (updAll !== a) return; // 期间重开了一轮,旧的链子就此打住
  if (snap) {
    const evs = Array.isArray(snap.events) ? snap.events : [];
    if (evs.length) {
      a.events.push(...evs);
      a.since = evs[evs.length - 1].seq;
      a.delay = UPD_ALL_MIN;
    } else {
      a.delay = Math.min(a.delay * 2, UPD_ALL_MAX);
    }
    if (snap.state === "failed" || snap.state === "done") {
      a.state = snap.state;
      renderUpdAll();
      updAll = null;
      refreshUpdatePill(true);
      return;
    }
    if (snap.state === "restarting") { updRestartWait(); return; }
    a.state = snap.state;
    renderUpdAll();
  }
  if (document.hidden) return; // 轮询间隙页面藏了:交给 visibilitychange 恢复
  a.timer = setTimeout(pollUpdateAll, a.delay);
}
// 进度视图:每条事件一行,按 step 分组列出来;失败的那条单独报出,配上「关闭」。
function renderUpdAll() {
  const a = updAll;
  if (!a) return;
  const groups = [];
  for (const ev of a.events) {
    const last = groups[groups.length - 1];
    if (last && last.step === ev.step) last.evs.push(ev);
    else groups.push({ step: ev.step, evs: [ev] });
  }
  const failed = a.state === "failed" ? a.events.filter(e => e.status === "failed").pop() : null;
  const done = a.state === "done";
  const title = failed ? "更新失败" : done ? "已是最新" : "正在更新";
  const m = $("#modal");
  m.innerHTML = val(H`<div class="mbody"><h2>${title}</h2>
  <div class="upd-steps">${groups.map(g => H`<div class="upd-step">${updStepName(g.step)}</div>${g.evs.map(ev => H`<div class="upd-ev ${ev.status}">${ev.msg}</div>`)}`)}</div>
  ${failed ? H`<div class="err" role="alert">${failed.msg}</div>` : ""}</div>
  ${(failed || done) ? H`<div class="mf"><button class="btn pri" data-act="modal-cancel">关闭</button></div>` : ""}`);
  $("#scrim").classList.add("open"); m.classList.add("open");
  const box = $(".upd-steps");
  if (box) box.scrollTop = box.scrollHeight;
}
// state=restarting,或轮询连不上(进程正在被换):改问 /ui/meta,问通了整页
// 刷新;会话沿用,cookie 继续有效。最多等 60 秒,超时提示手动刷新。
function updRestartWait() {
  if (updAll) { clearTimeout(updAll.timer); updAll = null; }
  const title = updProgName() + " 正在重启";
  const m = $("#modal");
  m.innerHTML = val(H`<div class="mbody"><h2>${title}…</h2>
  <p class="sub">新版本起来后本页会自动刷新,会话沿用,无需重新登录。</p></div>`);
  $("#scrim").classList.add("open"); m.classList.add("open");
  const deadline = Date.now() + 60000;
  const tick = async () => {
    try { await api("/ui/meta"); location.reload(); return; } catch {}
    if (Date.now() >= deadline) {
      m.innerHTML = val(H`<div class="mbody"><h2>${title}…</h2>
      <p class="sub">等了一分钟还没起来,请手动刷新本页确认状态。</p></div>
      <div class="mf"><button class="btn pri" data-act="modal-cancel">关闭</button></div>`);
      $("#scrim").classList.add("open"); m.classList.add("open");
      return;
    }
    setTimeout(tick, 1000);
  };
  setTimeout(tick, 1000);
}

// 侧栏按「使用顺序」排(先有节点,再有用户,最后发订阅),不再按数据表分组。
// 每项:[路由, 名字, 图标];null 是分隔线。套餐在用户页里当第二个页签,
// 「入站与入口」并进节点详情的「连接方式」,都不再占一级。
const NAV = [["overview", "总览", "home"], ["nodes", "节点", "nodes"], ["users", "用户", "users"], ["sub", "订阅", "sub"], ["rules", "分流", "split"], null, ["doctor", "体检", "pulse"], ["system", "系统", "set"]];
// 当前页在侧栏里算哪一项:套餐页(#users/plans)亮「用户」,旧的入站页亮「节点」。
const navKey = () => PAGES[route[0]]?.navAs || route[0];
let CP_OPEN = false, CP_DEVICE;  // CP_DEVICE:这台电脑在控制面上登记的设备名(展开卡片时才去查)
const fpGroups = fp => String(fp).replace(/^SHA256:/, "").match(/.{1,4}/g) || [];
function cpCard() {
  const name = cpName(META.profile, META.server), addr = cpAddr(META.server), fp = META.fingerprint || "";
  // 连上是墨色点,连不上才红;还没探过是灰的。
  const dot = CP_OK === true ? "ok" : CP_OK === false ? "bad" : "";
  return H`<div class="cpw"><div class="profile" data-act="cp-toggle" role="button" tabindex="0" aria-expanded="${CP_OPEN}" title="控制面"><span class="dot ${dot}"></span><div class="pf-t"><b title="${name === addr ? META.server : name}">${name}</b>${name !== addr ? H`<span title="${META.server}">${addr}</span>` : ""}</div>${ic("down", "pf-car" + (CP_OPEN ? " up" : ""))}</div>
  ${CP_OPEN ? H`<div class="cp-pop"><dl class="kv flat cp-kv"><dt>地址</dt><dd class="mono">${META.server || "—"}</dd>
    <dt>证书指纹</dt><dd class="mono fp" title="${fp}">${fp ? fpGroups(fp).map(g => H`<i>${g}</i>`) : H`<span class="sub">未使用 HTTPS 指纹</span>`}</dd>
    ${CP_DEVICE ? H`<dt>这台电脑</dt><dd>${CP_DEVICE}</dd>` : ""}</dl>
    <div class="cp-acts">${fp ? H`<button type="button" class="btn sm" data-act="copy" data-text="${fp}">${ic("copy")}复制指纹</button>` : ""}${META.profiles ? H`<button type="button" class="btn sm" data-act="cp-switch">${ic("swap")}切换控制面</button>` : ""}</div>
    <p class="sub cp-note">只在这台电脑上能打开,别的设备需要单独接入。</p></div>` : ""}</div>`;
}
function shell(page, body, crumb) {
  const dark = document.documentElement.dataset.theme === "dark", on = navKey();
  // 数字只给用户、节点;总览只在有节点连不上(退避中)时亮一个红点。
  const cnt = { users: PAGES.users?.count?.(), nodes: PAGES.nodes?.count?.() };
  const warn = { overview: !!STATUS?.backoffs?.length };
  const nav = NAV.filter(x => !x || PAGES[x[0]]).map(x => x ? H`<a class="nav ${on === x[0] ? "on" : ""}" href="#${x[0]}">${ic(x[2])}${x[1]}${warn[x[0]] ? H`<span class="ct"><i class="dot bad" aria-label="有节点连不上"></i></span>` : cnt[x[0]] ? H`<span class="ct">${cnt[x[0]]}</span>` : ""}</a>` : H`<div class="nsep"></div>`);
  const mob = [NAV[0], NAV[1], NAV[2], NAV[3]].filter(x => PAGES[x[0]]);
  const stale = current.at ? H`<span class="sub hide-m">更新于 ${hms(current.at)}</span>` : "";
  return H`<div class="app"><aside class="side"><div class="brand"><img class="lk lk-l" src="/lockup-light.svg" alt="MPX"><img class="lk lk-d" src="/lockup-dark.svg" alt="MPX"><small>${META.cli_version || ""}</small></div>
  ${cpCard()}
  ${nav}</aside>
  <main><div class="top"><div class="crumb">${crumb}</div><div class="sp"></div>${stale}
  <button type="button" id="upd-pill" class="upd-pill"${showUpdatePill(UPDATE) ? "" : raw(" hidden")} data-act="update-open">更新</button>
  <button class="ib" title="刷新" aria-label="刷新" data-act="reload">${ic("refresh")}</button>
  <button class="ib" title="切换主题" aria-label="切换主题" data-act="theme">${ic(dark ? "sun" : "moon")}</button></div>
  <div class="page" id="page">${body}</div></main></div>
  <nav class="mnav">${mob.map(x => H`<a href="#${x[0]}" class="${on === x[0] ? "on" : ""}">${ic(x[2])}${x[1]}</a>`)}<a href="#system" class="${["rules", "doctor", "system"].includes(on) ? "on" : ""}">${ic("more")}更多</a></nav>`;
}

// 旧地址(书签、文档、别处的链接)跳到新位置。只换地址不留历史,后退不会又跳回来。
const OLD_ROUTES = { settings: "sub", "settings/general": "sub", "settings/rules": "sub/clients", "settings/templates": "sub/templates", "settings/devices": "system/devices", "settings/backup": "system/backup", plans: "users/plans" };
function migrateKey(key) {
  if (OLD_ROUTES[key]) return OLD_ROUTES[key];
  if (key.startsWith("plans/")) return "users/" + key;
  return key;
}
// 地址 → 页面路由。#users/plans[/<id>] 由套餐页渲染,路由写成 ["plans", id]。
function routeOf(key) {
  const r = key.split("/").map(decodeURIComponent);
  if (r[0] === "users" && r[1] === "plans") return r.slice(1);
  return r;
}

/* ---------- 总览 ---------- */
// 「开始使用」:4 步的完成与否全从数据推出来,不靠手动勾选。「先用 IP」(跳过订阅地址)和
// 「不再显示」记在这台电脑上,按控制面分开(同一台电脑接了两个控制面互不影响)。
const clKey = k => `mpx-cl-${k}:${META.server || "-"}`;
const clGet = k => { try { return localStorage.getItem(clKey(k)) === "1"; } catch { return false; } };
const clSet = (k, v) => { try { v ? localStorage.setItem(clKey(k), "1") : localStorage.removeItem(clKey(k)); } catch { /* 隐私模式 */ } };
let CL_OPEN = false;
function startSteps(d) {
  const { nodes, settings, users, pulls } = d, U = users.users.slice().sort((a, b) => new Date(a.created_at) - new Date(b.created_at));
  const on = nodes.find(x => x.status?.reachable), u = U[0];
  const pull = pulls && pulls.slice().sort((a, b) => new Date(a.first_pulled_at) - new Date(b.first_pulled_at))[0];
  const userLink = name => "#users/" + encodeURIComponent(name);
  return [
    { t: "第一个节点在线", done: !!on, dDone: on ? `${nodeLabel(on)} 在线` : "",
      dTodo: nodes.length ? "节点还连不上。打开节点看看哪里出了问题" : "装好控制面时会顺带建好第一个节点;也可以在「节点」里添加",
      go: PAGES.nodes ? H`<a class="btn sm" href="#nodes">查看节点</a>` : "" },
    { t: "设好订阅地址", done: !!settings.sub_url_prefix || clGet("skip-sub"), skip: true,
      dDone: settings.sub_url_prefix || "先用服务器 IP,之后在「订阅」里随时能改",
      dTodo: "填你给用户用的域名。不设的话,链接里会直接露出服务器 IP",
      go: PAGES.sub ? H`<a class="btn sm pri" href="#sub">去设置</a>` : "" },
    { t: "添加第一个用户", done: users.total > 0, dDone: u ? u.username : "",
      dTodo: "每个用户一条自己的订阅,可以单独停用和续期",
      go: can("POST", "/api/users") ? H`<button class="btn sm pri" data-act="user-new">${ic("plus")}添加用户</button>` : "" },
    // 任意用户的客户端第一次拉取订阅才算,复制了链接不算。老控制面读不到拉取记录:不拦着。
    { t: "把订阅发给他", done: pulls ? !!pull : users.total > 0,
      dDone: pull ? `${clientLabel(pull.client)} 已经导入` : pulls ? "" : "控制面版本较旧,读不到拉取记录",
      dTodo: u ? `给 ${u.username} 扫码或发链接;他的客户端第一次拉取订阅后,这一步自动完成` : "建好用户后,在这里扫码或复制链接",
      go: u ? H`<a class="btn sm pri" href="${userLink(u.username)}">${ic("qr")}发给 ${u.username}</a>` : "" },
  ];
}
function startBlock(S, done, allDone) {
  if (allDone && clGet("hide")) return "";
  const cur = S.findIndex(x => !x.done);
  const item = (x, i) => H`<li class="ck ${x.done ? "done" : i === cur ? "cur" : ""}"><span class="ck-i">${x.done ? ic("check") : String(i + 1)}</span>
    <div class="ck-t"><b>${x.t}</b><span class="sub">${x.done ? x.dDone : x.dTodo}</span></div>
    <div class="ck-a">${!x.done && i === cur ? H`${x.skip ? H`<button class="btn sm" data-act="cl-skip">先用 IP</button>` : ""}${x.go}` : ""}</div></li>`;
  const optional = H`<li class="ck opt"><span class="ck-i">${ic("plus")}</span><div class="ck-t"><b>可选:让它更好用</b><span class="sub">随时都能做,不影响上面的进度</span></div>
    <div class="ck-a">${PAGES.nodes ? H`<a class="btn sm" href="#nodes">${ic("nodes")}再加一个节点</a>` : ""}${PAGES.rules ? H`<a class="btn sm" href="#rules">${ic("split")}调整分流</a>` : ""}${PAGES.system ? H`<a class="btn sm" href="#system">${ic("bell")}打开到期提醒</a>` : ""}</div></li>`;
  const list = H`<section class="card cl" id="start"><div class="cl-h"><div><h3>开始使用</h3><p class="sub">${done === 0 ? "四步把第一条订阅发出去,大约 5 分钟" : allDone ? "4 步都完成了" : `还差 ${S.length - done} 步`}</p></div><span class="sp" style="flex:1"></span><span class="cl-n"><b>${done}</b> / ${S.length}</span></div>
    <div class="cl-bar">${S.map(x => H`<i class="${x.done ? "on" : ""}"></i>`)}</div>
    <ol class="cks">${S.map(item)}${optional}</ol></section>`;
  if (!allDone) return H`<div class="mb">${list}</div>`;
  return H`<div class="cl-done" id="start-done">${ic("check")}<span><b>开始使用</b> · 4 步都完成了</span><span class="sp" style="flex:1"></span><button type="button" class="btn sm ghost" data-act="cl-open">${CL_OPEN ? "收起" : "再看一眼"}</button><button type="button" class="ib" title="不再显示" aria-label="不再显示" data-act="cl-hide">${ic("x")}</button></div>${CL_OPEN ? H`<div class="mb">${list}</div>` : ""}`;
}
let STATUS = null;
// 最近一次读控制面成没成:侧栏控制面圆点的颜色(未知时灰)。
let CP_OK = null;
PAGES.overview = {
  title: "总览",
  async load() {
    const [status, nodes, settings, users, pulls] = await Promise.all([
      api("/api/status"), api("/api/nodes"), api("/api/settings"), api("/api/users?limit=1000"),
      api("/api/sub-pulls").catch(() => null),  // 老控制面没有拉取记录
    ]);
    STATUS = status;
    return { status, nodes, settings, users, pulls };
  },
  render(d) {
    const L = !d;
    const stat = (label, v, sub, dot, tip) => H`<div class="card stat" ${tip ? raw(`title="${esc(val(tip))}"`) : ""}><div class="l">${dot ? H`<span class="dot ${dot}"></span>` : ""}${label}</div><div class="v">${L ? H`<span class="sk h26 w60"></span>` : v}</div><div class="s">${L ? H`<span class="sk w80"></span>` : sub}</div></div>`;
    if (L) return H`<div class="ph"><div><h1>总览</h1><p><span class="sk" style="width:260px"></span></p></div></div>
      <div class="grid g4">${stat()}${stat()}${stat()}${stat()}</div>
      <div class="grid g2 mt"><div class="card"><div class="ch">需要处理的用户</div><table><tbody>${skRows(4, 3)}</tbody></table></div><div class="card"><div class="ch">节点</div><table><tbody>${skRows(4, 2)}</tbody></table></div></div>`;
    const { status: s, nodes, settings, users, pulls } = d, U = users.users;
    const S = startSteps(d), done = S.filter(x => x.done).length, allDone = done === S.length;
    const pulled24 = (pulls || []).filter(p => Date.now() - new Date(p.last_pulled_at) < 864e5).length;
    const n = k => U.filter(u => u.status === k).length;
    const usagePct = settings.notify_usage_percent || 80;
    const soon = u => u.status === "active" && u.expire_at && daysLeft(u.expire_at) <= EXPIRE_SOON_DAYS;
    const near = u => u.status === "active" && u.data_limit && pct(u) >= usagePct;
    const attention = [...U.filter(u => u.status === "limited"), ...U.filter(u => u.status === "expired"), ...U.filter(soon), ...U.filter(u => near(u) && !soon(u))];
    const run = nodes.filter(x => x.status?.reachable && !x.draining).length, drain = nodes.filter(x => x.draining).length, down = nodes.filter(x => x.status && !x.status.reachable).length;
    const tick = s.last_tick_at ? H`上一轮 ${hms(new Date(s.last_tick_at))},用时 ${fmtSecs(s.last_tick_seconds)}` : "还没跑过一轮";
    const upd = s.update || {}, behind = upd.behind > 0 && !isDevVer(s.version);
    const certWarn = x => (x.status?.certificates || []).filter(c => c.not_after && c.days_left <= 14).sort((a, b) => a.days_left - b.days_left)[0];
    const partial = users.total > U.length;
    // 标题下一句按稿子:还没走完「开始使用」时是欢迎语,走完了是人数和节点数。
    return H`<div class="ph"><div><h1>总览</h1><p>${allDone ? `${users.total} 个用户 · ${nodes.length} 个节点` : "欢迎用 MPX。跟着下面几步,把第一条订阅发出去。"}</p></div></div>
    ${startBlock(S, done, allDone)}
    ${s.backoffs.length === 1 ? s.backoffs.map(b => H`<div class="callout mb">${ic("warn", "ico-warn")}<div><b>${labelOfName(b.node_name)} 在退避中</b> · 连续失败 ${b.failures} 次,${hm(new Date(b.skip_until))} 前跳过采样与收敛。${PAGES.nodes ? H`<a href="#nodes/${b.node_id}" class="u-link">查看节点</a>` : ""}</div></div>`)
      : s.backoffs.length ? H`<div class="callout mb">${ic("warn", "ico-warn")}<div><b>${s.backoffs.length} 个节点在退避中</b> · ${s.backoffs.map(b => labelOfName(b.node_name)).join("、")}:连续失败,暂时跳过采样与收敛。</div></div>` : ""}
    <div class="grid g4">
      ${stat("用户", users.total, H`${n("active")} 正常 · ${n("limited")} 超限 · ${n("expired")} 过期 · ${n("disabled")} 停用${partial ? H`<span class="stale">· 只统计了前 ${U.length} 个</span>` : ""}`)}
      ${stat("节点", s.counts.nodes, `${run} 运行 · ${drain} 暂停分配 · ${down} 连不上`)}
      ${pulls ? stat("近 24 小时在用", H`${pulled24}<small>人</small>`, "客户端拉取过订阅") : stat("连接方式", H`${s.counts.inbounds}<small>/ ${s.counts.hosts}</small>`, `${s.counts.inbounds} 条连接 · ${s.counts.hosts} 个入口`)}
      ${stat("控制面", H`<span class="lead ell" title="${s.version}">${shortVer(s.version) || "未知版本"}</span>`, behind ? H`<span class="tag lab">有新版本 ${upd.latest}</span> · 运行 ${fmtDur(s.uptime_seconds)}` : `${isDevVer(s.version) ? "开发版,不比较版本" : upd.latest ? "已是最新" : "没查到新版本信息"} · 运行 ${fmtDur(s.uptime_seconds)}`, "ink", tick)}
    </div>
    <div class="grid g2 mt">
      <div class="card"><div class="ch">需要处理的用户<small>${attention.length ? `· ${attention.length}` : ""}</small><span class="sp"></span>${PAGES.users ? H`<a class="btn ghost sm" href="#users">全部用户 ${ic("arrow")}</a>` : ""}</div>
      ${attention.length ? H`<div class="tw"><table><tbody>${attention.slice(0, 6).map(u => H`<tr data-href="${PAGES.users ? "#users/" + encodeURIComponent(u.username) : ""}"><td><div class="u">${avatar(u.username)}<b class="bold">${u.username}</b></div></td>
        <td>${u.status !== "active" ? userSt(u.status) : soon(u) ? stBadge("warn", "快到期") : stBadge("warn", "快用完")}</td><td class="hide-m">${usageBar(u)}</td>
        <td class="num sub">${u.expire_at ? (daysLeft(u.expire_at) < 0 ? "已过期" : `剩 ${daysLeft(u.expire_at)} 天`) : "—"}</td></tr>`)}</tbody></table></div>`
        : !users.total ? emptyBox("users", "还没有用户", "加一个用户,他就有自己的订阅链接。")
        : emptyBox("check", "没有要处理的用户", `没有超限、过期、${EXPIRE_SOON_DAYS} 天内到期或用量过 ${usagePct}% 的用户。`)}</div>
      <div class="card"><div class="ch">节点<small>${nodes.length ? `· ${nodes.length}` : ""}</small><span class="sp"></span>${PAGES.nodes ? H`<a class="btn ghost sm" href="#nodes">全部 ${ic("arrow")}</a>` : ""}</div>
      ${nodes.length ? nodes.map(x => { const c = certWarn(x); return H`<a class="row" ${PAGES.nodes ? raw(`href="#nodes/${x.id}"`) : ""}><b class="bold" style="min-width:72px">${nodeLabel(x)}</b><span class="sub mono hide-m">${x.address}</span><span class="sp"></span>${c ? H`<span class="tag ${c.days_left < 0 ? "c-bad" : "c-warn"}">证书${c.days_left < 0 ? "已过期" : `剩 ${c.days_left} 天`}</span>` : ""}${nodeSt(x)}</a>`; })
        : emptyBox("nodes", "还没有节点", "登记一台能从控制面 SSH 上去的服务器。节点上不装任何 mpx 组件。到「节点」页点「添加节点」开始。")}</div>
    </div>
    <div class="card mt"><div class="ch">通知</div>
      <dl class="kv"><dt>Webhook</dt><dd class="mono">${settings.webhook_url || H`<span class="sub">未配置,不发通知</span>`}</dd>
      <dt>到期提醒</dt><dd>${settings.notify_days_left ? `剩 ${settings.notify_days_left} 天时` : "关闭"}</dd>
      <dt>用量提醒</dt><dd>${settings.notify_usage_percent ? `用到 ${settings.notify_usage_percent}% 时` : "关闭"}</dd></dl></div>`;
  },
};

// releaseNotesBody:发布说明里只留「这次改了什么」。v0.9.4 起正文开头是「我该下哪个」下载表和
// 折叠的文件清单(给发布页看的),更早的版本末尾是「## 下载」一节;更新弹窗里都不需要。
function releaseNotesBody(md) {
  let s = String(md || "");
  s = s.replace(/^## 我该下哪个[\s\S]*?<\/details>\s*/m, "");
  s = s.replace(/^## 下载\s*$[\s\S]*/m, "");
  return s.trim();
}

// releaseHighlights:更新弹窗只列这一版的要点,不展示整份发布说明。v0.9.5 起发布说明开头是
// 「## 更新要点」一节(取自 CHANGELOG 的「### 要点」);更早的发布没有这一节,就从下载表 /
// 「## 下载」之前的正文里取前几条列表项。下载表、安装说明、折叠的文件清单一律不要。
const HL_MAX = 8;
function releaseHighlights(md) {
  const s = String(md || "").replace(/\r\n/g, "\n");
  const items = text => text.split("\n").map(l => /^\s{0,1}[-*]\s+(.+)$/.exec(l)).filter(Boolean).map(m => m[1].trim());
  const sec = /^##\s+(?:v[\d.]+\s+)?更新要点[ \t]*$([\s\S]*?)(?=^#{1,6}\s|(?![\s\S]))/m.exec(s);
  if (sec) return items(sec[1]).slice(0, HL_MAX);
  const body = releaseNotesBody(s).replace(/^##\s+我该下哪个[\s\S]*$/m, "");
  const li = items(body);
  if (li.length) return li.slice(0, HL_MAX);
  // 连列表都没有(很老的发布):取前几行正文,标题、表格、代码围栏、HTML 都跳过。
  return body.split("\n").map(l => l.trim()).filter(l => l && !/^(#|\||```|<)/.test(l)).slice(0, 3);
}
// inlineMd:要点里只认 `代码` 和 **粗体**,其余原样转义。
function inlineMd(t) {
  return esc(t).replace(/`([^`]+)`/g, "<code>$1</code>").replace(/\*\*([^*]+)\*\*/g, "<b>$1</b>");
}

/* ---------- 抽屉、确认框、提示 ---------- */
// 抽屉在下一帧才加 open(让过渡动画生效)。期间如果已经关掉了,这一帧不能再把它和
// 遮罩打开,否则会留下一层没有抽屉的遮罩挡住整页。
// Windows 桌面窗口的最小化/最大化/关闭画在窗口右上角(--mygo-titlebar-inset-right 是它们占的宽)。
// 有这块时给 <html> 加 tb-right:顶栏移到卡片外、和这三个按钮同一行,内容卡片从标题栏下面开始。
function syncTitlebarRoom() {
  const r = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--mygo-titlebar-inset-right")) || 0;
  document.documentElement.classList.toggle("tb-right", r > 0);
}
syncTitlebarRoom(); addEventListener("load", syncTitlebarRoom); addEventListener("resize", syncTitlebarRoom); for (const t of [300, 1200, 3000]) setTimeout(syncTitlebarRoom, t);
/* ---------- 下拉:灰底,弹出我们自己的列表 ---------- */
// 原生 <select> 留着(表单取值、键盘上下键、测试的 selectOption 都照旧),只是鼠标按下时不弹系统的
// 列表,改弹 .selmenu。选中后写回 select.value 并派发 input / change。
let SELMENU = null;
function closeSelMenu() { if (SELMENU) { SELMENU.el.remove(); SELMENU = null; } }
function openSelMenu(sel) {
  closeSelMenu();
  const r = sel.getBoundingClientRect(), el = document.createElement("div");
  el.className = "selmenu"; el.setAttribute("role", "listbox");
  for (const o of sel.options) {
    if (o.hidden) continue;
    const b = document.createElement("button");
    b.type = "button"; b.className = "selopt" + (o.selected ? " on" : ""); b.disabled = o.disabled;
    b.setAttribute("role", "option"); b.setAttribute("aria-selected", o.selected); b.dataset.v = o.value;
    b.innerHTML = val(H`<span>${o.textContent}</span>${o.selected ? ic("check") : ""}`);
    el.append(b);
  }
  document.body.append(el);
  const h = Math.min(el.scrollHeight, 300), below = innerHeight - r.bottom - 8;
  el.style.minWidth = r.width + "px"; el.style.left = Math.min(r.left, innerWidth - el.offsetWidth - 8) + "px";
  el.style.top = (below >= h || below >= r.top ? r.bottom + 4 : Math.max(8, r.top - 4 - h)) + "px";
  el.querySelector(".on")?.scrollIntoView({ block: "nearest" });
  el.addEventListener("mousedown", e => e.preventDefault());
  el.addEventListener("click", e => {
    const b = e.target.closest(".selopt"); if (!b || b.disabled) return;
    closeSelMenu();
    if (sel.value !== b.dataset.v) { sel.value = b.dataset.v; sel.dispatchEvent(new Event("input", { bubbles: true })); sel.dispatchEvent(new Event("change", { bubbles: true })); }
    sel.focus();
  });
  SELMENU = { el, sel };
}
document.addEventListener("mousedown", e => {
  if (SELMENU && !SELMENU.el.contains(e.target)) { const was = SELMENU.sel; closeSelMenu(); if (e.target === was) { e.preventDefault(); return; } }
  const sel = e.target.closest && e.target.closest("select.in");
  if (!sel || sel.disabled || sel.multiple || e.button !== 0) return;
  e.preventDefault(); sel.focus(); openSelMenu(sel);
}, true);
document.addEventListener("keydown", e => { if (SELMENU && e.key === "Escape") { e.stopPropagation(); const s = SELMENU.sel; closeSelMenu(); s.focus(); } }, true);
addEventListener("resize", closeSelMenu); document.addEventListener("scroll", e => { if (SELMENU && !SELMENU.el.contains(e.target)) closeSelMenu(); }, true);
let DRAWER_RAF = 0;
function openDrawer(html, cls = "") { const d = $("#drawer"); d.classList.toggle("wide", cls === "wide"); d.innerHTML = val(html); cancelAnimationFrame(DRAWER_RAF); DRAWER_RAF = requestAnimationFrame(() => { DRAWER_RAF = 0; d.classList.add("open"); $("#scrim").classList.add("open"); }); }
function closeOverlays() {
  cancelAnimationFrame(DRAWER_RAF); DRAWER_RAF = 0;
  $("#drawer").classList.remove("open"); $("#modal").classList.remove("open"); $("#scrim").classList.remove("open");
  updPollStop();
  if (route[1] && PAGES[route[0]]?.closeTo) { history.replaceState(null, "", "#" + PAGES[route[0]].closeTo); route = [route[0]]; }
}
function toast(text, bad) { document.querySelector(".toast")?.remove(); const e = document.createElement("div"); e.className = "toast" + (bad ? " bad" : ""); e.setAttribute("role", "status"); e.innerHTML = val(H`${ic(bad ? "warn" : "check")}${text}`); document.body.append(e); setTimeout(() => e.remove(), bad ? 6000 : 2600); }

// 写操作:发出请求、成功后提示并刷新当前页;失败把控制面原话报出来。
async function write(method, path, body, okText) {
  const init = { method, headers: {} };
  if (body !== undefined) { init.body = JSON.stringify(body); init.headers["Content-Type"] = "application/json"; }
  const res = await api(path, init);
  if (okText) toast(okText);
  afterWrite();
  return res;
}
// 任何写成功后都重新拉当前页。放到宏任务里,等调用方关掉抽屉/改完地址再读 location.hash;
// 否则地址没变(例如在 #users 删掉用户后又设 location.hash="users")时不会有 hashchange,列表就停在旧数据。
function afterWrite() { setTimeout(() => load(true, true), 0); }
// 确认框。typed 给了就要求逐字输入它才放行;run 抛错时框不关,错误显示在框里。
// extra 是插在说明下面的表单。
// alt:{ label, run } 再给一个次要选项(比如「只删除，稍后应用」),和主按钮一样受 typed 约束。
function confirmBox({ title, text, ok, danger, typed, run, extra, alt }) {
  const m = $("#modal");
  m.innerHTML = val(H`<div class="mbody"><h2>${danger ? H`<span class="warnico">${ic("warn")}</span>` : ""}${title}</h2><p>${text}</p>
  ${typed ? H`<div class="field"><label class="sub">输入 <b class="c-ink">${typed}</b> 确认</label><input class="in mono" id="typed" autocomplete="off" spellcheck="false"></div>` : ""}
  ${extra || ""}<div class="err" id="modal-err" role="alert"></div></div>
  <div class="mf"><button class="btn" data-act="modal-cancel">取消</button>${alt ? H`<button id="altb" class="btn" ${typed ? raw("disabled") : ""}>${alt.label}</button>` : ""}<button id="okb" class="btn ${danger ? "danger" : "pri"}" ${typed ? raw("disabled") : ""}>${ok}</button></div>`);
  $("#scrim").classList.add("open"); m.classList.add("open");
  const okb = $("#okb"), altb = $("#altb"), t = $("#typed");
  if (t) { t.addEventListener("input", () => { okb.disabled = t.value !== typed; if (altb) altb.disabled = okb.disabled; }); setTimeout(() => t.focus(), 50); } else setTimeout(() => okb.focus(), 50);
  const go = (b, fn) => b && b.addEventListener("click", async () => {
    okb.disabled = true; if (altb) altb.disabled = true; b.classList.add("busy");
    try { await fn(); m.classList.remove("open"); if (!$("#drawer.open")) $("#scrim").classList.remove("open"); }
    catch (e) { $("#modal-err").textContent = e.message; okb.disabled = false; if (altb) altb.disabled = false; b.classList.remove("busy"); }
  });
  go(okb, run); go(altb, alt && alt.run);
}

/* ---------- 切换控制面 ---------- */
// 控制面在界面上的叫法:名称是自动生成的 default 时直接用地址,否则用名称。
const cpAddr = server => String(server || "").replace(/^https?:\/\//, "").replace(/\/$/, "");
const cpName = (name, server) => (!name || name === "default") ? (cpAddr(server) || "default") : name;
// 点左上角的控制面卡片:拉一次列表。有多个档,或者能在页面里接入新控制面时,
// 弹窗列出全部(当前档打勾,每项带指纹前 16 位);点别的就 POST 切换,成功后整页
// reload。只有一个档又接不了新控制面时,不弹,只提示一句。
async function openProfiles() {
  if (!META.profiles) return;
  let list;
  try { list = await api("/local/profiles", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" }); }
  catch (e) { toast("读不到控制面列表:" + e.message, true); return; }
  if (!Array.isArray(list)) list = [];
  const canAdd = !!META.connect && typeof window.openConnectForm === "function";
  if (list.length <= 1 && !canAdd) {
    toast(H`只有一个控制面 ${cpName(list[0]?.name, list[0]?.server)};这里不能添加新的控制面`);
    return;
  }
  // 每项只摆名称和地址;证书指纹在侧栏控制面卡片的展开面板里看。
  const m = $("#modal");
  m.innerHTML = val(H`<div class="mbody"><h2>切换控制面</h2>
  <p>切换后会记住，下次打开仍连你选的这个。</p>
  <div class="pflist">${list.map(p => { const addr = cpAddr(p.server), name = cpName(p.name, p.server);
    return H`<button type="button" class="pfitem${p.current ? " on" : ""}" data-name="${p.name}">
    <span class="dot ${p.current ? "ok" : ""}"></span><span class="pf-t"><b>${name}</b>${name !== addr ? H`<span>${addr}</span>` : ""}</span>${p.current ? ic("check") : ""}</button>`; })}</div>
  <div class="err" id="prof-err" role="alert"></div></div>
  <div class="mf">${canAdd ? H`<button type="button" class="btn" data-act="profile-add">${ic("plus")}添加控制面…</button><span class="sp" style="flex:1"></span>` : ""}<button class="btn" data-act="modal-cancel">取消</button></div>`);
  $("#scrim").classList.add("open"); m.classList.add("open");
  for (const b of m.querySelectorAll(".pfitem")) b.addEventListener("click", () => useProfile(b.dataset.name, b));
}
// useProfile:切到 name。成功整页刷新,让所有页面数据都跟着新控制面重来。
async function useProfile(name, el) {
  if (el) { el.disabled = true; el.classList.add("busy"); }
  try {
    await api("/local/profiles/use", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name }) });
    location.reload();
  } catch (e) {
    const box = $("#prof-err");
    if (box) box.textContent = e.message; else toast(e.message, true);
    if (el) { el.disabled = false; el.classList.remove("busy"); }
  }
}

/* ---------- 渲染与路由 ---------- */
let loadSeq = 0;
// 写操作之后 write() 会排一次刷新,不少调用方又紧跟着 await load():两次 load 各自
// 把这一页的几个 GET 全发一遍,白白多耗一倍限流桶,连着点两下就会被控制面 429。
// 同一页、同一代(期间没有写请求回来)还在路上的读直接复用,不再重发。
let inflight = null;
function pageData(p, key) {
  if (inflight && inflight.key === key && inflight.gen === writeGen) return inflight.promise;
  const promise = p.load(route);
  const mine = inflight = { key, gen: writeGen, promise };
  promise.then(() => {}, () => {}).then(() => { if (inflight === mine) inflight = null; });
  return promise;
}
function paint() {
  const p = PAGES[route[0]] || PAGES.overview;
  const crumb = p.crumb ? p.crumb(route, current.data) : H`<b>${p.title}</b>`;
  let body;
  if (current.error && !current.data) body = H`<div class="card">${errorBox(p.title + "数据", current.error)}</div>`;
  else body = p.render(current.loading && !current.data ? null : current.data, route);
  // 滚动的是内容区 main,不是整页。
  const y = $("main")?.scrollTop || 0;
  $("#root").innerHTML = val(shell(p, body, crumb));
  const m = $("main"); if (m) m.scrollTop = y;
  p.after?.(current.data, route);
}
async function load(soft, force) {
  let key = (location.hash.slice(1) || "overview");
  const moved = migrateKey(key);
  if (moved !== key) { history.replaceState(null, "", "#" + moved); key = moved; }
  route = routeOf(key);
  if (!PAGES[route[0]]) route = ["overview"];
  const p = PAGES[route[0]];
  const seq = ++loadSeq;
  // 页面在取数据时 <html data-loading>,画完摘掉:测试和截图脚本等它摘掉再点,免得点到正被换掉的元素。
  document.documentElement.dataset.loading = "1";
  if (!soft || current.page !== p) { current = { page: p, data: null, error: null, loading: true, at: null }; paint(); }
  try {
    const data = await pageData(p, key);
    if (seq !== loadSeq) return;
    current = { page: p, data, error: null, loading: false, at: new Date() };
    CP_OK = true;
  } catch (e) {
    if (seq !== loadSeq) return;
    current = { ...current, error: e, loading: false };
    // 连不上控制面(本地服务没了或网关 5xx)时,侧栏控制面的绿点不能还亮着。
    if (!(e && e.status >= 400 && e.status < 500)) CP_OK = false;
    if (current.data) toast("刷新失败:" + e.message, true);
  }
  if (!soft || force || !$("#drawer.open, #modal.open")) paint();
  if (seq === loadSeq) delete document.documentElement.dataset.loading;
  if (!current.error) refreshUpdatePill();
}

// reportTheme 把页面当前主题交给桌面窗口(标题栏、窗口底色一起换)。浏览器里
// 没有这个本地接口,404 就算了,不能影响页面。
async function reportTheme(theme) {
  theme = theme === "dark" ? "dark" : theme === "system" ? "system" : "light";
  try {
    await fetch("/local/app/theme", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ theme }) });
  } catch {}
}
// 页面加载时报一次:存过 light/dark 就报那个,没存过表示跟随系统。
function savedTheme() {
  try { const t = localStorage.getItem("mpx-theme"); if (t === "light" || t === "dark") return t; } catch {}
  return "system";
}

const ACTS = {
  reload: () => load(true),
  theme: () => { const d = document.documentElement, t = d.dataset.theme === "dark" ? "light" : "dark"; d.dataset.theme = t; try { localStorage.setItem("mpx-theme", t); } catch {} reportTheme(t); paint(); },
  close: () => closeOverlays(),
  "modal-cancel": () => { $("#modal").classList.remove("open"); if (!$("#drawer.open")) $("#scrim").classList.remove("open"); },
  // 节点行要显示名:还没拉过节点列表(比如直接停在设置页)就先拉一次。
  "update-open": async () => { if (!Object.keys(NODE_LABELS).length && can("GET", "/api/nodes")) { try { await api("/api/nodes"); } catch {} } openUpdateModal(); },
  // 「稍后」:把当前 latest 记进本会话,同版本不再出现;新版本再显示。
  "update-skip": () => { const v = updLatest(UPDATE); if (v) { try { sessionStorage.setItem(UPD_SKIP_KEY, v); } catch {} } closeModal(); const b = $("#upd-pill"); if (b) b.hidden = true; },
  "update-apply": (d, el) => applyUpdate(el),
  // 「立即重试」:让后台立刻再下一次,不等退避;随后接着每 2 秒看进度。
  "update-prefetch-retry": async () => {
    try { await api("/local/update/prefetch", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" }); }
    catch (e) { const box = $("#upd-err"); if (box) box.textContent = e.message; return; }
    if (UPDATE && UPDATE.prefetch) UPDATE.prefetch.state = "downloading";
    const mf = $("#modal .mf");
    if (mf) mf.replaceWith(dom(updFooter(UPDATE, true)));
    updPollStart();
  },
  "profile-open": () => openProfiles(),
  "cp-toggle": async () => {
    CP_OPEN = !CP_OPEN; paint();
    // 设备名只在本机是「接入的设备」时才有(本机控制面直连时没有,就不显示这一行)。
    if (CP_OPEN && CP_DEVICE === undefined && can("GET", "/api/devices")) {
      CP_DEVICE = "";
      try { CP_DEVICE = ((await api("/api/devices")) || []).find(x => x.current)?.name || ""; } catch {}
      if (CP_DEVICE && CP_OPEN) paint();
    }
  },
  "cp-switch": () => openProfiles(),
  // 「添加控制面…」:关掉弹窗,交给 setup.js 里的接入表单(同一套取指纹→核对→
  // 设备密码的手续,存成新控制面再切过去)。
  "profile-add": () => { $("#modal").classList.remove("open"); if (!$("#drawer.open")) $("#scrim").classList.remove("open"); window.openConnectForm(); },
  "cl-skip": () => { clSet("skip-sub", true); toast("先用服务器 IP;之后在「订阅」里随时能改"); paint(); },
  "cl-open": () => { CL_OPEN = !CL_OPEN; paint(); },
  "cl-hide": () => { clSet("hide", true); CL_OPEN = false; paint(); },
  copy: async d => { try { await navigator.clipboard.writeText(d.text); toast("已复制"); } catch { toast("复制失败,请手动选中复制", true); } },
};
document.addEventListener("click", e => {
  const a = e.target.closest("[data-act]");
  if (a && ACTS[a.dataset.act]) { e.preventDefault(); ACTS[a.dataset.act](a.dataset, a, e); return; }
  const tr = e.target.closest("tr[data-href]");
  if (tr && tr.dataset.href) location.hash = tr.dataset.href;
});
document.addEventListener("keydown", e => {
  if (e.key === "Escape") closeOverlays();
  if ((e.key === "Enter" || e.key === " ") && e.target.matches?.(".profile[data-act=cp-toggle]")) { e.preventDefault(); ACTS["cp-toggle"](); }
});
// 一键更新轮询随页面可见性走:藏起来停拍,回到可见立刻补一次。
document.addEventListener("visibilitychange", () => {
  if (document.hidden || !updAll) return;
  clearTimeout(updAll.timer);
  pollUpdateAll();
});
// 换到别的页(侧栏、浏览器后退、页面里的链接)时把抽屉和确认框收起来,不然上一页的抽屉
// 会盖在新页面上。同一页里换子路由(如 #users/alice)不动,由各页自己开关抽屉。
addEventListener("hashchange", () => {
  if (routeOf(migrateKey(location.hash.slice(1) || "overview"))[0] !== route[0]) { $("#drawer").classList.remove("open"); $("#modal").classList.remove("open"); $("#scrim").classList.remove("open"); }
  load(true, true);
});
// 每 30 秒刷一次(和后台循环同一个节奏);页面在后台时不刷,省得白白耗限流桶。
setInterval(() => { if (!document.hidden && !$("#drawer.open, #modal.open")) load(true); }, 30000);
// 更新检查慢一些:60 秒一次,同样避开抽屉/模态与后台标签页。
setInterval(() => { if (!document.hidden && !$("#drawer.open, #modal.open")) refreshUpdatePill(true); }, 60000);

/* ---------- 国旗 ---------- */
// 区域指示符国旗(🇯🇵 = 两个 U+1F1E6–U+1F1FF)在 Windows 上会显示成两个字母。
// 这里把文本里的成对指示符换成本地 /flags/<xx>.svg;服务端 model 里的 emoji 不动。
// 自己插入的 <img class="flag"> 没有文本子节点,再次扫描也不会再动它;文本被拆成
// 「普通文本 + img」后普通文本里已没有指示符,所以替换是幂等的,不会来回触发。
// flagHost 判断文本所在的位置:drop = 放不了图片,直接删掉旗帜(连同后面一个空格);
// skip = 根本不碰;"" = 正常替换。
function flagHost(el) {
  for (; el; el = el.parentElement) {
    const tag = el.tagName;
    if (tag === "SCRIPT" || tag === "STYLE" || tag === "TEXTAREA" || tag === "INPUT") return "skip";
    if (tag === "OPTION" || tag === "TITLE") return "drop";
    if (el.isContentEditable) return "skip";
  }
  return "";
}
function flagText(node) {
  const text = node.nodeValue;
  if (!text || !RI.test(text)) return;
  const mode = flagHost(node.parentElement);
  if (mode === "skip") return;
  if (mode === "drop") {
    // <option>/<title> 里只能是纯文本:旗帜连着后面一个空格一起删。
    node.nodeValue = text.replace(/([\u{1F1E6}-\u{1F1FF}]{2}) ?/gu, "");
    return;
  }
  const frag = document.createDocumentFragment();
  let last = 0, found = false, m;
  RI_PAIR.lastIndex = 0;
  while ((m = RI_PAIR.exec(text))) {
    found = true;
    if (m.index > last) frag.append(document.createTextNode(text.slice(last, m.index)));
    const img = document.createElement("img");
    img.className = "flag";
    img.src = "/flags/" + riCode(m[1], m[2]) + ".svg";
    img.alt = m[0];
    img.draggable = false;
    img.dataset.flag = "1"; // 自己插入的节点,再遇到时不再处理
    frag.append(img);
    last = RI_PAIR.lastIndex;
  }
  if (!found) return;
  if (last < text.length) frag.append(document.createTextNode(text.slice(last)));
  node.parentNode.replaceChild(frag, node);
}
function walkFlags(root) {
  if (!root) return;
  if (root.nodeType === 3) { flagText(root); return; }
  if (root.nodeType !== 1 && root.nodeType !== 11) return;
  // 先收齐再换:换掉一个文本节点会让 TreeWalker 的当前节点脱离文档,后面的就遍历不到了。
  const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes = [];
  for (let n = w.nextNode(); n; n = w.nextNode()) nodes.push(n);
  for (const n of nodes) flagText(n);
}
function stripTitle() {
  if (RI.test(document.title)) document.title = document.title.replace(/([\u{1F1E6}-\u{1F1FF}]{2}) ?/gu, "");
}
// 监听 body 的新增节点与文本变化。childList 覆盖 innerHTML 整块重画,characterData
// 覆盖页面里改了文字的少数地方。自己插入的 img 是元素,walkFlags 只找文本节点,
// 不会再触发替换;重画时新文本里已没有指示符,也不会再来一轮。
const flagObserver = new MutationObserver(ms => {
  let changed = false;
  for (const m of ms) {
    if (m.type === "characterData") { if (RI.test(m.target.nodeValue || "")) { flagText(m.target); changed = true; } }
    else for (const n of m.addedNodes) { if (n.nodeType === 3 ? RI.test(n.nodeValue || "") : n.nodeType === 1 || n.nodeType === 11) { walkFlags(n); changed = true; } }
  }
  if (changed) stripTitle();
});
flagObserver.observe(document.body, { childList: true, characterData: true, subtree: true });
// <title> 在 head 里:document.title 被改时也要把旗帜删掉(它放不了图片)。
// 改 title 是换一个文本节点(childList);新节点里没有旗帜就什么都不做,不会成环。
flagObserver.observe(document.head, { childList: true, characterData: true, subtree: true });
walkFlags(document.body);
stripTitle();

document.addEventListener("DOMContentLoaded", async () => {
  reportTheme(savedTheme());
  try { META = await api("/ui/meta"); } catch (e) { /* 会在页面加载时一并报出来 */ }
  if (META && META.mode === "setup") { openSetup(); return; }
  load(false);
});
