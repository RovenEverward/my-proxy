// 首次安装向导(向导 B)前 4 屏:欢迎 / 连接 / 检查 / 设置。第 5 屏由后续实现。
// 对外只暴露 window.openSetup;第 5 屏入口读 window.setupStartRun。
"use strict";
(() => {
Object.assign(ICONS, {
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  eyeoff: '<path d="M3 3l18 18M10.6 5.1A10 10 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.2 4M6.6 6.6A17 17 0 0 0 2 12s3.5 7 10 7a10 10 0 0 0 4.4-1"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/>',
  back: '<path d="M15 6l-6 6 6 6"/>',
  srv: '<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>',
});
const X = ic("x");
const BN = ["连接", "检查", "设置", "安装", "完成"];
const S = {
  step: 1, host: "", sshPort: 22, sshUser: "root", auth: "password", pw: "", identity: "",
  pwShown: false, probe: null, checkErr: "", checkAt: 0,
  username: "user1", name: "default", dpw: "", dpw2: "", dpwShown: false,
  addr: "0.0.0.0:8080", subPrefix: "", sni: "www.cloudflare.com", errs: {},
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
const row = (st, t, du) => H`<li class="${st}"><span class="pi">${st === "run" ? raw('<span class="spin"></span>') : st === "done" ? ic("check") : st === "fail" ? X : st === "skip" ? ic("dash") : raw("<i></i>")}</span><span class="t">${t}</span><span class="du">${du || ""}</span></li>`;
function clearErr(el, key) {
  if (!S.errs || !S.errs[key]) return;
  delete S.errs[key];
  el.classList.remove("bad");
  const fe = el.closest(".field")?.querySelector(".fe");
  if (fe) fe.remove();
}
function fmtDur(ms) {
  const s = Math.max(0, ms) / 1000;
  return s < 60 ? s.toFixed(1) + "s" : Math.floor(s / 60) + ":" + String(Math.round(s % 60)).padStart(2, "0");
}
function setup(st, bodyRaw, footRaw) {
  document.querySelector(".setup")?.remove();
  const d = document.createElement("div");
  d.className = "setup";
  d.innerHTML = val(H`<div class="sbrand"><img src="/logo.svg" alt="">mpx · 首次安装${META?.version ? H`<small>${META.version}</small>` : ""}</div><div class="scard">${st ? steps(st) : ""}<div class="db">${bodyRaw}</div><div class="df">${footRaw}</div></div>`);
  document.body.append(d);
}
function updateCmd(fn) { const c = $("#s-cmd"); if (c) c.textContent = fn(); }
function bindInput(sel, key, fn) {
  const el = $(sel);
  if (!el) return;
  el.addEventListener("input", () => { S[key] = el.value; updateCmd(fn); clearErr(el, key); });
}

/* ---------- 屏 1:欢迎 ---------- */
function renderWelcome() {
  S.step = 1;
  setup(null, H`<div class="welcome"><div class="ill"><img src="/logo.svg" alt=""></div><h2>在你的 VPS 上装好 mpx</h2>
  <p class="lead">和 <code>mpx init</code> 一样：一台机器同时当控制面和第一个节点，大约 5–10 分钟。</p>
  <ul><li>${ic("srv")}<span>装控制面，并在同一台机器上建第一个节点、入站和入口</span></li><li>${ic("users")}<span>建第一个用户，给你一条可以直接导入的订阅链接</span></li>
  <li>${ic("lock")}<span>设一个设备密码，以后别的电脑用它接入</span></li><li>${ic("term")}<span>这台电脑自动接入，装完直接进管理界面</span></li></ul>
  <p class="sub">需要：一台 Linux VPS 的地址和 root 密码（或这台电脑已能用密钥登录）。</p></div>`,
  H`<span class="sub" style="flex:1">已经有控制面？在终端运行 <code>mpx connect</code></span>${btn("开始", "pri", "", "s-start")}`);
  $("#s-start").addEventListener("click", () => renderConnect());
}

/* ---------- 屏 2:连接 ---------- */
function cmdHost() {
  let s = `mpx init --host ${shq(S.host || "<地址>")}`;
  if (String(S.sshPort) !== "22") s += ` --ssh-port ${S.sshPort}`;
  if (S.sshUser !== "root") s += ` --ssh-user ${shq(S.sshUser)}`;
  if (S.auth === "password") s += " --ssh-password";
  else if (S.identity) s += ` --identity ${shq(S.identity)}`;
  return s;
}
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
  <details class="adv"><summary>高级</summary>${field("SSH 用户", H`<input id="s-user" class="in mono" autocomplete="off">`, "固定 root：mpx init 要求 uid=0")}</details>
  <div class="field mt"><label>等价命令</label><div class="cmd" id="s-cmd">${cmdHost()}</div></div>
  <p class="sub">等价命令里不写密码，终端会询问。</p>`,
  H`${btn("上一步", "ghost", "back", "s-back")}<span class="sp"></span>${btn("检查机器", "pri", "", "s-check")}`);
  $("#s-host").value = S.host; $("#s-port").value = S.sshPort; $("#s-user").value = S.sshUser;
  if (key) $("#s-identity").value = S.identity; else $("#s-pw").value = S.pw;
  bindInput("#s-host", "host", cmdHost); bindInput("#s-port", "sshPort", cmdHost);
  bindInput("#s-user", "sshUser", cmdHost);
  if (key) bindInput("#s-identity", "identity", cmdHost);
  else {
    bindInput("#s-pw", "pw", cmdHost);
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
  setup(["done", "done", "", "", ""], H`<h2>检查机器</h2><p class="lead">登录一次、装上本机公钥，再看看这台机器能不能装控制面。</p>
  <ol class="plist">${row("done", first, dur)}${row("done", "检查系统、架构、权限和下载工具", dur)}</ol>
  <div class="probe">${pr("系统", p.os || "未知", osBad)}${pr("架构", p.arch || "未知")}${pr("用户", uidBad ? `uid=${p.uid}` : "root", uidBad)}${pr("下载", dl || "无", dlBad)}</div>
  ${allOk ? H`<div class="callout stone mb">${ic("check")}<div><b>可以安装。</b>本机公钥已装上，后面的步骤不再需要密码。</div></div>`
    : H`<div class="callout bad mb">${ic("warn", "ico-bad")}<div><b>${title}</b><div class="mt2">${reasons.join(" ")}</div></div></div>`}`,
  allOk ? H`${btn("上一步", "ghost", "back", "s-back")}<span class="sp"></span>${btn("下一步", "pri", "", "s-next")}`
    : H`${btn("返回修改", "pri", "back", "s-back2")}`);
  const back = $("#s-back") || $("#s-back2");
  back.addEventListener("click", backToConnect);
  if (allOk) $("#s-next").addEventListener("click", renderSettings);
}

/* ---------- 屏 4:设置 ---------- */
function cmdSetup() {
  let s = cmdHost() + ` --username ${shq(S.username || "<用户>")} --name ${shq(S.name || "<节点名>")}`;
  if (String(S.addr || "") !== "0.0.0.0:8080") s += ` --addr ${shq(S.addr)}`;
  if (S.subPrefix) s += ` --sub-prefix ${shq(S.subPrefix)}`;
  if (String(S.sni || "") !== "www.cloudflare.com") s += ` --sni ${shq(S.sni)}`;
  return s;
}
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
  const name = String(S.name || "").trim();
  if (!name) e.name = "请填写节点名";
  else if (/^\d+$/.test(name)) e.name = "节点名不能是纯数字";
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
  ${field("节点名", H`<input id="s-name" class="in mono ${e.name ? "bad" : ""}" autocomplete="off">`, "建议用地区名", e.name)}</div>
  <div class="two"><div class="field"><label>设备密码</label>${pwBox("s-dpw", S.dpwShown)}<div class="meter" id="s-meter">${raw(meterHtml(S.dpw))}</div><span class="h" id="s-str">强度：${SW[strength(S.dpw)]}</span>${e.dpw ? H`<span class="fe">${e.dpw}</span>` : ""}</div>
  <div class="field"><label>再输一次</label>${pwBox("s-dpw2", S.dpwShown)}${e.dpw2 ? H`<span class="fe">${e.dpw2}</span>` : ""}</div></div>
  <p class="sub" style="margin-top:-4px">别的电脑运行 <code>mpx connect</code> 时要用它。不会保存在这台电脑之外。</p>
  <details class="adv"><summary>高级</summary>
   <div class="two">${field("监听地址", H`<input id="s-addr" class="in mono ${e.addr ? "bad" : ""}" autocomplete="off">`, "", e.addr)}${field("订阅前缀", H`<input id="s-sub" class="in mono" autocomplete="off">`)}</div>${field("SNI", H`<input id="s-sni" class="in mono" autocomplete="off">`, "第一个入站用的 REALITY SNI")}</details>
  <div class="field mt"><label>等价命令</label><div class="cmd" id="s-cmd">${cmdSetup()}</div></div>
  <p class="sub">等价命令里不写密码，终端会询问。</p>`,
  H`${btn("上一步", "ghost", "back", "s-back")}<span class="sp"></span>${btn("开始安装", "pri", "", "s-run")}`);
  $("#s-username").value = S.username; $("#s-name").value = S.name;
  $("#s-dpw").value = S.dpw; $("#s-dpw2").value = S.dpw2;
  $("#s-addr").value = S.addr; $("#s-sub").value = S.subPrefix; $("#s-sni").value = S.sni;
  bindInput("#s-username", "username", cmdSetup); bindInput("#s-name", "name", cmdSetup);
  bindInput("#s-addr", "addr", cmdSetup); bindInput("#s-sub", "subPrefix", cmdSetup); bindInput("#s-sni", "sni", cmdSetup);
  $("#s-dpw").addEventListener("input", () => {
    S.dpw = $("#s-dpw").value;
    const m = $("#s-meter"); if (m) m.innerHTML = meterHtml(S.dpw);
    const t = $("#s-str"); if (t) t.textContent = "强度：" + SW[strength(S.dpw)];
    clearErr($("#s-dpw"), "dpw"); updateCmd(cmdSetup);
  });
  bindInput("#s-dpw2", "dpw2", cmdSetup);
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
      window.setupStartRun({ host: S.host, ssh_port: +S.sshPort || 22, ssh_user: S.sshUser || "root", identity: S.identity, name: S.name, username: S.username, device_password: S.dpw, addr: S.addr, sub_prefix: S.subPrefix, sni: S.sni });
    } else toast("安装步骤尚未实现", true);
  });
}

function openSetup() {
  S.probe = null; S.checkErr = ""; S.errs = {};
  renderWelcome();
}
window.openSetup = openSetup;
})();
