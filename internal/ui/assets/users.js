// 用户页:列表、详情抽屉、新建/编辑、以及 enable / disable / renew / reset-traffic /
// revoke-sub / rm。每个写操作发出的请求与对应的 mpx user 命令一致(读-改-写整体
// 回写、同样的字段与取值),e2e 测试逐条对照 CLI 实际发出的请求。
"use strict";

/* ---- 与 CLI 同一套解析(internal/cli/user.go) ---- */
// parseBytes:B/KB/MB/GB/TB 按 1024 进位,可带小数。
function parseBytes(s) {
  let text = String(s).trim();
  if (!text) throw new Error("大小不能为空");
  let mult = 1;
  for (const [suf, m] of [["TB", 2 ** 40], ["GB", 2 ** 30], ["MB", 2 ** 20], ["KB", 2 ** 10], ["B", 1]]) {
    if (text.toUpperCase().endsWith(suf)) { mult = m; text = text.slice(0, -suf.length).trim(); break; }
  }
  if (!text) throw new Error(`大小缺少数字： "${s}"`);
  if (/^[+-]?\d+$/.test(text)) { const n = Number(text); if (n < 0) throw new Error(`大小不能是负数： "${s}"`); return n * mult; }
  const f = Number(text);
  if (!/^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(text) || !isFinite(f)) throw new Error(`无法解析大小 "${s}"，单位用 B/KB/MB/GB/TB`);
  if (f < 0) throw new Error(`大小不能是负数： "${s}"`);
  return Math.trunc(f * mult);
}
// parseExpire:YYYY-MM-DD → 本机时区当天 23:59:59(和 CLI 一样以操作机时区为准)。
function parseExpire(s) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(s).trim());
  const d = m && new Date(+m[1], +m[2] - 1, +m[3], 23, 59, 59);
  if (!d || d.getMonth() !== +m[2] - 1 || d.getDate() !== +m[3]) throw new Error(`到期日要写成 YYYY-MM-DD: "${s}"`);
  return d.toISOString();
}
// renewTarget:从「当前到期日与现在中较晚的那个」往后数整天,落在当天 23:59:59。
function renewTarget(current, days, now = new Date()) {
  const cur = current ? new Date(current) : null;
  const base = cur && cur > now ? cur : now;
  return new Date(base.getFullYear(), base.getMonth(), base.getDate() + days, 23, 59, 59).toISOString();
}
const USERNAME_RE = /^[A-Za-z0-9_.-]{1,64}$/;
// asModelUser:把 GET 回来的 userView 摆成 model.User 的 JSON 形状 —— CLI 把它解进
// model.User 再整体 PUT 回去,这里发出的就是同一份(同样的字段、同样的 omitempty)。
function asModelUser(v) {
  const o = { id: v.id || 0, username: v.username };
  if (v.key) o.key = v.key;
  if (v.sub_token) o.sub_token = v.sub_token;
  o.enabled = !!v.enabled;
  o.data_limit = v.data_limit || 0;
  o.used_traffic = v.used_traffic || 0;
  o.lifetime_used_traffic = v.lifetime_used_traffic || 0;
  if (v.expire_at) o.expire_at = v.expire_at;
  o.reset_strategy = v.reset_strategy || "";
  if (v.traffic_reset_at) o.traffic_reset_at = v.traffic_reset_at;
  if (v.inbound_ids && v.inbound_ids.length) o.inbound_ids = v.inbound_ids;
  if (v.note) o.note = v.note;
  o.created_at = v.created_at || "0001-01-01T00:00:00Z";
  return o;
}
const userPath = n => "/api/users/" + encodeURIComponent(n);
async function readModify(name, change) {
  const u = asModelUser(await api(userPath(name)));
  change(u);
  const res = await api(userPath(name), { method: "PUT", body: JSON.stringify(u), headers: { "Content-Type": "application/json" } });
  afterWrite();
  return res;
}

/* ---- 用户列表的「套餐」列 ---- */
// userPlanCell 显示用户挂的套餐名,并在有额外授权时加 +N —— 只数用户自己的
// inbound_ids 里不在套餐范围内的入站;套餐是 all_nodes 时没有「额外」可言。
// 没挂套餐时显示「全部入站」或「单独授权 · N 个入站」(旧语义)。
function userPlanCell(u, d) {
  const plans = d.plans || [], inbounds = d.inbounds || [];
  const p = plans.find(x => x.id === u.plan_id);
  if (!p) {
    const n = (u.inbound_ids || []).length;
    return n ? H`<span class="pl-ptag"><span class="tag g" title="单独授权的 ${n} 个连接方式">单独授权 · ${n} 个连接方式</span></span>`
             : H`<span class="sub">全部连接方式</span>`;
  }
  const extra = extrasOf(u, p, inbounds);
  return H`<span class="pl-ptag"><span class="tag ${p.is_default ? "g" : ""}">${p.name}</span>${extra.length ? H`<span class="sub" title="额外授权：${extra.map(i => planInboundLabel(i)).join("、")}">+${extra.length}</span>` : ""}</span>`;
}

// extrasOf 返回用户额外授权里不在套餐范围内的入站对象;all_nodes 套餐固定为空。
function extrasOf(u, p, inbounds) {
  if (p.all_nodes) return [];
  const set = planEffectiveSet(p, inbounds);
  return (u.inbound_ids || []).filter(id => !set.has(id)).map(id => inbounds.find(i => i.id === id)).filter(Boolean);
}

// inboundDisplay 把入站 id 摆成入站标签;找不到时退回 #id。
function inboundDisplay(id) { return planInboundLabel((PLAN_CACHE.inbounds || []).find(i => i.id === id) || { id }); }

// userPlanDetail 是用户详情里的套餐描述。
function userPlanDetail(u) {
  const p = (PLAN_CACHE.plans || []).find(x => x.id === u.plan_id);
  if (!p) return (u.inbound_ids || []).length ? H`单独授权 · ${u.inbound_ids.length} 个连接方式` : H`全部连接方式`;
  return p.all_nodes ? H`${p.name} · 全部节点` : H`${p.name} · ${(p.node_ids || []).length} 个节点`;
}

/* ---- 列表状态 ---- */
const PAGE_SIZE = 50;
// dir 是「最近在线」这一列的排序方向:-1 最新在前(默认),1 反过来;从未连接的两边都在最后。
const US = { status: "", search: "", plan: "", offset: 0, dir: -1 };
const STATUS_TABS = [["", "全部"], ["active", "正常"], ["limited", "已超限"], ["expired", "已过期"], ["disabled", "已停用"]];

// 用户页顶部的「用户 | 套餐」页签:套餐是用户的属性,不再单独占侧栏一级(#users/plans)。
const userTabs = plans => PAGES.plans ? H`<div class="tabs"><a class="${plans ? "" : "on"}" href="#users">用户</a><a class="${plans ? "on" : ""}" href="#users/plans">套餐</a></div>` : "";

