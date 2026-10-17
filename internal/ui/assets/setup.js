// 首次安装向导(向导 B)全部 6 屏:欢迎 / 连接 / 检查 / 设置 / 安装 / 完成;
// 欢迎屏另有「接入已有控制面」一条路:填地址、核对证书指纹、输设备密码。
// 对外暴露 window.openSetup 与 window.setupStartRun;轮询状态挂在 window.__setupPoll。
"use strict";
(() => {
Object.assign(ICONS, {
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  eyeoff: '<path d="M3 3l18 18M10.6 5.1A10 10 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.2 4M6.6 6.6A17 17 0 0 0 2 12s3.5 7 10 7a10 10 0 0 0 4.4-1"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/>',
  back: '<path d="M15 6l-6 6 6 6"/>',
  srv: '<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
});
const X = ic("x");
const BN = ["连接", "检查", "设置", "安装", "完成"];
const S = {
  step: 1, host: "", sshPort: 22, sshUser: "root", auth: "password", pw: "", identity: "",
  pwShown: false, probe: null, checkErr: "", checkAt: 0,
  username: "user1", dpw: "", dpw2: "", dpwShown: false,
  addr: "0.0.0.0:8080", inboundPort: 0, portProbe: null, subPrefix: "", sni: "www.cloudflare.com", errs: {}, run: null,
};

/* ---------- 小工具(与 wizard.js 同款) ---------- */
function steps(st) {
  return H`<div class="wsteps">${BN.map((n, i) => H`${i ? H`<span class="ln ${st[i - 1] === "done" ? "done" : ""}"></span>` : ""}<span class="s ${st[i] || ""}"><i>${st[i] === "done" ? ic("check") : st[i] === "fail" ? X : String(i + 1)}</i><span>${n}</span></span>`)}</div>`;
}
function field(label, inputRaw, hint, err) {
  return H`<div class="field"><label>${label}</label>${inputRaw}${err ? H`<span class="fe">${err}</span>` : hint ? H`<span class="h">${hint}</span>` : ""}</div>`;
}
const btn = (t, cls = "", icn, id = "") => H`<button type="button" class="btn ${cls}"${id ? raw(` id="${id}"`) : ""}>${icn ? ic(icn) : ""}${t}</button>`;
const opt = (on, id, title, h) => H`<div class="opt ${on ? "on" : ""}" id="${id}"><span class="rd"></span><div><b>${title}</b><span class="h">${h}</span></div></div>`;
const pwBox = (id, shown) => H`<div class="pw"><input id="${id}" class="in ${shown ? "mono" : ""}" type="${shown ? "text" : "password"}" autocomplete="off"><button type="button" class="ib" id="${id}-eye" aria-label="${shown ? "隐藏密码" : "显示密码"}">${ic(shown ? "eyeoff" : "eye")}</button></div>`;
const row = (st, t, du, extraRaw) => H`<li class="${st}"><span class="pi">${st === "run" ? raw('<span class="spin"></span>') : st === "done" ? ic("check") : st === "fail" ? X : st === "skip" ? ic("dash") : raw("<i></i>")}</span><span class="t">${t}</span><span class="du">${du || ""}</span>${extraRaw || ""}</li>`;
function clearErr(el, key) {
  if (!S.errs || !S.errs[key]) return;
  delete S.errs[key];
  el.classList.remove("bad");
  const fe = el.closest(".field")?.querySelector(".fe");
  if (fe) fe.remove();
}
function fmtDur(ms) {
  const s = Math.max(0, ms) / 1000;
  return s < 60 ? s.toFixed(1) + " 秒" : Math.floor(s / 60) + ":" + String(Math.round(s % 60)).padStart(2, "0");
}
function setup(st, bodyRaw, footRaw) {
  document.querySelector(".setup")?.remove();
  const d = document.createElement("div");
  d.className = "setup";
  d.innerHTML = val(H`<div class="tb-drag"></div><div class="sbrand"><img class="lk lk-l" src="/lockup-light.svg" alt="MPX"><img class="lk lk-d" src="/lockup-dark.svg" alt="MPX"> · 首次安装${META?.version ? H`<small>${META.version}</small>` : ""}</div><div class="scard">${st ? steps(st) : ""}<div class="db">${bodyRaw}</div><div class="df">${footRaw}</div></div>`);
  document.body.append(d);
}
function bindInput(sel, key) {
  const el = $(sel);
  if (!el) return;
  el.addEventListener("input", () => { S[key] = el.value; clearErr(el, key); });
}

/* ---------- 屏 1:欢迎 ---------- */
function renderWelcome() {
  S.step = 1;
  setup(null, H`<div class="welcome"><div class="ill"><img class="mk-l" src="/mark-light.svg" alt=""><img class="mk-d" src="/mark-dark.svg" alt=""></div><h2>在你的 VPS 上装好 MPX</h2>
  <p class="lead">一台机器同时当控制面和第一个节点，大约 5–10 分钟。</p>
  <ul><li>${ic("srv")}<span>装控制面，并在同一台机器上建第一个节点、连接方式和对外地址</span></li><li>${ic("users")}<span>建第一个用户，给你一条可以直接导入的订阅链接</span></li>
  <li>${ic("lock")}<span>设一个设备密码，以后别的电脑用它接入</span></li><li>${ic("term")}<span>这台电脑自动接入，装完直接进管理界面</span></li></ul>
  <p class="sub">需要：一台 Linux VPS 的地址和 root 密码（或这台电脑已能用密钥登录）。已经有控制面？选右边的「接入已有控制面」。</p></div>`,
  H`<span class="sub" style="flex:1">${META?.profiles ? H`<a href="#" class="u-link" id="s-profiles">切换到已有的控制面</a>` : ""}</span>${btn("接入已有控制面", "", "lock", "s-connect")}${btn("首次安装（新服务器）", "pri", "", "s-start")}`);
  $("#s-start").addEventListener("click", () => renderConnect());
  $("#s-connect").addEventListener("click", () => { resetCN("setup"); renderConnectCP(); });
  $("#s-profiles")?.addEventListener("click", e => { e.preventDefault(); openProfiles(); });
}

/* ---------- 接入已有控制面 ---------- */
// CN 是接入流程的状态:addr/name/profile 是表单值,server/fp 是取指纹的结果,pw 只在
// 内存里,请求发出后立刻清掉。mode 决定走哪条路:"setup" 是首次安装的欢迎屏进来的
// (/local/setup/connect*,存进实现方挑的档);"add" 是正常模式的「添加控制面」
// (/local/profiles/connect*,存进指名的新档,app.js 也从这进)。
const CN = { mode: "setup", addr: "", name: "", profile: "", server: "", fp: "", fpErr: "", err: "", probing: false, ok: false, pw: "", pwShown: false, errs: {} };

// CN_EP 是两种模式各自的一组本地接口前缀。
const CN_EP = { setup: "/local/setup", add: "/local/profiles" };

function resetCN(mode) {
  Object.assign(CN, { mode, addr: "", name: "", profile: "", server: "", fp: "", fpErr: "", err: "", probing: false, ok: false, pw: "", pwShown: false, errs: {} });
}

function renderConnectCP() {
  const add = CN.mode === "add", e = CN.errs || {}, fp = CN.fp && !CN.fpErr;
  setup(null, H`<h2>${add ? "添加控制面" : "接入已有控制面"}</h2>
  <p class="lead">${add ? H`把另一套控制面接进来，记下来并切过去；先取回证书指纹核对，再输入设备密码。` : H`把这台电脑接进已经在跑的控制面：先取回证书指纹核对，再输入设备密码。`}</p>
  <div class="two">${field("控制面地址", H`<input id="s-cp-addr" class="in mono ${e.addr ? "bad" : ""}" placeholder="203.0.113.10:8080" autocomplete="off">`, "IP 或域名，端口默认 8080", e.addr)}
  ${field("设备名", H`<input id="s-cp-name" class="in mono" autocomplete="off">`, "这台电脑在设备列表里叫什么；留空用主机名")}</div>
  ${add ? field("名称", H`<input id="s-cp-profile" class="in mono ${e.profile ? "bad" : ""}" placeholder="work" autocomplete="off">`, "只能用字母、数字、- 和 _，1–32 个字符；名字已存在会提示换一个", e.profile) : ""}
  ${CN.err ? H`<div class="callout bad mb">${ic("warn", "ico-bad")}<div><b>接入失败</b><div class="raw">${CN.err}</div></div></div>` : ""}
  ${CN.fpErr ? H`<div class="callout bad mb">${ic("warn", "ico-bad")}<div><b>取指纹失败</b><div class="raw">${CN.fpErr}</div></div></div>` : ""}
  ${CN.probing ? H`<ol class="plist">${row("run", "连上控制面，取回证书指纹")}</ol>` : ""}
  ${fp ? H`<div class="field"><label>证书指纹</label><div class="cmd">${CN.fp}</div><span class="h">和首次安装完成时显示的那一行对一下，或向控制面的管理员确认</span></div>
  <label class="chk mb"><input type="checkbox" id="s-cp-ok" ${CN.ok ? raw("checked") : ""}> 指纹核对无误，是我的控制面</label>
  ${field("设备密码", pwBox("s-cp-pw", CN.pwShown), "首次安装时设的；忘了可以在已接入设备的「系统 → 设备与接入」里重设")}` : ""}
  <p class="sub">接入时会核对证书指纹，设备密码只用于这一次接入。</p>`,
  H`${btn(add ? "取消" : "上一步", "ghost", "back", "s-cp-back")}<span class="sp"></span>${fp ? H`${btn("重新取指纹", "", "refresh", "s-cp-probe")}${btn("接入", "pri", "", "s-cp-go")}` : raw(`<button type="button" class="btn pri" id="s-cp-probe"${CN.probing ? ' disabled style="opacity:.5"' : ""}>${CN.probing ? "取指纹中…" : "取指纹"}</button>`)}`);
  $("#s-cp-addr").value = CN.addr; $("#s-cp-name").value = CN.name;
  if (add) $("#s-cp-profile").value = CN.profile;
  if (fp) $("#s-cp-pw").value = CN.pw;
  // 改地址会作废已取的指纹:得重新取、重新核对。
  $("#s-cp-addr").addEventListener("input", ev => {
    CN.addr = ev.target.value; CN.errs = {};
    if (CN.server || CN.fpErr) {
      CN.server = ""; CN.fp = ""; CN.fpErr = ""; CN.ok = false;
      renderConnectCP();
      const a = $("#s-cp-addr"); a.focus(); a.setSelectionRange(a.value.length, a.value.length);
      return;
    }
  });
  $("#s-cp-name").addEventListener("input", ev => { CN.name = ev.target.value; });
  if (add) $("#s-cp-profile").addEventListener("input", ev => { CN.profile = ev.target.value; CN.errs = {}; });
  $("#s-cp-back").addEventListener("click", () => {
    CN.errs = {};
    // 正常模式:拆掉遮罩回到管理界面;setup 模式(欢迎屏也能从控制面弹窗进到这)
    // 没有底层页面可回,退回欢迎屏。
    if (add && META.mode !== "setup") document.querySelector(".setup")?.remove();
    else renderWelcome();
  });
  $("#s-cp-probe").addEventListener("click", async () => {
    const addr = String(CN.addr || "").trim();
    if (!addr) { CN.errs = { addr: "请填写控制面地址" }; renderConnectCP(); return; }
    CN.errs = {}; CN.err = ""; CN.fpErr = ""; CN.server = ""; CN.fp = ""; CN.ok = false; CN.probing = true;
    renderConnectCP();
    try {
      const r = await api(`${CN_EP[CN.mode]}/connect/probe`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ addr }) });
      CN.server = (r && r.server) || ""; CN.fp = (r && r.fingerprint) || "";
      if (!CN.server || !CN.fp) CN.fpErr = "控制面没有报证书指纹";
    } catch (e2) { CN.fpErr = e2.message || String(e2); }
    CN.probing = false;
    renderConnectCP();
  });
  if (fp) {
    const go = $("#s-cp-go");
    const updGo = () => { go.disabled = !(CN.ok && CN.pw); };
    $("#s-cp-ok").addEventListener("change", ev => { CN.ok = ev.target.checked; updGo(); });
    $("#s-cp-pw").addEventListener("input", ev => { CN.pw = ev.target.value; updGo(); });
    $("#s-cp-pw-eye").addEventListener("click", () => { CN.pw = $("#s-cp-pw").value; CN.pwShown = !CN.pwShown; renderConnectCP(); $("#s-cp-pw").focus(); });
    updGo();
    go.addEventListener("click", async () => {
      if (!CN.ok || !CN.pw) return;
      const profile = String(CN.profile || "").trim();
      if (add && !/^[A-Za-z0-9_-]{1,32}$/.test(profile)) {
        CN.errs = { profile: "只能用字母、数字、- 和 _，1–32 个字符" };
        renderConnectCP();
        return;
      }
      const body = { server: CN.server, fingerprint: CN.fp, password: CN.pw, name: String(CN.name || "").trim() };
      if (add) body.profile = profile;
      CN.pw = ""; CN.err = "";
      go.disabled = true; go.classList.add("busy");
      try {
        const r = await api(`${CN_EP[CN.mode]}/connect`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
        if (r && r.ok) { location.reload(); return; } // 已切到新控制面(setup 模式则是转成正常模式),整页刷新
        CN.err = "控制面没有确认接入";
      } catch (e2) { CN.err = e2.message || String(e2); }
      renderConnectCP();
    });
  }
}

