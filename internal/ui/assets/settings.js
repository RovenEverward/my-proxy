// 设置页:通用(先读后写、整体回写)、UA 识别规则(add / rm / move / reset)、
// 客户端模板(get / set / del)、设备(list / revoke)、备份下载。PUT /api/settings 是整体替换:每次写都先读回
// 最新一份,只改这次动的地方,与 mpx settings 各子命令一样。
"use strict";

const TPL_FORMATS = ["clash-meta", "sing-box", "xray"];
const RULE_RESULTS = ["clash-meta", "clash", "sing-box", "xray", "links", "base64-links", "block"];
// asModelSettings:摆成 model.Settings 的 JSON 形状(nil 的 rules / templates 是 null)。
const asModelSettings = v => ({ sub_url_prefix: v.sub_url_prefix || "", profile_title: v.profile_title || "", support_link: v.support_link || "", update_interval: v.update_interval || 0,
  webhook_url: v.webhook_url || "", notify_days_left: v.notify_days_left || 0, notify_usage_percent: v.notify_usage_percent || 0, rules: v.rules ?? null, templates: v.templates ?? null });
async function modifySettings(change) {
  const s = asModelSettings(await api("/api/settings"));
  change(s);
  return write("PUT", "/api/settings", s);
}
const GENERAL = [["sub_url_prefix", "订阅地址前缀", "s", "发给用户的链接以它开头,前面通常是 TLS 反代"], ["profile_title", "订阅标题", "s", "客户端里显示的订阅名,支持 {USERNAME} 等变量"], ["update_interval", "客户端刷新间隔(小时)", "i", ""],
  ["support_link", "支持链接", "s", ""], ["webhook_url", "Webhook", "s", "到期、超限等通知发到这里;留空不发"], ["notify_days_left", "到期前提醒(天)", "i", "0 表示关闭"], ["notify_usage_percent", "用量提醒(%)", "i", "0 表示关闭"]];
const SET_TABS = [["general", "通用"], ["rules", "识别规则"], ["templates", "模板"], ["devices", "设备"], ["backup", "备份"]];

