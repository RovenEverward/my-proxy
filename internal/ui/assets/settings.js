// 「订阅」「系统」两页(原设置页拆分):通用(先读后写、整体回写)、UA 识别规则草稿、
// 客户端模板(get / set / del)、设备(list / revoke)、备份下载。PUT /api/settings 是整体替换:每次写都先读回
// 最新一份,只改这次动的地方,与 mpx settings 各子命令一样。
"use strict";

const TPL_FORMATS = ["clash-meta", "sing-box", "xray"];
// asModelSettings:摆成 model.Settings 的 JSON 形状(nil 的 rules / templates 是 null)。
const asModelSettings = v => ({ sub_url_prefix: v.sub_url_prefix || "", profile_title: v.profile_title || "", support_link: v.support_link || "", update_interval: v.update_interval || 0,
  webhook_url: v.webhook_url || "", notify_days_left: v.notify_days_left || 0, notify_usage_percent: v.notify_usage_percent || 0, rules: v.rules ?? null, templates: v.templates ?? null });
async function modifySettings(change) {
  const s = asModelSettings(await api("/api/settings"));
  change(s);
  return write("PUT", "/api/settings", s);
}
const GENERAL = [["sub_url_prefix", "订阅地址前缀", "s", "发给用户的链接以它开头，前面通常是 TLS 反代"], ["profile_title", "订阅标题", "s", "客户端里显示的订阅名，支持 {USERNAME} 等变量"], ["update_interval", "客户端刷新间隔(小时)", "i", ""],
  ["support_link", "支持链接", "s", ""], ["webhook_url", "Webhook", "s", "到期、超限等通知发到这里;留空不发"], ["notify_days_left", "到期前提醒(天)", "i", "0 表示关闭"], ["notify_usage_percent", "用量提醒(%)", "i", "0 表示关闭"]];
// 原「设置」拆成两页(都还是同一份 settings,保存时先读回最新一份、只改自己那几项):
// 「订阅」= 地址与显示 · 客户端识别 · 订阅底稿(高级);「系统」= 通知 · 设备与接入 · 备份与恢复 · 更新与版本。
const SYS_TABS = [["", "通知"], ["devices", "设备与接入"], ["backup", "备份与恢复"], ["update", "更新与版本"]];
const NOTIFY_FIELDS = ["webhook_url", "notify_days_left", "notify_usage_percent"];
const tabsOf = (page, tabs, t) => H`<div class="tabs">${tabs.map(([k, l]) => H`<a class="${t === k ? "on" : ""}" href="#${page}${k ? "/" + k : ""}">${l}${k === "templates" ? H`<small class="tab-adv">高级</small>` : ""}</a>`)}</div>`;
const tabKey = (tabs, r) => tabs.some(x => x[0] === (r[1] || "")) ? (r[1] || "") : "";

function settingsField(s, W, [k, l, ty, h]) {
  return H`<div class="field"><label for="s-${k}">${l}</label>${k === "webhook_url" ? H`<div class="flex8"><input class="in mono" id="s-${k}" name="${k}" value="${s[k] || ""}" ${W ? "" : raw("readonly")}>${can("POST", "/api/settings/test-webhook") ? H`<button type="button" class="btn" data-act="set-test">发测试</button>` : ""}</div>`
    : H`<input class="in ${ty === "i" || k === "sub_url_prefix" ? "mono" : ""}" id="s-${k}" name="${k}" value="${ty === "i" ? (s[k] || 0) : (s[k] || "")}" ${ty === "i" ? raw('inputmode="numeric"') : ""} ${W ? "" : raw("readonly")}>`}${h ? H`<div class="sub hint">${h}</div>` : ""}</div>`;
}
function settingsForm(s, W, keys, inner) {
  return H`<form class="card pad18" id="set-form" novalidate>${inner}
    <div class="err" id="s-err" role="alert"></div>
    ${W ? H`<div class="flex8 end"><button type="button" class="btn" data-act="set-revert">还原</button><button type="submit" class="btn pri" id="s-save">保存</button></div>` : ""}</form>`;
}