/* ---------- 屏 2:连接 ---------- */
function validateConnect() {
  const e = {}, port = String(S.sshPort ?? "").trim();
  if (!String(S.host || "").trim()) e.host = "请填写服务器地址";
  if (!/^\d+$/.test(port) || +port < 1 || +port > 65535) e.sshPort = "端口要在 1–65535 之间";
  if (S.auth === "password" && !S.pw) e.pw = "请输入 root 密码";
  return e;
}
function renderConnect() {
  S.step = 2;
  const e = S.errs || {}, key = S.auth === "key";
  setup(["cur", "", "", "", ""], H`<h2>连接 VPS</h2><p class="lead">控制面要装在这台机器上。</p>
  <div class="two">${field("服务器地址", H`<input id="s-host" class="in mono ${e.host ? "bad" : ""}" placeholder="203.0.113.10" autocomplete="off">`, "", e.host)}
  ${field("SSH 端口", H`<input id="s-port" class="in mono ${e.sshPort ? "bad" : ""}" placeholder="22" inputmode="numeric" autocomplete="off">`, "", e.sshPort)}</div>
  <div class="field"><label>登录方式</label><div class="opts" style="margin:0">${opt(!key, "s-auth-pw", "SSH 密码", "只用来装一次本机公钥，用完即丢")}${opt(key, "s-auth-key", "这台电脑已能用密钥登录", "例如之前跑过 ssh-copy-id")}</div></div>
  ${key ? field("私钥路径（可选）", H`<input id="s-identity" class="in mono" placeholder="~/.ssh/id_ed25519" autocomplete="off">`, "留空时和 ssh 命令一样自动找")
    : field("root 密码", pwBox("s-pw", S.pwShown), "", e.pw)}
  <details class="adv"><summary>高级</summary>${field("SSH 用户", H`<input id="s-user" class="in mono" autocomplete="off">`, "固定 root：控制面要求以 root 身份安装")}</details>`,
  H`${btn("上一步", "ghost", "back", "s-back")}<span class="sp"></span>${btn("检查机器", "pri", "", "s-check")}`);
  $("#s-host").value = S.host; $("#s-port").value = S.sshPort; $("#s-user").value = S.sshUser;
  if (key) $("#s-identity").value = S.identity; else $("#s-pw").value = S.pw;
  bindInput("#s-host", "host"); bindInput("#s-port", "sshPort"); bindInput("#s-user", "sshUser");
  if (key) bindInput("#s-identity", "identity");
  else {
    bindInput("#s-pw", "pw");
    $("#s-pw-eye").addEventListener("click", () => { S.pw = $("#s-pw").value; S.pwShown = !S.pwShown; renderConnect(); $("#s-pw").focus(); });
  }
  $("#s-auth-pw").addEventListener("click", () => { S.auth = "password"; S.errs = {}; renderConnect(); });
  $("#s-auth-key").addEventListener("click", () => { S.auth = "key"; S.errs = {}; renderConnect(); });
  $("#s-back").addEventListener("click", () => { S.errs = {}; renderWelcome(); });
  $("#s-check").addEventListener("click", () => {
    S.errs = validateConnect();
    if (Object.keys(S.errs).length) return renderConnect();
    S.checkErr = ""; S.probe = null; S.checkAt = Date.now();
    renderCheck(); runCheck();
  });
}

