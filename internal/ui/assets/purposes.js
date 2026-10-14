// 用途页:列表、完整 JSON 的 create / set / del、订阅预览,以及节点/入口的用途标签
// (metadata get / set)。发出的请求与 mpx purpose 一致:JSON 按 model.Purpose 的字段
// 重新摆一遍并补上同样的默认值(Normalize),校验以控制面为准。
"use strict";

const purposePath = s => "/api/purposes/" + encodeURIComponent(s);
const PURPOSE_KINDS = { daily: "日常", media: "流媒体", download: "下载", game: "游戏" };
const MEDIA_DOMAINS = ["netflix.com", "nflxvideo.net", "youtube.com", "googlevideo.com", "disneyplus.com", "dssott.com", "spotify.com"];
// asModelPurpose:等价于 CLI 的 json.Unmarshal 进 model.Purpose + p.Slug = SLUG + Normalize。
function asModelPurpose(v, slug) {
  if (!v || typeof v !== "object" || Array.isArray(v)) throw new Error("用途要是一个 JSON 对象");
  if (v.slug && v.slug !== slug) throw new Error("slug 与参数不一致");
  const str = k => { const x = v[k]; if (x != null && typeof x !== "string") throw new Error(`${k} 要是字符串`); return x || ""; };
  const list = k => { const x = v[k]; if (x == null) return []; if (!Array.isArray(x) || x.some(y => typeof y !== "string")) throw new Error(`${k} 要是字符串数组`); return x; };
  const o = { slug, name: str("name"), description: str("description"), kind: str("kind") };
  for (const k of ["regions", "lines", "tags", "domains"]) { const x = list(k); if (x.length) o[k] = x; }
  o.strategy = str("strategy") || "url-test";
  o.test_url = str("test_url") || "https://www.gstatic.com/generate_204";
  if (v.interval != null && !Number.isInteger(v.interval)) throw new Error("interval 要是整数");
  o.interval = v.interval || 300;
  if (o.kind === "media" && !o.domains) o.domains = MEDIA_DOMAINS.slice();
  const caps = list("required_capabilities"); if (caps.length) o.required_capabilities = caps;
  for (const k of ["capability_region", "daily_purpose"]) { const x = str(k); if (x) o[k] = x; }
  if (v.templates && typeof v.templates === "object" && Object.keys(v.templates).length) o.templates = v.templates;
  return o;
}
const PURPOSE_STARTER = { name: "", description: "", kind: "daily", regions: [], lines: [], tags: [], strategy: "url-test" };

