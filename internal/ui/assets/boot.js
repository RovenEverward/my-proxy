// 首帧之前定好主题,避免深色模式下先闪一下白。
(() => {
  let t = null;
  try { t = localStorage.getItem("mpx-theme"); } catch {}
  if (t !== "light" && t !== "dark") t = matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  document.documentElement.dataset.theme = t;
})();
