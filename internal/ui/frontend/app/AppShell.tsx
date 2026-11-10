import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { useApp, type Crumb } from "@/runtime/app-context";
import { api } from "@/runtime/api";
import { navigation, navKey, shortVersion } from "@/runtime/navigation";
import { useShellState } from "@/runtime/shell-state";
import { cpAddr, cpName } from "@/runtime/profiles";
import { reportTheme } from "@/runtime/platform";
import { Button } from "@/components/ui/button";
import { Icon, FlagText } from "@/components/primitives";

export function AppShell({ crumbs, busy, updateAvailable, children }: { crumbs: Crumb[]; busy: boolean; updateAvailable: boolean; children: ReactNode }) {
  const app = useApp(), summary = useShellState(), main = useRef<HTMLElement>(null), timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [dark, setDark] = useState(document.documentElement.dataset.theme === "dark");
  const [scrolling, setScrolling] = useState(false);
  const on = navKey(app.route), home = on === "overview";
  const clearScroll = useCallback(() => { if (timer.current) clearTimeout(timer.current); timer.current = null; setScrolling(false); }, []);
  useLayoutEffect(() => {
    clearScroll();
    if (main.current) main.current.scrollTop = 0;
    const frame = requestAnimationFrame(() => {
      const heading = document.querySelector<HTMLElement>("#page h1");
      if (heading) { heading.tabIndex = -1; heading.focus({ preventScroll: true }); }
    });
    return () => cancelAnimationFrame(frame);
  }, [app.route[0] === "users" ? "users" : app.route.join("/"), clearScroll]);
  useEffect(() => { addEventListener("pagehide", clearScroll); return () => { removeEventListener("pagehide", clearScroll); if (timer.current) clearTimeout(timer.current); }; }, [clearScroll]);
  const toggleTheme = () => {
    const next = dark ? "light" : "dark";
    document.documentElement.dataset.theme = next; setDark(!dark);
    try { localStorage.setItem("mpx-theme", next); } catch { /* Storage can be unavailable. */ }
    reportTheme(next);
  };
  return <><div className={"app" + (home ? " home-workspace" : "")}><aside className="side"><div className="tb-drag" /><div className="brand"><img className="lk lk-l" src="/lockup-light.svg" alt="MPX" /><img className="lk lk-d" src="/lockup-dark.svg" alt="MPX" /><small title={app.meta.cli_version}>{shortVersion(app.meta.cli_version)}</small></div><ControlPlaneCard />
    {navigation.map(item => <span key={item.key} className="nav-entry">{item.key === "doctor" && <div className="nsep" />}<a className={"nav" + (on === item.key ? " on" : "")} href={"#" + item.key} aria-current={on === item.key ? "page" : undefined}><Icon name={item.icon} />{item.label}{item.key === "overview" && summary.warning ? <span className="ct"><i className="dot bad" aria-label="有节点连不上" /></span> : summary.counts[item.key] ? <span className="ct">{summary.counts[item.key]}</span> : null}</a></span>)}</aside>
    <main ref={main} aria-busy={busy || undefined} className={scrolling ? "scrolling" : ""} onScroll={() => { clearScroll(); setScrolling(true); timer.current = setTimeout(clearScroll, 700); }}><div className="top"><div className="crumb">{home && <Icon name="home" />}{crumbs.map((crumb, index) => <span key={index}>{index > 0 && <i>/</i>}{crumb.href ? <a href={crumb.href}><FlagText>{crumb.label}</FlagText></a> : <b><FlagText>{crumb.label}</FlagText></b>}</span>)}</div><span className="sp" />{summary.at && <span className="sub hide-m">更新于 {summary.at.toLocaleTimeString("zh-CN", { hour12: false })}</span>}<button type="button" id="upd-pill" className="upd-pill" hidden={!updateAvailable} onClick={app.openUpdate} data-act="update-open">更新</button><Button size="icon" title="刷新" aria-label="刷新" data-act="reload" onClick={() => void app.refresh()}><Icon name="refresh" /></Button><Button size="icon" title="切换主题" aria-label="切换主题" data-act="theme" onClick={toggleTheme}><Icon name={dark ? "sun" : "moon"} /></Button></div><div className="page" id="page">{children}</div></main></div>
    <nav className="mnav">{navigation.slice(0, 4).map(item => <a key={item.key} href={"#" + item.key} className={on === item.key ? "on" : ""}><Icon name={item.icon} />{item.label}</a>)}<a href="#system" className={["rules", "doctor", "system"].includes(on) ? "on" : ""}><Icon name="more" />更多</a></nav></>;
}
function ControlPlaneCard() {
  const app = useApp(), summary = useShellState();
  const [open, setOpen] = useState(false), [device, setDevice] = useState("");
  const name = cpName(app.meta.profile, app.meta.server), address = cpAddr(app.meta.server), fingerprint = app.meta.fingerprint || "";
  useEffect(() => { if (open && !device) void api<{ devices: { current?: boolean; name: string }[] } | { current?: boolean; name: string }[]>("/api/devices").then(data => { const devices = Array.isArray(data) ? data : data.devices; setDevice(devices?.find(value => value.current)?.name || ""); }).catch(() => {}); }, [open, device]);
  return <div className="cpw"><button type="button" className="profile" onClick={() => setOpen(value => !value)} aria-expanded={open} title="控制面" data-act="cp-toggle"><span className={"dot " + (summary.online === true ? "ok" : summary.online === false ? "bad" : "")} /><span className="pf-t"><b title={name === address ? app.meta.server : name}>{name}</b>{name !== address && <span title={app.meta.server}>{address}</span>}</span><Icon name="down" className={"pf-car" + (open ? " up" : "")} /></button>{open && <div className="cp-pop"><dl className="kv flat cp-kv"><dt>地址</dt><dd className="mono">{app.meta.server || "—"}</dd><dt>证书指纹</dt><dd className="mono fp" title={fingerprint}>{fingerprint ? (fingerprint.replace(/^SHA256:/, "").match(/.{1,4}/g) || []).map((group, index) => <i key={index}>{group}</i>) : <span className="sub">未使用 HTTPS 指纹</span>}</dd>{device && <><dt>这台电脑</dt><dd>{device}</dd></>}</dl><div className="cp-acts">{fingerprint && <Button size="sm" data-act="copy" data-text={fingerprint} onClick={() => void navigator.clipboard.writeText(fingerprint).then(() => app.toast("已复制"))}><Icon name="copy" />复制指纹</Button>}{app.meta.profiles && <Button size="sm" onClick={app.openProfiles} data-act="cp-switch"><Icon name="swap" />切换控制面</Button>}</div><p className="sub cp-note">只在这台电脑上能打开，别的设备需要单独接入。</p></div>}</div>;
}
