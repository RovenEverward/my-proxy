// mpx ui 的页面。不用框架:每个页面是一个 load() + render(),渲染成字符串交给
// innerHTML;所有插进去的数据都经过 H`` 转义。交互用 data-act 委托,不写行内脚本
// (CSP 不允许)。页面只认控制面 /api 的原始 JSON,判定一律以控制面为准。
"use strict";

/* ---------- 基础 ---------- */
const $ = (s, el = document) => el.querySelector(s);
class Raw { constructor(s) { this.s = s; } toString() { return this.s; } }
const raw = s => new Raw(s);
const esc = v => String(v ?? "").replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
const val = v => v instanceof Raw ? v.s : Array.isArray(v) ? v.map(val).join("") : esc(v);
function H(strings, ...vals) { let s = strings[0]; vals.forEach((v, i) => { s += val(v) + strings[i + 1]; }); return raw(s); }

class APIError extends Error { constructor(status, msg) { super(msg); this.status = status; } }
async function api(path, init = {}) {
  for (let attempt = 0; ; attempt++) {
    let res;
    try {
      res = await fetch(path, { credentials: "same-origin", ...init, headers: { Accept: "application/json", ...(init.headers || {}) } });
    } catch (e) { throw new APIError(0, "连不上 mpx ui:终端里的 mpx ui 可能已经退出"); }
    // 控制面 /api 按 IP 限流,被拒的请求没有执行过,等一下再试是安全的(同 CLI)。
    if (res.status === 429 && attempt < 3) { await sleep(1000 * (+res.headers.get("Retry-After") || 2)); continue; }
    const text = await res.text();
    // asText:要原文(模板),不要把看着像 JSON 的正文解析掉。
    if (init.asText && res.ok) return text;
    let body = null;
    try { body = text ? JSON.parse(text) : null; } catch { body = text; }
    if (!res.ok) throw new APIError(res.status, (body && body.error) || text || res.statusText);
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
  warn: '<path d="M12 9v4M12 17h.01"/><path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/>',
  copy: '<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/>',
  dl: '<path d="M12 3v12M7 10l5 5 5-5M4 21h16"/>', term: '<path d="m4 17 6-5-6-5M12 19h8"/>',
  more: '<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
  arrow: '<path d="M9 6l6 6-6 6"/>', check: '<path d="M5 12.5 10 17 19 7"/>',
  tpl: '<path d="M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8z"/><path d="M14 3v5h5M9 13h6M9 17h4"/>',
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
const sinceText = iso => { if (!iso) return "—"; const d = new Date(iso), s = (Date.now() - d) / 1e3; if (s < 60) return "刚刚"; if (s < 3600) return `${Math.floor(s / 60)} 分钟前`; if (s < 86400) return hm(d); return ymd(d); };

const USER_ST = { active: ["ok", "正常"], disabled: ["", "已停用"], expired: ["bad", "已过期"], limited: ["warn", "已超限"] };
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
// shq:拼等价命令时按 shell 规则给参数加引号。
const shq = s => /^[A-Za-z0-9_.,:/@%+=-]+$/.test(s) ? s : `"${String(s).replace(/(["\\$`])/g, "\\$1")}"`;
const avatar = n => H`<span class="av">${[...String(n)][0]?.toUpperCase() || "?"}</span>`;
function nodeSt(n) {
  if (!n.status) return n.draining ? stBadge("info", "排水中") : stBadge("", "未巡检");
  if (!n.status.reachable) return stBadge("bad", "连不上");
  if (n.draining) return stBadge("info", "排水中");
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
  <p>${e ? e.message : ""}</p>${auth ? H`<div class="cmd">mpx login --server ${META.server || "URL"} --token …</div><p class="sub mt">重新登录后需要重启 mpx ui。</p>` : ""}
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

function shell(page, body, crumb) {
  const dark = document.documentElement.dataset.theme === "dark";
  const items = Object.entries(PAGES).filter(([, p]) => p.nav).sort((a, b) => a[1].nav[2] - b[1].nav[2]);
  let lastGroup = null, nav = "";
  for (const [k, p] of items) {
    if (p.nav[0] && p.nav[0] !== lastGroup) { nav += H`<div class="navg">${p.nav[0]}</div>`; }
    lastGroup = p.nav[0];
    nav += H`<a class="nav ${route[0] === k ? "on" : ""}" href="#${k}">${ic(p.nav[1])}${p.title}${p.count ? H`<span class="ct">${p.count()}</span>` : ""}</a>`;
  }
  const stale = current.at ? H`<span class="sub hide-m">更新于 ${hms(current.at)}</span>` : "";
  return H`<div class="app"><aside class="side"><div class="brand"><img src="/logo.svg" alt="">mpx<small>${STATUS?.version || ""}</small></div>
  <div class="profile"><span class="dot ${STATUS ? "ok" : current.error ? "bad" : ""}"></span><div style="min-width:0"><b>${META.profile || "default"}</b><br><span title="${META.server}">${META.server}</span></div></div>
  ${raw(nav)}
  <div class="foot"><span class="ro">${ic("term")} 由 <code>mpx ui</code> 提供</span><span>仅本机可访问 · token 不出本进程</span></div></aside>
  <main><div class="top"><div class="crumb">${crumb}</div><div class="sp"></div>${stale}
  <button class="ib" title="刷新" aria-label="刷新" data-act="reload">${ic("refresh")}</button>
  <button class="ib" title="切换主题" aria-label="切换主题" data-act="theme">${ic(dark ? "sun" : "moon")}</button></div>
  <div class="page" id="page">${body}</div></main></div>
  <nav class="mnav">${items.map(([k, p]) => H`<a href="#${k}" class="${route[0] === k ? "on" : ""}">${ic(p.nav[1])}${p.short || p.title}</a>`)}</nav>`;
}

/* ---------- 总览 ---------- */
let STATUS = null;
PAGES.overview = {
  title: "总览", nav: ["", "home", 0],
  async load() {
    const [status, nodes, settings, users] = await Promise.all([
      api("/api/status"), api("/api/nodes"), api("/api/settings"), api("/api/users?limit=1000"),
    ]);
    STATUS = status;
    return { status, nodes, settings, users };
  },
  render(d) {
    const L = !d;
    const stat = (label, v, sub, dot) => H`<div class="card stat"><div class="l">${dot ? H`<span class="dot ${dot}"></span>` : ""}${label}</div><div class="v">${L ? H`<span class="sk h26 w60"></span>` : v}</div><div class="s">${L ? H`<span class="sk w80"></span>` : sub}</div></div>`;
    if (L) return H`<div class="ph"><div><h1>总览</h1><p><span class="sk" style="width:260px"></span></p></div></div>
      <div class="grid g4">${stat()}${stat()}${stat()}${stat()}</div>
      <div class="grid g2 mt"><div class="card"><div class="ch">需要处理的用户</div><table><tbody>${skRows(4, 3)}</tbody></table></div><div class="card"><div class="ch">节点</div><table><tbody>${skRows(4, 2)}</tbody></table></div></div>`;
    const { status: s, nodes, settings, users } = d, U = users.users;
    const n = k => U.filter(u => u.status === k).length;
    const usagePct = settings.notify_usage_percent || 80;
    const soon = u => u.status === "active" && u.expire_at && daysLeft(u.expire_at) <= EXPIRE_SOON_DAYS;
    const near = u => u.status === "active" && u.data_limit && pct(u) >= usagePct;
    const attention = [...U.filter(u => u.status === "limited"), ...U.filter(u => u.status === "expired"), ...U.filter(soon), ...U.filter(u => near(u) && !soon(u))];
    const run = nodes.filter(x => x.status?.reachable && !x.draining).length, drain = nodes.filter(x => x.draining).length, down = nodes.filter(x => x.status && !x.status.reachable).length;
    const tick = s.last_tick_at ? H`上一轮 ${hms(new Date(s.last_tick_at))},用时 ${(+s.last_tick_seconds).toFixed(2)}s` : "还没跑过一轮";
    const upd = s.update || {}, behind = upd.behind > 0;
    const certWarn = x => (x.status?.certificates || []).filter(c => c.not_after && c.days_left <= 14).sort((a, b) => a.days_left - b.days_left)[0];
    const partial = users.total > U.length;
    return H`<div class="ph"><div><h1>总览</h1><p>后台循环每 ${s.interval_seconds} 秒一轮 · ${tick}</p></div></div>
    ${s.backoffs.length === 1 ? s.backoffs.map(b => H`<div class="callout mb">${ic("warn", "ico-warn")}<div><b>${b.node_name} 在退避中</b> · 连续失败 ${b.failures} 次,${hm(new Date(b.skip_until))} 前跳过采样与收敛。${PAGES.nodes ? H`<a href="#nodes/${b.node_id}" class="u-link">查看节点</a>` : ""}</div></div>`)
      : s.backoffs.length ? H`<div class="callout mb">${ic("warn", "ico-warn")}<div><b>${s.backoffs.length} 个节点在退避中</b> · ${s.backoffs.map(b => b.node_name).join("、")}:连续失败,暂时跳过采样与收敛。</div></div>` : ""}
    <div class="grid g4">
      ${stat("用户", users.total, H`<div class="seg-bar">${["active", "limited", "expired", "disabled"].map(k => H`<i class="${k}" style="flex:${n(k)}"></i>`)}</div>${n("active")} 正常 · ${n("limited")} 超限 · ${n("expired")} 过期 · ${n("disabled")} 停用${partial ? H`<span class="stale">· 只统计了前 ${U.length} 个</span>` : ""}`)}
      ${stat("节点", s.counts.nodes, `${run} 运行 · ${drain} 排水 · ${down} 连不上`)}
      ${stat("入站 / 入口", H`${s.counts.inbounds}<small>/ ${s.counts.hosts}</small>`, "入口决定订阅里发什么地址")}
      ${stat("控制面", H`<span class="lead ell" title="${s.version}">${s.version || "未知版本"}</span>`, behind ? H`<span class="c-warn">有新版本 ${upd.latest}</span> · 运行 ${fmtDur(s.uptime_seconds)}` : `${upd.latest ? "已是最新" : "没查到新版本信息"} · 运行 ${fmtDur(s.uptime_seconds)}`, behind ? "warn" : "ok")}
    </div>
    <div class="grid g2 mt">
      <div class="card"><div class="ch">需要处理的用户<small>${attention.length ? `· ${attention.length}` : ""}</small><span class="sp"></span>${PAGES.users ? H`<a class="btn ghost sm" href="#users">全部用户 ${ic("arrow")}</a>` : ""}</div>
      ${attention.length ? H`<div class="tw"><table><tbody>${attention.slice(0, 6).map(u => H`<tr data-href="${PAGES.users ? "#users/" + encodeURIComponent(u.username) : ""}"><td><div class="u">${avatar(u.username)}<b class="bold">${u.username}</b></div></td>
        <td>${u.status !== "active" ? userSt(u.status) : soon(u) ? stBadge("warn", "快到期") : stBadge("warn", "快用完")}</td><td class="hide-m">${usageBar(u)}</td>
        <td class="num sub">${u.expire_at ? (daysLeft(u.expire_at) < 0 ? "已过期" : `剩 ${daysLeft(u.expire_at)} 天`) : "—"}</td></tr>`)}</tbody></table></div>`
        : emptyBox("check", "没有要处理的用户", `没有超限、过期、${EXPIRE_SOON_DAYS} 天内到期或用量过 ${usagePct}% 的用户。`)}</div>
      <div class="card"><div class="ch">节点<small>${nodes.length ? `· ${nodes.length}` : ""}</small><span class="sp"></span>${PAGES.nodes ? H`<a class="btn ghost sm" href="#nodes">全部 ${ic("arrow")}</a>` : ""}</div>
      ${nodes.length ? nodes.map(x => { const c = certWarn(x); return H`<a class="row" ${PAGES.nodes ? raw(`href="#nodes/${x.id}"`) : ""}><b class="bold" style="min-width:72px">${x.name}</b><span class="sub mono hide-m">${x.address}</span><span class="sp"></span>${c ? H`<span class="tag ${c.days_left < 0 ? "c-bad" : "c-warn"}">证书${c.days_left < 0 ? "已过期" : `剩 ${c.days_left} 天`}</span>` : ""}${nodeSt(x)}</a>`; })
        : emptyBox("nodes", "还没有节点", "登记一台能从控制面 SSH 上去的服务器。节点上不装任何 mpx 组件。", "mpx node add hk --address 203.0.113.10")}</div>
    </div>
    <div class="card mt"><div class="ch">通知<small>· mpx settings show</small></div>
      <dl class="kv"><dt>Webhook</dt><dd class="mono">${settings.webhook_url || H`<span class="sub">未配置,不发通知</span>`}</dd>
      <dt>到期提醒</dt><dd>${settings.notify_days_left ? `剩 ${settings.notify_days_left} 天时` : "关闭"}</dd>
      <dt>用量提醒</dt><dd>${settings.notify_usage_percent ? `用到 ${settings.notify_usage_percent}% 时` : "关闭"}</dd></dl></div>`;
  },
};

/* ---------- 抽屉、确认框、提示 ---------- */
function openDrawer(html, cls = "") { const d = $("#drawer"); d.classList.toggle("wide", cls === "wide"); d.innerHTML = val(html); requestAnimationFrame(() => { d.classList.add("open"); $("#scrim").classList.add("open"); }); }
function closeOverlays() {
  $("#drawer").classList.remove("open"); $("#modal").classList.remove("open"); $("#scrim").classList.remove("open");
  if (route[1] && PAGES[route[0]]?.closeTo) { history.replaceState(null, "", "#" + PAGES[route[0]].closeTo); route = [route[0]]; }
}
function toast(text, bad) { document.querySelector(".toast")?.remove(); const e = document.createElement("div"); e.className = "toast" + (bad ? " bad" : ""); e.setAttribute("role", "status"); e.innerHTML = val(H`${ic(bad ? "warn" : "check")}${text}`); document.body.append(e); setTimeout(() => e.remove(), bad ? 6000 : 2600); }

// 写操作:发出请求、成功后提示并刷新当前页;失败把控制面原话报出来。
async function write(method, path, body, okText) {
  const init = { method, headers: {} };
  if (body !== undefined) { init.body = JSON.stringify(body); init.headers["Content-Type"] = "application/json"; }
  const res = await api(path, init);
  if (okText) toast(okText);
  return res;
}
// 确认框。typed 给了就要求逐字输入它才放行;run 抛错时框不关,错误显示在框里。
// extra 是插在说明下面的表单;cmdOf(modal) 给了就随输入重算等价命令。
function confirmBox({ title, text, cmd, ok, danger, typed, run, extra, cmdOf }) {
  const m = $("#modal");
  m.innerHTML = val(H`<div class="mb"><h2>${danger ? H`<span class="warnico">${ic("warn")}</span>` : ""}${title}</h2><p>${text}</p>
  ${typed ? H`<div class="field"><label class="sub">输入 <b class="c-ink">${typed}</b> 确认</label><input class="in mono" id="typed" autocomplete="off" spellcheck="false"></div>` : ""}
  ${extra || ""}${cmd || cmdOf ? H`<div class="cmd mb" id="modal-cmd">${cmd || ""}</div>` : ""}<div class="err" id="modal-err" role="alert"></div></div>
  <div class="mf"><button class="btn" data-act="modal-cancel">取消</button><button id="okb" class="btn ${danger ? "danger" : "pri"}" ${typed ? raw("disabled") : ""}>${ok}</button></div>`);
  $("#scrim").classList.add("open"); m.classList.add("open");
  const okb = $("#okb"), t = $("#typed");
  if (cmdOf) { const upd = () => { $("#modal-cmd").textContent = cmdOf(m); }; m.oninput = m.onchange = upd; upd(); } else m.oninput = m.onchange = null;
  if (t) { t.addEventListener("input", () => { okb.disabled = t.value !== typed; }); setTimeout(() => t.focus(), 50); } else setTimeout(() => okb.focus(), 50);
  okb.addEventListener("click", async () => {
    okb.disabled = true; okb.classList.add("busy");
    try { await run(); m.classList.remove("open"); if (!$("#drawer.open")) $("#scrim").classList.remove("open"); }
    catch (e) { $("#modal-err").textContent = e.message; okb.disabled = false; okb.classList.remove("busy"); }
  });
}

/* ---------- 渲染与路由 ---------- */
let loadSeq = 0;
function paint() {
  const p = PAGES[route[0]] || PAGES.overview;
  const crumb = p.crumb ? p.crumb(route, current.data) : H`<b>${p.title}</b>`;
  let body;
  if (current.error && !current.data) body = H`<div class="card">${errorBox(p.title + "数据", current.error)}</div>`;
  else body = p.render(current.loading && !current.data ? null : current.data, route);
  const y = scrollY;
  $("#root").innerHTML = val(shell(p, body, crumb));
  scrollTo(0, y);
  p.after?.(current.data, route);
}
async function load(soft, force) {
  const key = (location.hash.slice(1) || "overview");
  route = key.split("/").map(decodeURIComponent);
  if (!PAGES[route[0]]) route = ["overview"];
  const p = PAGES[route[0]];
  const seq = ++loadSeq;
  if (!soft || current.page !== p) { current = { page: p, data: null, error: null, loading: true, at: null }; paint(); }
  try {
    const data = await p.load(route);
    if (seq !== loadSeq) return;
    current = { page: p, data, error: null, loading: false, at: new Date() };
  } catch (e) {
    if (seq !== loadSeq) return;
    current = { ...current, error: e, loading: false };
    if (current.data) toast("刷新失败:" + e.message, true);
  }
  if (!soft || force || !$("#drawer.open, #modal.open")) paint();
}

const ACTS = {
  reload: () => load(true),
  theme: () => { const d = document.documentElement, t = d.dataset.theme === "dark" ? "light" : "dark"; d.dataset.theme = t; try { localStorage.setItem("mpx-theme", t); } catch {} paint(); },
  close: () => closeOverlays(),
  "modal-cancel": () => { $("#modal").classList.remove("open"); if (!$("#drawer.open")) $("#scrim").classList.remove("open"); },
  copy: async d => { try { await navigator.clipboard.writeText(d.text); toast("已复制"); } catch { toast("复制失败,请手动选中复制", true); } },
};
document.addEventListener("click", e => {
  const a = e.target.closest("[data-act]");
  if (a && ACTS[a.dataset.act]) { e.preventDefault(); ACTS[a.dataset.act](a.dataset, a, e); return; }
  const tr = e.target.closest("tr[data-href]");
  if (tr && tr.dataset.href) location.hash = tr.dataset.href;
});
document.addEventListener("keydown", e => { if (e.key === "Escape") closeOverlays(); });
addEventListener("hashchange", () => load(true, true));
// 每 30 秒刷一次(和后台循环同一个节奏);页面在后台时不刷,省得白白耗限流桶。
setInterval(() => { if (!document.hidden && !$("#drawer.open, #modal.open")) load(true); }, 30000);

document.addEventListener("DOMContentLoaded", async () => {
  try { META = await api("/ui/meta"); } catch (e) { /* 会在页面加载时一并报出来 */ }
  load(false);
});