/* ---- 「最近在线」的时间 ---- */
// tzLabel 按时区算标签(浏览器的时区偏移,和操作者看到的钟一致)。
const tzLabel = () => { const o = -new Date().getTimezoneOffset() / 60; return `UTC${o >= 0 ? "+" : ""}${o}`; };
// 悬停提示里的绝对时间:「2026 年 10 月 17 日 21:14:40（UTC+8）」。
const absTime = iso => { const d = new Date(iso); return `${d.getFullYear()} 年 ${d.getMonth() + 1} 月 ${d.getDate()} 日 ${hms(d)}（${tzLabel()}）`; };
// agoShort 是给「几十秒前」这种短跨度用的:采样 30 秒一次,这里只说个大概。
const agoShort = iso => { const s = Math.max(0, (Date.now() - new Date(iso)) / 1e3); return s < 60 ? `${Math.max(1, Math.round(s))} 秒前` : s < 3600 ? `${Math.floor(s / 60)} 分钟前` : relTime(iso); };
// 一段使用:「今天 07:42 – 现在」「昨天 21:14 – 23:50」。start 是这几天内时,当天写时刻。
function spanText(sg) {
  const a = new Date(sg.start), b = sg.end ? new Date(sg.end) : null;
  const head = relTime(sg.start).replace(/^(\d+ 分钟前|刚刚)$/, `今天 ${hm(a)}`);
  const day = /日$/.test(head) ? `${head} ${hm(a)}` : head;
  return `${day} – ${b ? (sameDay(a, b) ? hm(b) : relTime(sg.end)) : "现在"}`;
}
// 中文和字母数字之间才加空格:「42 分钟前拉取过」「Shadowrocket 10 月 16 日拉取过」。
const cat = (a, b) => /[A-Za-z0-9]$/.test(a) || /^[A-Za-z0-9]/.test(b) ? `${a} ${b}` : a + b;

/* ---- 「最近在线」这一列 ---- */
// 界面上说节点一律用显示名(nodeLabel);内部名只在节点详情里作次要信息出现。
const presNodeText = (d, id) => { const n = (d.nodes || []).find(x => x.id === id); return n ? nodeLabel(n) : `节点 #${id}`; };
// 提示是纯文本,旗帜在里面只会显示成两个字母:去掉前缀,只留地区名。
const bareNode = s => s.replace(/^\S+ /, "");
// 控制面这次没算出在线状态:顶层没有 presence,或者给了 presence 却没有更新时间。
// 这时不猜 —— 不说「在线」,也不说「从未连接」,只报最后有流量的时间。
const presInfo = d => (d && d.page && d.page.presence) || null;
const presKnown = d => { const p = presInfo(d); return !!(p && p.updated_at); };
// 两轮(约 60 秒)没更新就算旧:旧数据不写「在线」,只写最后看到的时间。
const presStale = d => presKnown(d) && Date.now() - new Date(presInfo(d).updated_at) > 90e3;
const staleNodeOf = (d, id) => (presInfo(d)?.stale_nodes || []).find(s => s.node_id === id);
// 只有「节点连不上之前 2 分钟内还有流量」的人才写「可能还在线」;更早就走了的人照常显示时间。
const staleHint = (d, p) => { const s = p.last_node_id ? staleNodeOf(d, p.last_node_id) : null; return s && new Date(p.last_active_at) >= new Date(s.since) - 12e4 ? s : null; };
// 最后一次有流量的绝对时间(悬停提示)。提示是纯文本:节点去掉旗帜,只留地区名。
const activeTip = (d, p) => "最后一次有流量：" + absTime(p.last_active_at) + (p.last_node_id ? " · " + bareNode(presNodeText(d, p.last_node_id)) : "");
// 在线的节点:一个写名字,多个写「洛杉矶 A 等 2 个节点」。
function nodesText(d, ids) {
  const list = ids || [];
  if (!list.length) return "";
  if (list.length === 1) return presNodeText(d, list[0]);
  return `${presNodeText(d, list[0])} 等 ${list.length} 个节点`;
}
// presenceCell 画「最近在线」这一列:在线(带晕圈的实心点、IP 数、节点)、相对时间,
// 或者「从未连接」。IP 数只数来源 IP,不写成「台设备」—— 一台路由器后面一家人只算 1 个。
function presenceCell(u, d) {
  const p = u.presence || {}, pull = (d.pulls || []).find(x => x.username === u.username);
  // 读不到在线状态:只写最后有流量的时间,别的都不猜。
  if (!presKnown(d)) return H`<div class="pres">${p.last_active_at ? H`<span class="pres-l ${Date.now() - new Date(p.last_active_at) > DAY ? "old" : ""}">${relTime(p.last_active_at)}</span>` : ""}</div>`;
  if (p.online && !presStale(d)) {
    const tip = activeTip(d, p) + (p.online_nodes?.length > 1 ? " · " + p.online_nodes.map(id => bareNode(presNodeText(d, id))).join("、") : "");
    return H`<div class="pres" data-tip="${tip}"><span class="pres-l on"><span class="pdot"></span>在线</span><div class="sub ell">${p.online_ips ? `${p.online_ips} 个 IP · ` : ""}${nodesText(d, p.online_nodes)}</div></div>`;
  }
  // 整轮采样落后:不再说「在线」,只说最后看到的时间。
  if (p.online) return H`<div class="pres" data-tip="${activeTip(d, p)}"><span class="pres-l"><span class="pdot hollow"></span>${agoShort(presInfo(d).updated_at)}在线</span><div class="sub ell">${nodesText(d, p.online_nodes)}</div></div>`;
  if (!p.last_active_at) {
    const tip = pull ? `${clientLabel(pull.client)} ${absTime(pull.last_pulled_at)} 拉取过订阅，之后没有流量` : "";
    return H`<div class="pres" ${raw(tip ? `data-tip="${esc(tip)}"` : "")}><span class="pres-l never">从未连接</span>${pull ? H`<div class="sub ell">导入过订阅，还没连上</div>` : ""}</div>`;
  }
  const sn = staleHint(d, p);
  return H`<div class="pres" data-tip="${activeTip(d, p)}"><span class="pres-l ${Date.now() - new Date(p.last_active_at) > DAY ? "old" : ""}">${relTime(p.last_active_at)}</span>${sn ? H`<div class="sub ell">${bareNode(presNodeText(d, p.last_node_id))} 连不上，可能还在线</div>` : ""}</div>`;
}
// 排序键:在线的最前(IP 多的靠前),然后按最后有流量的时间;从未连接的按创建时间排。
function presKey(d, u) {
  const p = u.presence || {};
  if (presKnown(d) && p.online) return 4e12 + (p.online_ips || 0);
  return p.last_active_at ? +new Date(p.last_active_at) : -4e12 + +new Date(u.created_at || 0) / 1e3;
}
// 默认按最近在线倒序,点表头后反过来;从未连接的不管正序倒序都排最后,所以先按「有没有
// 最后在线时间」分成两段,再按 dir 排同段里的先后,最后按用户名兜底(时间一样时顺序才是稳的)。
// 在线的固定在最前(IP 多的靠前):在不在线是「现在」的先后,不跟着方向翻转;同段里再按方向排。
// 只翻转时间会让正序时最久没连的排到在线的人前面(两小时前不算最新,但排第一也说不通)。
const presCompare = d => (a, b) =>
  ((!!b.presence?.online) - (!!a.presence?.online)) ||
  (!!b.presence?.last_active_at - !!a.presence?.last_active_at) ||
  US.dir * (presKey(d, a) - presKey(d, b)) || a.username.localeCompare(b.username);
