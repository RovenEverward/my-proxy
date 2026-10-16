// 添加节点向导:第 1 步(连接)和第 2 步(入站),第 3 步由后续实现。
"use strict";
(() => {
Object.assign(ICONS, {
  eye: '<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>',
  eyeoff: '<path d="M3 3l18 18M10.6 5.1A10 10 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.2 4M6.6 6.6A17 17 0 0 0 2 12s3.5 7 10 7a10 10 0 0 0 4.4-1"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/>',
  back: '<path d="M15 6l-6 6 6 6"/>',
});
const X = ic("x"), AN = ["连接", "入站", "安装", "完成"];
// 向导状态。pw 只在内存里,绝不出现在等价命令或请求体之外。
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
function cmd1() {
  let s = `mpx node add ${shq(W.name || "<名称>")} --address ${shq(W.address || "<地址>")}`;
  if (W.sshPort && String(W.sshPort) !== "22") s += ` --ssh-port ${W.sshPort}`;
  if (W.sshUser && W.sshUser !== "root") s += ` --ssh-user ${W.sshUser}`;
  return s + " --ssh-password";
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
    ${field("SSH 密码", pwBox, "机房给的初始密码即可", e.pw)}
    <div class="field mt"><label>等价命令</label><div class="cmd" id="w-cmd">${cmd1()}</div></div>
    <p class="sub">等价命令里不写密码，终端会询问。</p>`,
    H`<button type="button" class="btn" data-act="close">取消</button><button type="button" class="btn pri" id="w-next">下一步</button>`);
  $("#w-pw").value = W.pw || "";
  const bind = (sel, key) => {
    const el = $(sel);
    el.addEventListener("input", () => {
      W[key] = el.value;
      const c = $("#w-cmd");
      if (c) c.textContent = cmd1();
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
function cmd2() {
  return `mpx inbound add --node ${shq(W.name)} --protocol vless --port ${W.inbPort} --network tcp --security reality --sni ${W.sni} --reality-dest ${W.dest} --flow xtls-rprx-vision`;
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
    <p class="lead">给这台节点建一个入站。默认值和 <code>mpx bootstrap</code> 一致。</p>
    <div class="opts">
      <div class="opt ${W.preset === "reality" ? "on" : ""}" id="w-opt-reality"><span class="rd"></span><div><b>REALITY · 443（推荐）</b><span class="h">VLESS + TCP + REALITY，SNI 和回落目标 www.cloudflare.com</span></div></div>
      <div class="opt ${W.preset === "custom" ? "on" : ""}" id="w-opt-custom"><span class="rd"></span><div><b>自定义</b><span class="h">在下面的高级里改端口、SNI 和回落目标</span></div></div>
    </div>
    <details class="adv" id="w-adv" ${W.preset === "custom" ? raw("open") : ""}><summary>高级</summary>
      <div class="two">${field("端口", H`<input id="w-inb-port" class="in mono ${e.inbPort ? "bad" : ""}" value="${W.inbPort}" placeholder="443" inputmode="numeric" autocomplete="off">`, "", e.inbPort)}
      ${field("SNI", H`<input id="w-sni" class="in mono ${e.sni ? "bad" : ""}" value="${W.sni}" placeholder="www.cloudflare.com" autocomplete="off">`, "", e.sni)}</div>
      ${field("回落目标", H`<input id="w-dest" class="in mono ${e.dest ? "bad" : ""}" value="${W.dest}" placeholder="域名:端口" autocomplete="off">`, "", e.dest)}</details>
    <div class="field mt"><label>等价命令</label><div class="cmd" id="w-cmd">${cmd2()}</div></div>`,
    H`<button type="button" class="btn ghost" id="w-back">${ic("back")}上一步</button><span style="flex:1"></span><button type="button" class="btn" data-act="close">取消</button><button type="button" class="btn pri" id="w-start">开始安装</button>`);
  const bind = (sel, key) => {
    const el = $(sel);
    el.addEventListener("input", () => {
      W[key] = el.value;
      const c = $("#w-cmd");
      if (c) c.textContent = cmd2();
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
window.openNodeWizard = () => {
  Object.assign(W, { step: 1, name: "", address: "", sshPort: 22, sshUser: "root", pw: "", showPw: false, preset: "reality", inbPort: 443, sni: "www.cloudflare.com", dest: "www.cloudflare.com:443", errs: {} });
  render1();
};
window.nodeWizardStart = null;
})();
