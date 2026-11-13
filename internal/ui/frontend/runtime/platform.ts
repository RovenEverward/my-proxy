import { useEffect } from "react";

export function reportTheme(theme: string) { void fetch("/local/app/theme", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ theme }) }).catch(() => {}); }
export function useTitlebar() {
  useEffect(() => {
    const sync = () => document.documentElement.classList.toggle("tb-right", (parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--mygo-titlebar-inset-right")) || 0) > 0);
    sync(); addEventListener("resize", sync);
    const timers = [300, 1200, 3000].map(delay => setTimeout(sync, delay));
    return () => { removeEventListener("resize", sync); timers.forEach(clearTimeout); };
  }, []);
}