// 某台节点连不上时的灰条:不是用户做错了什么,只是数据旧了,所以用灰色不用红色。
// 整句是一个 .pres-note 的直接子元素 span,里面不再套块:节点名里的旗帜换成图后仍在这一句里。
const staleNote = (d, sn) => sn ? H`<div class="pres-note">${ic("clock")}<span>${presNodeText(d, sn.node_id)} 从 ${hm(new Date(sn.since))} 起连不上，这台上的在线状态停在 ${agoShort(sn.since)}。</span><a class="lnk" href="#nodes/${sn.node_id}">看节点</a></div>` : "";
// 在线人数放在工具栏右边,不进状态筛选:筛选是账号状态(互斥),在线是另一回事;默认
// 排序已经把在线的人排在最上面。读不到在线状态时直说「读不到」,不写「0 人在线」。
function presSum(d) {
  if (!presKnown(d)) return H`<span class="pres-sum stale-t" data-tip="控制面这次没算出在线状态，刷新页面再试">${ic("clock")}在线状态读不到</span>`;
  const at = presInfo(d).updated_at;
  if (presStale(d)) return H`<span class="pres-sum stale-t" data-tip="${"上一次拿到在线状态：" + absTime(at)}">${ic("clock")}在线状态 ${agoShort(at)}更新</span>`;
  return H`<span class="pres-sum" data-tip="${"在线状态 " + agoShort(at) + "更新，每 30 秒一次"}"><span class="pdot"></span>${presInfo(d).online} 人在线</span>`;
}

PAGES.users = {
  title: "用户", closeTo: "users",
  count: () => USERS_TOTAL ?? STATUS?.counts?.users ?? "",
  crumb: r => r[1] ? H`<a href="#users">用户</a><i>/</i><b>${r[1]}</b>` : H`<b>用户</b>`,
  async load(r) {
    const q = new URLSearchParams({ limit: PAGE_SIZE, offset: US.offset });
    if (US.status) q.set("status", US.status);
    if (US.search) q.set("search", US.search);
    if (US.plan) q.set("plan", US.plan);
    // 各状态的人数:一次取回(≤1000 人)按控制面给的 status 数,不再为每个标签各打一次
    // 请求 —— /api 按 IP 限流,一页五六个请求很容易撞上。人更多时不显示数字。
    const counts = api("/api/users?limit=1000").then(p => {
      const c = { "": p.total };
      if (p.total <= p.users.length) for (const [s] of STATUS_TABS.slice(1)) c[s] = p.users.filter(u => u.status === s).length;
      return c;
    });
    const plans = PLAN_CACHE.loaded ? Promise.resolve(PLAN_CACHE.plans) : api("/api/plans");
    const inbounds = PLAN_CACHE.loaded ? Promise.resolve(PLAN_CACHE.inbounds) : api("/api/inbounds");
    // 「最近在线」这一列要节点的显示名(在线节点、最后所在节点);「导入过订阅，还没连上」
    // 要看订阅拉取记录。两个都读不到时只是少显示一点,不影响列表。
    const nodes = api("/api/nodes").catch(() => []);
    const pulls = api("/api/sub-pulls").catch(() => null);
    // 详情里的「最近连接」是另一个接口:读不到只让这一块说读不到,抽屉其余部分照常。
    const activity = r[1] ? api(userPath(r[1]) + "/activity").then(a => ({ a }), e => ({ error: e.message })) : null;
    // 详情还要:订阅前缀(没设时提示链接里是 IP)。
    const detail = r[1] ? Promise.all([api(userPath(r[1])), api(userPath(r[1]) + "/links"), api("/api/settings").catch(() => null)]) : null;
    const [page, cs, pl, ib, nd, pu, d, act] = await Promise.all([api("/api/users?" + q), counts, plans, inbounds, nodes, pulls, detail, activity]);
    USERS_TOTAL = cs[""];
    PLAN_CACHE.plans = pl || [];
    PLAN_CACHE.inbounds = ib || [];
    PLAN_CACHE.loaded = true;
    return { page, counts: cs, plans: PLAN_CACHE.plans, inbounds: PLAN_CACHE.inbounds, nodes: nd || [], pulls: pu || [],
      user: d && d[0], links: d && d[1], settings: d && d[2],
      pull: pu && r[1] ? pu.find(p => p.username === r[1]) || null : null, pullsKnown: !!pu,
      activity: act && act.a || null, activityError: act && act.error || "" };
  },
  render(d) {
    // 「最近在线」是唯一能排序的列:表头深色并带箭头,aria-sort 说明当前方向。
    const head = H`<tr><th>用户</th><th class="th-sort" aria-sort="${US.dir < 0 ? "descending" : "ascending"}"><button type="button" data-act="pres-sort" title="按最近在线排序">最近在线${ic(US.dir < 0 ? "down" : "chevup", "srt")}</button></th><th>状态</th><th>套餐</th><th>本期用量</th><th class="hide-m">到期</th><th class="hide-m hide-n">重置</th></tr>`;
    let inner;
    if (!d) inner = H`<table><thead>${head}</thead><tbody>${skRows(8, 7)}</tbody></table>`;
    else if (!d.page.users.length) inner = US.status || US.search
      ? emptyBox("search", "没有匹配的用户", "换个筛选条件或搜索词试试。", "")
      : emptyBox("users", "还没有用户", "加一个用户，他就会拿到一条订阅地址。之后续期、停用、重置流量都在这里。", "", can("POST", "/api/users") ? H`<button class="btn pri sm" data-act="user-new">${ic("plus")}添加用户</button>` : "");
    else {
      const P = d.page, end = P.offset + P.users.length;
      const rows = P.users.slice().sort(presCompare(d));
      inner = H`${staleNote(d, (presInfo(d)?.stale_nodes || [])[0])}<div class="tw"><table class="ut"><thead>${head}</thead><tbody>${rows.map(u => H`<tr data-href="#users/${encodeURIComponent(u.username)}" tabindex="0"><td><div class="u">${avatar(u.username)}<div><b class="bold">${u.username}</b>${u.note ? H`<div class="sub">${u.note}</div>` : ""}</div></div></td>
        <td class="td-pres">${presenceCell(u, d)}</td><td>${userSt(u.status)}</td><td>${userPlanCell(u, d)}</td><td>${usageBar(u)}</td><td class="hide-m">${expireCell(u)}</td><td class="hide-m hide-n sub">${RESET[u.reset_strategy] || u.reset_strategy}</td></tr>`)}</tbody></table></div>
        <div class="pager">显示 ${P.offset + 1}–${end}，共 ${P.total} 个<span class="sp"></span><button class="btn sm" data-act="user-page" data-dir="-1" ${P.offset ? "" : raw("disabled")}>上一页</button><button class="btn sm" data-act="user-page" data-dir="1" ${end < P.total ? "" : raw("disabled")}>下一页</button></div>`;
    }
    const c = d ? d.counts : {};
    const planOpts = d ? d.plans.slice().sort((a, b) => (b.is_default - a.is_default) || (a.id - b.id)) : [];
    return H`<div class="ph"><div><h1>用户</h1><p>每个用户一条自己的订阅</p></div><div class="sp"></div>
      <button class="btn hide-m" data-act="user-export">${ic("dl")}导出 CSV</button>
      ${can("POST", "/api/users") ? H`<button class="btn pri" data-act="user-new">${ic("plus")}添加用户</button>` : ""}</div>
      ${userTabs(false)}
      <div class="card"><div class="tb"><div class="seg" role="tablist">${STATUS_TABS.map(([k, t]) => H`<button role="tab" class="${US.status === k ? "on" : ""}" data-act="user-filter" data-status="${k}">${t}<em>${c[k] ?? ""}</em></button>`)}</div>
      <span class="sp" style="flex:1"></span>${d ? presSum(d) : ""}<select class="in w180" id="uf-plan" aria-label="按套餐筛选"><option value="">全部套餐</option>${planOpts.map(p => H`<option value="${p.id}" ${raw(US.plan == p.id ? "selected" : "")}>${p.name}</option>`)}</select>
      <label class="search in-card">${ic("search")}<input type="search" id="user-search" placeholder="搜索用户名" value="${US.search}" autocomplete="off"></label></div>${inner}</div>`;
  },
  after(d, r) {
    const s = $("#user-search");
    if (s) { let t; s.addEventListener("input", () => { clearTimeout(t); t = setTimeout(() => { US.search = s.value.trim(); US.offset = 0; load(true).then(() => { const n = $("#user-search"); n?.focus(); n?.setSelectionRange(n.value.length, n.value.length); }); }, 250); }); }
    const pf = $("#uf-plan");
    if (pf) pf.addEventListener("change", () => { US.plan = pf.value; US.offset = 0; load(true); });
    if (d && r[1] && d.user) userDrawer(drawerUser(d), d.links, d);
  },
};
let USERS_TOTAL = null;

