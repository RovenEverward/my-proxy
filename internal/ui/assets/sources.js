"use strict";

// External sources use the existing subscription entry and control-plane API.
const SOURCES = { user: "", format: "clash-meta", preview: null, previewSeq: 0, poll: 0 };
const sourcePath = id => "/api/sources/" + id;
const sourceFormatsUI = [["clash-meta", "Clash Meta"], ["sing-box", "sing-box"], ["links", "分享链接"], ["base64-links", "Base64"], ["clash", "Clash"], ["xray", "Xray"]];
const sourceStatus = s => !s.enabled ? stBadge("off", "已停用") : s.refreshing ? stBadge("warn", "刷新中") : s.last_error ? stBadge("warn", s.node_count ? "使用上次结果" : "未就绪") : s.node_count ? stBadge("ok", "可用") : stBadge("warn", "未就绪");
const sourceInterval = s => s.interval_seconds ? `每 ${+(s.interval_seconds / 3600).toFixed(2)} 小时` : "仅手动刷新";

PAGES.sources = {
  navAs: "sub", title: "外部订阅",
  crumb: () => H`<a href="#sub">订阅</a><i>/</i><b>外部订阅</b>`,
  async load(r, read = api) {
    const [sources, users, groups] = await Promise.all([read("/api/sources"), subUsers(read), read("/api/proxy-groups")]);
    if (!users.some(u => u.username === SOURCES.user)) SOURCES.user = users[0]?.username || "";
    return { sources, users, groups };
  },
  render(d) {
    const head = H`<div class="ph"><div><h1>外部订阅</h1><p>将已有订阅与自建节点合并，继续使用原来的订阅链接。</p></div><span class="sp"></span>${can("POST", "/api/sources") ? H`<button type="button" class="btn pri" data-act="source-new">${ic("plus")}添加订阅</button>` : ""}</div>`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(4, 3)}</tbody></table></div>`;
    return H`${head}<div class="card source-list">${d.sources.length ? d.sources.map(s => H`<div class="row source-row"><div class="grow source-name"><button type="button" class="lnk" data-act="source-open" data-id="${s.id}">${s.name}</button><div class="sub">${sourceInterval(s)} · ${s.last_good_at ? "最近成功 " + sinceText(s.last_good_at) : "尚未成功刷新"}</div></div><span class="sub">${s.node_count} 个节点</span>${sourceStatus(s)}<button type="button" class="ib" title="刷新 ${s.name}" aria-label="刷新 ${s.name}" data-act="source-refresh" data-id="${s.id}" ${!s.enabled || s.refreshing || !can("POST", "/api/sources") ? raw("disabled") : ""}>${ic("refresh")}</button></div>`) : emptyBox("sub", "还没有外部订阅", "添加订阅地址后，先检查导入结果，再选择接收用户。")}</div>
      <section class="card pad18 mt" aria-labelledby="source-preview-title"><div class="flex8"><h2 id="source-preview-title" class="b6">混合订阅预览</h2><span class="sp"></span><a href="#rules/groups">管理策略组</a></div><p class="sub">按用户授权和客户端格式显示实际输出。用量只统计自建节点。</p><div class="two"><div class="field"><label for="source-preview-user">接收用户</label><select class="in" id="source-preview-user" ${d.users.length ? "" : raw("disabled")}><option value="">选择用户</option>${d.users.map(u => H`<option value="${u.username}" ${raw(u.username === SOURCES.user ? "selected" : "")}>${u.username}</option>`)}</select></div><div class="field"><label for="source-preview-format">客户端格式</label><select class="in" id="source-preview-format">${sourceFormatsUI.map(([v, label]) => H`<option value="${v}" ${raw(v === SOURCES.format ? "selected" : "")}>${label}</option>`)}</select></div></div><div id="source-preview" aria-live="polite">${d.users.length ? H`<p class="sub">正在读取预览…</p>` : H`<p class="sub">先添加接收用户。</p>`}</div></section>`;
  },
  after(d) {
    clearTimeout(SOURCES.poll);
    if (!d) return;
    for (const id of ["source-preview-user", "source-preview-format"]) $("#" + id)?.addEventListener("change", e => withFeedback(e.target, async () => { SOURCES.user = $("#source-preview-user").value; SOURCES.format = $("#source-preview-format").value; await sourcePreview(); }));
    sourcePreview();
    if (d.sources.some(s => s.refreshing)) SOURCES.poll = setTimeout(() => { if (route[0] === "sources" && !document.hidden) load(true, true); }, 1000);
  },
};

async function sourcePreview() {
  const host = $("#source-preview"), seq = ++SOURCES.previewSeq;
  if (!host || !SOURCES.user) { if (host) host.textContent = "选择接收用户后查看。"; return; }
  host.setAttribute("aria-busy", "true");
  try {
    const result = await api(`/api/users/${encodeURIComponent(SOURCES.user)}/subscription-preview?format=${encodeURIComponent(SOURCES.format)}`);
    if (seq !== SOURCES.previewSeq || !host.isConnected) return;
    SOURCES.preview = result;
    host.innerHTML = val(H`${result.status !== "active" ? H`<p class="sub">用户当前不可用，订阅仅显示状态占位。</p>` : ""}${result.nodes.length ? result.nodes.map(n => H`<div class="row source-preview-row"><div class="grow source-name"><b>${n.name}</b><div class="sub">${n.source_name} · ${n.protocol}</div>${n.reasons?.map(reason => H`<div class="c-bad sub">${reason}</div>`)}</div><span class="sub">${n.groups.join("、")}</span></div>`) : H`<p class="sub">当前没有可输出的节点。</p>`}${sourceIssues(result.issues)}`);
  } catch (e) { if (seq === SOURCES.previewSeq && host.isConnected) host.innerHTML = val(H`<p class="c-bad" role="alert">${e.message}</p><button type="button" class="btn" data-act="source-preview-retry">重试预览</button>`); }
  finally { if (host.isConnected) host.removeAttribute("aria-busy"); }
}

function sourceIssues(issues) {
  if (!issues?.length) return "";
  return H`<details class="mt"><summary>查看 ${issues.length} 条兼容说明</summary>${issues.map(i => H`<div class="row sub"><span>${i.index ? "第 " + i.index + " 条 · " : ""}${i.name}</span><span>${i.reason}</span></div>`)}</details>`;
}

// Keep focus inside this form. Cleanup also runs when another drawer replaces it.
function sourceDrawerFocus(form, trigger) {
  SOURCES.releaseDrawer?.();
  const drawer = $("#drawer"), root = $("#root");
  drawer.setAttribute("aria-labelledby", "source-drawer-title"); root.inert = true;
  const key = e => {
    if (e.key !== "Tab") return;
    const items = [...drawer.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), a[href], summary, [tabindex="0"]')].filter(el => el.getClientRects().length);
    if (!items.length) return;
    if (e.shiftKey && (document.activeElement === items[0] || !items.includes(document.activeElement))) { e.preventDefault(); items.at(-1).focus(); }
    else if (!e.shiftKey && document.activeElement === items.at(-1)) { e.preventDefault(); items[0].focus(); }
  };
  drawer.addEventListener("keydown", key);
  const cleanup = () => {
    root.inert = false; drawer.removeEventListener("keydown", key); drawer.removeAttribute("aria-labelledby"); observer.disconnect();
    if (SOURCES.releaseDrawer === cleanup) SOURCES.releaseDrawer = null;
  };
  const observer = new MutationObserver(() => {
    if (form.isConnected && drawer.classList.contains("open")) return;
    cleanup();
    if (!drawer.classList.contains("open") && trigger?.isConnected) trigger.focus();
  });
  SOURCES.releaseDrawer = cleanup;
  observer.observe(drawer, { attributes: true, attributeFilter: ["class"], childList: true, subtree: true });
  requestAnimationFrame(() => form.querySelector("input")?.focus());
}

function sourceRuntime(src) {
  return H`<div class="flex8">${sourceStatus(src)}<span class="sub">${src.node_count} 个节点 · ${src.input_format || "尚未识别格式"}</span></div>${src.last_error ? H`<p class="c-bad" role="alert">${src.last_error}${src.last_good_at ? "；正在使用 " + new Date(src.last_good_at).toLocaleString() + " 的结果。" : ""}</p>` : ""}${src.snapshot_stale ? H`<p class="sub">旧地址结果，等待新地址刷新。</p>` : ""}`;
}
function sourceFormatDetails(src) {
  return H`${sourceFormatsUI.map(([f, label]) => H`<div class="row"><b class="grow">${label}</b><span class="sub">${src.formats[f]?.count || 0} 个节点</span></div>${sourceIssues(src.formats[f]?.issues)}`)}${sourceIssues(src.issues)}`;
}

async function sourceEditor(id, trigger) {
  let src = null, grants = { users: [] }, users = [];
  if (id) {
    const packet = await readDrawer("订阅详情", () => Promise.all([api(sourcePath(id)), api(sourcePath(id) + "/grants"), subUsers(api)]), () => sourceEditor(id, trigger), "wide");
    if (!packet) return;
    [src, grants, users] = packet.value;
  }
  const title = src ? src.name : "添加外部订阅";
  openDrawer(H`<div class="source-drawer-frame"><div class="dh"><h2 id="source-drawer-title" tabindex="-1">${title}</h2><span class="sp"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div><div class="db">
    ${src ? H`<div id="source-runtime" aria-live="polite">${sourceRuntime(src)}</div>` : ""}
    <form id="source-form" novalidate><div class="field"><label for="source-name">来源名称</label><input class="in" id="source-name" name="name" maxlength="64" value="${src?.name || ""}" required></div><div class="field"><label for="source-url">${src ? "替换订阅地址" : "订阅地址"}</label><input class="in mono" id="source-url" name="url" type="url" autocomplete="off" spellcheck="false" aria-describedby="source-url-hint source-err" placeholder="${src?.url_masked || "https://"}"><span class="sub" id="source-url-hint">${src ? "留空保留当前地址。新地址刷新成功前，继续使用上次结果。" : "支持 Clash、sing-box 与分享链接订阅。添加后默认不分配给用户。"}</span></div><div class="two"><div class="field"><label for="source-interval">刷新间隔（小时）</label><input class="in" id="source-interval" name="interval" type="number" min="0" step="any" value="${src ? src.interval_seconds / 3600 : 6}" aria-describedby="source-interval-hint source-err"><span class="sub" id="source-interval-hint">0 表示仅手动刷新。</span></div><div class="field"><label for="source-region">默认地区（选填）</label><input class="in" id="source-region" name="region" maxlength="2" value="${src?.region || ""}" placeholder="如 JP"></div></div><div class="err" id="source-err" role="alert"></div><div class="flex8 end"><button type="submit" class="btn pri" id="source-save">${src ? "保存配置" : "添加并拉取"}</button></div></form>
    ${src ? H`<section class="mt"><h3>接收用户</h3><p class="sub">这些用户共享上游凭证。撤回授权后，不再提供这些节点。已保存的凭证仍需由上游轮换。</p><form id="source-grants-form"><div class="two source-user-list">${users.length ? users.map(u => H`<label class="chk"><input type="checkbox" name="user" value="${u.username}" ${raw(grants.users.includes(u.username) ? "checked" : "")}>${u.username}</label>`) : H`<p class="sub">还没有用户。添加用户后可分配。</p>`}</div><div class="err" id="source-grants-err" role="alert"></div><div class="flex8 end mt8"><button type="submit" class="btn" id="source-grants-save">保存接收用户</button></div></form></section><section class="mt"><h3>格式兼容</h3><div id="source-format-results">${sourceFormatDetails(src)}</div></section><div class="flex8 mt"><button type="button" class="btn" data-act="source-refresh" data-id="${src.id}" ${!src.enabled || src.refreshing ? raw("disabled") : ""}>${ic("refresh")}刷新来源</button><button type="button" class="btn" data-act="source-toggle" data-id="${src.id}" data-enabled="${src.enabled ? "0" : "1"}">${src.enabled ? "停用" : "恢复"}</button><span class="sp"></span><button type="button" class="btn danger" data-act="source-delete" data-id="${src.id}" data-name="${src.name}">删除</button></div>` : ""}
  </div></div>`, "wide");
  const form = $("#source-form"); sourceDrawerFocus(form, trigger);
  const updateResult = async () => {
    if (!form.isConnected || document.hidden) return;
    try {
      const current = await api(sourcePath(src.id));
      if (!form.isConnected) return;
      $("#source-runtime").innerHTML = val(sourceRuntime(current));
      $("#source-format-results").innerHTML = val(sourceFormatDetails(current));
      const refreshButton = $("#drawer [data-act=source-refresh]");
      if (refreshButton) refreshButton.disabled = !current.enabled || current.refreshing;
      if (current.refreshing) setTimeout(updateResult, 1000);
    } catch (err) { if (form.isConnected) $("#source-runtime").innerHTML = val(H`<p class="c-bad" role="alert">读取刷新结果失败：${err.message}</p><button type="button" class="btn" data-act="source-open" data-id="${src.id}">重试读取</button>`); }
  };
  if (src?.refreshing) setTimeout(updateResult, 1000);
  form.addEventListener("submit", async e => {
    e.preventDefault(); const error = $("#source-err"), name = form.elements.name.value.trim(), url = form.elements.url.value.trim(), seconds = Number(form.elements.interval.value) * 3600;
    for (const el of form.querySelectorAll("input")) el.removeAttribute("aria-invalid");
    if (!name || (!src && !url) || form.elements.interval.value === "" || !Number.isSafeInteger(seconds) || seconds < 0) {
      error.textContent = "请填写来源名称、有效订阅地址和非负刷新间隔。";
      const bad = !name ? form.elements.name : !src && !url ? form.elements.url : form.elements.interval; bad.setAttribute("aria-invalid", "true"); bad.focus(); return;
    }
    const body = { name, interval_seconds: seconds, region: form.elements.region.value.trim(), ...(src ? { revision: src.revision } : {}) }; if (url) body.url = url;
    try {
      const saved = await withFeedback($("#source-save"), () => write(src ? "PATCH" : "POST", src ? sourcePath(src.id) : "/api/sources", body));
      if (!form.isConnected) return;
      form.elements.url.value = ""; closeOverlays(); toast(src ? "来源配置已保存" : "来源已添加，正在拉取节点"); await load(true, true);
      if (!src) sourceEditor(saved.id, trigger);
    } catch (err) { if (form.isConnected) { error.textContent = err.message; form.elements.url.setAttribute("aria-invalid", "true"); } }
  });
  $("#source-grants-form")?.addEventListener("submit", async e => {
    e.preventDefault(); const f = e.currentTarget;
    try { await withFeedback($("#source-grants-save"), () => write("PUT", sourcePath(src.id) + "/grants", { users: [...f.querySelectorAll("input:checked")].map(x => x.value) })); toast("接收用户已保存，客户端刷新订阅后生效"); }
    catch (err) { if (f.isConnected) $("#source-grants-err").textContent = err.message; }
  });
}

Object.assign(ACTS, {
  "source-new": (_, el) => sourceEditor(null, el),
  "source-open": (d, el) => sourceEditor(d.id, el),
  "source-preview-retry": () => sourcePreview(),
  "source-refresh": async (d, el) => { await write("POST", sourcePath(d.id) + "/refresh"); toast("已开始刷新，可在来源详情查看结果"); if (el.closest("#drawer")) await sourceEditor(d.id); },
  "source-toggle": async d => { await write("PATCH", sourcePath(d.id), { enabled: d.enabled === "1" }); closeOverlays(); toast(d.enabled === "1" ? "已恢复来源" : "已停用来源，后续订阅不再输出这些节点"); },
  "source-delete": d => confirmBox({ title: `删除订阅来源 ${d.name}？`, text: "删除来源、快照及用户授权。其他来源和自建节点不受影响。已保存的上游凭证仍可能可用。", ok: "删除", danger: true, run: async () => { await write("DELETE", sourcePath(d.id)); closeOverlays(); toast("已删除来源"); } }),
});