/* ---------- 屏 3:检查 ---------- */
async function runCheck() {
  const pw = S.pw; S.pw = "";
  const body = { host: S.host, ssh_port: +S.sshPort || 22, ssh_user: S.sshUser || "root" };
  if (S.auth === "password") body.password = pw;
  else if (S.identity) body.identity = S.identity;
  try {
    const r = await api("/local/setup/check", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    S.probe = (r && r.probe) || {};
  } catch (e) { S.checkErr = e.message || String(e); }
  renderCheck();
}
function backToConnect() {
  S.pw = ""; S.errs = S.auth === "password" ? { pw: "请重新输入 root 密码" } : {};
  renderConnect();
}
function renderCheck() {
  S.step = 3;
  const key = S.auth === "key", first = key ? "用密钥登录" : "用密码登录并装上本机公钥";
  if (S.checkErr) {
    setup(["done", "fail", "", "", ""], H`<h2>检查机器</h2><p class="lead">登录一次、装上本机公钥，再看看这台机器能不能装控制面。</p>
    <ol class="plist">${row("fail", first)}${row("wait", "检查系统、架构、权限和下载工具")}</ol>
    <div class="callout bad mb">${ic("warn", "ico-bad")}<div><b>登录或检查失败</b><div class="mt2">密码不对、这台机器关了密码登录，或连不上。</div><div class="raw">${S.checkErr}</div></div></div>`,
    H`${btn("返回修改", "pri", "back", "s-back2")}`);
    $("#s-back2").addEventListener("click", backToConnect);
    return;
  }
  if (!S.probe) {
    setup(["done", "cur", "", "", ""], H`<h2>检查机器</h2><p class="lead">登录一次、装上本机公钥，再看看这台机器能不能装控制面。</p>
    <ol class="plist">${row("run", first)}${row("wait", "检查系统、架构、权限和下载工具")}</ol>`,
    H`<span class="sp"></span>${raw('<button type="button" class="btn pri" disabled style="opacity:.5">检查中…</button>')}`);
    return;
  }
  const p = S.probe, dl = p.fetch ? String(p.fetch).split("/").pop() : "";
  const osBad = p.os !== "Linux", uidBad = String(p.uid) !== "0", dlBad = !p.fetch;
  const allOk = !osBad && !uidBad && !dlBad;
  const dur = fmtDur(Date.now() - S.checkAt);
  const pr = (l, v, no) => H`<div class="${no ? "no" : ""}"><span>${l}</span><b>${v}</b></div>`;
  const reasons = [];
  if (osBad) reasons.push(`系统是 ${p.os || "未知"}，控制面只能装在 Linux 上。`);
  if (uidBad) reasons.push(`登录用户不是 root（uid=${p.uid}）。回到上一步，在高级里确认 SSH 用户是 root，或先在 VPS 上开启 root 登录。`);
  if (dlBad) reasons.push("机器上没有 curl 或 wget，装不了控制面。先在 VPS 上装一个。");
  const title = osBad ? "只能装在 Linux 上" : uidBad ? "要用 root 登录" : "缺少下载工具";
  const portNotes = allOk ? applyPortProbe(p) : [];
  setup(["done", "done", "", "", ""], H`<h2>检查机器</h2><p class="lead">登录一次、装上本机公钥，再看看这台机器能不能装控制面。</p>
  <ol class="plist">${row("done", first, dur)}${row("done", "检查系统、架构、权限和下载工具", dur)}</ol>
  <div class="probe">${pr("系统", p.os || "未知", osBad)}${pr("架构", p.arch || "未知")}${pr("用户", uidBad ? `uid=${p.uid}` : "root", uidBad)}${pr("下载", dl || "无", dlBad)}</div>
  ${allOk ? H`<div class="callout stone mb">${ic("check")}<div><b>可以安装。</b>本机公钥已装上，后面的步骤不再需要密码。</div></div>${portNotes.length ? H`<div class="callout warn mb" id="s-ports">${ic("warn", "ico-warn")}<div><b>端口已换</b>${portNotes.map(t => H`<div class="mt2">${t}</div>`)}</div></div>` : ""}`
    : H`<div class="callout bad mb">${ic("warn", "ico-bad")}<div><b>${title}</b><div class="mt2">${reasons.join(" ")}</div></div></div>`}`,
  allOk ? H`${btn("上一步", "ghost", "back", "s-back")}<span class="sp"></span>${btn("下一步", "pri", "", "s-next")}`
    : H`${btn("返回修改", "pri", "back", "s-back2")}`);
  const back = $("#s-back") || $("#s-back2");
  back.addEventListener("click", backToConnect);
  if (allOk) $("#s-next").addEventListener("click", renderSettings);
}

// applyPortProbe:检查结果里带了端口占用(v0.9.5 起),被占的端口换成推荐的空闲端口,
// 返回给人看的说明。只在第一次看到这份结果时改,之后用户在「高级」里改的值不覆盖。
function applyPortProbe(p) {
  const notes = [];
  if (p.api_busy) {
    if (p.api_suggest) {
      if (S.portProbe !== p) S.addr = String(S.addr || "0.0.0.0:8080").replace(/:\d+$/, ":" + p.api_suggest);
      notes.push(`控制面默认用的 ${p.api_port} 端口已被 ${p.api_busy} 占用，已改用 ${p.api_suggest}。可在下面「高级 → 监听地址」里改。`);
    } else notes.push(`控制面默认用的 ${p.api_port} 端口已被 ${p.api_busy} 占用，请在下面「高级 → 监听地址」里换一个端口。`);
  }
  if (p.inbound_busy && p.inbound_suggest) {
    if (S.portProbe !== p) S.inboundPort = +p.inbound_suggest;
    notes.push(`连接方式默认用的 ${p.inbound_port} 端口已被 ${p.inbound_busy} 占用，已改用 ${p.inbound_suggest}。`);
  }
  S.portProbe = p;
  return notes;
}

/* ---------- 屏 4:设置 ---------- */
function strength(pw) {
  let n = 0;
  if (pw.length >= 12) n++;
  if (pw.length >= 16) n++;
  if (/\d/.test(pw) && /[A-Za-z]/.test(pw)) n++;
  if (/[^A-Za-z0-9]/.test(pw)) n++;
  return n;
}
const SW = ["太短", "弱", "中", "强", "强"];
function meterHtml(pw) {
  const n = strength(pw);
  return `<i class="${n >= 1 ? "on" : ""}"></i><i class="${n >= 2 ? "on" : ""}"></i><i class="${n >= 3 ? "on" : ""}"></i><i class="${n >= 4 ? "on" : ""}"></i>`;
}
function validateSetup() {
  const e = {};
  if (!/^[A-Za-z0-9_-]+$/.test(String(S.username || "").trim())) e.username = "只能用字母、数字、- 和 _";
  if (String(S.dpw || "").length < 12) e.dpw = "设备密码至少 12 个字符";
  if (S.dpw2 !== S.dpw) e.dpw2 = "两次输入不一致";
  if (!/^[^:\s]+:\d{1,5}$/.test(String(S.addr || "").trim())) e.addr = "监听地址写成 地址:端口";
  return e;
}
function renderSettings() {
  S.step = 4;
  const e = S.errs || {};
  setup(["done", "done", "cur", "", ""], H`<h2>设置</h2><p class="lead">之后都能在管理界面里改。</p>
  <div class="two">${field("第一个用户", H`<input id="s-username" class="in mono ${e.username ? "bad" : ""}" autocomplete="off">`, "只能用字母、数字、- 和 _", e.username)}
  <div class="field"><label>节点显示名</label><div class="sub" id="s-autoname" style="padding-top:8px">按这台 VPS 的 IP 自动识别（如 🇯🇵 东京 A），装好后可在节点页改。</div></div></div>
  <div class="two"><div class="field"><label>设备密码</label>${pwBox("s-dpw", S.dpwShown)}<div class="meter" id="s-meter">${raw(meterHtml(S.dpw))}</div><span class="h" id="s-str">强度：${SW[strength(S.dpw)]}</span>${e.dpw ? H`<span class="fe">${e.dpw}</span>` : ""}</div>
  <div class="field"><label>再输一次</label>${pwBox("s-dpw2", S.dpwShown)}${e.dpw2 ? H`<span class="fe">${e.dpw2}</span>` : ""}</div></div>
  <p class="sub" style="margin-top:-4px">在另一台设备的 MPX 里选「接入已有控制面」时要用它。不会保存在这台电脑之外。</p>
  ${field("订阅地址", H`<input id="s-sub" class="in mono" autocomplete="off" placeholder="例如 https://sub.example.com">`, "发给用户的链接以它开头。不填就会把服务器 IP 发给用户;之后在「订阅」里也能改。")}
  <details class="adv"><summary>高级</summary>
   ${field("监听地址", H`<input id="s-addr" class="in mono ${e.addr ? "bad" : ""}" autocomplete="off">`, "", e.addr)}${field("SNI", H`<input id="s-sni" class="in mono" autocomplete="off">`, "第一个连接方式用的 REALITY SNI")}</details>
  `,
  H`${btn("上一步", "ghost", "back", "s-back")}<span class="sp"></span>${btn("开始安装", "pri", "", "s-run")}`);
  $("#s-username").value = S.username;
  $("#s-dpw").value = S.dpw; $("#s-dpw2").value = S.dpw2;
  $("#s-addr").value = S.addr; $("#s-sub").value = S.subPrefix; $("#s-sni").value = S.sni;
  bindInput("#s-username", "username");
  bindInput("#s-addr", "addr"); bindInput("#s-sub", "subPrefix"); bindInput("#s-sni", "sni");
  $("#s-dpw").addEventListener("input", () => {
    S.dpw = $("#s-dpw").value;
    const m = $("#s-meter"); if (m) m.innerHTML = meterHtml(S.dpw);
    const t = $("#s-str"); if (t) t.textContent = "强度：" + SW[strength(S.dpw)];
    clearErr($("#s-dpw"), "dpw");
  });
  bindInput("#s-dpw2", "dpw2");
  const eye = () => {
    S.dpw = $("#s-dpw").value; S.dpw2 = $("#s-dpw2").value; S.dpwShown = !S.dpwShown; renderSettings();
    $("#s-dpw").focus();
  };
  $("#s-dpw-eye").addEventListener("click", eye);
  $("#s-dpw2-eye").addEventListener("click", eye);
  $("#s-back").addEventListener("click", () => { S.errs = {}; renderConnect(); });
  $("#s-run").addEventListener("click", () => {
    S.errs = validateSetup();
    if (Object.keys(S.errs).length) return renderSettings();
    if (typeof window.setupStartRun === "function") {
      window.setupStartRun({ host: S.host, ssh_port: +S.sshPort || 22, ssh_user: S.sshUser || "root", identity: S.identity, name: "", username: S.username, device_password: S.dpw, addr: S.addr, sub_prefix: S.subPrefix, sni: S.sni, inbound_port: S.inboundPort || 0 });
    } else toast("安装步骤尚未实现", true);
  });
}

/* ---------- 屏 5:安装(轮询) ---------- */
// P 是轮询状态,单独成一个对象方便测试 window.__setupPoll 读 delay。
const P = { id: "", since: 0, delay: 2000, idle: 0, timer: null, steps: [], lines: [], elapsed: 0, ended: false, result: null, error: "", busy: false };
window.__setupPoll = P;
// poll/schedule/start 也挂在 P 上,便于测试直接驱动。
Object.assign(P, { poll, schedule, start: startPoll });

function fmtClock(secs) {
  const s = Math.max(0, Math.floor(secs || 0));
  return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
}
function stepDur(secs) { return secs > 0 ? fmtDur(secs * 1000) : ""; }
function logBlock() { return H`<div class="logt">${P.lines.join("\n")}</div>`; }
function scrollLog() { const lg = $(".logt"); if (lg) lg.scrollTop = lg.scrollHeight; }

function renderInstall() {
  const list = P.steps.length ? P.steps : [{ title: "准备安装", state: "run", secs: 0 }];
  setup(["done", "done", "done", "cur", ""], H`<h2>安装</h2>
  <div class="banner">${ic("term")}<span>安装期间不要关闭 MPX。可以刷新这个页面，进度会接着显示。</span></div>
  <ol class="plist">${list.map(s => row(s.state, s.title, stepDur(s.secs),
    s.state === "run" ? H`<div class="sub2"><span class="tog">${ic("arrow")}最近 50 行输出</span></div>${logBlock()}` : ""))}</ol>`,
  H`<span class="sub" style="flex:1">已用 ${fmtClock(P.elapsed)} · 最长约 25 分钟</span>${raw('<button type="button" class="btn pri" disabled style="opacity:.5">安装中…</button>')}`);
  scrollLog();
}

function renderFail() {
  setup(["done", "done", "done", "fail", ""], H`<h2>安装</h2>
  <ol class="plist">${P.steps.map(s => row(s.state, s.title, stepDur(s.secs), s.state === "fail" ? logBlock() : ""))}</ol>
  <div class="callout bad mb">${ic("warn", "ico-bad")}<div><b>安装失败</b><div class="raw">${P.error || "安装没有完成"}</div><div class="mt2">每一步都可以安全重跑，已经装好的部分会复用。处理完点「重新运行」即可。</div></div></div>`,
  H`<span class="sp"></span>${btn("重新运行", "pri", "refresh", "s-rerun")}`);
  scrollLog();
  $("#s-rerun").addEventListener("click", () => {
    if (S.run) window.setupStartRun(S.run);
    else renderSettings();
  });
}

function schedule() {
  P.timer = null;
  if (document.hidden || P.ended) return;
  P.timer = setTimeout(poll, P.delay);
}

async function poll() {
  if (P.timer) { clearTimeout(P.timer); P.timer = null; }
  if (P.ended || !P.id || P.busy) return;
  P.busy = true;
  let resp;
  try {
    resp = await api(`/local/setup/jobs/${encodeURIComponent(P.id)}?since=${P.since}`);
  } catch (e) {
    // 网络/接口失败:退避到最多 10s 再试,任务本身可能还在跑。
    P.busy = false;
    P.delay = Math.min(P.delay * 2, 10000);
    schedule();
    return;
  }
  P.busy = false;
  let changed = false;
  if (resp.seq != null) P.since = resp.seq;
  if (resp.elapsed != null) P.elapsed = resp.elapsed;
  if (resp.steps && resp.steps.length) { P.steps = resp.steps; changed = true; }
  if (resp.lines && resp.lines.length) {
    P.lines = P.lines.concat(resp.lines);
    if (P.lines.length > 50) P.lines = P.lines.slice(P.lines.length - 50);
    changed = true;
  }
  if (changed) { P.idle = 0; P.delay = 2000; }
  else {
    P.idle++;
    if (P.idle >= 6) P.delay = 10000;
    else if (P.idle >= 3) P.delay = 5000;
  }
  if (resp.state === "done") {
    P.ended = true;
    if (resp.result) P.result = resp.result;
    renderDone(P.result);
    return;
  }
  if (resp.state === "failed") {
    P.ended = true;
    P.error = resp.error || "";
    renderFail();
    return;
  }
  if (changed) renderInstall();
  if (resp.more) { P.timer = setTimeout(poll, 0); return; }
  schedule();
}

function startPoll(id) {
  P.id = id; P.since = 0; P.idle = 0; P.delay = 2000;
  P.steps = []; P.lines = []; P.elapsed = 0;
  P.ended = false; P.result = null; P.error = ""; P.timer = null; P.busy = false;
  renderInstall();
  poll();
}

document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    if (P.timer) { clearTimeout(P.timer); P.timer = null; }
  } else if (!P.ended && P.id && !P.timer) {
    poll();
  }
});

