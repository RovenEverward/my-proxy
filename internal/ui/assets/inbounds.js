// 入站与入口页。连接组件(节点分组、连接行、三个抽屉)全部在 conn.js 里;
// 这一页只剩页头 + CONN.groups(d)。共用函数 inbTitle / inbTitleById / INB_CACHE /
// inbKind 也搬到了 conn.js(plans.js、users.js、nodes.js 仍在用),这里不再定义。
"use strict";

PAGES.inbounds = {
  title: "入站与入口", short: "入站", nav: ["管理", "inb", 30],
  count: () => CONN.count(),
  load: () => CONN.load(),
  render(d) {
    const W = can("POST", "/api/inbounds");
    const head = H`<div class="ph"><div><h1>入站与入口</h1><p>每条连接会出现在用户的订阅里</p></div><div class="sp"></div>${W && d?.nodes.length ? H`<button class="btn pri" data-act="conn-new">${ic("plus")}添加连接</button>` : ""}</div>`;
    if (!d) return H`${head}<div class="card"><table><tbody>${skRows(6, 4)}</tbody></table></div>`;
    if (!d.nodes.length) return H`${head}<div class="card">${emptyBox("nodes", "还没有节点", "先有节点,再在节点上加入站。到「节点」页点「添加节点」开始。")}</div>`;
    return H`${head}${CONN.groups(d)}`;
  },
};
