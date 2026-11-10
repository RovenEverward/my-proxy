import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorState, Icon } from "@/components/primitives";
import { Button } from "@/components/ui/button";
import { useApp } from "./app-context";
import { write } from "./api";

export const cpAddr = (server?: string | null): string => String(server || "").replace(/^https?:\/\//, "").replace(/\/$/, "");
export const cpName = (name?: string | null, server?: string | null): string => !name || name === "default" ? cpAddr(server) || "default" : name;
interface Profile { name: string; server: string; current?: boolean }
type DialogState =
  | { phase: "loading" }
  | { phase: "list"; items: Profile[] }
  | { phase: "load-error"; error: Error }
  | { phase: "switching"; target: Profile }
  | { phase: "switch-error"; target: Profile; error: string };
export function ProfilesDialog({ onAdd, initialTarget }: { onAdd?: () => void; initialTarget?: Profile }) {
  const { meta, closeModal, toast, openConnect, openModal, runGuarded, setModalLocked } = useApp();
  const [state, setState] = useState<DialogState>(initialTarget ? { phase: "switching", target: initialTarget } : { phase: "loading" });
  const [retry, setRetry] = useState(0);
  const busy = useRef(false);
  const active = useRef(true);
  const canAdd = !!meta.connect;
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  const switching = state.phase === "switching";
  useEffect(() => { setModalLocked(switching); return () => setModalLocked(false); }, [switching, setModalLocked]);
  useEffect(() => {
    if (initialTarget) { void useProfile(initialTarget); return; }
    let current = true;
    setState({ phase: "loading" });
    void write<Profile[]>("POST", "/local/profiles", {}).then(response => {
      if (!current) return;
      const items = Array.isArray(response) ? response : [];
      if (items.length <= 1 && !canAdd) {
        closeModal(); toast(`只有一个控制面 ${cpName(items[0]?.name, items[0]?.server)};这里不能添加新的控制面`);
      } else setState({ phase: "list", items });
    }).catch((error: unknown) => {
      if (current) setState({ phase: "load-error", error: error instanceof Error ? error : new Error(String(error)) });
    });
    return () => { current = false; };
  }, [retry, canAdd, closeModal, toast, initialTarget]);
  const useProfile = useCallback(async (target: Profile) => {
    if (busy.current) return;
    busy.current = true; setModalLocked(true); setState({ phase: "switching", target });
    try {
      await write<{ ok?: boolean }>("POST", "/local/profiles/use", { name: target.name });
      if (active.current) location.reload();
    } catch (error) {
      if (active.current) setState({ phase: "switch-error", target, error: error instanceof Error ? error.message : String(error) });
    } finally {
      busy.current = false;
      if (active.current) setModalLocked(false);
    }
  }, [setModalLocked]);
  if (state.phase === "switching") return <div className="mbody" aria-busy="true"><h2>正在切换到 {state.target.name}</h2><p role="status">正在连接，完成后加载页面…</p></div>;
  if (state.phase === "switch-error") return <><div className="mbody"><h2>切换到 {state.target.name}</h2><div id="prof-err" role="alert">{state.error}</div></div><div className="mf"><Button data-act="modal-cancel" onClick={closeModal}>关闭</Button><Button id="profile-retry" variant="primary" onClick={() => void useProfile(state.target)}>重试</Button></div></>;
  return <><div className="mbody" aria-busy={state.phase === "loading" || undefined}><h2>切换控制面</h2>
    {state.phase === "loading" && <p role="status">正在加载…</p>}
    {state.phase === "load-error" && <ErrorState title="控制面列表" error={state.error} onRetry={() => setRetry(value => value + 1)} />}
    {state.phase === "list" && <><p>切换后会记住，下次打开仍连你选的这个。</p><div className="pflist">{state.items.map(profile => {
      const address = cpAddr(profile.server), name = cpName(profile.name, profile.server);
      return <button type="button" className={"pfitem" + (profile.current ? " on" : "")} data-name={profile.name} key={profile.name} aria-current={profile.current ? "true" : undefined} onClick={() => runGuarded(() => openModal(<ProfilesDialog key={profile.name} initialTarget={profile} onAdd={onAdd} />))}>
        <span className={"dot " + (profile.current ? "ok" : "")} /><span className="pf-t"><b>{name}</b>{name !== address && <span>{address}</span>}</span>{profile.current && <Icon name="check" />}
      </button>;
    })}</div><div className="err" id="prof-err" role="alert" /></>}
  </div><div className="mf">{canAdd && state.phase === "list" && <><Button data-act="profile-add" onClick={() => { closeModal(); (onAdd || openConnect)(); }}><Icon name="plus" />添加控制面…</Button><span className="sp" style={{ flex: 1 }} /></>}<Button data-act="modal-cancel" onClick={closeModal}>取消</Button></div></>;
}
