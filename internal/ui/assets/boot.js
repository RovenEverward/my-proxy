// 首帧之前定好主题,避免深色模式下先闪一下白。
(() => {
  let t = null;
  try { t = localStorage.getItem("mpx-theme"); } catch {}
  if (t !== "light" && t !== "dark") t = "light";
  document.documentElement.dataset.theme = t;
})();

// Mac 使用系统滚动条，其他平台使用细滚动条。
(() => {
  const p = navigator.userAgentData?.platform || navigator.platform || "";
  if (/^Mac/i.test(p)) document.documentElement.classList.add("os-mac");
})();
