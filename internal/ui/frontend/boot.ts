// Apply the saved theme before loading the application styles.
(() => {
  let theme: string | null = null;
  try { theme = localStorage.getItem("mpx-theme"); } catch { /* Storage can be unavailable. */ }
  document.documentElement.dataset.theme = theme === "dark" ? "dark" : "light";
  const platform = (navigator as Navigator & { userAgentData?: { platform?: string } }).userAgentData?.platform || navigator.platform || "";
  if (/^Mac/i.test(platform)) document.documentElement.classList.add("os-mac");
})();