/* ---------- 屏 6:完成 ---------- */
async function renderDone(res) {
  S.step = 6;
  res = res || {};
  S.dpw = ""; S.dpw2 = "";
  if (S.run) S.run.device_password = "";
  const hp = res.host_port || S.host || "", fp = res.fingerprint || "", username = res.username || S.username || "";
  let url = "";
  if (res.connected) {
    try {
      const d = await api(`/api/users/${encodeURIComponent(username)}/links`);
      url = (d && (d.auto || d.links)) || "";
    } catch { url = ""; }
  }
  const copyBtn = t => raw(`<button type="button" class="ib" data-act="copy" data-text="${esc(t)}" aria-label="复制">${ic("copy")}</button>`);
  const cmdBox = t => H`<div class="cmdc"><div class="cmd">${t}</div>${copyBtn(t)}</div>`;
  const addrBox = hp ? cmdBox(hp) : H`<span class="h">控制面地址未知</span>`;
  setup(["done", "done", "done", "done", "cur"], H`<div class="done-hd"><span class="ok">${ic("check")}</span><div><h3>装好了</h3><p>控制面已经在 <b>${hp}</b> 上运行${res.connected ? "，这台电脑已经接入。" : "。"}</p></div></div>
  ${res.connected ? (url ? H`<div class="qrw"><img class="qr" src="/local/qr.svg?d=${encodeURIComponent(url)}" alt="订阅二维码">
    <div class="field"><label>${username} 的订阅链接</label>${cmdBox(url)}<span class="h">在手机客户端里扫码，或复制链接导入</span></div></div>`
    : H`<div class="field"><label>订阅链接</label><span class="h">到用户页查看订阅链接</span></div>`)
  : H`<div class="callout stone mb">${ic("info")}<div><b>本机没有自动接入</b><div class="mt2">在这台电脑的 MPX 里选「接入已有控制面」，输入下面的地址和设备密码。</div></div></div>`}
  <div class="field"><label>在另一台设备上接入</label>${addrBox}<span class="h">在 MPX 里选「接入已有控制面」，输入这个地址，核对证书指纹后输入设备密码。</span></div>
  <div class="field"><label>证书指纹</label><div class="cmd">${fp || "（未知）"}</div></div>
  <p class="sub">本机用了 <code>~/.ssh</code> 里的一把密钥；VPS 上追加了这把公钥和控制面自己的公钥。</p>`,
  H`<span class="sp"></span>${btn("进入管理界面", "pri", "arrow", "s-enter")}`);
  $("#s-enter").addEventListener("click", () => location.reload());
}