PAGES.settings = {
  title: "设置", nav: ["配置", "set", 50],
  crumb: r => H`<a href="#settings">设置</a><i>/</i><b>${(SET_TABS.find(t => t[0] === r[1]) || SET_TABS[0])[1]}</b>`,
  async load(r) {
    const settings = await api("/api/settings");
    if (r[1] === "devices") return { settings, devices: await api("/api/devices") };
    if (r[1] !== "templates") return { settings };
    // 列表要显示大小与行数,所以把三份生效正文都取回来(GET 没自定义时返回内置默认)。
    const tpl = Object.fromEntries(await Promise.all(TPL_FORMATS.map(async f => [f, await api("/api/settings/templates/" + f, { asText: true, headers: { Accept: "text/plain" } })])));
    return { settings, tpl };
  },
  render(d, r) {
    const t = SET_TABS.some(x => x[0] === r[1]) ? r[1] : "general";
    const head = H`<div class="ph"><div><h1>设置</h1><p>订阅、通知、识别规则与模板 · 对应 <code>mpx settings</code></p></div></div><div class="tabs">${SET_TABS.map(([k, l]) => H`<a class="${t === k ? "on" : ""}" href="#settings/${k}">${l}</a>`)}</div>`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(6, 2)}</tbody></table></div>`;
    const s = d.settings, W = can("PUT", "/api/settings");
    if (t === "general") {
      const f = ([k, l, ty, h]) => H`<div class="field"><label for="s-${k}">${l}</label>${k === "webhook_url" ? H`<div class="flex8"><input class="in mono" id="s-${k}" name="${k}" value="${s[k] || ""}" ${W ? "" : raw("readonly")}>${can("POST", "/api/settings/test-webhook") ? H`<button type="button" class="btn" data-act="set-test">测试</button>` : ""}</div>` : H`<input class="in ${ty === "i" ? "" : "mono"}" id="s-${k}" name="${k}" value="${s[k] ?? ""}" ${ty === "i" ? raw('inputmode="numeric"') : ""} ${W ? "" : raw("readonly")}>`}${h ? H`<span class="h">${h}</span>` : ""}</div>`;
      const by = Object.fromEntries(GENERAL.map(g => [g[0], g]));
      return H`${head}<div class="grid g2"><form class="card pad18" id="set-form" novalidate>
        ${f(by.sub_url_prefix)}<div class="two">${f(by.profile_title)}${f(by.update_interval)}</div>${f(by.support_link)}${f(by.webhook_url)}<div class="two">${f(by.notify_days_left)}${f(by.notify_usage_percent)}</div>
        <div class="err" id="s-err" role="alert"></div>
        ${W ? H`<div class="flex8 end"><button type="button" class="btn" data-act="set-revert">还原</button><button type="submit" class="btn pri" id="s-save">保存</button></div>` : ""}</form>
        <div class="card pad16 self-start"><div class="sub mb8">怎么保存的</div><p class="sub">设置是整体替换的:保存时先读回最新一份,只改你动过的字段,规则与模板原样带回。等价于改好 <code>mpx -o json settings show</code> 的输出再 <code>mpx settings set -f</code>。</p>
        <div class="callout stone mt"><div><code>{USERNAME}</code> 会替换成用户名;订阅地址形如 <code>${s.sub_url_prefix || "https://…"}/sub/&lt;token&gt;</code>。</div></div></div></div>`;
    }
    if (t === "rules") {
      const R = s.rules || [];
      return H`${head}<div class="card"><div class="ch">User-Agent 识别规则<small>· 自上而下,先命中先用 · 共 ${R.length} 条</small><span class="sp"></span>${W ? H`<button class="btn ghost sm" data-act="rule-reset">恢复默认</button><button class="btn sm" data-act="rule-add">${ic("plus")}加规则</button>` : ""}</div>
        ${R.map((x, i) => H`<div class="row"><span class="sub mono w18">${i + 1}</span><code class="ell grow">${x.pattern}</code><span class="sub">→</span><span class="tag">${x.result}</span>
          ${W ? H`<button class="ib sm" data-act="rule-move" data-i="${i}" data-to="${i - 1}" ${i ? "" : raw("disabled")} aria-label="上移">↑</button><button class="ib sm" data-act="rule-move" data-i="${i}" data-to="${i + 1}" ${i < R.length - 1 ? "" : raw("disabled")} aria-label="下移">↓</button><button class="ib sm c-bad" data-act="rule-rm" data-i="${i}" aria-label="删除">${ic("x", "sm")}</button>` : ""}</div>`)}
        <div class="row sub">没命中任何规则的客户端拿到分享链接(links)。想看某个 UA 会拿到什么:<code>mpx settings rules test '&lt;UA&gt;'</code></div></div>`;
    }
    if (t === "templates") return templatesTab(head, s, d.tpl || {});
    if (t === "devices") {
      const D = d.devices || [], X = can("DELETE", "/api/devices");
      return H`${head}<div class="card"><div class="ch">接入的设备<small>· 共 ${D.length} 台 · 对应 <code>mpx device list</code></small></div>
        ${D.length ? D.map(x => H`<div class="row"><span class="sub mono w18">${x.id}</span><b class="ell grow">${x.name}</b>${x.current ? H`<span class="pill on">这台</span>` : ""}
          <span class="sub">${x.last_used_at ? "上次 " + sinceText(x.last_used_at) : "未用过"}${x.last_ip ? " · " + x.last_ip : ""}</span>
          ${X && !x.current ? H`<button class="btn sm danger" data-act="dev-revoke" data-id="${x.id}" data-name="${x.name}">吊销</button>` : ""}</div>`) : H`<div class="row sub">还没有设备用 <code>mpx connect</code> 接入。</div>`}
        <div class="row sub">「这台」就是 mpx ui 正在用的设备,界面里不让吊销它,免得把自己踢下线;要吊销请在终端 <code>mpx device revoke &lt;id&gt;</code>。</div></div>`;
    }
    return H`${head}<div class="grid g2"><div class="card pad18"><b class="b6">下载备份</b><p class="sub mt6 mb14">一份数据库快照加控制面密钥,打包成一个归档。里面有全部用户的订阅 token,妥善保管。等价于 <code>mpx backup</code>。</p><button class="btn pri" data-act="backup">${ic("dl")}下载备份</button></div>
      <div class="card pad18"><b class="b6">从备份恢复</b><p class="sub mt6 mb14">恢复会整库替换、要重启控制面,只在终端里做,界面里不提供。</p><div class="cmd">mpx restore mpx-backup.tar.gz</div></div></div>`;
  },
  after(d, r) {
    const form = $("#set-form");
    if (!form || !d) return;
    form.addEventListener("submit", async e => {
      e.preventDefault();
      const fd = new FormData(form), err = [], vals = {};
      for (const [k, l, ty] of GENERAL) {
        const x = String(fd.get(k) ?? "").trim();
        if (ty === "i") { if (x && !/^\d+$/.test(x)) err.push(`${l}要是非负整数`); vals[k] = x ? +x : 0; } else vals[k] = x;
      }
      $("#s-err").textContent = err.join("\n");
      if (err.length) return;
      // 只改动过的字段:和打开页面时比,别把别人刚改的值冲回去。
      const changed = Object.keys(vals).filter(k => vals[k] !== (d.settings[k] ?? (typeof vals[k] === "number" ? 0 : "")));
      if (!changed.length) { toast("没有改动"); return; }
      const btn = $("#s-save"); btn.disabled = true; btn.classList.add("busy");
      try { await modifySettings(s => { for (const k of changed) s[k] = vals[k]; }); toast("已保存订阅设置"); await load(true, true); }
      catch (e2) { $("#s-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
    });
  },
};

/* ---- 模板 ---- */
// 每种格式的真实含义,出自 internal/render 与 internal/server/templates.go。
const TPL_META = {
  "clash-meta": { lang: "yaml", L: "YAML", ext: ".yaml", inject: "proxies", how: "注入 proxies,rule-providers / proxy-groups / rules / dns 原样带出;必须有顶层 rule-providers 段" },
  "sing-box": { lang: "json", L: "JSON", ext: ".json", inject: "outbounds", how: "注入节点 outbounds,并把 proxy / auto / direct / block 四个占位 tag 换成本次节点的版本" },
  "xray": { lang: "json", L: "JSON", ext: ".json", inject: "outbounds", how: "每个入口生成一份完整配置:节点作为第一条 outbound 插进模板的 outbounds,其余原样" },
};
const tplBytes = t => new TextEncoder().encode(t).length;
const tplLines = t => t ? t.split("\n").length - (t.endsWith("\n") ? 1 : 0) : 0;
const tplSize = n => n < 1024 ? `${n} B` : `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`;

function templatesTab(head, s, bodies) {
  const W = can("PUT", "/api/settings/templates/x"), D = can("DELETE", "/api/settings/templates/x");
  const custom = f => !!(s.templates && s.templates[f]);
  const n = TPL_FORMATS.filter(custom).length;
  const rows = TPL_FORMATS.map(f => { const m = TPL_META[f], b = bodies[f] || "", c = custom(f); return H`<tr data-act="tpl-open" data-f="${f}" tabindex="0">
    <td><div class="u"><span class="fi">${ic("tpl")}</span><div><b class="bold mono">${f}</b><div class="sub tpl-how">${m.how}</div></div></div></td>
    <td class="hide-m"><span class="tag">${m.L}</span></td>
    <td>${c ? H`<span class="pill on">自定义</span>` : H`<span class="pill">内置默认</span>`}</td>
    <td class="num hide-m">${b ? tplSize(tplBytes(b)) : "—"}</td><td class="num hide-m">${b ? tplLines(b) : "—"}</td>
    <td class="num"><div class="flex6 end"><button class="btn sm" data-act="tpl-open" data-f="${f}">${W ? (c ? "编辑" : "查看 / 自定义") : "查看"}</button>${c && D ? H`<button class="btn ghost sm c-bad" data-act="tpl-del" data-f="${f}">恢复内置</button>` : ""}</div></td></tr>`; });
  return H`${head}
  <div class="card pad16 mb tpl-intro"><div class="u"><span class="fi lg">${ic("tpl")}</span><div class="grow">
    <b class="b6">模板是订阅的底稿</b><p class="sub mt4">用户拉 clash-meta / sing-box / xray 订阅时,控制面把这个人能用的节点注入模板,模板里的其余部分(分流规则、策略组、DNS…)原样下发给所有人。没存自定义模板的格式用内置默认。</p></div></div>
    ${n === 0 ? H`<div class="callout stone mt12"><div><b>三种格式都在用内置默认</b> —— 这对多数人就够了。想改分流或 DNS:点「查看 / 自定义」在内置模板上改,或先导出到本地改完再传:<div class="cmd mt8">mpx settings template get sing-box > sing-box.json</div><div class="cmd">mpx settings template set sing-box -f sing-box.json</div></div></div>` : ""}</div>
  <div class="card"><div class="ch">模板<small>· ${n} 个自定义 / ${TPL_FORMATS.length} 种格式</small></div>
    <div class="tw"><table class="tpl-t"><thead><tr><th>格式</th><th class="hide-m">语言</th><th>状态</th><th class="num hide-m">大小</th><th class="num hide-m">行数</th><th></th></tr></thead><tbody>${rows}</tbody></table></div>
    <div class="row sub">clash(原版)、links、base64-links 不用模板:前者固定输出最小集合,后两者是纯分享链接。控制面不记录模板的修改时间。</div></div>`;
}

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
      return { line, msg: `不是合法的 JSON${line ? `:第 ${line} 行第 ${col} 列附近` : ""}(${e.message.replace(/^JSON\.parse: /, "")})` };
    }
  }
  const L = text.split("\n");
  const tab = L.findIndex(l => /^\s*\t/.test(l)); if (tab >= 0) return { line: tab + 1, msg: `第 ${tab + 1} 行用 Tab 缩进了,YAML 只能用空格` };
  const first = L.findIndex(l => l.trim() && !/^\s*#/.test(l) && l.trim() !== "---");
  if (first >= 0 && !/^[^\s#-][^:]*:(\s|$)/.test(L[first])) return { line: first + 1, msg: "顶层必须是映射(key: value),不能是列表或纯文本" };
  if (!L.some(l => /^(rule-providers|"rule-providers"|'rule-providers')\s*:/.test(l))) return { msg: "缺顶层 rule-providers 段(proxies 的插入点),服务端会拒收" };
  return null;
}

async function templateDrawer(f) {
  let text = "";
  try { text = await api("/api/settings/templates/" + f, { asText: true, headers: { Accept: "text/plain" } }); } catch (e) { toast(e.message, true); return; }
  const m = TPL_META[f], W = can("PUT", "/api/settings/templates/x"), c = !!(current.data?.settings?.templates || {})[f], orig = text;
  openDrawer(H`<form id="tpl-form" novalidate><div class="dh"><span class="fi">${ic("tpl")}</span><div style="min-width:0"><h2><span class="mono">${f}</span> 模板 <span class="pill ${c ? "on" : ""}">${c ? "自定义" : "内置默认"}</span></h2><div class="sub ell">${m.how}</div></div><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db tpl-db">
    ${c ? "" : H`<div class="callout stone mb8 tpl-note"><div>这是内置默认的副本。${W ? "改动后保存,就成为 " : ""}${W ? H`<b>${f}</b> 的自定义模板,对所有用户生效。` : ""}</div></div>`}
    <div id="tpl-ed"></div>
    <div class="ed-s"><span class="tag">${m.L}</span><span id="tpl-pos">行 1,列 1</span><span id="tpl-size"></span><span id="tpl-dirty" class="c-warn"></span><span class="sp"></span><span id="tpl-ok"></span></div>
    <div class="callout bad mt8 hidden" id="tpl-err" role="alert"></div>
    <div class="field mt12"><label>等价命令</label><div class="cmd">mpx settings template get ${f} > ${f}${m.ext}</div>${W ? H`<div class="cmd">mpx settings template set ${f} -f ${f}${m.ext}</div>` : ""}</div></div>
  <div class="df">${W ? H`<label class="btn sm ghost">${ic("up")}从文件读…<input type="file" id="tpl-file" accept="${m.ext},.txt,.yml" hidden></label>${m.lang === "json" ? H`<button type="button" class="btn sm ghost" id="tpl-fmt">格式化</button>` : ""}<button type="button" class="btn sm ghost" id="tpl-revert" disabled>撤销改动</button>` : ""}<span class="sp" style="flex:1"></span><button type="button" class="btn" data-act="close">关闭</button>${W ? H`<button type="submit" class="btn pri" id="tpl-save" disabled>保存</button>` : ""}</div></form>`, "wide");
  const err = $("#tpl-err"), save = $("#tpl-save");
  const showErr = (r, server, jump = true) => { err.classList.toggle("hidden", !r); err.innerHTML = r ? val(H`${ic("warn")}<div>${server ? H`<b>服务端拒收:</b>` : ""}${r.msg}${r.line ? H` <button type="button" class="lnk" id="tpl-goto">跳到第 ${r.line} 行</button>` : ""}</div>`) : ""; if (r?.line) { ed.mark(r.line, jump); $("#tpl-goto").onclick = () => { ed.mark(r.line); ed.focus(); }; } };
  let t = 0;
  const status = kind => {
    const [ln, col] = ed.cursor(); $("#tpl-pos").textContent = `行 ${ln},列 ${col}`;
    if (kind === "cursor") return;
    const v = ed.value, dirty = v !== orig;
    $("#tpl-size").textContent = `${tplLines(v)} 行 · ${tplSize(tplBytes(v))}`;
    $("#tpl-dirty").textContent = dirty ? "● 未保存" : "";
    if ($("#tpl-revert")) $("#tpl-revert").disabled = !dirty;
    clearTimeout(t); t = setTimeout(() => { const r = validateTpl(f, ed.value); $("#tpl-ok").innerHTML = val(r ? H`<span class="c-bad">${ic("warn", "sm")}有错误</span>` : H`<span class="c-ok">${ic("check", "sm")}语法正确</span>`); if (save) save.disabled = !dirty || !!r; showErr(r, false, false); }, 400);
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
      toast(`已保存 ${f} 模板`); closeOverlays(); load(true, true);
    } catch (e2) { const l = /line (\d+)/.exec(e2.message); showErr({ msg: e2.message, line: l ? +l[1] : 0 }, true); save.disabled = false; save.classList.remove("busy"); }
  });
}

Object.assign(ACTS, {
  "set-revert": () => load(true, true),
  "set-test": (d, b) => busy(b, async () => { await write("POST", "/api/settings/test-webhook", undefined, "测试通知已送到"); }),
  "dev-revoke": d => confirmBox({ title: `吊销设备 ${d.name}?`, text: "它的 token 立即失效,要再用得在那台机器上重新 mpx connect。", cmd: `mpx device revoke ${d.id}`, ok: "吊销", danger: true,
    run: async () => { await write("DELETE", "/api/devices/" + d.id); toast(`已吊销设备 ${d.name}`); await load(true, true); } }),
  "rule-add": () => confirmBox({ title: "加一条识别规则", text: "PATTERN 是 Go 正则,不区分大小写就以 (?i) 开头。顺序即优先级:宽的正则会盖住后面的。第一次改动会把内置默认一起落库。", ok: "加规则",
    extra: H`<div class="field"><label for="r-pat">User-Agent 正则</label><input class="in mono" id="r-pat" placeholder="(?i)karing" autocomplete="off"></div>
      <div class="two"><div class="field"><label for="r-res">命中后返回</label><select class="in" id="r-res">${RULE_RESULTS.map(x => H`<option>${x}</option>`)}</select></div>
      <div class="field"><label for="r-idx">插到第几位</label><input class="in mono" id="r-idx" placeholder="留空加在最后" inputmode="numeric"></div></div>`,
    cmdOf: m => { const p = $("#r-pat", m).value, i = $("#r-idx", m).value.trim(); return `mpx settings rules add ${p ? shq(p) : "<PATTERN>"} --result ${$("#r-res", m).value}${i ? " --index " + i : ""}`; },
    run: async () => {
      const pattern = $("#r-pat").value, result = $("#r-res").value, idx = $("#r-idx").value.trim();
      if (!pattern) throw new Error("正则不能为空");
      let at;
      await modifySettings(s => {
        const R = s.rules || [];
        at = R.length;
        if (idx) { const i = /^\d+$/.test(idx) ? +idx : NaN; if (!(i >= 1 && i <= R.length + 1)) throw new Error(`序号 ${idx} 超出范围:当前 ${R.length} 条`); at = i - 1; }
        R.splice(at, 0, { pattern, result }); s.rules = R;
      });
      toast(`已加规则 ${at + 1}: ${pattern} → ${result}`); await load(true, true);
    } }),
  "rule-rm": d => { const i = +d.i; confirmBox({ title: `删掉第 ${i + 1} 条规则?`, text: "删完如果一条不剩,就回到内置默认。", cmd: `mpx settings rules rm ${i + 1}`, ok: "删除规则", danger: true,
    run: async () => { await modifySettings(s => { s.rules.splice(i, 1); }); toast(`已删掉第 ${i + 1} 条规则`); await load(true, true); } }); },
  "rule-move": (d, b) => busy(b, async () => { const i = +d.i, to = +d.to; await modifySettings(s => { const [x] = s.rules.splice(i, 1); s.rules.splice(to, 0, x); }); toast(`已把第 ${i + 1} 条挪到第 ${to + 1} 位`); await load(true, true); }),
  "rule-reset": () => confirmBox({ title: "恢复内置默认规则?", text: "清掉全部自定义规则,回到内置默认那套(认得出主流客户端及其改名版)。", cmd: "mpx settings rules reset", ok: "恢复默认", danger: true,
    run: async () => { await modifySettings(s => { s.rules = null; }); toast("已恢复内置默认规则"); await load(true, true); } }),
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
  "tpl-del": d => confirmBox({ title: `恢复 ${d.f} 为内置模板?`, text: `会删除你存的 ${d.f} 自定义模板(不可撤销,控制面不留历史);之后所有用户的 ${d.f} 订阅改用内置默认。想留底,先用 mpx settings template get ${d.f} 导出一份。`,
    cmd: `mpx settings template del ${d.f}`, ok: "删除自定义模板", danger: true, typed: d.f,
    run: async () => { await write("DELETE", "/api/settings/templates/" + d.f, undefined, `已恢复 ${d.f} 为内置模板`); closeOverlays(); await load(true, true); } }),
});
