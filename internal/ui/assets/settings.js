// 设置页:通用(先读后写、整体回写)、UA 识别规则(add / rm / move / reset)、
// 客户端模板(get / set / del)、备份下载。PUT /api/settings 是整体替换:每次写都先读回
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
const SET_TABS = [["general", "通用"], ["rules", "识别规则"], ["templates", "模板"], ["backup", "备份"]];

PAGES.settings = {
  title: "设置", nav: ["配置", "set", 50],
  crumb: r => H`<a href="#settings">设置</a><i>/</i><b>${(SET_TABS.find(t => t[0] === r[1]) || SET_TABS[0])[1]}</b>`,
  async load() { return { settings: await api("/api/settings") }; },
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
    if (t === "templates") return H`${head}<div class="grid g3">${TPL_FORMATS.map(f => { const custom = !!(s.templates && s.templates[f]); return H`<div class="card stat"><div class="l">${ic("tpl")}<b class="c-ink b56">${f}</b><span class="ml-auto">${custom ? H`<span class="tag">自定义</span>` : H`<span class="sub">内置</span>`}</span></div>
      <div class="s flex6 mt12"><button class="btn sm" data-act="tpl-open" data-f="${f}">${W ? (custom ? "查看 / 替换" : "查看 / 自定义") : "查看"}</button>${custom && can("DELETE", "/api/settings/templates/x") ? H`<button class="btn ghost sm c-bad" data-act="tpl-del" data-f="${f}">删除</button>` : ""}</div></div>`; })}</div>
      <p class="sub mt">clash、links、base64-links 没有可定制的模板。</p>`;
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

/* ---- 模板编辑 ---- */
async function templateDrawer(f) {
  let text = "";
  try { text = await api("/api/settings/templates/" + f, { headers: { Accept: "text/plain" } }); } catch (e) { toast(e.message, true); return; }
  if (typeof text !== "string") text = JSON.stringify(text, null, 2);
  const W = can("PUT", "/api/settings/templates/x");
  openDrawer(H`<form id="tpl-form"><div class="dh"><div style="min-width:0"><h2>${f} 模板</h2><div class="sub">当前生效的正文;没自定义时是内置默认的副本 · <code>mpx settings template get ${f}</code></div></div><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db"><textarea class="in tpl" id="tpl-text" spellcheck="false" ${W ? "" : raw("readonly")}>${text}</textarea>
    ${W ? H`<div class="flex8 mt"><label class="btn sm">从文件读…<input type="file" id="tpl-file" hidden></label><span class="sub">服务端先校验语法,不合法不存。等价于 <code>mpx settings template set ${f} -f FILE</code></span></div>` : ""}
    <div class="err mt" id="tpl-err" role="alert"></div></div>
  <div class="df"><button type="button" class="btn" data-act="close">关闭</button>${W ? H`<button type="submit" class="btn pri" id="tpl-save">保存为自定义模板</button>` : ""}</div></form>`);
  $("#tpl-file")?.addEventListener("change", async e => { const file = e.target.files[0]; if (file) $("#tpl-text").value = await file.text(); });
  $("#tpl-form").addEventListener("submit", async e => {
    e.preventDefault();
    const btn = $("#tpl-save"); btn.disabled = true; btn.classList.add("busy");
    try {
      // 与 CLI 一样原样发正文,不做 JSON 序列化。
      await api("/api/settings/templates/" + f, { method: "PUT", body: $("#tpl-text").value, headers: { "Content-Type": "text/plain; charset=utf-8" } });
      toast(`已保存 ${f} 模板`); closeOverlays(); load(true, true);
    } catch (e2) { $("#tpl-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

Object.assign(ACTS, {
  "set-revert": () => load(true, true),
  "set-test": (d, b) => busy(b, async () => { await write("POST", "/api/settings/test-webhook", undefined, "测试通知已送到"); }),
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
  "tpl-del": d => confirmBox({ title: `删除 ${d.f} 自定义模板?`, text: "之后这个格式回到内置模板。", cmd: `mpx settings template del ${d.f}`, ok: "删除模板", danger: true,
    run: async () => { await write("DELETE", "/api/settings/templates/" + d.f, undefined, `已删除 ${d.f} 模板`); await load(true, true); } }),
});
