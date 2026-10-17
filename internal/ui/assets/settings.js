// 「订阅」「系统」两页(原设置页拆分):通用(先读后写、整体回写)、UA 识别规则(add / rm / move / reset)、
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
// 原「设置」拆成两页(都还是同一份 settings,保存时先读回最新一份、只改自己那几项):
// 「订阅」= 地址与显示 · 客户端识别 · 订阅底稿(高级);「系统」= 通知 · 设备与接入 · 备份与恢复 · 更新与版本。
const SUB_TABS = [["", "地址与显示"], ["clients", "客户端识别"], ["templates", "订阅底稿"]];
const SYS_TABS = [["", "通知"], ["devices", "设备与接入"], ["backup", "备份与恢复"], ["update", "更新与版本"]];
const SUB_FIELDS = ["sub_url_prefix", "profile_title", "update_interval", "support_link"];
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

PAGES.sub = {
  title: "订阅",
  crumb: r => H`<a href="#sub">订阅</a><i>/</i><b>${(SUB_TABS.find(t => t[0] === tabKey(SUB_TABS, r)) || SUB_TABS[0])[1]}</b>`,
  async load(r) {
    const settings = await api("/api/settings");
    if (tabKey(SUB_TABS, r) !== "templates") return { settings };
    // 列表要显示大小与行数,所以把三份生效正文都取回来(GET 没自定义时返回内置默认)。
    const tpl = Object.fromEntries(await Promise.all(TPL_FORMATS.map(async f => [f, await api("/api/settings/templates/" + f, { asText: true, headers: { Accept: "text/plain" } })])));
    return { settings, tpl };
  },
  render(d, r) {
    const t = tabKey(SUB_TABS, r);
    const head = H`<div class="ph"><div><h1>订阅</h1><p>用户拿到的链接长什么样、客户端拿到什么格式</p></div></div>${tabsOf("sub", SUB_TABS, t)}`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(6, 2)}</tbody></table></div>`;
    const s = d.settings, W = can("PUT", "/api/settings");
    if (t === "") {
      const by = Object.fromEntries(GENERAL.map(g => [g[0], g])), f = k => settingsField(s, W, by[k]);
      return H`${head}<div class="grid g2">${settingsForm(s, W, SUB_FIELDS, H`${f("sub_url_prefix")}<div class="two">${f("profile_title")}${f("update_interval")}</div>${f("support_link")}`)}
        <div class="card pad16 self-start"><div class="sub mb8">用户拿到的链接</div><p class="sub">${s.sub_url_prefix ? "" : H`<b>还没设订阅地址:</b>发出去的链接会直接带服务器 IP。`}订阅地址形如 <code>${s.sub_url_prefix || "http://服务器 IP:端口"}/sub/&lt;token&gt;</code>。</p>
        <div class="callout stone mt"><div>标题里的 <code>{USERNAME}</code> 会换成用户名。</div></div></div></div>`;
    }
    if (t === "clients") return clientRulesCard(head, s, W);
    return templatesTab(head, s, d.tpl || {});
  },
  after(d, r) { settingsAfter(d, r); },
};

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
        <div class="card pad16 self-start"><div class="sub mb8">会通知什么</div><p class="sub">用户快到期、用量快用完时,往 Webhook 发一条消息。两个提醒填 0 就不发。</p></div></div>`;
    }
    if (t === "devices") return devicesTab(head, d);
    return backupTab(head);
  },
  after(d, r) { settingsAfter(d, r); },
};

function clientRulesCard(head, s, W) {
      const R = s.rules || [];
      return H`${head}<div class="card"><div class="ch">客户端识别<small>· 自上而下,先命中先用 · 共 ${R.length} 条</small><span class="sp"></span>${W ? H`<button class="btn ghost sm" data-act="rule-reset">恢复默认</button><button class="btn sm" data-act="rule-add">${ic("plus")}加规则</button>` : ""}</div>
        ${R.map((x, i) => H`<div class="row"><span class="sub mono w18">${i + 1}</span><code class="ell grow">${x.pattern}</code><span class="sub ua-to" aria-label="发">${ic("arrow")}</span><span class="tag">${x.result}</span>
          ${W ? H`<button class="ib sm" data-act="rule-move" data-i="${i}" data-to="${i - 1}" ${i ? "" : raw("disabled")} aria-label="上移">${ic("chevup")}</button><button class="ib sm" data-act="rule-move" data-i="${i}" data-to="${i + 1}" ${i < R.length - 1 ? "" : raw("disabled")} aria-label="下移">${ic("down")}</button><button class="ib sm" data-act="rule-rm" data-i="${i}" aria-label="删除">${ic("x", "sm")}</button>` : ""}</div>`)}
        <div class="row sub">没命中任何规则的客户端拿到分享链接(links)。规则从上往下比对,想调整优先级就上下移动。</div></div>`;
    }