PAGES.purposes = {
  title: "用途", nav: ["配置", "tpl", 40], closeTo: "purposes",
  crumb: r => r[1] ? H`<a href="#purposes">用途</a><i>/</i><b>${r[1]}</b>` : H`<b>用途</b>`,
  async load(r) {
    const [purposes, detail] = await Promise.all([api("/api/purposes"), r[1] ? api(purposePath(r[1])) : null]);
    return { purposes, detail };
  },
  render(d) {
    const W = can("POST", "/api/purposes");
    const head = H`<div class="ph"><div><h1>用途</h1><p>日常、流媒体、下载、游戏分组:按地区、线路、标签筛选节点 · 对应 <code>mpx purpose</code></p></div><div class="sp"></div>${W ? H`<button class="btn pri" data-act="purpose-new">${ic("plus")}新建用途</button>` : ""}</div>`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(4, 4)}</tbody></table></div>`;
    const P = Array.isArray(d.purposes) ? d.purposes : d.purposes?.purposes || [];
    const list = P.length ? H`<div class="tw"><table><thead><tr><th>用途</th><th>种类</th><th class="hide-m">筛选</th><th class="hide-m">策略</th><th class="hide-m">域名</th></tr></thead><tbody>${P.map(p => H`<tr data-href="#purposes/${encodeURIComponent(p.slug)}" tabindex="0"><td><b class="bold">${p.name || p.slug}</b><div class="sub mono">${p.slug}</div></td><td>${PURPOSE_KINDS[p.kind] || p.kind}</td>
      <td class="hide-m sub">${[...(p.regions || []).map(x => "地区 " + x), ...(p.lines || []).map(x => "线路 " + x), ...(p.tags || []).map(x => "标签 " + x)].join(" · ") || "全部已授权节点"}</td><td class="hide-m mono sub">${p.strategy}</td><td class="hide-m sub">${(p.domains || []).length || "—"}</td></tr>`)}</tbody></table></div>`
      : emptyBox("tpl", "还没有用途", "用途把已授权节点按地区、线路、标签分组,给流媒体、下载、游戏各配一组。", "mpx purpose create media -f media.json", W ? H`<button class="btn pri sm" data-act="purpose-new">${ic("plus")}新建用途</button>` : "");
    return H`${head}<div class="card">${list}</div>
      <div class="card mt"><div class="ch">节点 / 入口标签<small>· mpx purpose metadata</small></div>
        <div class="row wrap8"><select class="in w120" id="md-kind"><option value="nodes">节点</option><option value="hosts">入口</option></select><input class="in mono w120" id="md-id" placeholder="ID" inputmode="numeric"><button class="btn sm" data-act="md-load">读取</button><span class="sub">入口上非空的字段覆盖节点的。</span></div>
        <div id="md-box"></div></div>`;
  },
  after(d, r) { if (d && r[1] && d.detail) purposeDrawer(d.detail); },
};

function purposeDrawer(p) {
  const isNew = !p, W = can(isNew ? "POST" : "PUT", "/api/purposes/x");
  const body = isNew ? PURPOSE_STARTER : (({ slug, ...rest }) => rest)(p);
  openDrawer(H`<form id="pp-form" novalidate><div class="dh"><div style="min-width:0"><h2 class="ell">${isNew ? "新建用途" : p.name || p.slug}</h2><div class="sub mono">${isNew ? "完整 JSON,与 mpx purpose create 收的一样" : p.slug}</div></div><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    ${isNew ? H`<div class="field"><label for="pp-slug">slug</label><input class="in mono" id="pp-slug" placeholder="media" autocomplete="off"><span class="h">小写字母开头,字母、数字、连字符,最多 48 位</span></div>` : ""}
    <div class="field"><label for="pp-json">用途 JSON</label><textarea class="in tpl short" id="pp-json" spellcheck="false" ${W ? "" : raw("readonly")}>${JSON.stringify(body, null, 2)}</textarea>
    <span class="h">kind:daily / media / download / game;strategy 留空为 url-test;非日常用途要配 domains。</span></div>
    <div class="field mt"><label>等价命令</label><div class="cmd" id="pp-cmd">${isNew ? "mpx purpose create <slug> -f purpose.json" : `mpx purpose set ${p.slug} -f purpose.json`}</div></div>
    <div class="err" id="pp-err" role="alert"></div>
    ${isNew ? "" : H`<div class="sub mb8 mt22">预览 · 含凭证,别截图外发</div><div class="flex8 wrap8"><input class="in mono w160" id="pv-user" placeholder="用户名" autocomplete="off"><select class="in w120" id="pv-fmt"><option>clash-meta</option><option>sing-box</option></select><button type="button" class="btn sm" data-act="purpose-preview" data-slug="${p.slug}">预览</button></div><pre class="code mt hidden" id="pv-out"></pre>
      ${can("DELETE", "/api/purposes/x") ? H`<div class="card danger-zone mt22"><div class="row"><div><b class="bold">删除用途</b><div class="sub">已发出的用途 URL 随即失效。</div></div><span class="sp"></span><button type="button" class="btn sm c-bad" data-act="purpose-rm" data-slug="${p.slug}">删除</button></div></div>` : ""}`}
  </div><div class="df"><button type="button" class="btn" data-act="close">取消</button>${W ? H`<button type="submit" class="btn pri" id="pp-save">${isNew ? "创建" : "保存"}</button>` : ""}</div></form>`);
  const slugIn = $("#pp-slug");
  slugIn?.addEventListener("input", () => { $("#pp-cmd").textContent = `mpx purpose create ${slugIn.value.trim() || "<slug>"} -f purpose.json`; });
  $("#pp-form").addEventListener("submit", async e => {
    e.preventDefault();
    const btn = $("#pp-save");
    try {
      const slug = isNew ? slugIn.value.trim() : p.slug;
      if (!slug) throw new Error("slug 不能为空");
      let v; try { v = JSON.parse($("#pp-json").value); } catch (e3) { throw new Error("JSON 解析失败:" + e3.message); }
      const body = asModelPurpose(v, slug);
      btn.disabled = true; btn.classList.add("busy");
      await write(isNew ? "POST" : "PUT", isNew ? "/api/purposes" : purposePath(slug), body, `用途 ${slug} 已保存`);
      closeOverlays(); location.hash = "purposes/" + encodeURIComponent(slug); load(true, true);
    } catch (e2) { $("#pp-err").textContent = e2.message; if (btn) { btn.disabled = false; btn.classList.remove("busy"); } }
  });
}

function metadataForm(kind, id, m) {
  const W = can("PUT", `/api/${kind}/x`);
  $("#md-box").innerHTML = val(H`<form id="md-form" class="pad16" novalidate><div class="grid g3">
    <div class="field"><label for="md-region">地区</label><input class="in mono" id="md-region" value="${m.region || ""}" placeholder="HK"></div>
    <div class="field"><label for="md-line">线路</label><input class="in mono" id="md-line" value="${m.line || ""}" placeholder="cn2"></div>
    <div class="field"><label for="md-purposes">用途标签</label><input class="in mono" id="md-purposes" value="${(m.purposes || []).join(",")}" placeholder="逗号分隔"></div></div>
    <div class="err" id="md-err" role="alert"></div>
    <div class="flex8"><div class="cmd grow">mpx purpose metadata set ${kind} ${id} -f metadata.json</div>${W ? H`<button class="btn pri sm" id="md-save">保存</button>` : ""}</div></form>`);
  $("#md-form").addEventListener("submit", async e => {
    e.preventDefault();
    const body = { region: $("#md-region").value.trim(), line: $("#md-line").value.trim() };
    const ps = splitCSV($("#md-purposes").value); if (ps.length) body.purposes = ps;
    try { await write("PUT", `/api/${kind}/${id}/metadata`, body, `已保存 ${kind === "nodes" ? "节点" : "入口"} ${id} 的标签`); } catch (e2) { $("#md-err").textContent = e2.message; }
  });
}

Object.assign(ACTS, {
  "purpose-new": () => purposeDrawer(null),
  "purpose-preview": async (d, b) => busy(b, async () => {
    const u = $("#pv-user").value.trim();
    if (!u) throw new Error("先填用户名");
    const out = await api(`${purposePath(d.slug)}/preview/${encodeURIComponent(u)}/${$("#pv-fmt").value}`, { headers: { Accept: "*/*" } });
    const pre = $("#pv-out"); pre.textContent = typeof out === "string" ? out : JSON.stringify(out, null, 2); pre.classList.remove("hidden");
  }),
  "purpose-rm": d => confirmBox({ title: `删除用途 ${d.slug}?`, text: "不可恢复:已发出的用途 URL 随即失效。", cmd: `mpx purpose del ${d.slug}`, ok: "删除用途", danger: true, typed: d.slug,
    run: async () => { await write("DELETE", purposePath(d.slug), undefined, `用途 ${d.slug} 已删除`); closeOverlays(); location.hash = "purposes"; } }),
  "md-load": (d, b) => busy(b, async () => {
    const kind = $("#md-kind").value, id = $("#md-id").value.trim();
    if (!/^[1-9]\d*$/.test(id)) throw new Error("ID 必须为正整数");
    metadataForm(kind, id, await api(`/api/${kind}/${id}/metadata`));
  }),
});