/* ---- 详情抽屉 ---- */
// 套餐里有几台节点:全部节点套餐 = 现有节点数;否则是勾选节点和单独勾选入站所在节点的并集。
function planNodeCount(p, nodes, inbounds) {
  if (!p) return 0;
  const ids = new Set((nodes || []).map(n => n.id));
  if (p.all_nodes) return ids.size;
  const got = new Set((p.node_ids || []).filter(id => ids.has(id)));
  for (const iid of p.inbound_ids || []) { const i = (inbounds || []).find(x => x.id === iid); if (i && ids.has(i.node_id)) got.add(i.node_id); }
  return got.size;
}
// 控制面记的是 User-Agent 第一段,常见的换成客户端自己的写法;认不出的照原样。
const CLIENT_NAMES = { "clash-verge": "Clash Verge", "clash.meta": "Clash Meta", mihomo: "Mihomo", clash: "Clash", shadowrocket: "Shadowrocket", stash: "Stash", "sing-box": "sing-box", sfa: "sing-box", sfi: "sing-box", sfm: "sing-box", v2rayn: "v2rayN", v2rayng: "v2rayNG", karing: "Karing", hiddify: "Hiddify", nekobox: "NekoBox", quantumult: "Quantumult X", surge: "Surge", loon: "Loon" };
const clientLabel = c => !c || c === "unknown" ? "客户端" : CLIENT_NAMES[c.toLowerCase()] || c;
// 「sing-box 42 分钟前拉取过」:和「最近在线」用同一套相对时间,不再写「在 2026-10-16 拉取过」。
function pullText(p) { return cat(`${clientLabel(p.client)} ${relTime(p.last_pulled_at)}`, "拉取过"); }
const SEND_FORMATS = [["links", "分享链接", "一行一个节点，粘贴进 v2rayN、Shadowrocket"], ["base64-links", "Base64", "老客户端认的订阅格式"], ["clash-meta", "Clash Meta", "Clash Verge、Mihomo"], ["clash", "Clash", "老版 Clash 内核"], ["sing-box", "sing-box", "sing-box 官方客户端、Karing"], ["xray", "Xray", "Xray 内核的 JSON 配置"]];
// 「发给他」:创建后和用户详情共用。二维码由本机 /local/qr.svg 画,永远放在白底上(暗色也一样),保证能扫。
function sendBlock(u, links, d) {
  const name = u.username, L = (links && links.auto) || "";
  // 按稿子用 ?u=<名字>,由本机进程向控制面取链接再画;v 让换了新链接后不吃旧图。
  // 本机取不到(比如撞上控制面限流)时,改用页面手里现成的链接 ?d= 画,见 userDrawer。
  // 「保存二维码」直接用 ?d=:点的时候不必再问一次控制面。
  const qr = `/local/qr.svg?u=${encodeURIComponent(name)}&v=${encodeURIComponent(L.slice(-10))}`, qrD = `/local/qr.svg?d=${encodeURIComponent(L)}`;
  const noPrefix = d && d.settings && !d.settings.sub_url_prefix;
  const pull = d && d.pull;
  return H`<section class="send" id="send">
    <div class="send-h"><h3>发给他</h3><span class="sub" id="send-pull">${pull ? pullText(pull) : d && d.pullsKnown ? "还没有客户端导入过" : ""}</span></div>
    <div class="send-b"><div class="qr-tile"><img class="qrs" src="${qr}" data-fb="${qrD}" alt="${name} 的订阅二维码"></div>
      <div class="send-r">
        <button type="button" class="btn pri send-copy" data-act="copy" data-text="${L}">${ic("copy")}复制订阅链接</button>
        <div class="send-link mono" title="${L}">${L.replace(/^https?:\/\//, "")}</div>
        <a class="btn" href="${qrD}" download="${name}-订阅二维码.svg">${ic("dl")}保存二维码</a>
        <p class="send-hint">Clash Verge、Shadowrocket、sing-box、v2rayN 都能直接扫码或粘贴导入，会自动拿到适合它的格式。</p>
      </div></div>
    ${noPrefix ? H`<div class="send-ip"><span class="send-ip-t" title="链接里是服务器 IP，设好订阅地址后新旧链接都能用">${ic("warn")}链接里是服务器 IP，设好订阅地址后新旧链接都能用</span><a class="lnk u-link" href="#sub">去设置</a></div>` : ""}
    <details class="fmts"><summary>${ic("arrow", "car")}其他格式<span class="sub">客户端不认自动识别时再用</span></summary>
      <div class="fmt-l">${SEND_FORMATS.filter(([k]) => links && links[k]).map(([k, n, t]) => H`<div class="fmt"><div class="fmt-t"><b>${n}</b><span class="sub">${t}</span></div><button type="button" class="ib" data-act="copy" data-text="${links[k]}" title="复制 ${n} 链接" aria-label="复制 ${n} 链接">${ic("copy")}</button></div>`)}</div>
    </details></section>`;
}
let JUST_CREATED = "";  // 刚建好的用户:详情顶上多一行「已创建 X」
/* ---- 详情抽屉里的「最近连接」 ---- */
// 这个人连过的话这一块排在最前面(打开抽屉最常看的就是它);刚建好、还没连过的,
// 「发给他」仍然排第一。数据一律以控制面给的为准:读不到就说读不到。
function presenceBlock(u, d) {
  const p = u.presence || {}, a = d.activity || { segments: [], ips: [] }, pull = d.pull;
  const known = presKnown(d), stale = presStale(d), sn = known && p.last_active_at ? staleHint(d, p) : null;
  const upd = !known ? "" : sn
    ? H`<span class="sub udp-upd" data-tip="${absTime(sn.since)}">${ic("clock")}${bareNode(presNodeText(d, sn.node_id))} 的在线状态 ${agoShort(sn.since)}更新</span>`
    : H`<span class="sub udp-upd" data-tip="${absTime(presInfo(d).updated_at)}">${stale ? H`${ic("clock")}` : ""}在线状态 ${agoShort(presInfo(d).updated_at)}更新</span>`;
  let now;
  if (known && p.online && !stale) now = H`<div class="udp-now"><span class="pres-l on"><span class="pdot"></span>在线</span><span class="sub">${p.online_ips ? `${p.online_ips} 个 IP · ` : ""}${nodesText(d, p.online_nodes)}</span></div>`;
  else if (p.last_active_at) now = H`<div class="udp-now"><span class="pres-l">${relTime(p.last_active_at)}</span><span class="sub">最后一次有流量 · ${presNodeText(d, p.last_node_id)}${sn ? "（这台现在连不上，可能还在线）" : ""}</span></div>`;
  else now = H`<div class="udp-now"><span class="pres-l never">还没连接过</span></div>`;
  const segs = (a.segments || []).slice(0, 4);
  const list = segs.length ? H`<div class="udp-list">${segs.map(sg => H`<div class="udp-r" data-tip="${absTime(sg.start) + " 起"}"><span class="udp-t">${spanText(sg)}</span><span class="udp-n ell">${presNodeText(d, sg.node_id)}</span><span class="udp-b">${fmtBytes(sg.bytes)}</span></div>`)}</div>` : "";
  // 只在这个人此刻在线、而且这一轮数据不旧时才报 IP:IP 是「现在」的事,下线了就没有了。
  const ips = known && p.online && !stale ? H`<div class="udp-ips"><span class="sub">此刻的 IP</span>${(a.ips || []).map(x => H`<span class="udp-ip"><span class="mono">${x.ip}</span><span class="sub">${agoShort(x.last_seen)}</span></span>`)}</div>` : "";
  // 最近连接读不到:这一块直说,错误原文收在「详情」里,抽屉其余部分照常画。
  const broken = d.activityError ? H`<p class="sub udp-foot">读不到最近连接</p><details class="sum-raw"><summary>详情${ic("down")}</summary><ul class="summary mono"><li>${d.activityError}</li></ul></details>` : "";
  const hint = !p.last_active_at && pull ? H`<div class="udp-hint">${ic("info")}<div><p>${cat(`${clientLabel(pull.client)} ${relTime(pull.last_pulled_at)}`, "拉取过订阅")}，但之后一直没有流量。多半是客户端里没打开代理，或者连不上节点。</p><a class="btn sm" href="#doctor">${ic("pulse")}去体检</a></div></div>`
    : !p.last_active_at ? H`<p class="sub udp-foot">把上面的二维码或链接发给他，他用起来以后会显示在这里。</p>` : "";
  // 订阅导入单独一行、换了底色:和上面的流量分开,免得「拉过订阅」被当成「在用」。
  const pullRow = H`<div class="udp-pull">${ic("sub")}<div><b>订阅导入</b><span class="sub">${pull ? H`${pullText(pull)} · ${(SEND_FORMATS.find(f => f[0] === pull.format) || [, pull.format])[1]}` : "还没有客户端导入过"}</span></div></div>`;
  return H`<section class="ud-pres" id="ud-pres"><div class="udp-h"><h3>最近连接</h3>${upd}</div>${now}${broken || list}${broken ? "" : ips}${hint}
    ${p.last_active_at ? H`<p class="sub udp-foot">按节点上的流量算，每 30 秒更新一次。<br>客户端开着但没有流量，不算在线。</p>` : ""}${hint && pull ? "" : pullRow}</section>`;
}
function userDrawer(u, links, d) {
  const W = can("PUT", "/api/users/x"), name = u.username;
  const p = (d && d.plans || PLAN_CACHE.plans || []).find(x => x.id === u.plan_id);
  const nn = p ? planNodeCount(p, d && d.nodes, d && d.inbounds || PLAN_CACHE.inbounds) : 0;
  const created = JUST_CREATED === name; JUST_CREATED = "";
  const use = u.data_limit ? H`<div class="bar w100 tall"><i class="${pct(u) >= 100 ? "bad" : pct(u) >= 80 ? "warn" : ""}" style="width:${pct(u).toFixed(1)}%"></i></div><div class="sub mt8">${fmtBytes(u.used_traffic)} / ${fmtBytes(u.data_limit)}${u.reset_strategy && u.reset_strategy !== "none" ? ` · ${RESET[u.reset_strategy]}清零` : ""}</div>` : H`<div class="sub mt8">不限流量</div>`;
  // 连过的人:「最近连接」排最前(最常来看的就是它);刚建好、还没连过的:「发给他」排最前。
  const connected = !!u.presence?.last_active_at && !created;
  // 「最近连接」里已经有「订阅导入」这一行,「发给他」标题右边就不再重复一遍。
  const pres = presenceBlock(u, d), send = sendBlock(u, links, connected ? { ...d, pull: null, pullsKnown: false } : d);
  openDrawer(H`<div class="dh ud-h">${avatar(name)}<div class="ud-t"><h2 class="ell">${name}</h2>${userSt(u.status)}</div><span class="sp" style="flex:1"></span>
    ${W ? H`<button class="ib" title="编辑" aria-label="编辑" data-act="user-edit" data-name="${name}">${ic("pen")}</button>
    <span class="mw"><button class="ib" title="更多" aria-label="更多" aria-haspopup="menu" data-act="user-menu">${ic("more")}</button><div class="menu" role="menu" id="user-menu" hidden>
      <button type="button" data-act="user-renew" data-name="${name}">${ic("clock")}续期 30 天</button>
      <button type="button" data-act="user-reset" data-name="${name}">${ic("undo")}清零本期用量</button>
      <button type="button" data-act="user-toggle" data-name="${name}" data-on="${u.enabled ? "1" : ""}">${ic("pause")}${u.enabled ? "停用" : "启用"}</button>
      <div class="msep"></div>
      <button type="button" data-act="user-revoke" data-name="${name}">${ic("key")}换一条新链接</button>
      <button type="button" class="dg" data-act="user-rm" data-name="${name}">${ic("trash")}删除用户</button></div></span>` : ""}
    <button class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db ud-b">${created ? H`<div class="created">${ic("check")}<span>已创建 <b>${name}</b>。把下面的二维码或链接发给他就行。</span></div>` : ""}
    ${connected ? H`${pres}${send}` : H`${send}${pres}`}
    <section class="ud-use"><div class="sub">本期用量</div><div class="ud-v">${fmtBytes(u.used_traffic)}</div>${use}</section>
    <dl class="kv flat ud-kv"><dt>套餐</dt><dd>${p ? H`${p.name} <span class="sub">· ${nn ? `${nn} 个节点` : "没有节点"}</span>` : userPlanDetail(u)}</dd>
      ${u.inbound_ids?.length ? H`<dt>额外授权</dt><dd>${u.inbound_ids.map(i => H`<span class="tag">${inboundDisplay(i)}</span> `)}</dd>` : ""}
      <dt>到期</dt><dd>${expireCell(u)}</dd>
      <dt>创建于</dt><dd>${ymd(new Date(u.created_at))}</dd>${u.note ? H`<dt>备注</dt><dd>${u.note}</dd>` : ""}</dl>
  </div>`);
  const img = $("#send .qrs");
  img?.addEventListener("error", () => { if (img.dataset.fb && img.src.indexOf("?u=") >= 0) img.src = img.dataset.fb; });
}
// drawerUser:抽屉里画的那个人 = 用户本身 + 最近连接接口带回的在线情况(和列表页打开抽屉时同一份)。
// 列表条目里的 presence 来自另一次请求,可能比 activity 旧;activity 里有就用它的。activity 里没有
// (旧控制面,或者没算出来)时用列表里同一个人的那份,列表没有这个人才当作没有。
const drawerUser = d => {
  const p = d.activity?.presence || d.page?.users?.find(u => u.username === d.user.username)?.presence;
  return p ? { ...d.user, presence: p } : d.user;
};
async function refreshDrawer(name) {
  await load(true, true);
  const d = current.data;
  if (route[0] === "users" && route[1] === name && d?.user) userDrawer(drawerUser(d), d.links, d);
}

