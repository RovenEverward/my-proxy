import { useCallback, useEffect, useRef, useState } from "react";
import { useApp } from "@/runtime/app-context";
import { useShellState } from "@/runtime/shell-state";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/primitives";

// 顶栏会话胶囊(UC-26):控制面主机名 + 连通状态。点开看地址、版本、证书指纹,
// 末尾是退出登录。取代了 mpx ui 时代的多控制面切换菜单 —— 网页管理台只对着
// 当前这台控制面,没有「切换」的对象。
export function SessionMenu() {
  const app = useApp(), summary = useShellState();
  const [open, setOpen] = useState(false);
  const wrap = useRef<HTMLDivElement>(null), pill = useRef<HTMLButtonElement>(null);
  const close = useCallback(() => setOpen(false), []);
  const restore = useCallback(() => { close(); pill.current?.focus({ preventScroll: true }); }, [close]);
  useEffect(() => {
    if (!open) return;
    const down = (event: PointerEvent) => { if (!wrap.current?.contains(event.target as Node)) close(); };
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") { event.stopPropagation(); restore(); } };
    document.addEventListener("pointerdown", down);
    document.addEventListener("keydown", key);
    return () => { document.removeEventListener("pointerdown", down); document.removeEventListener("keydown", key); };
  }, [open, close, restore]);
  const host = location.host;
  const fingerprint = app.meta.fingerprint || "";
  const offline = summary.online === false;
  const copy = (text: string) => { void navigator.clipboard.writeText(text).then(() => app.toast("已复制")); };
  return <div className="cp-menu" ref={wrap}><button type="button" className="profile" data-act="cp-toggle" aria-expanded={open} aria-haspopup="dialog" title={offline ? "控制面最近没连上" : "这台控制面"} onClick={() => setOpen(value => !value)}>
    <span className={"dot " + (summary.online === true ? "ok" : offline ? "bad" : "")} /><span className="pf-t"><b>{app.meta.username || "管理员"}</b><span className="hide-m mono">{host}</span></span><Icon name="down" className={"pf-car" + (open ? " up" : "")} /></button>
    {open && <div className="cp-pop" role="dialog" aria-label="控制面">
      <dl className="kv flat cp-kv"><dt>地址</dt><dd className="mono">{location.origin}</dd><dt>版本</dt><dd className="mono">{app.meta.version || "—"}</dd><dt>证书指纹</dt><dd className="mono fp" title={fingerprint}>{fingerprint ? (fingerprint.replace(/^SHA256:/, "").match(/.{1,4}/g) || []).map((group, index) => <i key={index}>{group}</i>) : <span className="sub">未使用 HTTPS 指纹</span>}</dd></dl>
      <div className="cp-acts">
        {fingerprint && <Button size="sm" data-act="copy" onClick={() => copy(fingerprint)}><Icon name="copy" />复制指纹</Button>}
        <Button size="sm" variant="primary" data-act="logout" onClick={() => app.logout()}>退出登录</Button>
      </div>
      <p className="sub cp-note">改密码在服务器上跑 mpx admin password;改完所有会话即刻失效。</p>
    </div>}
  </div>;
}
