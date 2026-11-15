import { useCallback, useEffect, useRef, useState } from "react";
import { useApp } from "@/runtime/app-context";
import { api } from "@/runtime/api";
import { useShellState } from "@/runtime/shell-state";
import { cpAddr, cpName, useProfiles, ProfileItems, type Profile } from "@/runtime/profiles";
import { Button } from "@/components/ui/button";
import { Icon, ErrorState } from "@/components/primitives";

// 顶栏控制面状态胶囊 + 单层浮层:列表里点非当前项直接切换,
// 次级区展示地址、4 位一组证书指纹与本机设备名,底部入口接入另一套控制面。
export function ControlPlaneMenu() {
  const app = useApp(), summary = useShellState();
  const [open, setOpen] = useState(false), [view, setView] = useState<"list" | "detail">("list"), [device, setDevice] = useState("");
  const wrap = useRef<HTMLDivElement>(null), pill = useRef<HTMLButtonElement>(null);
  const { state, useProfile, reloadList } = useProfiles(undefined, !!app.meta.profiles);
  const name = cpName(app.meta.profile, app.meta.server), address = cpAddr(app.meta.server), fingerprint = app.meta.fingerprint || "";
  const switching = state.phase === "switching";
  const close = useCallback(() => { if (!switching) setOpen(false); }, [switching]);
  const restore = useCallback(() => { close(); pill.current?.focus({ preventScroll: true }); }, [close]);
  useEffect(() => {
    if (!open) return;
    const down = (event: PointerEvent) => { if (!wrap.current?.contains(event.target as Node)) close(); };
    const key = (event: KeyboardEvent) => { if (event.key === "Escape") { event.stopPropagation(); restore(); } };
    document.addEventListener("pointerdown", down);
    document.addEventListener("keydown", key);
    return () => { document.removeEventListener("pointerdown", down); document.removeEventListener("keydown", key); };
  }, [open, close, restore]);
  useEffect(() => {
    if (!open || device) return;
    void api<{ devices: { current?: boolean; name: string }[] } | { current?: boolean; name: string }[]>("/api/devices").then(data => {
      const devices = Array.isArray(data) ? data : data.devices;
      setDevice(devices?.find(value => value.current)?.name || "");
    }).catch(() => {});
  }, [open, device]);
  const select = (profile: Profile) => { if (!profile.current) app.runGuarded(() => void useProfile(profile)); };
  const offline = summary.online === false;
  return <div className="cp-menu" ref={wrap}><button type="button" className="profile" data-act="cp-toggle" aria-expanded={open} aria-haspopup="dialog" disabled={switching} title={offline ? "控制面最近没连上，点开可查看或切换" : "控制面"} onClick={() => { setOpen(value => !value); setView("list"); }}>
    <span className={"dot " + (summary.online === true ? "ok" : offline ? "bad" : "")} /><span className="pf-t"><b title={name === address ? app.meta.server : name}>{name}</b>{name !== address && <span className="hide-m" title={app.meta.server}>{address}</span>}</span><Icon name="down" className={"pf-car" + (open ? " up" : "")} /></button>
    {open && <div className="cp-pop" role="dialog" aria-label="控制面">{view === "detail" ? <CpDetail server={app.meta.server} fingerprint={fingerprint} device={device} onBack={() => setView("list")} onCopy={() => { void navigator.clipboard.writeText(fingerprint).then(() => app.toast("已复制")); }} /> :
      state.phase === "switching" ? <div className="cp-busy" role="status"><h2>正在切换到 {state.target.name}</h2><p>正在连接，完成后加载页面…</p></div> :
      state.phase === "switch-error" ? <div className="cp-busy"><h2>切换到 {state.target.name}</h2><div id="prof-err" role="alert">{state.error}</div><div className="cp-acts"><Button size="sm" data-act="cp-close" onClick={restore}>关闭</Button><Button size="sm" variant="primary" id="profile-retry" onClick={() => void useProfile(state.target)}>重试</Button></div></div> :
      <>{app.meta.profiles && <div className="cp-list">
          {state.phase === "loading" && <p className="sub" role="status">正在加载…</p>}
          {state.phase === "load-error" && <ErrorState title="控制面列表" error={state.error} onRetry={reloadList} />}
          {state.phase === "list" && <ProfileItems items={state.items} onSelect={select} />}
        </div>}
        <div className="cp-acts">
          {!!app.meta.connect && <button type="button" className="cp-act" data-act="profile-add" onClick={() => { setOpen(false); app.openConnect(); }}><Icon name="plus" />添加控制面…</button>}
          <button type="button" className="cp-act" data-act="cp-detail" onClick={() => setView("detail")}><span><Icon name="copy" />证书指纹与本机信息</span><Icon name="arrow" /></button>
        </div></>}</div>}</div>;
}

function CpDetail({ server, fingerprint, device, onBack, onCopy }: { server?: string | null; fingerprint: string; device: string; onBack: () => void; onCopy: () => void }) {
  return <><button type="button" className="cp-act" data-act="cp-back" onClick={onBack}><Icon name="back" />返回控制面列表</button>
    <dl className="kv flat cp-kv"><dt>地址</dt><dd className="mono">{server || "—"}</dd><dt>证书指纹</dt><dd className="mono fp" title={fingerprint}>{fingerprint ? (fingerprint.replace(/^SHA256:/, "").match(/.{1,4}/g) || []).map((group, index) => <i key={index}>{group}</i>) : <span className="sub">未使用 HTTPS 指纹</span>}</dd>{device && <><dt>这台电脑</dt><dd>{device}</dd></>}</dl>
    <p className="sub cp-note">此凭据仅保存在本机；其他电脑接入时核对上方指纹。<a href="#system/devices" data-act="cp-devices"><Icon name="arrow" />已接入设备</a></p>
    <div className="cp-acts">{fingerprint && <Button size="sm" variant="primary" data-act="copy" data-text={fingerprint} onClick={onCopy}><Icon name="copy" />复制指纹</Button>}</div></>;
}