/* ---- 编辑器 ---- */
async function userEditor(name) {
  let u = null, inbounds = [], nodes = [], plans = [];
  try { [u, inbounds, nodes, plans] = await Promise.all([name ? api(userPath(name)) : null, api("/api/inbounds"), api("/api/nodes"), api("/api/plans")]); }
  catch (e) { toast(e.message, true); return; }
  const isNew = !u;
  u = u || { username: "", data_limit: 0, expire_at: null, reset_strategy: "none", inbound_ids: [], note: "", enabled: true, plan_id: null };
  const nodeName = id => { const n = nodes.find(x => x.id === id); return n ? nodeLabel(n) : "节点"; };
  const planList = plans.slice().sort((a, b) => (b.is_default - a.is_default) || (a.id - b.id));
  const defPlan = planList.find(p => p.is_default);
  const currentPlan = u.plan_id != null ? String(u.plan_id) : "";
  let plan = isNew ? (defPlan ? String(defPlan.id) : "") : currentPlan;
  const sel = new Set(u.inbound_ids || []);
  const allPlan = planList.find(p => p.all_nodes && planNodeCount(p, nodes, inbounds));
  const limitText = u.data_limit ? (u.data_limit % 2 ** 30 === 0 ? u.data_limit / 2 ** 30 + "GB" : u.data_limit + "B") : "";
  openDrawer(H`<form id="user-form" novalidate><div class="dh"><h2>${isNew ? "添加用户" : "编辑 " + u.username}</h2><span class="sp" style="flex:1"></span><button type="button" class="ib" data-act="close" aria-label="关闭">${ic("x")}</button></div>
  <div class="db">
    <div class="field"><label for="f-name">用户名</label><input class="in" id="f-name" name="username" value="${u.username}" ${isNew ? raw("autofocus") : raw("disabled")} placeholder="alice" autocomplete="off"><span class="h">字母、数字、下划线、点和减号，1–64 位${isNew ? "" : ";建好后不能改"}</span></div>
    <div class="field"><label id="f-plan-l">套餐</label><div class="plans" role="radiogroup" aria-labelledby="f-plan-l" id="f-plans">
      ${!isNew && u.plan_id == null ? H`<label class="plan"><input type="radio" name="plan" value="" checked disabled><span class="rd"></span><span class="plan-t"><b>未挂套餐</b><span class="sub">单独授权;选一个套餐后不能再移出</span></span></label>` : ""}
      ${planList.map(p => { const n = planNodeCount(p, nodes, inbounds); return H`<label class="plan"><input type="radio" name="plan" value="${p.id}" ${raw(plan === String(p.id) ? "checked" : "")}><span class="rd"></span><span class="plan-t"><b>${p.name}</b>${p.note || p.is_default ? H`<span class="sub">${p.note || "新用户默认挂这个"}</span>` : ""}</span><span class="plan-n ${n ? "" : "zero"}">${n ? `${n} 个节点` : "没有节点"}</span></label>`; })}</div>
      ${planList.filter(p => !planNodeCount(p, nodes, inbounds)).map(p => H`<div class="block" role="alert" data-plan="${p.id}" hidden>${ic("warn")}<div><b>「${p.name}」里还没有节点</b><p>挂在它上面的用户什么都连不上。先给套餐加节点，或者换一个套餐。</p><div class="block-a"><a class="btn sm" href="#users/plans">${ic("plus")}给「${p.name}」加节点</a>${allPlan ? H`<button type="button" class="btn sm ghost" data-act="f-plan-all">换成「${allPlan.name}」</button>` : ""}</div></div></div>`)}</div>
    <div class="two"><div class="field"><label for="f-limit">流量上限</label><input class="in" id="f-limit" name="limit" value="${limitText}" placeholder="100GB，留空不限"><span class="h">B / KB / MB / GB / TB，按 1024 进位</span></div>
    <div class="field"><label for="f-expire">到期日</label><input class="in" id="f-expire" name="expire" value="${u.expire_at ? ymd(new Date(u.expire_at)) : ""}" placeholder="YYYY-MM-DD，留空永不"><span class="h">本机时区当天 23:59:59</span></div></div>
    <details class="fmts more" id="f-more"><summary>${ic("arrow", "car")}更多选项<span class="sub">清零周期、额外授权、备注</span></summary><div class="more-b">
    <div class="field"><label>流量重置</label><div class="chips">${Object.entries(RESET).map(([k, t]) => H`<label class="chip"><input type="radio" name="reset" value="${k}" ${(u.reset_strategy || "none") === k ? raw("checked") : ""}>${t}</label>`)}</div></div>
    <div class="field"><label id="f-inb-label">额外授权</label><div class="chips">${inbounds.length ? inbounds.map(i => H`<label class="chip"><input type="checkbox" name="inb" value="${i.id}" ${sel.has(i.id) ? raw("checked") : ""}>${nodeName(i.node_id)} · ${inbTitle(i, null)}</label>`) : H`<span class="sub">还没有连接方式</span>`}</div><span class="h" id="f-inb-h"></span></div>
    <div class="field"><label for="f-note">备注</label><input class="in" id="f-note" name="note" value="${u.note || ""}" placeholder="可选"></div>
    ${isNew ? H`<label class="chk"><input type="checkbox" name="disabled"> 先不启用(建好后手动启用)</label>` : ""}</div></details>
    <div class="err" id="f-err" role="alert"></div>
  </div><div class="df"><span class="sub df-why" id="f-why" hidden>${ic("warn")}选的套餐没有节点</span><span class="sp" style="flex:1"></span><button type="button" class="btn" data-act="close">取消</button><button type="submit" class="btn pri" id="f-save">${isNew ? "创建" : "保存"}</button></div></form>`);
  const form = $("#user-form");
  // 表单 → { ... }:collect 同时把套餐三态收出来 —— 没变就不发 plan_id,变了才发具体 id。
  // 挂的是 all_nodes 套餐时额外授权没有意义:勾了和它一起提交会被控制面摘掉
  // 套餐(store.UpdateUser 的规则),于是固定按空处理 —— 新建不带 inbound_ids,
  // 编辑清空,命令里也不出现 --inbounds(编辑时原本有授权才用 --inbounds "" 清掉)。
  const collect = () => {
    const f = new FormData(form), err = [];
    const username = isNew ? String(f.get("username") || "").trim() : u.username;
    const limitS = String(f.get("limit") || "").trim(), expS = String(f.get("expire") || "").trim();
    const reset = String(f.get("reset") || "none"), note = String(f.get("note") || "");
    const planVal = String(f.get("plan") || "");
    const allNodes = !!planList.find(p => String(p.id) === planVal)?.all_nodes;
    const inb = allNodes ? [] : f.getAll("inb").map(Number);
    const planChanged = isNew ? planVal !== "" : (planVal !== "" && planVal !== currentPlan);
    const planName = planList.find(p => String(p.id) === planVal)?.name || "";
    let limit = 0, exp = null;
    if (isNew && !USERNAME_RE.test(username)) err.push("用户名只能用字母、数字、下划线、点和减号，长度 1-64");
    try { limit = limitS ? parseBytes(limitS) : 0; } catch (e) { err.push(e.message); }
    try { exp = expS ? parseExpire(expS) : null; } catch (e) { err.push(e.message); }
    return { err, username, limit, exp, reset, inb, note, disabled: !!f.get("disabled"), planVal, planChanged };
  };
  const sync = () => {
    const c = collect();
    const p = planList.find(x => String(x.id) === c.planVal);
    const allNodes = !!p && !!p.all_nodes;
    // all_nodes 套餐下额外授权不可选(见 collect),切回普通套餐恢复。
    document.querySelectorAll('#user-form input[name="inb"]').forEach(el => { el.disabled = allNodes; });
    // 选了没有节点的套餐:灰色提示块 + 创建按钮置灰(原因写在底栏)。编辑老用户不拦保存。
    const empty = !!p && !planNodeCount(p, nodes, inbounds);
    form.querySelectorAll(".block[data-plan]").forEach(b => { b.hidden = b.dataset.plan !== c.planVal; });
    if (isNew) { $("#f-why").hidden = !empty; $("#f-save").disabled = empty; }
    const label = $("#f-inb-label"), hint = $("#f-inb-h");
    if (label && hint) {
      if (!p) { label.textContent = "可用连接方式"; hint.textContent = "一个都不选 = 全部连接方式，含以后新加的"; }
      else if (allNodes) { label.textContent = "额外授权"; hint.textContent = "全部节点套餐不需要额外授权"; }
      else { label.textContent = "额外授权(可选)"; hint.textContent = "在套餐之外再给这个用户开放的连接方式"; }
    }
  };
  form.addEventListener("input", sync); form.addEventListener("change", sync); sync();
  // 有已填的低频字段时「更多选项」默认展开,免得编辑时看不到。
  if (!isNew && (u.reset_strategy && u.reset_strategy !== "none" || u.inbound_ids?.length || u.note)) $("#f-more").open = true;
  form.querySelector('[data-act="f-plan-all"]') && form.addEventListener("click", e => {
    if (!e.target.closest('[data-act="f-plan-all"]')) return;
    const r = form.querySelector(`input[name="plan"][value="${allPlan.id}"]`); r.checked = true; r.focus(); sync();
  });
  form.addEventListener("submit", async e => {
    e.preventDefault();
    const c = collect(), btn = $("#f-save");
    if (btn.disabled) return;
    $("#f-err").textContent = c.err.join("\n");
    if (c.err.length) return;
    btn.disabled = true; btn.classList.add("busy");
    try {
      if (isNew) {
        // 与 user add 一致:没给的字段保持零值,model.User 整体序列化;选中的套餐
        // 显式带上 plan_id。
        const body = asModelUser({ username: c.username, enabled: !c.disabled, reset_strategy: c.reset, note: c.note, data_limit: c.limit, expire_at: c.exp, inbound_ids: c.inb });
        if (c.planChanged) body.plan_id = +c.planVal;
        await write("POST", "/api/users", body);
        // 同一个抽屉直接切到这个用户的详情,顶上写「已创建」,下面就是「发给他」。
        JUST_CREATED = c.username;
        location.hash = "users/" + encodeURIComponent(c.username);
      } else {
        await readModify(u.username, m => {
          m.data_limit = c.limit;
          if (c.exp) m.expire_at = c.exp; else delete m.expire_at;
          m.reset_strategy = c.reset;
          if (c.inb.length) m.inbound_ids = c.inb; else delete m.inbound_ids;
          if (c.note) m.note = c.note; else delete m.note;
          if (c.planChanged) m.plan_id = +c.planVal;
        });
        toast(`已更新用户 ${u.username}`);
        await refreshDrawer(u.username);
      }
    } catch (e2) { $("#f-err").textContent = e2.message; btn.disabled = false; btn.classList.remove("busy"); }
  });
}

