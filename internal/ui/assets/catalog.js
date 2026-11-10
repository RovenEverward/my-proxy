"use strict";
// This directory reuses product primitives and does not start the application.
document.addEventListener("DOMContentLoaded", () => {
  const root = document.documentElement;
  const themeButton = $("#catalog-theme");
  const showTheme = () => { themeButton.innerHTML = val(ic(root.dataset.theme === "dark" ? "sun" : "moon")); };
  themeButton.addEventListener("click", () => { root.dataset.theme = root.dataset.theme === "dark" ? "light" : "dark"; showTheme(); });
  $("#density").addEventListener("change", event => { root.dataset.density = event.target.value; });
  $("#catalog-copy").innerHTML = val(ic("copy"));
  $("#catalog-copy").addEventListener("click", () => { $("#catalog-feedback").textContent = "已触发示例操作"; });
  $("#catalog-status").innerHTML = val([stBadge("ok", "运行中"), stBadge("warn", "需要处理"), stBadge("bad", "连接失败")]);
  $("#catalog-empty").innerHTML = val(emptyBox("nodes", "还没有节点", "添加节点后，列表会显示在这里。"));
  $("#catalog-failure").innerHTML = val(errorBox("节点", new Error("连接超时")));
  $("#catalog-failure").addEventListener("click", event => {
    if (event.target.closest('[data-act="reload"]')) $("#catalog-feedback").textContent = "已触发重试示例";
  });
  $("#catalog-skeleton").innerHTML = val(skRows(3, 2));
  $("#catalog-icon-grid").innerHTML = val(Object.keys(ICONS).map(name => H`<div class="catalog-icon-item" data-icon-name="${name}" title="Lucide ${ICON_NAMES[name]}">${ic(name)}<span>${name}</span></div>`));
  showTheme();
});