// openConnectForm:正常模式「添加控制面」的入口,app.js 的控制面弹窗调它。重置
// 状态再进表单,免得带上 setup 流程里输过的地址或指纹;成功后表单自己 reload。
function openConnectForm() {
  resetCN("add");
  renderConnectCP();
}
window.openConnectForm = openConnectForm;

window.setupStartRun = async params => {
  S.run = params;
  let data = null, ok = false, id = "";
  try {
    const res = await fetch("/local/setup/run", {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(params),
    });
    const text = await res.text();
    try { data = text ? JSON.parse(text) : null; } catch { data = null; }
    if (res.status === 202 && data && data.id) { id = data.id; ok = true; }
    else if (res.status === 409 && data && data.id) { id = data.id; ok = true; }
  } catch (e) { data = null; }
  if (!ok) { toast((data && data.error) || "安装启动失败", true); return; }
  renderInstall();
  startPoll(id);
};

function openSetup() {
  S.probe = null; S.checkErr = ""; S.errs = {};
  return api("/local/setup").then(st => {
    if (st && st.job && st.job.state === "running") { startPoll(st.job.id); return; }
    if (st && st.mode === "normal") { location.reload(); return; }
    renderWelcome();
  }).catch(() => renderWelcome());
}
window.openSetup = openSetup;
})();