/* ---- 动作 ---- */
Object.assign(ACTS, {
  "user-filter": d => { US.status = d.status; US.offset = 0; load(true); },
  // 表头点了就换个方向:默认最新在前,再点一次反过来。从未连接的始终在最后。
  "pres-sort": () => { US.dir = -US.dir; paint(); },
  "user-page": d => { US.offset = Math.max(0, US.offset + PAGE_SIZE * +d.dir); load(true); },
  "user-new": () => userEditor(null),
  "user-edit": d => userEditor(d.name),
  "user-export": () => confirmBox({ title: "导出 CSV", text: "导出用户清单暂不支持在窗口里操作。", ok: "知道了", run: async () => {} }),
  "user-renew": async d => {
    try {
      let target;
      await readModify(d.name, m => { target = renewTarget(m.expire_at, 30); m.expire_at = target; });
      toast(`已把用户 ${d.name} 的到期日设为 ${ymd(new Date(target))}`);
      await refreshDrawer(d.name);
    } catch (e) { toast(e.message, true); }
  },
  "user-toggle": d => {
    const off = !!d.on, verb = off ? "停用" : "启用";
    const run = async () => { await readModify(d.name, m => { m.enabled = !off; }); toast(`已${verb}用户 ${d.name}`); await refreshDrawer(d.name); };
    if (!off) return run().catch(e => toast(e.message, true));
    confirmBox({ title: `停用 ${d.name}？`, text: "下一轮收敛(最多 30 秒)把他从所有节点上摘掉，订阅里只剩「已停用」占位节点。记录保留，随时可以再启用。", ok: "停用", run });
  },
  "user-reset": d => confirmBox({ title: `重置 ${d.name} 的本期流量？`, text: "本期用量清零，累计用量不变。超限的用户会在下一轮收敛时恢复。", ok: "重置流量",
    run: async () => { await write("POST", userPath(d.name) + "/reset-traffic", undefined, `已重置用户 ${d.name} 的流量`); await refreshDrawer(d.name); } }),
  // 换链接不是删除:不用红色,但要确认 —— 旧链接马上失效,他得重新导入。
  "user-revoke": d => confirmBox({ title: `给 ${d.name} 换一条新链接？`, text: "旧链接马上失效，他的客户端要重新扫码或粘贴新链接才能更新订阅。已经连着的不会断。", ok: "换一条新链接",
    run: async () => { await write("POST", userPath(d.name) + "/revoke-sub", undefined, "已换成新链接，旧链接已失效"); await refreshDrawer(d.name); } }),
  // ⋯ 菜单:点按钮开合;点菜单里的项或别处就收起。
  "user-menu": () => { const m = $("#user-menu"); if (m) m.hidden = !m.hidden; },
  "user-rm": d => confirmBox({ title: `删除 ${d.name}？`, text: "不可恢复：协议凭证与订阅 token 一起删掉，已经连上的客户端会断开，订阅地址立刻 404。只想让人连不上，用「停用」。", ok: "删除用户", danger: true, typed: d.name,
    run: async () => { await write("DELETE", userPath(d.name), undefined, `已删除用户 ${d.name}`); closeOverlays(); location.hash = "users"; } }),
});
/* ---- 悬停提示:data-tip 的绝对时间浮在 body 上(固定定位),表格滚动区裁不掉;键盘聚焦也显示。 ---- */
let TIP = null;
const tipHide = () => { TIP?.remove(); TIP = null; };
// 只管用户页里的 [data-tip](「最近在线」的格子、工具栏的在线人数、抽屉里的各段时间);
// 别的页的 [aria-disabled=true][data-tip] 仍用 app.css 里的 ::after 提示,这里不碰。
const tipOf = t => t?.closest?.(".td-pres, .pres-sum, .udp-list, .udp-upd") ? t : null;
function tipShow(t) {
  if (TIP && TIP.dataset.tip === t.dataset.tip) { tipPlace(t); return; }
  tipHide();
  TIP = document.createElement("div"); TIP.className = "ftip"; TIP.textContent = t.dataset.tip; TIP.dataset.tip = t.dataset.tip;
  document.body.append(TIP);
  tipPlace(t);
}
// 放在元素的左下方,靠右时往左收,靠底时放到元素上面。
function tipPlace(t) {
  if (!TIP) return;
  const r = t.getBoundingClientRect();
  TIP.style.left = Math.max(8, Math.min(r.left, innerWidth - TIP.offsetWidth - 8)) + "px";
  TIP.style.top = (r.bottom + 6 + TIP.offsetHeight > innerHeight ? r.top - 6 - TIP.offsetHeight : r.bottom + 6) + "px";
}
document.addEventListener("mouseover", e => { const t = tipOf(e.target.closest?.("[data-tip]")); if (t) tipShow(t); else tipHide(); });
document.addEventListener("mouseout", e => { if (e.target.closest?.("[data-tip]") && !e.relatedTarget?.closest?.("[data-tip]")) tipHide(); });
document.addEventListener("focusin", e => { const t = tipOf(e.target.closest?.("[data-tip]")); if (t) tipShow(t); else tipHide(); });
document.addEventListener("focusout", e => { if (e.target.closest?.("[data-tip]")) tipHide(); });
// 滚动、换页、点别处时提示跟着收掉,不留在原处。
// 没加 capture:冒泡到 document 的是 main、抽屉这些滚动区自己的滚动,换了页面就不再跟。
document.addEventListener("scroll", e => { if (e.target?.closest?.(".td-pres, .pres-sum, .udp-list")) tipHide(); });
document.addEventListener("click", e => { if (!e.target.closest?.("[data-tip]")) tipHide(); });
document.addEventListener("keydown", e => { if (e.key === "Enter") { const tr = e.target.closest?.("tr[data-href]"); if (tr) location.hash = tr.dataset.href; } });
document.addEventListener("click", e => { const m = $("#user-menu"); if (m && !m.hidden && (!e.target.closest(".mw") || e.target.closest("#user-menu button"))) m.hidden = true; });
