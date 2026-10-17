// 添加节点向导:第 1 步(连接)和第 2 步(入站),第 3 步由后续实现。
"use strict";
(() => {
Object.assign(ICONS, {
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  eyeoff: '<path d="M3 3l18 18M10.6 5.1A10 10 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.2 4M6.6 6.6A17 17 0 0 0 2 12s3.5 7 10 7a10 10 0 0 0 4.4-1"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/>',
  back: '<path d="M15 6l-6 6 6 6"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
  dash: '<path d="M6 12h12"/>',
});
const X = ic("x"), AN = ["连接", "入站", "安装", "完成"];
// 向导状态。pw 只留在内存和密码框里,只随请求体发出,不拼进其他文字,也不落盘。
const W = {};
function steps(st) {
  return H`<div class="wsteps">${AN.map((n, i) => H`${i ? H`<span class="ln ${st[i - 1] === "done" ? "done" : ""}"></span>` : ""}<span class="s ${st[i] || ""}"><i>${st[i] === "done" ? ic("check") : st[i] === "fail" ? X : String(i + 1)}</i><span>${n}</span></span>`)}</div>`;
}
function field(label, inputRaw, hint, err) {
  return H`<div class="field"><label>${label}</label>${inputRaw}${err ? H`<span class="fe">${err}</span>` : hint ? H`<span class="h">${hint}</span>` : ""}</div>`;
}
function drawer(st, bodyRaw, footRaw) {
  openDrawer(H`<div class="dh"><h2>添加节点</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${X}</button></div>
  ${steps(st)}<div class="db" style="padding-top:4px">${bodyRaw}</div><div class="df">${footRaw}</div>`);
  $("#drawer").classList.add("wiz");
}
function validate1() {
  const e = {}, name = String(W.name || "").trim(), port = String(W.sshPort ?? "").trim();
  if (!String(W.address || "").trim()) e.address = "请填写服务器地址";
  if (!name) e.name = "请填写节点名";
  else if (/^\d+$/.test(name)) e.name = "节点名不能是纯数字";
  if (!/^\d+$/.test(port) || +port < 1 || +port > 65535) e.sshPort = "端口要在 1–65535 之间";
  if (!W.pw) e.pw = "请输入 SSH 密码";
  return e;
}
function clearErr(el, key) {
  if (!W.errs || !W.errs[key]) return;
  delete W.errs[key];
  el.classList.remove("bad");
  const fe = el.closest(".field")?.querySelector(".fe");
  if (fe) fe.remove();
}
function render1() {
  const e = W.errs || {};
  const pwBox = H`<div class="pw"><input id="w-pw" type="${W.showPw ? "text" : "password"}" class="in ${e.pw ? "bad" : ""}" autocomplete="off"><button type="button" class="ib" id="w-pw-eye" aria-label="${W.showPw ? "隐藏密码" : "显示密码"}">${ic(W.showPw ? "eyeoff" : "eye")}</button></div>`;
  drawer(["cur", "", "", ""], H`
    <p class="lead">填一台新 VPS 的 SSH 信息。密码只用来装一次控制面的公钥，用完即丢，不保存。<a class="lnk wlnk" data-act="node-new" href="#">已能免密 SSH，直接登记</a></p>
    <div class="two">${field("服务器地址", H`<input id="w-address" class="in mono ${e.address ? "bad" : ""}" value="${W.address}" placeholder="203.0.113.10" autocomplete="off">`, "", e.address)}
    ${field("节点名", H`<input id="w-name" class="in mono ${e.name ? "bad" : ""}" value="${W.name}" placeholder="jp-2" autocomplete="off">`, "只用字母、数字和 -，之后会自动识别显示名", e.name)}</div>
    <div class="two">${field("SSH 端口", H`<input id="w-ssh-port" class="in mono ${e.sshPort ? "bad" : ""}" value="${W.sshPort}" placeholder="22" inputmode="numeric" autocomplete="off">`, "", e.sshPort)}
    ${field("SSH 用户", H`<input id="w-ssh-user" class="in mono" value="${W.sshUser}" placeholder="root" autocomplete="off">`, "", "")}</div>
    ${field("SSH 密码", pwBox, "机房给的初始密码即可", e.pw)}`,
    H`<button type="button" class="btn" data-act="close">取消</button><button type="button" class="btn pri" id="w-next">下一步</button>`);
  $("#w-pw").value = W.pw || "";
  const bind = (sel, key) => {
    const el = $(sel);
    el.addEventListener("input", () => {
      W[key] = el.value;
      clearErr(el, key);
    });
  };
  bind("#w-address", "address"); bind("#w-name", "name"); bind("#w-ssh-port", "sshPort"); bind("#w-ssh-user", "sshUser"); bind("#w-pw", "pw");
  $("#w-pw-eye").addEventListener("click", () => {
    W.pw = $("#w-pw").value; W.showPw = !W.showPw; render1();
    const p = $("#w-pw"); p.focus(); p.setSelectionRange(p.value.length, p.value.length);
  });
  $("#w-next").addEventListener("click", () => {
    W.errs = validate1();
    if (Object.keys(W.errs).length) return render1();
    W.step = 2; render2();
  });
}
function validate2() {
  const e = {}, port = String(W.inbPort ?? "").trim();
  if (!/^\d+$/.test(port) || +port < 1 || +port > 65535) e.inbPort = "端口要在 1–65535 之间";
  if (!/^[^:\s]+:\d{1,5}$/.test(String(W.dest || "").trim())) e.dest = "回落目标写成 域名:端口";
  if (!String(W.sni || "").trim()) e.sni = "请填写 SNI";
  return e;
}
function render2() {
  const e = W.errs || {};
  drawer(["done", "cur", "", ""], H`
    <p class="lead">给这台节点建一个入站。默认值与首次安装时建的入站一致。</p>
    <div class="opts">
      <div class="opt ${W.preset === "reality" ? "on" : ""}" id="w-opt-reality"><span class="rd"></span><div><b>REALITY · 443（推荐）</b><span class="h">VLESS + TCP + REALITY，SNI 和回落目标 www.cloudflare.com</span></div></div>
      <div class="opt ${W.preset === "custom" ? "on" : ""}" id="w-opt-custom"><span class="rd"></span><div><b>自定义</b><span class="h">在下面的高级里改端口、SNI 和回落目标</span></div></div>
    </div>
    <details class="adv" id="w-adv" ${W.preset === "custom" ? raw("open") : ""}><summary>高级</summary>
      <div class="two">${field("端口", H`<input id="w-inb-port" class="in mono ${e.inbPort ? "bad" : ""}" value="${W.inbPort}" placeholder="443" inputmode="numeric" autocomplete="off">`, "", e.inbPort)}
      ${field("SNI", H`<input id="w-sni" class="in mono ${e.sni ? "bad" : ""}" value="${W.sni}" placeholder="www.cloudflare.com" autocomplete="off">`, "", e.sni)}</div>
      ${field("回落目标", H`<input id="w-dest" class="in mono ${e.dest ? "bad" : ""}" value="${W.dest}" placeholder="域名:端口" autocomplete="off">`, "", e.dest)}</details>`,
    H`<button type="button" class="btn ghost" id="w-back">${ic("back")}上一步</button><span style="flex:1"></span><button type="button" class="btn" data-act="close">取消</button><button type="button" class="btn pri" id="w-start">开始安装</button>`);
  const bind = (sel, key) => {
    const el = $(sel);
    el.addEventListener("input", () => {
      W[key] = el.value;
      clearErr(el, key);
    });
  };
  bind("#w-inb-port", "inbPort"); bind("#w-sni", "sni"); bind("#w-dest", "dest");
  $("#w-opt-reality").addEventListener("click", () => { Object.assign(W, { preset: "reality", inbPort: 443, sni: "www.cloudflare.com", dest: "www.cloudflare.com:443", errs: {} }); render2(); });
  $("#w-opt-custom").addEventListener("click", () => { W.preset = "custom"; W.errs = {}; render2(); });
  $("#w-back").addEventListener("click", () => { W.errs = {}; W.step = 1; render1(); });
  $("#w-start").addEventListener("click", () => {
    W.errs = validate2();
    if (Object.keys(W.errs).length) return render2();
    if (typeof window.nodeWizardStart === "function") window.nodeWizardStart(W);
    else toast("安装步骤尚未实现", true);
  });
}
/* ---------- 第 3 步:安装 ---------- */
function row(st, t, du, extraRaw) {
  return H`<li class="${st}"><span class="pi">${st === "run" ? raw('<span class="spin"></span>') : st === "done" ? ic("check") : st === "fail" ? X : st === "skip" ? ic("dash") : raw("<i></i>")}</span><span class="t">${t}</span><span class="du">${du || ""}</span>${extraRaw || ""}</li>`;
}
const ROW_TITLES = ["登录并装公钥", "登记节点", "建入站和入口", "装核心并下发"];
const HINTS = [
  "密码不对，或这台机器关了密码登录。节点还没有建，不用回退。",
  "",
  "节点已经建了，公钥也在上面，重试不再需要密码。",
  "节点、入站、入口都保留。常见原因是机器暂时下不了包，过一会儿重试即可。",
];
function newTask() {
  return { stage: 0, nodeId: null, inboundId: null, rows: ROW_TITLES.map(t => ({ st: "wait", t, du: "", extra: null })), err: null };
}
// 耗时:<60s 写 "6.1s",否则 "1:02"。
function fmtDur(ms) {
  const s = Math.max(0, ms) / 1000;
  return s < 60 ? s.toFixed(1) + "s" : Math.floor(s / 60) + ":" + String(Math.round(s % 60)).padStart(2, "0");
}
// 失败行的原话来自控制面,extra 是控制面原话 + 针对这一步的提示。
function failExtra(stage, msg) {
  return H`<div class="sub2"><div class="raw">${msg}</div><div class="mt2">${HINTS[stage]}</div></div>`;
}
function render3() {
  const t = W.task, stages = t.rows.map(r => r.st);
  const keyFail = stages[0] === "fail", inbFail = stages[2] === "fail", coreFail = stages[3] === "fail";
  const st = keyFail ? ["fail", "", "", ""] : ["done", "done", "cur", ""];
  if (!keyFail && (inbFail || coreFail)) st[2] = "fail";
  const body = H`<ol class="plist">${t.rows.map(r => row(r.st, r.t, r.du, r.extra))}</ol>`;
  let foot;
  if (keyFail) foot = H`<span style="flex:1"></span><button type="button" class="btn pri" id="w-back1">${ic("back")}返回修改</button>`;
  else if (inbFail) foot = H`<button type="button" class="btn ghost" id="w-del">删除刚加的节点</button><span style="flex:1"></span><button type="button" class="btn pri" id="w-retry">${ic("refresh")}重试这一步</button>`;
  else if (coreFail) foot = H`<span style="flex:1"></span><button type="button" class="btn" id="w-later">稍后在节点页处理</button><button type="button" class="btn pri" id="w-retry">${ic("refresh")}重试安装</button>`;
  else foot = H`<span class="sub" style="flex:1">每一步都是一次普通请求，关掉抽屉也能在节点页接着装</span><button type="button" class="btn pri" disabled>安装中…</button>`;
  drawer(st, body, foot);
  if ($("#w-back1")) $("#w-back1").addEventListener("click", () => { W.errs = { pw: "请重新输入 SSH 密码" }; W.step = 1; render1(); });
  if ($("#w-retry")) $("#w-retry").addEventListener("click", () => runFrom(t.stage));
  if ($("#w-del")) $("#w-del").addEventListener("click", () => {
    const nodeId = t.nodeId;
    confirmBox({ title: `删除刚加的节点 ${W.name}?`, text: "节点、它的入站和入口一起删掉。公钥会留在这台机器上，要清理得自己登上去。", ok: "删除节点", danger: true,
      run: async () => { await api(`/api/nodes/${nodeId}`, { method: "DELETE" }); closeOverlays(); load(true, true); } });
  });
  if ($("#w-later")) $("#w-later").addEventListener("click", () => { closeOverlays(); location.hash = "nodes/" + t.nodeId; });
}
async function runFrom(stage) {
  const t = W.task; if (!t) return;
  t.stage = stage;
  const set = (i, st, extra) => { const r = t.rows[i]; r.st = st; if (extra !== undefined) r.extra = extra; if (st === "fail") r.du = r.du || fmtDur(Date.now() - r._t0); render3(); };
  // 密码只在这一个请求体里出现,请求发出后立刻从内存里清掉。
  if (stage <= 0 && t.rows[0].st !== "done") {
    const pw = W.pw, name = W.name, address = W.address, sshPort = W.sshPort, sshUser = W.sshUser;
    W.pw = ""; t.rows[0]._t0 = Date.now(); t.rows[0].st = "run";
    try {
      const res = await api("/api/nodes", { method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id: 0, name, address, ssh_port: +sshPort, ssh_user: sshUser === "root" ? "" : sshUser, api_port: 0, config_path: "", service_name: "", draining: false, ssh_password: pw }) });
      t.nodeId = res.id; W.display = res.display || ""; W.locateNote = res.locate_note || "";
      const ms = Date.now() - t.rows[0]._t0;
      t.rows[0].st = "done"; t.rows[0].du = fmtDur(ms);
      t.rows[1].st = "done"; t.rows[1].t = `登记节点 ${name} #${res.id}`; t.rows[1].du = fmtDur(Date.now() - t.rows[0]._t0);
      render3();
    } catch (e) { set(0, "fail", failExtra(0, e.message)); return; }
  }
  if (stage <= 2 && t.rows[2].st !== "done") {
    t.rows[2].st = "run"; t.rows[2]._t0 = Date.now(); render3();
    try {
      if (!t.inboundId) {
        const inb = await api("/api/inbounds", { method: "POST", headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ id: 0, node_id: t.nodeId, tag: "", protocol: "vless", listen: "", port: +W.inbPort, network: "tcp", security: "reality", sni: W.sni, reality_dest: W.dest, wg_mtu: 1420, flow: "xtls-rprx-vision" }) });
        t.inboundId = inb.id;
      }
      await api(`/api/inbounds/${t.inboundId}/hosts`, { method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ id: 0, inbound_id: t.inboundId, remark: W.name, address: "", port: 0 }) });
      t.rows[2].du = fmtDur(Date.now() - t.rows[2]._t0);
      set(2, "done");
    } catch (e) { set(2, "fail", failExtra(2, e.message)); return; }
  }
  if (stage <= 3) {
    t.rows[3].st = "run"; t.rows[3]._t0 = Date.now(); render3();
    try {
      await api(`/api/nodes/${t.nodeId}/install`, { method: "POST" });
      t.rows[3].du = fmtDur(Date.now() - t.rows[3]._t0);
      set(3, "done");
      await render4();
    } catch (e) { set(3, "fail", failExtra(3, e.message)); return; }
  }
}