function devicesTab(head, d) {
      const D = d.devices || [], X = can("DELETE", "/api/devices");
      const PW = can("PUT", "/api/device-password"), PWOFF = can("DELETE", "/api/device-password");
      return H`${head}<div class="card"><div class="ch">接入的设备<small>· 共 ${D.length} 台</small></div>
        ${D.length ? D.map(x => H`<div class="row"><span class="sub mono w18">${x.id}</span><b class="ell grow">${x.name}</b>${x.current ? H`<span class="pill on">这台</span>` : ""}
          <span class="sub">${x.last_used_at ? "上次 " + sinceText(x.last_used_at) : "未用过"}${x.last_ip ? " · " + x.last_ip : ""}</span>
          ${X && !x.current ? H`<button class="btn sm" data-act="dev-revoke" data-id="${x.id}" data-name="${x.name}">吊销</button>` : ""}</div>`) : H`<div class="row sub">还没有设备接入。在另一台设备的 MPX 里选「接入已有控制面」即可。</div>`}
        <div class="row sub">「这台」就是当前正在用的设备,界面里不让吊销它,免得把自己踢下线;要吊销它,请换一台已接入的设备操作。</div></div>
        ${PW || PWOFF ? devicePasswordCard(PW, PWOFF) : ""}`;
    }

function backupTab(head) {
    return H`${head}<div class="grid g2"><div class="card pad18"><b class="b6">下载备份</b><p class="sub mt6 mb14">一份数据库快照加控制面密钥,打包成一个归档。里面有全部用户的订阅 token,妥善保管。</p><button class="btn pri" data-act="backup">${ic("dl")}下载备份</button></div>
      <div class="card pad18"><b class="b6">从备份恢复</b><p class="sub mt6 mb14">从备份恢复暂不支持在窗口里操作,见文档「备份控制面」。恢复会整库替换并重启控制面,务必先确认备份文件。</p></div></div>`;
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
          toast(revoke ? `设备密码已更新,吊销了 ${(res && res.revoked) || 0} 台设备(这台也要重新接入)` : "设备密码已更新;已接入的设备不受影响");
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
    <label class="chk"><input type="checkbox" id="dpw-revoke" ${PW ? "" : raw("disabled")}> 同时吊销所有已接入设备(包括这台,之后要重新接入)</label>
    <div class="err mt8" id="dpw-err" role="alert"></div>
    <div class="flex8 end mt">
      ${PWOFF ? H`<button type="button" class="btn" data-act="dpw-off">关闭设备密码接入</button>` : ""}
      ${PW ? H`<button type="submit" class="btn pri" id="dpw-save">保存设备密码</button>` : ""}
    </div>
    <p class="sub mt8">关闭后新设备不能再接入,已接入的设备不受影响;要再开,在这里设一个新密码。</p></form>`;
}

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
    <b class="b6">订阅底稿</b><p class="sub mt4">用户拉 clash-meta / sing-box / xray 订阅时,控制面把这个人能用的节点注入模板,模板里的其余部分(分流规则、策略组、DNS…)原样发给所有人。没存自定义底稿的格式用内置默认。</p></div></div>
    ${n === 0 ? H`<div class="callout stone mt12"><div><b>三种格式都在用内置默认</b> —— 这对多数人就够了。想改分流或 DNS:点「查看 / 自定义」在内置模板上直接改;也可以先存成文件改完,再用抽屉里的「从文件读…」传回来。</div></div>` : ""}</div>
  <div class="card"><div class="ch">订阅底稿<small>· ${n} 个自定义 / ${TPL_FORMATS.length} 种格式</small></div>
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
  openDrawer(H`<form id="tpl-form" novalidate><div class="dh"><span class="fi">${ic("tpl")}</span><div style="min-width:0"><h2><span class="mono">${f}</span> 订阅底稿 <span class="pill ${c ? "on" : ""}">${c ? "自定义" : "内置默认"}</span></h2><div class="sub ell">${m.how}</div></div><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db tpl-db">
    ${c ? "" : H`<div class="callout stone mb8 tpl-note"><div>这是内置默认的副本。${W ? "改动后保存,就成为 " : ""}${W ? H`<b>${f}</b> 的自定义模板,对所有用户生效。` : ""}</div></div>`}
    <div id="tpl-ed"></div>
    <div class="ed-s"><span class="tag">${m.L}</span><span id="tpl-pos">行 1,列 1</span><span id="tpl-size"></span><span id="tpl-dirty" class="c-warn"></span><span class="sp"></span><span id="tpl-ok"></span></div>
    <div class="callout bad mt8 hidden" id="tpl-err" role="alert"></div></div>
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
      toast(`已保存 ${f} 订阅底稿`); closeOverlays(); load(true, true);
    } catch (e2) { const l = /line (\d+)/.exec(e2.message); showErr({ msg: e2.message, line: l ? +l[1] : 0 }, true); save.disabled = false; save.classList.remove("busy"); }
  });
}