PAGES.system = {
  title: "系统",
  crumb: r => H`<a href="#system">系统</a><i>/</i><b>${(SYS_TABS.find(t => t[0] === tabKey(SYS_TABS, r)) || SYS_TABS[0])[1]}</b>`,
  async load(r) {
    const t = tabKey(SYS_TABS, r);
    if (t === "update") return {};
    const settings = await api("/api/settings");
    if (t === "devices") return { settings, devices: await api("/api/devices") };
    return { settings };
  },
  render(d, r) {
    const t = tabKey(SYS_TABS, r);
    const head = H`<div class="ph"><div><h1>系统</h1><p>通知、设备、备份与版本</p></div></div>${tabsOf("system", SYS_TABS, t)}`;
    if (t === "update") return H`${head}<div class="card pad18"><b class="b6">当前版本</b><p class="sub mt6 mb14 mono">${META.cli_version || "—"}</p><button type="button" class="btn pri" data-act="update-open">${ic("refresh")}检查更新</button></div>`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(6, 2)}</tbody></table></div>`;
    const s = d.settings, W = can("PUT", "/api/settings");
    if (t === "") {
      const by = Object.fromEntries(GENERAL.map(g => [g[0], g])), f = k => settingsField(s, W, by[k]);
      return H`${head}<div class="grid g2">${settingsForm(s, W, NOTIFY_FIELDS, H`${f("webhook_url")}<div class="two">${f("notify_days_left")}${f("notify_usage_percent")}</div>`)}
        <div class="card pad16 self-start"><div class="sub mb8">会通知什么</div><p class="sub">用户快到期、用量快用完时，往 Webhook 发一条消息。两个提醒填 0 就不发。</p></div></div>`;
    }
    if (t === "devices") return devicesTab(head, d);
    return backupTab(head);
  },
  after(d, r) { settingsAfter(d, r); },
};

function devicesTab(head, d) {
      const D = d.devices || [], X = can("DELETE", "/api/devices");
      const PW = can("PUT", "/api/device-password"), PWOFF = can("DELETE", "/api/device-password");
      return H`${head}<div class="card"><div class="ch">接入的设备<small>· 共 ${D.length} 台</small></div>
        ${D.length ? D.map(x => H`<div class="row"><span class="sub mono w18">${x.id}</span><b class="ell grow">${x.name}</b>${x.current ? H`<span class="pill on">这台</span>` : ""}
          <span class="sub">${x.last_used_at ? "上次 " + sinceText(x.last_used_at) : "未用过"}${x.last_ip ? " · " + x.last_ip : ""}</span>
          ${X && !x.current ? H`<button class="btn sm" data-act="dev-revoke" data-id="${x.id}" data-name="${x.name}">吊销</button>` : ""}</div>`) : H`<div class="row sub">还没有设备接入。在另一台设备的 MPX 里选「接入已有控制面」即可。</div>`}
        <div class="row sub">「这台」就是当前正在用的设备，界面里不让吊销它，免得把自己踢下线;要吊销它，请换一台已接入的设备操作。</div></div>
        ${PW || PWOFF ? devicePasswordCard(PW, PWOFF) : ""}`;
    }

function backupTab(head) {
    return H`${head}<div class="grid g2"><div class="card pad18"><b class="b6">下载备份</b><p class="sub mt6 mb14">一份数据库快照加控制面密钥，打包成一个归档。里面有全部用户的订阅 token，妥善保管。</p><button class="btn pri" data-act="backup">${ic("dl")}下载备份</button></div>
      <div class="card pad18"><b class="b6">从备份恢复</b><p class="sub mt6 mb14">从备份恢复暂不支持在窗口里操作，见文档「备份控制面」。恢复会整库替换并重启控制面，务必先确认备份文件。</p></div></div>`;
}

function settingsAfter(d, r) {
    if (!d) return;
    const dpw = $("#dpw-form");
    if (dpw) {
      dpw.addEventListener("submit", async e => {
        e.preventDefault();
        const pw = $("#dpw1").value, again = $("#dpw2").value, revoke = $("#dpw-revoke").checked;
        const err = [];
        if ([...pw].length < 12) err.push("设备密码至少 12 个字符");
        if (pw !== again) err.push("两次输入不一致");
        $("#dpw-err").textContent = err.join("\n");
        if (err.length) return;
        const btn = $("#dpw-save"); btn.disabled = true; btn.classList.add("busy");
        try {
          const res = await api("/api/device-password", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ password: pw, revoke_all: revoke }) });
          toast(revoke ? `设备密码已更新，吊销了 ${(res && res.revoked) || 0} 台设备(这台也要重新接入)` : "设备密码已更新;已接入的设备不受影响");
          $("#dpw1").value = ""; $("#dpw2").value = ""; $("#dpw-revoke").checked = false;
          afterWrite();
        } catch (e2) { $("#dpw-err").textContent = e2.message; }
        btn.disabled = false; btn.classList.remove("busy");
      });
    }
    const form = $("#set-form");
    if (!form) return;
    form.addEventListener("submit", async e => {
      e.preventDefault();
      const fd = new FormData(form), err = [], vals = {};
      // 两页各自只有自己那几项:表单里没有的字段不碰(由 modifySettings 从最新一份原样带回)。
      for (const [k, l, ty] of GENERAL) {
        if (!form.elements[k]) continue;
        const x = String(fd.get(k) ?? "").trim();
        if (ty === "i") { if (x && !/^\d+$/.test(x)) err.push(`${l}要是非负整数`); vals[k] = x ? +x : 0; } else vals[k] = x;
      }
      $("#s-err").textContent = err.join("\n");
      if (err.length) return;
      // 只改动过的字段:和打开页面时比,别把别人刚改的值冲回去。
      const changed = Object.keys(vals).filter(k => vals[k] !== (d.settings[k] ?? (typeof vals[k] === "number" ? 0 : "")));
      if (!changed.length) { toast("没有改动"); return; }
      const btn = $("#s-save"); btn.disabled = true; btn.classList.add("busy");
      try { await modifySettings(s => { for (const k of changed) s[k] = vals[k]; }); toast(form.elements.webhook_url ? "已保存通知设置" : "已保存订阅设置"); await load(true, true); }
      catch (e2) { $("#s-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
    });
}

/* ---- 设备密码 ---- */
// 设备密码一节:新设备接入时输入它。改密码不影响已接入的设备;勾上吊销
// 会把所有设备(包括这台)踢下线;关闭则不再受理新设备接入。
function devicePasswordCard(PW, PWOFF) {
  return H`<form class="card pad18 mt" id="dpw-form" novalidate>
    <b class="b6">设备密码</b><p class="sub mt6 mb14">在另一台设备的 MPX 里选「接入已有控制面」时输入它。改密码不影响已接入的设备。</p>
    <div class="two">
      <div class="field"><label for="dpw1">新密码</label><input class="in" id="dpw1" type="password" autocomplete="new-password" ${PW ? "" : raw("readonly")}><span class="h">至少 12 个字符</span></div>
      <div class="field"><label for="dpw2">再输一次</label><input class="in" id="dpw2" type="password" autocomplete="new-password" ${PW ? "" : raw("readonly")}></div>
    </div>
    <label class="chk"><input type="checkbox" id="dpw-revoke" ${PW ? "" : raw("disabled")}> 同时吊销所有已接入设备(包括这台，之后要重新接入)</label>
    <div class="err mt8" id="dpw-err" role="alert"></div>
    <div class="flex8 end mt">
      ${PWOFF ? H`<button type="button" class="btn" data-act="dpw-off">关闭设备密码接入</button>` : ""}
      ${PW ? H`<button type="submit" class="btn pri" id="dpw-save">保存设备密码</button>` : ""}
    </div>
    <p class="sub mt8">关闭后新设备不能再接入，已接入的设备不受影响;要再开，在这里设一个新密码。</p></form>`;
}

/* ---- 模板 ---- */
// 每种格式的真实含义,出自 internal/render 与 internal/server/templates.go。
const TPL_META = {
  "clash-meta": { lang: "yaml", L: "YAML", ext: ".yaml", inject: "proxies", how: "节点放进 proxies，分流规则、策略组和 DNS 原样带出。必须有顶层 rule-providers 段。" },
  "sing-box": { lang: "json", L: "JSON", ext: ".json", inject: "outbounds", how: "节点放进 outbounds，proxy、auto、direct、block 四个占位换成这次的节点。" },
  "xray": { lang: "json", L: "JSON", ext: ".json", inject: "outbounds", how: "每条连接方式生成一份完整配置，节点是第一条 outbound，其余原样。" },
};
const tplBytes = t => new TextEncoder().encode(t).length;
const tplLines = t => t ? t.split("\n").length - (t.endsWith("\n") ? 1 : 0) : 0;
const tplSize = n => n < 1024 ? `${n} B` : `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;

// jsonErrAt 找出 JSON 第一处语法错误的字符偏移(新版浏览器的 JSON.parse 报错不带位置)。
function jsonErrAt(t) {
  let i = 0;
  const ws = () => { while (/[ \t\n\r]/.test(t[i] || "")) i++; };
  const fail = () => { throw i; };
  const str = () => { i++; while (i < t.length && t[i] !== '"') { if (t[i] === "\n") fail(); i += t[i] === "\\" ? 2 : 1; } if (t[i] !== '"') fail(); i++; };
  const v = () => {
    ws(); const c = t[i];
    if (c === "{") { i++; ws(); if (t[i] === "}") { i++; return; } for (;;) { ws(); if (t[i] !== '"') fail(); str(); ws(); if (t[i] !== ":") fail(); i++; v(); ws(); if (t[i] === ",") { i++; continue; } if (t[i] === "}") { i++; return; } fail(); } }
    if (c === "[") { i++; ws(); if (t[i] === "]") { i++; return; } for (;;) { v(); ws(); if (t[i] === ",") { i++; continue; } if (t[i] === "]") { i++; return; } fail(); } }
    if (c === '"') return str();
    const m = /^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/.exec(t.slice(i, i + 64)); if (!m) fail(); i += m[0].length;
  };
  try { v(); ws(); return i < t.length ? i : null; } catch (at) { return at; }
}

// validateTpl 在浏览器里先做一遍与服务端 validateTemplate 相同的检查,出错时尽量给出行号。
// 服务端仍是最终裁决:YAML 这里只查得出常见错误,完整解析在服务端。
function validateTpl(f, text) {
  if (!text.trim()) return { msg: "模板不能是空的" };
  if (TPL_META[f].lang === "json") {
    try { JSON.parse(text); return null; } catch (e) {
      let line, col; const lc = /line (\d+) column (\d+)/.exec(e.message), pos = /position (\d+)/.exec(e.message);
      const at = lc ? null : pos ? +pos[1] : jsonErrAt(text);
      if (lc) { line = +lc[1]; col = +lc[2]; } else if (at != null) { const pre = text.slice(0, at).split("\n"); line = pre.length; col = pre[pre.length - 1].length + 1; }
      return { line, msg: `不是合法的 JSON${line ? `：第 ${line} 行第 ${col} 列附近` : ""}(${e.message.replace(/^JSON\.parse: /, "")})` };
    }
  }
  const L = text.split("\n");
  const tab = L.findIndex(l => /^\s*\t/.test(l)); if (tab >= 0) return { line: tab + 1, msg: `第 ${tab + 1} 行用 Tab 缩进了，YAML 只能用空格` };
  const first = L.findIndex(l => l.trim() && !/^\s*#/.test(l) && l.trim() !== "---");
  if (first >= 0 && !/^[^\s#-][^:]*:(\s|$)/.test(L[first])) return { line: first + 1, msg: "顶层必须是映射(key: value)，不能是列表或纯文本" };
  if (!L.some(l => /^(rule-providers|"rule-providers"|'rule-providers')\s*:/.test(l))) return { msg: "缺顶层 rule-providers 段(proxies 的插入点)，服务端会拒收" };
  return null;
}

async function templateDrawer(f) {
  let text = "";
  try { text = await api("/api/settings/templates/" + f, { asText: true, headers: { Accept: "text/plain" } }); } catch (e) { toast(e.message, true); return; }
  const m = TPL_META[f], W = can("PUT", "/api/settings/templates/x"), c = !!(current.data?.settings?.templates || {})[f], orig = text;
  openDrawer(H`<form id="tpl-form" novalidate><div class="dh"><span class="fi">${ic("tpl")}</span><div style="min-width:0"><h2><span class="mono">${f}</span> 订阅底稿 <span class="pill ${c ? "on" : ""}">${c ? "自定义" : "内置默认"}</span></h2><div class="sub ell">${m.how}</div></div><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db tpl-db">
    ${c ? "" : H`<div class="callout stone mb8 tpl-note"><div>这是内置默认的副本。${W ? "改动后保存，就成为 " : ""}${W ? H`<b>${f}</b> 的自定义模板，对所有用户生效。` : ""}</div></div>`}
    <div id="tpl-ed"></div>
    <div class="ed-s"><span class="tag">${m.L}</span><span id="tpl-pos">行 1，列 1</span><span id="tpl-size"></span><span id="tpl-dirty" class="c-warn"></span><span class="sp"></span><span id="tpl-ok"></span></div>
    <div class="callout bad mt8 hidden" id="tpl-err" role="alert"></div></div>
  <div class="df">${c && can("DELETE", "/api/settings/templates/x") ? H`<button type="button" class="btn ghost sm" data-act="tpl-del" data-f="${f}">恢复内置</button>` : ""}${W ? H`<label class="btn sm ghost">${ic("up")}从文件读…<input type="file" id="tpl-file" accept="${m.ext},.txt,.yml" hidden></label>${m.lang === "json" ? H`<button type="button" class="btn sm ghost" id="tpl-fmt">格式化</button>` : ""}<button type="button" class="btn sm ghost" id="tpl-revert" disabled>撤销改动</button>` : ""}<span class="sp" style="flex:1"></span><button type="button" class="btn" data-act="close">关闭</button>${W ? H`<button type="submit" class="btn pri" id="tpl-save" disabled>保存</button>` : ""}</div></form>`, "wide");
  const err = $("#tpl-err"), save = $("#tpl-save");
  const showErr = (r, server, jump = true) => { err.classList.toggle("hidden", !r); err.innerHTML = r ? val(H`${ic("warn")}<div>${server ? H`<b>服务端拒收：</b>` : ""}${r.msg}${r.line ? H` <button type="button" class="lnk" id="tpl-goto">跳到第 ${r.line} 行</button>` : ""}</div>`) : ""; if (r?.line) { ed.mark(r.line, jump); $("#tpl-goto").onclick = () => { ed.mark(r.line); ed.focus(); }; } };
  let t = 0;
  const status = kind => {
    const [ln, col] = ed.cursor(); $("#tpl-pos").textContent = `行 ${ln}，列 ${col}`;
    if (kind === "cursor") return;
    const v = ed.value, dirty = v !== orig;
    $("#tpl-size").textContent = `${tplLines(v)} 行 · ${tplSize(tplBytes(v))}`;
    $("#tpl-dirty").textContent = dirty ? "● 未保存" : "";
    if ($("#tpl-revert")) $("#tpl-revert").disabled = !dirty;
    clearTimeout(t); t = setTimeout(() => { const r = validateTpl(f, ed.value); $("#tpl-ok").innerHTML = val(r ? H`<span class="c-bad">${ic("warn", "sm")}有错误</span>` : H`<span class="c-ink">${ic("check", "sm")}语法正确</span>`); if (save) save.disabled = !dirty || !!r; showErr(r, false, false); }, 400);
  };
  const ed = codeEditor($("#tpl-ed"), { text, lang: m.lang, readOnly: !W, onChange: status });
  status();
  $("#tpl-file")?.addEventListener("change", async e => { const file = e.target.files[0]; if (file) { ed.value = await file.text(); showErr(validateTpl(f, ed.value)); } e.target.value = ""; });
  $("#tpl-fmt")?.addEventListener("click", () => { try { ed.value = JSON.stringify(JSON.parse(ed.value), null, 2) + "\n"; } catch { showErr(validateTpl(f, ed.value)); } });
  $("#tpl-revert")?.addEventListener("click", () => { ed.value = orig; showErr(null); });
  $("#tpl-form").addEventListener("submit", async e => {
    e.preventDefault();
    const r = validateTpl(f, ed.value); if (r) { showErr(r); return; }
    save.disabled = true; save.classList.add("busy");
    try {
      // 与 CLI 一样原样发正文,不做 JSON 序列化。
      await api("/api/settings/templates/" + f, { method: "PUT", body: ed.value, headers: { "Content-Type": "text/plain; charset=utf-8" } });
      toast(`已保存 ${f} 订阅底稿`); closeOverlays(); load(true, true);
    } catch (e2) { const l = /line (\d+)/.exec(e2.message); showErr({ msg: e2.message, line: l ? +l[1] : 0 }, true); save.disabled = false; save.classList.remove("busy"); }
  });
}

Object.assign(ACTS, {
  "set-revert": () => load(true, true),
  "set-test": (d, b) => busy(b, async () => { await write("POST", "/api/settings/test-webhook", undefined, "测试通知已送到"); }),
  "dev-revoke": d => confirmBox({ title: `吊销设备 ${d.name}？`, text: "它的 token 立即失效，要再用得在那台机器上重新接入。", ok: "吊销", danger: true,
    run: async () => { await write("DELETE", "/api/devices/" + d.id); toast(`已吊销设备 ${d.name}`); await load(true, true); } }),
  "dpw-off": () => confirmBox({ title: "关闭设备密码接入？", text: "之后新设备不能用设备密码接入;已接入的设备不受影响。要再开，在这里设一个新密码即可。", ok: "关闭设备密码接入", danger: true,
    run: async () => { await write("DELETE", "/api/device-password", undefined, "已关闭设备密码接入;已接入的设备不受影响"); } }),
  // 备份经 fetch 取回再存盘:直接点链接遇上限流(429)会把错误 JSON 当文件存下来。
  "backup": (d, b) => busy(b, async () => {
    let res;
    for (let i = 0; ; i++) {
      res = await fetch("/api/backup", { credentials: "same-origin" });
      if (res.status !== 429 || i >= 3) break;
      await sleep(2000);
    }
    if (!res.ok) { let m = res.statusText; try { m = (await res.json()).error || m; } catch {} throw new Error(m); }
    const name = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") || "")?.[1] || "mpx-backup.tar.gz";
    const url = URL.createObjectURL(await res.blob()), a = document.createElement("a");
    a.href = url; a.download = name; document.body.append(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 10000);
    toast("已开始下载 " + name);
  }),
  "tpl-open": d => templateDrawer(d.f),
  "tpl-del": d => confirmBox({ title: `恢复 ${d.f} 为内置订阅底稿？`, text: `会删除你存的 ${d.f} 自定义模板(不可撤销，控制面不留历史);之后所有用户的 ${d.f} 订阅改用内置默认。想留底，先打开模板把内容复制出来。`,
    ok: "删除自定义底稿", danger: true, typed: d.f,
    run: async () => { await write("DELETE", "/api/settings/templates/" + d.f, undefined, `已恢复 ${d.f} 为内置订阅底稿`); closeOverlays(); await load(true, true); } }),
});

const PV_CLIENTS = [["Clash Verge", "clash-verge/v2.0.3"], ["Shadowrocket", "Shadowrocket/2070 CFNetwork"], ["sing-box", "SFI/1.11.4"]];

// 格式的说法:名字按对象叫,后半句说用户会拿到什么。
const FMT = {
  "clash-meta": ["Clash Meta 配置", "带完整分流和策略组"], clash: ["Clash 配置", "精简版，只有节点和基本规则"],
  "sing-box": ["sing-box 配置", "带完整分流"], xray: ["Xray 配置", "每个节点一份完整配置"],
  "base64-links": ["节点链接 · Base64", "只有节点，没有分流"], links: ["节点链接", "每行一条，只有节点"], block: ["不给订阅", "返回 404"],
};
const FMT_ORDER = ["clash-meta", "clash", "sing-box", "xray", "base64-links", "links", "block"];
const toRe = p => {
  const flags = /^\(\?([ims]+)\)/.exec(p);
  try { return new RegExp(flags ? p.slice(flags[0].length) : p, flags ? [...new Set(flags[1])].join("") : ""); } catch { return null; }
};
// 浏览器只能辅助预览，Go regexp 由控制面最终检查。
const hit = (rules, ua) => { if (!ua) return -1; for (let i = 0; i < rules.length; i++) { const r = toRe(rules[i].pattern); if (r && r.test(ua)) return i; } return rules.length; };
const sameRules = (a, b) => JSON.stringify((a || []).map(({ pattern, result }) => ({ pattern, result }))) === JSON.stringify((b || []).map(({ pattern, result }) => ({ pattern, result })));
const clone = v => JSON.parse(JSON.stringify(v));

let SUB = null;          // { orig, draft, err, saving, pv, leaveTarget, preview }
let RD = null;           // 规则抽屉里的草稿 { rules, ua }
let ADV_OPEN = false;
// 应用内对话框自己收:不碰抽屉,和 modal-cancel 行为一致。
const subCloseModal = () => closeModal();
const subLeave = target => { if (typeof target === "function") target(); else if (target) location.hash = target; else paint(); };

const FIELDS = [["sub_url_prefix", "链接开头"], ["profile_title", "订阅名"], ["update_interval", "刷新间隔"], ["support_link", "帮助链接"]];
function changes() {
  if (!SUB) return [];
  const out = FIELDS.filter(([k]) => String(SUB.draft[k] ?? "") !== String(SUB.orig[k] ?? "")).map(x => x[1]);
  if (!sameRules(SUB.draft.rules, SUB.orig.rules)) out.push("识别规则");
  return out;
}
const ipBase = () => META.server || location.origin;
// 没填链接开头时的开头由控制面定:它按请求的 Host 拼,经 mpx ui 转发过来的就是控制面的地址,
// 不一定是页面地址。所以用控制面这次给的链接自带的开头(PV.appliedPrefix),没有才退回页面地址。
const subBase = d => {
  if (d.sub_url_prefix) return d.sub_url_prefix;
  const ap = SUB && SUB.preview && SUB.preview.appliedPrefix;
  return ap || ipBase();
};

/* ---- 客户端目录:按名字列 app,每个带一条典型标识(User-Agent),用当前规则算它拿到什么 ---- */
const B5_APPS = [
  ["Clash Verge", "clash-verge/v2.0.3"], ["Mihomo Party", "mihomo.party/v1.5.12 (clash.meta)"], ["FlClash", "FlClash/v0.8.80 clash.meta"],
  ["Stash", "Stash/2.7.0 Clash/1.11.0"], ["Karing", "karing/1.1.2"],
  ["sing-box", "SFI/1.11.4 (sing-box 1.11.4)"], ["SFA", "SFA/1.11.4 (sing-box 1.11.4; Android)"], ["Hiddify", "HiddifyNext/2.5.7 (sing-box)"],
  ["Shadowrocket", "Shadowrocket/2070 CFNetwork/1494"], ["v2rayN", "v2rayN/7.4.2"], ["v2rayNG", "v2rayNG/1.9.30"],
  ["Quantumult X", "Quantumult%20X/1.4.2"], ["Surge", "Surge iOS/3021"], ["Loon", "Loon/3.2.4"],
];
const B5_OTHER_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15";
const b5Rules = d => d.rules && d.rules.length ? d.rules : SUB.builtin;
const b5Fmt = (R, ua) => { const i = hit(R, ua); return i < R.length ? R[i].result : "links"; };
function b5Groups(R) {
  const g = new Map();
  for (const [n, ua] of B5_APPS) { const f = b5Fmt(R, ua); if (!g.has(f)) g.set(f, []); g.get(f).push(n); }
  return [...g].sort((a, b) => FMT_ORDER.indexOf(a[0]) - FMT_ORDER.indexOf(b[0]));
}

/* ---- 规则的说法:「客户端标识里含 clash」 ---- */
const b5Parse = p => { const ci = /^\(\?i\)/.test(p), body = ci ? p.slice(4) : p; return { ci, body, all: body === ".*", re: !/^[^\\^$()[\]{}*+?.]*$/.test(body.replace(/\\\./g, "")) }; };
const b5Say = p => { const x = b5Parse(p); if (x.all) return "其他所有客户端";
  const parts = x.re ? [x.body] : x.body.split("|").map(s => s.replace(/\\\./g, "."));
  return `${x.re ? "客户端标识符合" : "客户端标识里含"} ${parts.join(" 或 ")}`; };

/* ---- 页头:这页只管一件事 + 预览用户 ---- */
const tipI = t => H`<span class="r-tip" tabindex="0" role="note" aria-label="${t}" data-tip="${t}">${ic("info")}</span>`;
const secHead = (title, extra = "") => H`<div class="r-sh"><h2>${title}</h2>${extra}</div>`;

function b5Picker() {
  const PV = SUB.preview;
  if (!SUB.users.length) return H`<div class="b5-pick" title="还没有用户，下面用示例数据"><span class="b5-pick-l">预览用户</span><b>${PV.user}</b><span class="tag lab">示例</span></div>`;
  return H`<label class="b5-pick"><span class="b5-pick-l">预览用户</span><select class="in" id="pv-user">${SUB.users.map(u => H`<option value="${u.username}" ${u.username === PV.user ? raw("selected") : ""}>${u.username}</option>`)}</select></label>`;
}

/* ---- 第一段:发给用户的链接 ---- */
function b5Link(d) {
  const PV = SUB.preview;
  const kept = d.sub_url_prefix === SUB.orig.sub_url_prefix;
  const link = kept && PV.auto ? PV.auto : subBase(kept && PV.getAuto ? { sub_url_prefix: PV.appliedPrefix || "" } : d).replace(/\/+$/, "") + "/sub/" + PV.token;
  const base = link.slice(0, link.lastIndexOf("/sub/")), tail = link.slice(base.length);
  const can = PV.token && SUB.users.length;
  return H`<div class="r-link"><code class="r-url"><span class="lk-pre">${base}</span><span class="lk-tail">${PV.token ? tail : "/sub/…"}</span></code>
    <button type="button" class="btn ton r-copy" data-act="copy" data-text="${link}" ${can ? "" : raw('disabled title="加了用户才能复制"')}>${ic("copy")}复制链接</button></div>`;
}
function b5PfxNote(d) {
  if (d.sub_url_prefix) return H`<span class="h">客户端要能访问到这个地址。</span>`;
  const host = subBase(d).replace(/^https?:\/\//, "").replace(/:\d+$/, "");
  return H`<span class="h">没填，链接用服务器 IP（${host}）开头，拿到链接的人都能看到。</span>`;
}
function b5LinkCard(d, W) {
  const ro = W && !SUB.saving ? "" : raw("readonly");
  return H`<section class="r-sec b5-sec" id="sec-link">${secHead("订阅链接")}
    <div id="lk">${b5Link(d)}</div>
    <div class="field r-pfx"><label for="f-prefix">链接开头</label>
      <input class="in mono" id="f-prefix" data-k="sub_url_prefix" value="${d.sub_url_prefix}" placeholder="例如 https://sub.example.com" autocomplete="off" spellcheck="false" ${ro}>
      <span class="s5-field-err" id="pfx-err" role="alert">${SUB.fieldErrors?.sub_url_prefix || ""}</span>
      <div id="pfx-slot">${b5PfxNote(d)}</div></div></section>`;
}

/* ---- 第二段:在客户端里的样子 ---- */
let B5_HL = "";
function b5Card(d) {
  const PV = SUB.preview, title = (d.profile_title || "{USERNAME}").replaceAll("{USERNAME}", PV.user);
  const [, ua] = PV_CLIENTS[SUB.pv], f = b5Fmt(b5Rules(d), ua), iv = +d.update_interval || 0;
  return H`<div class="pv-app b5-app r-app" id="pv-app" data-hl="${B5_HL}">
      <div class="pv-prof"><span class="pv-ico">${ic("sub")}</span>
        <div class="grow"><span class="pa" data-p="1"><b class="pv-t">${title}</b></span>
          <div class="pv-m"><span class="nw">${fmtBytes(PV.used)} / ${PV.limit ? fmtBytes(PV.limit) : "不限流量"}</span><span class="nw">${PV.expire ? `${PV.expire} 到期` : "永不过期"}</span></div></div>
        <span class="pa pa-help ${d.support_link ? "" : "off"}" data-p="3"><span class="pv-help" title="${d.support_link}">${ic("help")}</span></span></div>
      <div class="bar pv-bar"><i style="width:${PV.limit ? Math.min(100, PV.used / PV.limit * 100) : 0}%"></i></div>
      <div class="pv-meta"><span class="pa" data-p="2">${ic("refresh")}${iv ? `每 ${iv} 小时更新` : "客户端自己决定多久更新"}</span></div>
      ${f === "block" ? H`<div class="pv-node pv-empty">这个客户端拿不到订阅</div>` : PV.node ? H`<div class="pv-node"><b>${PV.node}</b><span class="tag lab">${PV.proto}</span><span class="sp"></span>${PV.more > 0 ? H`<span class="sub">还有 ${PV.more} 个</span>` : ""}</div>` : ""}
    </div>`;
}
// 订阅名编辑框:「用户名」是一块整体的标签,存的时候换回 {USERNAME}。
const b5TokHTML = s => String(s || "").split("{USERNAME}").map(t => esc(t)).join('<span class="tk" contenteditable="false" data-tok="USERNAME">用户名</span>');
function b5TokVal(el) { let s = ""; for (const n of el.childNodes) s += n.nodeType === 3 ? n.textContent : n.dataset?.tok ? "{USERNAME}" : n.textContent; return s.replace(/\n/g, ""); }
function b5TitleRes(d) {
  const t = d.profile_title || "", other = SUB.users.find(u => u.username !== SUB.preview.user)?.username || "bob";
  const has = t.includes("{USERNAME}"), you = t.replaceAll("{USERNAME}", other);
  if (!t.trim()) return H`<span class="h">不填就只显示用户名。</span>`;
  return has ? H`<span class="h">${other} 看到「${you}」</span>`
    : H`<span class="h b5-sug">每个人看到的都一样。<button type="button" class="tk tk-add" data-act="b5-tok">${ic("plus")}用户名</button></span>`;
}
function b5LookCard(d, W) {
  const ro = W && !SUB.saving ? "" : raw("readonly");
  return H`<section class="r-sec b5-sec" id="sec-look">${secHead("客户端显示")}
    <div class="r-look">
      <div class="r-stage"><div class="seg" role="tablist" aria-label="预览的客户端">${PV_CLIENTS.map(([n], k) => H`<button type="button" class="${k === SUB.pv ? "on" : ""}" data-act="s5-pv" data-i="${k}">${n}</button>`)}</div>
        <div id="pv">${b5Card(d)}</div></div>
      <div class="r-fields">
        <div class="field b5-f" data-p="1"><label for="f-title">订阅名</label>
          <div class="in b5-tok" id="f-title" role="textbox" aria-label="订阅名" contenteditable="${W && !SUB.saving ? "true" : "false"}" spellcheck="false">${raw(b5TokHTML(d.profile_title))}</div>
          <div class="b5-tr-res" id="title-res">${b5TitleRes(d)}</div></div>
        <div class="field b5-f" data-p="2"><label for="f-int">刷新间隔${tipI("客户端每隔多久自动更新一次。填 0 由客户端决定。")}</label>
          <div class="s5-unit"><input class="in mono" id="f-int" data-k="update_interval" value="${d.update_interval}" inputmode="numeric" autocomplete="off" ${ro}><span>小时</span></div>
          <span class="s5-field-err" id="int-err" role="alert">${SUB.fieldErrors?.update_interval || ""}</span></div>
        <div class="field b5-f" data-p="3"><label for="f-help">帮助链接${tipI("客户端里帮助按钮打开的网页。不填就不显示这个按钮。")}</label>
          <input class="in mono" id="f-help" data-k="support_link" value="${d.support_link}" placeholder="例如 https://help.example.com" autocomplete="off" spellcheck="false" ${ro}></div>
      </div></div></section>`;
}

/* ---- 第三段:不同客户端拿到什么 ---- */
function b5Table(d) {
  const R = b5Rules(d), O = SUB.orig.rules && SUB.orig.rules.length ? SUB.orig.rules : SUB.builtin;
  const of = f => FMT[f] || FMT.links, other = b5Fmt(R, B5_OTHER_UA);
  const row = (apps, f, oldF, cls = "") => H`<div class="b5-tr ${cls}"><span class="b5-apps">${apps}</span><span class="b5-fmt"><b>${of(f)[0]}</b><span class="sub">${of(f)[1]}</span>${oldF !== f ? H`<span class="tag lab">改过</span>` : ""}</span></div>`;
  return H`<div class="b5-th"><span>客户端</span><span>拿到</span></div>
    <div class="b5-tb">${b5Groups(R).map(([f, apps]) => row(apps.join("、"), f, apps.every(a => b5Fmt(O, B5_APPS.find(x => x[0] === a)[1]) === f) ? f : null))}
    ${row(H`其他客户端<span class="sub">包括用浏览器打开</span>`, other, b5Fmt(O, B5_OTHER_UA), "b5-other")}</div>`;
}
function b5GetCard(d, W) {
  const custom = !sameRules(b5Rules(d), SUB.builtin);
  return H`<section class="r-sec b5-sec" id="sec-clients">${secHead(H`客户端格式`, H`${tipI("按各客户端常见的标识推算，实际以控制面判断为准。")}<span class="sp"></span>
      ${custom ? H`<span class="sub b5-cust">已改过识别规则</span>` : ""}${W ? H`<button type="button" class="btn sm ton" data-act="s5-rules">${ic("sliders")}识别规则</button>` : ""}`)}
    <div class="b5-tbl r-tbl" id="b5-tbl">${b5Table(d)}</div></section>`;
}

/* ---- 第三段:高级 · 订阅底稿 ---- */
const TPL_ROWS = [["clash-meta", "Clash Meta"], ["sing-box", "sing-box"], ["xray", "Xray"]];
function advCard(d, bodies) {
  const custom = f => !!(d.templates && d.templates[f]), n = TPL_ROWS.filter(([f]) => custom(f)).length;
  return H`<section class="r-sec r-adv ${ADV_OPEN ? "open" : ""}" id="sec-adv">
    <button type="button" class="r-sum" data-act="s5-adv" aria-expanded="${ADV_OPEN}"><h2>高级</h2><span class="sp"></span>
      <span class="sub">订阅底稿 · ${n ? `${n} 种自定义` : "都用内置"}</span>${ic("down", "s5-car")}</button>
    ${ADV_OPEN ? H`<div class="r-sh r-sh2"><h3>订阅底稿</h3>${tipI("分流规则、策略组和 DNS 写在底稿里。节点会自动放进去，其余部分原样发给所有人。")}</div>
      <div class="zebra r-zebra">${TPL_ROWS.map(([f, name]) => { const b = bodies[f] || "";
      return H`<button type="button" class="zr zr-tpl" data-act="tpl-open" data-f="${f}"><span class="zr-c"><b>${name}</b></span>
        <span class="pill ${custom(f) ? "on" : ""}">${custom(f) ? "自定义" : "内置"}</span><span class="sub mono hide-m">${b ? `${tplLines(b)} 行 · ${tplSize(tplBytes(b))}` : ""}</span>
        <span class="btn sm ton">${ic("tpl")}打开</span></button>`; })}</div>` : ""}</section>`;
}

/* ---- 保存条 ---- */
function saveBar() {
  const c = changes();
  if (!can("PUT", "/api/settings") || !c.length) return "";
  return H`<div class="s5-bar" role="region" aria-label="未保存的改动"><span class="s5-bar-t"><b>有 ${c.length} 处改动</b><span class="sub ell" title="${c.join("、")}">${c.join("、")}</span></span>
    ${SUB.err ? H`<span class="s5-bar-err" role="alert">${SUB.err}</span>` : ""}<span class="sp"></span>
    <button type="button" class="btn" data-act="s5-discard" ${SUB.saving ? raw("disabled") : ""}>放弃</button>
    <button type="button" class="btn pri ${SUB.saving ? "busy" : ""}" data-act="s5-save" ${SUB.saving ? raw("disabled") : ""}>保存</button></div>`;
}
// 切客户端只更新选中状态和预览，不重画其它字段或请求控制面。
function refreshPreview() {
  for (const button of document.querySelectorAll('[data-act="s5-pv"]')) button.classList.toggle("on", +button.dataset.i === SUB.pv);
  const pv = $("#pv"); if (pv) pv.innerHTML = val(b5Card(SUB.draft));
}
// 刷新不重绘整页,保留输入焦点:只换链接、预览卡片、订阅名结果和对照表。
function refresh() {
  if (route[0] !== "sub" || !SUB) return;
  const d = SUB.draft;
  const set = (id, h) => { const el = document.getElementById(id); if (el) el.innerHTML = val(h); };
  set("lk", b5Link(d)); set("pfx-slot", b5PfxNote(d)); set("title-res", b5TitleRes(d)); set("b5-tbl", b5Table(d));
  refreshPreview();
  for (const el of document.querySelectorAll(".b5 [data-k]")) el.readOnly = SUB.saving || !can("PUT", "/api/settings");
  const t = $("#f-title"); if (t) t.contentEditable = String(!SUB.saving && can("PUT", "/api/settings"));
  if ($("#pfx-err")) $("#pfx-err").textContent = SUB.fieldErrors?.sub_url_prefix || "";
  if ($("#int-err")) $("#int-err").textContent = SUB.fieldErrors?.update_interval || "";
  const sb = $("#sb"); if (sb) sb.innerHTML = val(saveBar());
}

/* ---- 保存 ---- */
// saveSub 是保存条和「保存并离开」共用的那段:校验、先读后写、失败把错误留在保存条。
// 成功返回 true(页面随后会自己重载);失败返回 false。
async function saveSub() {
  if (!can("PUT", "/api/settings") || SUB.saving || !changes().length) return false;
  const d = SUB.draft, errors = {};
  if (!/^\d+$/.test(String(d.update_interval)) || !Number.isSafeInteger(+d.update_interval)) errors.update_interval = "刷新间隔要是非负整数";
  SUB.fieldErrors = errors;
  if (Object.keys(errors).length) { SUB.err = Object.values(errors).join("；"); refresh(); return false; }
  SUB.saving = true; SUB.err = ""; refresh();
  const ks = FIELDS.map(x => x[0]).filter(k => String(d[k] ?? "") !== String(SUB.orig[k] ?? ""));
  const rules = !sameRules(d.rules, SUB.orig.rules), n = changes().length;
  try {
    await modifySettings(s => { for (const k of ks) s[k] = k === "update_interval" ? +d[k] : d[k]; if (rules) s.rules = sameRules(d.rules, SUB.builtin) ? null : clone(d.rules); });
    // 不清空 SUB:重载回来前页面上的按钮仍要能用;草稿记成已保存,重载时换成控制面的最新值。
    SUB.saving = false; SUB.err = ""; SUB.fieldErrors = {}; SUB.orig = clone(SUB.draft);
    // 地址改了:控制面给的旧链接和它当时的开头都作废,预览改用令牌按新地址拼。重载回来前,
    // 开头就是刚存下的那个(库里存的已去掉尾斜杠);没填时退回原来那台(旧开头或控制面地址)。
    if (ks.includes("sub_url_prefix") && SUB.preview) {
      const saved = String(d.sub_url_prefix || "").replace(/\/+$/, "");
      Object.assign(SUB.preview, { auto: "", appliedPrefix: saved || SUB.preview.appliedPrefix });
    }
    refresh();
    toast(`已保存 ${n} 处改动`); await load(true, true);
    return true;
  } catch (e) {
    SUB.saving = false;
    // 按字段名认错(旧控制面不报字段名,退回按原话里的「订阅地址」认);页面上统一叫「链接开头」。
    // 新控制面不报字段名时,原话里写的是「链接开头」(v0.7 前后改的措辞),两种说法都认。
    if (e.status === 400 && (e.field === "sub_url_prefix" || e.message.includes("订阅地址") || e.message.includes("链接开头"))) {
      SUB.fieldErrors.sub_url_prefix = e.message.replaceAll("订阅地址", "链接开头");
      SUB.err = "没保存上：链接开头的格式不对，改好再保存。"; refresh(); $("#f-prefix")?.focus();
      return false;
    }
    if (e.status === 400 && e.field) { SUB.fieldErrors[e.field] = e.message; refresh(); }
    // 不写进挡着的草稿:清掉这个字段下的红字就得重画整页,输入框里的字和光标会丢;留着它,
    // 草稿再改过时(见 PAGES.sub.after)红字自己消失。
    SUB.err = `没保存上：${e.message.replace(/[。.]$/, "")}。改动还在，可以再点一次保存。`; refresh();
    return false;
  }
}

// 离开确认:有未保存改动去别的页时,拦下来弹应用内对话框;同页锚点(#sub/clients 等)不算离开。
function subLeaveModal(target) {
  if (modalCleanup) closeModal();
  const state = SUB, c = changes();
  SUB.leaveTarget = target;
  const m = $("#modal"), previous = document.activeElement;
  const attrs = ["role", "aria-modal", "aria-labelledby"].map(k => [k, m.getAttribute(k)]);
  m.setAttribute("role", "dialog"); m.setAttribute("aria-modal", "true"); m.setAttribute("aria-labelledby", "sub-leave-title");
  const trap = e => {
    if (e.key !== "Tab") return;
    const buttons = [...m.querySelectorAll("button:not(:disabled)")];
    const first = buttons[0], last = buttons[buttons.length - 1];
    if (!m.contains(document.activeElement) || (e.shiftKey ? document.activeElement === first : document.activeElement === last)) {
      e.preventDefault(); (e.shiftKey ? last : first)?.focus();
    }
  };
  document.addEventListener("keydown", trap, true);
  modalCleanup = () => {
    clearTimeout(focusTimer); document.removeEventListener("keydown", trap, true);
    for (const [k, v] of attrs) { if (v == null) m.removeAttribute(k); else m.setAttribute(k, v); }
    if (SUB === state) SUB.leaveTarget = null;
    if (previous?.isConnected) previous.focus();
    else $('.profile[data-act="cp-toggle"]')?.focus();
  };
  m.innerHTML = val(H`<div class="mbody"><h2 id="sub-leave-title">有 ${c.length} 处改动还没保存</h2>
    <p>离开「订阅」页，这些改动就会丢：</p>
    <ul class="s5-leave-list">${c.map(x => H`<li>${x}</li>`)}</ul>
    <div class="err" id="modal-err" role="alert"></div></div>
    <div class="mf"><button class="btn" data-act="sub-leave-stay">留下</button><button class="btn" data-act="sub-leave-drop">不保存</button><button class="btn pri" id="sub-leave-ok" data-act="sub-leave-save">保存并离开</button></div>`);
  $("#scrim").classList.add("open"); m.classList.add("open");
  const focusTimer = setTimeout(() => $("#sub-leave-ok")?.focus(), 50);
}

PAGES.sub = {
  title: "订阅",
  crumb: () => H`<b>订阅</b>`,
  async load(r) {
    // 侧栏的节点、用户数字来自 STATUS;直接打开订阅页时也取一次。
    STATUS = await api("/api/status").catch(() => STATUS);
    if (r[1] === "templates") ADV_OPEN = true;
    const [settings, builtin, users, pulls, nodes, inbounds, plans] = await Promise.all([
      api("/api/settings"), api("/api/settings/builtin-rules"), subUsers(),
      api("/api/sub-pulls").catch(() => []), api("/api/nodes").catch(() => []),
      api("/api/inbounds").catch(() => []), api("/api/plans").catch(() => []),
    ]);
    const recent = [...(pulls || [])].sort((a, b) => new Date(b.last_pulled_at) - new Date(a.last_pulled_at)).find(p => users.some(u => u.username === p.username));
    const user = users.find(u => u.username === SUB?.preview.user) || users.find(u => u.username === recent?.username) || users[0];
    const preview = await subPreview(user, { users, nodes, inbounds, plans });
    const tpl = ADV_OPEN ? await subTemplates() : {};
    return { settings, builtin, users, nodes, inbounds, plans, preview, tpl };
  },
  render(d) {
    const head = sub => H`<div class="ph r-ph"><h1>订阅</h1><span class="sp"></span>${sub}</div>`;
    if (!d) return H`${head("")}<div class="card"><table><tbody>${skRows(6, 2)}</tbody></table></div>`;
    if (SUB && !changes().length && !SUB.saving) { SUB.orig = clone(d.settings); SUB.draft = clone(d.settings); }
    if (!SUB) SUB = { orig: clone(d.settings), draft: clone(d.settings), err: "", fieldErrors: {}, saving: false, pv: 0, leaveTarget: null };
    // 刷新不丢草稿，已经打开的底稿与用户数据仍取最新值。
    Object.assign(SUB, { builtin: d.builtin, users: d.users, preview: d.preview });
    const W = can("PUT", "/api/settings");
    return H`${head(b5Picker())}<div class="b5 r-page">${b5LinkCard(SUB.draft, W)}${b5LookCard(SUB.draft, W)}${b5GetCard(SUB.draft, W)}${advCard(d.settings, d.tpl || {})}</div><div class="s5-sb" id="sb">${saveBar()}</div>`;
  },
  after(d, r) {
    if (!d) { subAnchorKey = ""; return; }
    if (!SUB) return;
    wirePreview();
    subAnchor(r);
    for (const el of document.querySelectorAll(".b5 [data-k]")) el.addEventListener("input", () => {
      const k = el.dataset.k; let v = el.value;
      if (k === "update_interval") v = /^\d+$/.test(v.trim()) ? +v.trim() : v;
      SUB.draft[k] = v; SUB.err = ""; delete SUB.fieldErrors[k]; refresh();
    });
    const t = $("#f-title");
    if (t) {
      t.addEventListener("keydown", e => { if (e.key === "Enter") e.preventDefault(); });
      t.addEventListener("paste", e => { e.preventDefault(); document.execCommand("insertText", false, (e.clipboardData.getData("text/plain") || "").replace(/\s*\n\s*/g, " ")); });
      t.addEventListener("input", () => {
        let v = b5TokVal(t);
        // 有人手打了 {USERNAME}:换成标签块,光标放到最后。
        if (t.textContent.includes("{USERNAME}")) { t.innerHTML = b5TokHTML(v); const s = getSelection(); s.selectAllChildren(t); s.collapseToEnd(); }
        SUB.draft.profile_title = v; SUB.err = ""; delete SUB.fieldErrors.profile_title; refresh();
      });
    }
    // 字段与预览对应:鼠标或焦点在哪一栏,卡片上对应那块就亮起来;反过来也一样。
    const hl = n => { B5_HL = n; const a = $("#pv-app"); if (a) a.dataset.hl = n; for (const f of document.querySelectorAll(".b5-f")) f.classList.toggle("hl", f.dataset.p === n); };
    for (const f of document.querySelectorAll(".b5-f")) {
      f.addEventListener("mouseenter", () => hl(f.dataset.p)); f.addEventListener("mouseleave", () => { if (!f.contains(document.activeElement)) hl(""); });
      f.addEventListener("focusin", () => hl(f.dataset.p)); f.addEventListener("focusout", () => hl(""));
    }
    $("#pv")?.addEventListener("mouseover", e => { const p = e.target.closest(".pa"); hl(p ? p.dataset.p : (document.activeElement?.closest(".b5-f")?.dataset.p || "")); });
    $("#pv")?.addEventListener("mouseleave", () => hl(document.activeElement?.closest(".b5-f")?.dataset.p || ""));
    $("#pv")?.addEventListener("click", e => { const p = e.target.closest(".pa"); if (p) document.querySelector(`.b5-f[data-p="${p.dataset.p}"] .in`)?.focus(); });
    const f = $(".r-pfx");
    if (f) {
      const on = v => $("#lk")?.classList.toggle("hl", v);
      f.addEventListener("mouseenter", () => on(true)); f.addEventListener("mouseleave", () => on(f.contains(document.activeElement)));
      f.addEventListener("focusin", () => on(true)); f.addEventListener("focusout", () => on(f.matches(":hover")));
    }
  },
  canLeave(nextKey) {
    if (!SUB || !changes().length || String(nextKey || "").split("/")[0] === "sub") return true;
    subLeaveModal(nextKey);
    return false;
  },
};

/* ---- 「识别规则」抽屉 ---- */
const B5_TRY = [["Clash Verge", "clash-verge/v2.0.3"], ["Stash", "Stash/2.7.0 Clash/1.11.0"], ["Shadowrocket", "Shadowrocket/2070 CFNetwork/1494"], ["sing-box", "SFI/1.11.4 (sing-box 1.11.4)"]];
// 每条规则读作「客户端标识里含 [clash]，拿到 [格式]」;正则和先后顺序都留在这里,页面上只给人看的说法。
function rulesBody() {
  const R = RD.rules, h = hit(R, RD.ua), O = FMT_ORDER.map(f => [f, (FMT[f] || FMT.links)[0]]);
  const res = !RD.ua ? H`<span class="h">粘一段客户端标识，看它会拿到什么。</span>`
    : h < R.length ? H`<span class="b5-res">会拿到 <b>${FMT[R[h].result]?.[0] || R[h].result}</b>，对上的是「${b5Say(R[h].pattern)}」这条。</span>`
      : H`<span class="b5-res">一条都没对上，会拿到 <b>节点链接</b>。</span>`;
  return H`<div class="field rd-test"><label for="rd-ua">输入客户端标识试试</label>
      <input class="in mono" id="rd-ua" value="${RD.ua}" placeholder="例如 clash-verge/v2.0.3" autocomplete="off" spellcheck="false">
      <div class="b5-try"><span class="h">常见的：</span>${B5_TRY.map(([n, ua]) => H`<button type="button" class="btn sm ton ${RD.ua === ua ? "on" : ""}" data-act="b5-try" data-ua="${ua}">${n}</button>`)}</div>
      <div id="rd-hit">${res}${RD.ua ? H`<span class="sub" id="rd-ruling">实际以控制面判断为准</span>` : ""}</div></div>
    <p class="b5-dlead">从上往下比，用第一条对上的。</p>
    <div class="rd-list b5-rl">${R.map((r, i) => { const x = b5Parse(r.pattern), e = x.all ? "" : x.body.trim() ? "" : "要填一段标识";
      return H`<div class="rd-row b5-rr ${i === h ? "hit" : ""}" data-i="${i}">
        <div class="b5-rr-w">${x.all ? H`<span class="b5-k b5-all">其他所有客户端</span>` : H`<span class="b5-k">${x.re ? "客户端标识符合" : "客户端标识里含"}</span>
          <input class="in mono" data-rp="${i}" value="${x.body}" aria-label="客户端标识里含的文字" spellcheck="false">`}
          <span class="tag lab b5-on ${i === h ? "" : "off"}" ${i === h ? "" : raw('aria-hidden="true"')}>对上了</span></div>
        <div class="b5-rr-f"><span class="b5-k">拿到</span><select class="in" data-rf="${i}" aria-label="拿到的格式">${O.map(([f, n]) => H`<option value="${f}" ${f === r.result ? raw("selected") : ""}>${n}</option>`)}</select>
          <span class="rd-ops"><button type="button" class="ib sm" data-act="rd-mv" data-i="${i}" data-to="${i - 1}" ${i ? "" : raw("disabled")} aria-label="上移">${ic("chevup")}</button><button type="button" class="ib sm" data-act="rd-mv" data-i="${i}" data-to="${i + 1}" ${i < R.length - 1 ? "" : raw("disabled")} aria-label="下移">${ic("down")}</button><button type="button" class="ib sm" data-act="rd-rm" data-i="${i}" aria-label="删除">${ic("trash")}</button></span></div>
        ${e ? H`<span class="rd-err">${e}</span>` : r.label ? H`<span class="h b5-lbl">内置 · ${r.label}</span>` : x.all ? H`<span class="h b5-lbl">上面几条都没对上的，都按这条给</span>` : x.re ? H`<span class="h b5-lbl">这条按正则写的，保存时由控制面检查</span>` : x.body.includes("|") ? H`<span class="h b5-lbl">「|」表示「或」</span>` : ""}</div>`; })}
      ${R.some(r => b5Parse(r.pattern).all) ? "" : H`<div class="rd-row b5-rr rd-last"><div class="b5-rr-w"><span class="b5-k b5-all">其他所有客户端</span></div><div class="b5-rr-f"><span class="b5-k">拿到</span><span class="rd-fixed">节点链接</span><span class="rd-ops"></span></div></div>`}</div>
    <button type="button" class="btn sm ton" data-act="rd-add">${ic("plus")}加一条</button>`;
}
function rulesDrawer(focus) {
  RD = { rules: clone(b5Rules(SUB.draft)), ua: "", focus: focus ?? -1 };
  openDrawer(H`<div class="dh"><div class="dt"><h2>识别规则</h2><span class="sub">客户端拉订阅时会带上自己的标识（User-Agent），例如 clash-verge/v2.0.3。按它决定给哪种格式。</span></div><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
    <div class="db rd-db" id="rd-body">${rulesBody()}</div>
    <div class="df"><button type="button" class="btn ghost" data-act="rd-reset">${ic("undo")}恢复内置</button><span class="sp" style="flex:1"></span><span class="df-why" id="rd-why"></span><button type="button" class="btn" data-act="close">取消</button><button type="button" class="btn pri" data-act="rd-done" id="rd-done">完成</button></div>`, "wide");
  rdWire();
  // 加一条:新的一条滚到中间,光标放进它的标识框,接着就能打字。有些平台点完按钮以后才把焦点
  // 交回按钮(它不在抽屉里),那时再抢一次;焦点已经落在新行上(重画后被认出来)就不再动。
  // 已经在抽屉里别的控件上 = 用户自己点了,不抢。
  if (focus === -1) setTimeout(() => {
    if (!RD || RD.focus !== focus || !$("#drawer.open")) return;
    if (document.activeElement?.matches?.("[data-rp]") || $("#drawer.open").contains(document.activeElement)) return;
    focusNewRow(focus, true);
  }, 0);
}
// focusNewRow 把光标放进第 i 条规则的标识框:先滚到看得见的地方,焦点在抽屉外面才抢。
function focusNewRow(i, onlyIfOutside) {
  const drawer = $("#drawer.open");
  if (!drawer || !RD) return;
  const input = drawer.querySelector(`[data-rp="${i}"]`);
  if (!input) return;
  input.closest(".rd-row")?.scrollIntoView({ block: "center" });
  if (onlyIfOutside && drawer.contains(document.activeElement)) return;
  input.focus();
}
function rdPaint() { const y = $("#rd-body").scrollTop; $("#rd-body").innerHTML = val(rulesBody()); $("#rd-body").scrollTop = y; rdWire(); }
// change 可能在 Tab 的失焦阶段触发，等焦点移动完再记录目标并重绘。
function rdChangePaint() {
  setTimeout(() => {
    if (!$("#rd-body") || !$("#drawer.open")) return;
    const el = document.activeElement;
    const selector = el?.id === "rd-ua" ? "#rd-ua" : el?.matches("[data-rp]") ? `[data-rp="${el.dataset.rp}"]` : el?.matches("[data-rf]") ? `[data-rf="${el.dataset.rf}"]` : el?.matches("[data-act][data-i]") ? `[data-act="${el.dataset.act}"][data-i="${el.dataset.i}"]` : null;
    rdPaint();
    if (selector) $("#rd-body").querySelector(selector)?.focus();
  }, 0);
}
// 改标识文字时保留原来的 (?i) 前缀:不分大小写只能靠规则自己带着,界面不另设开关。
function rdWire() {
  const bad = RD.rules.some(r => { const x = b5Parse(r.pattern); return !x.all && !x.body.trim(); });
  $("#rd-done").disabled = bad; $("#rd-why").innerHTML = bad ? val(H`${ic("warn")}有一条还没填标识`) : "";
  $("#rd-ua").addEventListener("input", e => { RD.ua = e.target.value; const p = e.target.selectionStart; rdPaint(); const n = $("#rd-ua"); n.focus(); n.setSelectionRange(p, p); });
  for (const el of document.querySelectorAll("[data-rp]")) el.addEventListener("input", () => { const r = RD.rules[+el.dataset.rp], x = b5Parse(r.pattern), p = (x.ci ? "(?i)" : "") + el.value; if (p !== r.pattern) delete r.label; r.pattern = p; });
  for (const el of document.querySelectorAll("[data-rp]")) el.addEventListener("change", () => rdChangePaint());
  for (const el of document.querySelectorAll("[data-rf]")) el.addEventListener("change", () => { RD.rules[+el.dataset.rf].result = el.value; rdChangePaint(); });
}

Object.assign(ACTS, {
  "s5-pv": d => { SUB.pv = +d.i; B5_HL = ""; refreshPreview(); },
  "s5-adv": async () => {
    ADV_OPEN = !ADV_OPEN; paint();
    if (ADV_OPEN) {
      try { const data = current.data; data.tpl = await subTemplates(); if (current.data === data && route[0] === "sub") paint(); }
      catch (e) { toast(e.message, true); }
    }
  },
  "s5-rules": d => { if (can("PUT", "/api/settings") && !SUB.saving) rulesDrawer(d.i != null ? +d.i : undefined); },
  "s5-discard": () => {
    if (!SUB || SUB.saving) return;
    const state = SUB, dropped = {};
    for (const [k] of FIELDS) if (String(SUB.draft[k] ?? "") !== String(SUB.orig[k] ?? "")) dropped[k] = SUB.draft[k];
    if (!sameRules(SUB.draft.rules, SUB.orig.rules)) dropped.rules = clone(SUB.draft.rules);
    SUB.draft = clone(SUB.orig); SUB.err = ""; SUB.fieldErrors = {}; paint();
    toast("已放弃改动", false, { label: "撤销", run: () => { if (SUB !== state) return; Object.assign(SUB.draft, clone(dropped)); SUB.err = ""; SUB.fieldErrors = {}; paint(); } });
  },
  "s5-save": () => saveSub(),
  // 离开确认的三个选择:留下(地址已由 hashchange 守卫换回)、不保存直接走、保存成功再走。
  "sub-leave-stay": () => subCloseModal(),
  "sub-leave-drop": () => { if (!SUB) { subCloseModal(); return; } const t = SUB.leaveTarget; subCloseModal(); SUB.leaveTarget = null; SUB.draft = clone(SUB.orig); SUB.err = ""; SUB.fieldErrors = {}; if (typeof t === "function") paint(); subLeave(t); },
  "sub-leave-save": async () => { if (!SUB) { subCloseModal(); return; } const t = SUB.leaveTarget; subCloseModal(); SUB.leaveTarget = null; if (await saveSub()) subLeave(t); },
  "rd-mv": d => { const i = +d.i, to = +d.to; const [x] = RD.rules.splice(i, 1); RD.rules.splice(to, 0, x); rdPaint(); },
  "rd-rm": d => { RD.rules.splice(+d.i, 1); rdPaint(); },
  // 新的一条插在「其他所有客户端」前面,光标放进标识框里,接着就能打字。
  "rd-add": () => { const i = RD.rules.findIndex(r => b5Parse(r.pattern).all), at = i < 0 ? RD.rules.length : i;
    RD.rules.splice(at, 0, { pattern: "(?i)", result: "clash-meta" }); rdPaint(); focusNewRow(at); },
  "rd-reset": () => { RD.rules = clone(SUB.builtin); rdPaint(); },
  "rd-done": () => { if ($("#rd-done").disabled) return; SUB.draft.rules = clone(RD.rules.length ? RD.rules : SUB.builtin); SUB.err = ""; closeOverlays(); paint(); },
  "b5-try": d => { RD.ua = d.ua; rdPaint(); },
  // 「+ 用户名」:把「用户名」标签插到光标处;光标不在框里就放到最前面。
  "b5-tok": () => {
    const t = $("#f-title"); if (!t || t.contentEditable !== "true") return;
    const chip = document.createElement("span"); chip.className = "tk"; chip.contentEditable = "false"; chip.dataset.tok = "USERNAME"; chip.textContent = "用户名";
    const s = getSelection();
    if (s.rangeCount && t.contains(s.anchorNode)) { const r = s.getRangeAt(0); r.deleteContents(); r.insertNode(chip); }
    else t.prepend(chip, t.firstChild && t.firstChild.nodeType === 3 && !/^\s/.test(t.firstChild.textContent) ? " " : "");
    t.dispatchEvent(new Event("input")); t.focus(); const r = document.createRange(); r.setStartAfter(chip); r.collapse(true); s.removeAllRanges(); s.addRange(r);
  },
});

// 用户列表分页取全，预览下拉和默认人选使用同一份数据。
async function subUsers() {
  const users = [];
  for (let offset = 0; ; offset = users.length) {
    const page = await api(`/api/users?limit=1000&offset=${offset}`);
    users.push(...page.users);
    if (!page.users.length || users.length >= page.total) return users;
  }
}
function subTemplates() {
  return Promise.all(TPL_FORMATS.map(async f => [f, await api("/api/settings/templates/" + f, { asText: true, headers: { Accept: "text/plain" } })])).then(Object.fromEntries);
}
async function subPreview(u, data) {
  if (!u) return { user: "alice", token: "Wm5tR0aE3uLx9Nb2", used: 38.2 * 2 ** 30, limit: 200 * 2 ** 30, expire: "2026-12-31", node: "🇯🇵 东京 A", proto: "VLESS · Reality", more: 3, applied: null, nodes: data.nodes, inbounds: data.inbounds, plans: data.plans, users: data.users };
  const out = { user: u.username, token: "", used: u.used_traffic || 0, limit: u.data_limit || 0, expire: u.expire_at ? ymd(new Date(u.expire_at)) : "", node: "", proto: "", more: 0, auto: "", appliedPrefix: "", getAuto: false };
  // 链接开头按控制面这次拼链接用的那个认:从 auto 本身切出来,任何版本的控制面都给得出(只加的 base 字段同值)。
  // 没填链接开头时它是控制面这次请求的 Host(经 mpx ui 转发过来就是控制面的地址),不一定等于页面地址。
  try {
    const links = await api(userPath(u.username) + "/links"); out.auto = links.auto || "";
    const i = out.auto.lastIndexOf("/sub/"); out.token = i < 0 ? "" : out.auto.slice(i + 5); out.appliedPrefix = i < 0 ? "" : out.auto.slice(0, i);
    out.getAuto = true;
  } catch (e) { toast(e.message, true); }
  out.nodes = data.nodes; out.inbounds = data.inbounds; out.plans = data.plans; out.users = data.users;
  // 与订阅下发一样，套餐节点、套餐入站和额外授权取并集，排除排水与满员入站。
  // 授权数据不全时不猜测，省略无法确认的节点行。
  if (u.status !== "active") return out;
  const available = data.nodes.filter(n => !n.draining).flatMap(n => {
    const i = data.inbounds.find(i => {
      if (i.node_id !== n.id || subAllows(u, i, data.plans) !== true) return false;
      if (!i.max_users) return true;
      const active = data.users.filter(v => v.status === "active"), auth = active.map(v => subAllows(v, i, data.plans));
      return !auth.includes(null) && auth.filter(Boolean).length < i.max_users;
    });
    return i ? [{ n, i }] : [];
  });
  if (available.length) {
    const { n, i } = available[0]; out.node = nodeLabel(n); out.proto = `${i.protocol.toUpperCase()}${i.security && i.security !== "none" ? " · " + ({ reality: "Reality", tls: "TLS" }[i.security] || i.security) : ""}`; out.more = available.length - 1;
  }
  return out;
}
function subAllows(u, i, plans) {
  if ((u.inbound_ids || []).includes(i.id)) return true;
  if (!u.plan_id) return !(u.inbound_ids || []).length;
  const p = plans.find(p => p.id === u.plan_id);
  return p ? !!(p.all_nodes || (p.node_ids || []).includes(i.node_id) || (p.inbound_ids || []).includes(i.id)) : null;
}
let subPreviewSeq = 0;
function wirePreview() {
  $("#pv-user")?.addEventListener("change", async e => {
    const state = SUB, data = current.data, seq = ++subPreviewSeq;
    const user = state.users.find(u => u.username === e.target.value);
    // 先切名字和用量，取链接期间不展示上一个人的 token;开头也是上一个人的,一起清掉,等新的回来。
    state.preview = { ...state.preview, user: user.username, token: "", used: user.used_traffic || 0, limit: user.data_limit || 0, expire: user.expire_at ? ymd(new Date(user.expire_at)) : "", appliedPrefix: "", getAuto: false };
    data.preview = state.preview; refresh();
    const preview = await subPreview(user, data);
    if (seq !== subPreviewSeq || SUB !== state) return;
    state.preview = preview; data.preview = preview; refresh();
  });
}
let subAnchorKey = "";
function subAnchor(r) {
  const key = r.join("/");
  if (subAnchorKey === key) return;
  subAnchorKey = key;
  const id = { link: "sec-link", look: "sec-look", clients: "sec-clients", templates: "sec-adv" }[r[1]];
  // 只有 #sub/link、#sub/look、#sub/clients、#sub/templates 才滚到对应段;普通 #sub 停在顶上,页标题「订阅」露着。
  if (!id) return;
  requestAnimationFrame(() => { if (route.join("/") === key) document.getElementById(id)?.scrollIntoView({ block: "start" }); });
}