/* ---------- 第 4 步:完成 ---------- */
async function render4() {
  const t = W.task, nodeId = t.nodeId;
  let node = null, plans = null;
  try {
    const [n, p] = await Promise.all([api("/api/nodes/" + nodeId), api("/api/plans")]);
    node = n; plans = p;
  } catch (e) { /* 拉不到就只用已有数据 */ }
  const name = W.name, address = W.address;
  const display = (node && node.display) || W.display || "";
  const locateNote = (node && node.locate_note) || W.locateNote || "";
  let subs;
  if (!plans) subs = "到套餐页查看";
  else {
    const allPlans = (plans || []).filter(p => p.all_nodes);
    if (!allPlans.length) subs = "没有包含全部节点的套餐，需要的话到套餐页勾上";
    else subs = H`${allPlans.map(p => p.name).join("、")} · <b>${allPlans.reduce((s, p) => s + (+p.users || 0), 0)}</b> 个用户的订阅会在下次刷新时拿到它`;
  }
  drawer(["done", "done", "done", "cur"], H`
    <div class="done-hd"><span class="ok">${ic("check")}</span><div><h3>${display || name} 已就绪</h3><p>${name} · ${address} · xray 已安装，配置已下发</p></div></div>
    <dl class="kvw"><dt>显示名</dt><dd>${display || "—"} <span class="sub">· ${locateNote || "按 IP 自动识别，可在节点页改"}</span></dd>
    <dt>入站</dt><dd class="mono">REALITY · ${W.inbPort} · 1 个入口</dd>
    <dt>进了哪些订阅</dt><dd>${subs}</dd></dl>
    <div class="callout stone mb">${ic("info")}<div><b>包含全部节点的套餐会自动包含它。</b>只给部分节点的套餐不会自动加，需要的话到套餐页勾上。</div></div>`,
    H`<button type="button" class="btn" id="w-again">${ic("plus")}再加一台</button><span style="flex:1"></span><button type="button" class="btn pri" id="w-view">查看节点</button>`);
  $("#w-again").addEventListener("click", () => openNodeWizard());
  $("#w-view").addEventListener("click", () => { closeOverlays(); location.hash = "nodes/" + nodeId; });
  load(true, true); // 刷新背后的节点列表;load 不会关抽屉。
}
window.nodeWizardStart = W => { W.task = newTask(); runFrom(0); };
window.openNodeWizard = () => {
  Object.assign(W, { step: 1, name: "", address: "", sshPort: 22, sshUser: "root", pw: "", showPw: false, preset: "reality", inbPort: 443, sni: "www.cloudflare.com", dest: "www.cloudflare.com:443", errs: {} });
  render1();
};
})();