Object.assign(ACTS, {
  "set-revert": () => load(true, true),
  "set-test": (d, b) => busy(b, async () => { await write("POST", "/api/settings/test-webhook", undefined, "测试通知已送到"); }),
  "dev-revoke": d => confirmBox({ title: `吊销设备 ${d.name}?`, text: "它的 token 立即失效,要再用得在那台机器上重新接入。", ok: "吊销", danger: true,
    run: async () => { await write("DELETE", "/api/devices/" + d.id); toast(`已吊销设备 ${d.name}`); await load(true, true); } }),
  "dpw-off": () => confirmBox({ title: "关闭设备密码接入?", text: "之后新设备不能用设备密码接入;已接入的设备不受影响。要再开,在这里设一个新密码即可。", ok: "关闭设备密码接入", danger: true,
    run: async () => { await write("DELETE", "/api/device-password", undefined, "已关闭设备密码接入;已接入的设备不受影响"); } }),
  "rule-add": () => confirmBox({ title: "加一条客户端识别规则", text: "PATTERN 是 Go 正则,不区分大小写就以 (?i) 开头。顺序即优先级:宽的正则会盖住后面的。第一次改动会把内置默认一起落库。", ok: "加规则",
    extra: H`<div class="field"><label for="r-pat">User-Agent 正则</label><input class="in mono" id="r-pat" placeholder="(?i)karing" autocomplete="off"></div>
      <div class="two"><div class="field"><label for="r-res">命中后返回</label><select class="in" id="r-res">${RULE_RESULTS.map(x => H`<option>${x}</option>`)}</select></div>
      <div class="field"><label for="r-idx">插到第几位</label><input class="in mono" id="r-idx" placeholder="留空加在最后" inputmode="numeric"></div></div>`,
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
  "rule-rm": d => { const i = +d.i; confirmBox({ title: `删掉第 ${i + 1} 条规则?`, text: "删完如果一条不剩,就回到内置默认。", ok: "删除规则", danger: true,
    run: async () => { await modifySettings(s => { s.rules.splice(i, 1); }); toast(`已删掉第 ${i + 1} 条规则`); await load(true, true); } }); },
  "rule-move": (d, b) => busy(b, async () => { const i = +d.i, to = +d.to; await modifySettings(s => { const [x] = s.rules.splice(i, 1); s.rules.splice(to, 0, x); }); toast(`已把第 ${i + 1} 条挪到第 ${to + 1} 位`); await load(true, true); }),
  "rule-reset": () => confirmBox({ title: "恢复内置默认规则?", text: "清掉全部自定义规则,回到内置默认那套(认得出主流客户端及其改名版)。", ok: "恢复默认", danger: true,
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
  "tpl-del": d => confirmBox({ title: `恢复 ${d.f} 为内置订阅底稿?`, text: `会删除你存的 ${d.f} 自定义模板(不可撤销,控制面不留历史);之后所有用户的 ${d.f} 订阅改用内置默认。想留底,先打开模板把内容复制出来。`,
    ok: "删除自定义底稿", danger: true, typed: d.f,
    run: async () => { await write("DELETE", "/api/settings/templates/" + d.f, undefined, `已恢复 ${d.f} 为内置订阅底稿`); closeOverlays(); await load(true, true); } }),
});
