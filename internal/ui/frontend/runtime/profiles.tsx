import { useCallback, useEffect, useRef, useState } from "react";
import { ErrorState, Icon } from "@/components/primitives";
import { Button } from "@/components/ui/button";
import { useApp } from "./app-context";
import { write } from "./api";

export const cpAddr = (server?: string | null): string => String(server || "").replace(/^https?:\/\//, "").replace(/\/$/, "");
export const cpName = (name?: string | null, server?: string | null): string => !name || name === "default" ? cpAddr(server) || "default" : name;
export interface Profile { name: string; server: string; current?: boolean }
export type ProfilePhase =
  | { phase: "loading" }
  | { phase: "list"; items: Profile[] }
  | { phase: "load-error"; error: Error }
  | { phase: "switching"; target: Profile }
  | { phase: "switch-error"; target: Profile; error: string };

// 控制面列表 + 切换的共用状态机:顶栏浮层与欢迎页「切换到已有的控制面」弹窗共用。
// 切换锁定叠层管理器,成功后整页 reload;失败后留在原地可重试。
export function useProfiles(start?: Profile, enabled = true) {
  const { setModalLocked } = useApp();
  const [state, setState] = useState<ProfilePhase>(start ? { phase: "switching", target: start } : { phase: "loading" });
  const [retry, setRetry] = useState(0);
  const busy = useRef(false);
  const active = useRef(true);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
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
  useEffect(() => {
    if (!enabled) return;
    if (start) { void useProfile(start); return; }
    let current = true;
    setState({ phase: "loading" });
    void write<Profile[]>("POST", "/local/profiles", {}).then(response => {
      if (current) setState({ phase: "list", items: Array.isArray(response) ? response : [] });
    }).catch((error: unknown) => {
      if (current) setState({ phase: "load-error", error: error instanceof Error ? error : new Error(String(error)) });
    });
    return () => { current = false; };
  }, [retry, start, useProfile, enabled]);
  const switching = state.phase === "switching";
  useEffect(() => () => setModalLocked(false), [setModalLocked]);
  return { state, useProfile, reloadList: () => setRetry(value => value + 1), switching };
}

// 列表外观:名称 + 去协议头地址;当前项墨色点 + 打勾,非当前项点击即切换。
export function ProfileItems({ items, onSelect }: { items: Profile[]; onSelect: (profile: Profile) => void }) {
  return <div className="pflist">{items.map(profile => {
    const address = cpAddr(profile.server), name = cpName(profile.name, profile.server);
    return <button type="button" className={"pfitem" + (profile.current ? " on" : "")} data-name={profile.name} key={profile.name} aria-current={profile.current ? "true" : undefined} onClick={() => onSelect(profile)}>
      <span className={"dot " + (profile.current ? "ok" : "")} /><span className="pf-t"><b>{name}</b>{name !== address && <span>{address}</span>}</span>{profile.current && <Icon name="check" />}
    </button>;
  })}</div>;
}

export function ProfilesDialog({ onAdd, initialTarget }: { onAdd?: () => void; initialTarget?: Profile }) {
  const { meta, closeModal, toast, openConnect, openModal, runGuarded } = useApp();
  const { state, useProfile, reloadList } = useProfiles(initialTarget);
  const canAdd = !!meta.connect;
  useEffect(() => {
    // 唯一一套且不能添加时给出一句说明并直接关掉,不弹一个没东西可点的框。
    if (state.phase === "list" && state.items.length <= 1 && !canAdd) {
      closeModal(); toast(`只有一个控制面 ${cpName(state.items[0]?.name, state.items[0]?.server)};这里不能添加新的控制面`);
    }
  }, [state, canAdd, closeModal, toast]);
  if (state.phase === "switching") return <div className="mbody" aria-busy="true"><h2>正在切换到 {state.target.name}</h2><p role="status">正在连接，完成后加载页面…</p></div>;
  if (state.phase === "switch-error") return <><div className="mbody"><h2>切换到 {state.target.name}</h2><div id="prof-err" role="alert">{state.error}</div></div><div className="mf"><Button data-act="modal-cancel" onClick={closeModal}>关闭</Button><Button id="profile-retry" variant="primary" onClick={() => void useProfile(state.target)}>重试</Button></div></>;
  return <><div className="mbody" aria-busy={state.phase === "loading" || undefined}><h2>切换控制面</h2>
    {state.phase === "loading" && <p role="status">正在加载…</p>}
    {state.phase === "load-error" && <ErrorState title="控制面列表" error={state.error} onRetry={reloadList} />}
    {state.phase === "list" && <><p>切换后会记住，下次打开仍连你选的这个。</p><ProfileItems items={state.items} onSelect={profile => runGuarded(() => openModal(<ProfilesDialog key={profile.name} initialTarget={profile} onAdd={onAdd} />))} /><div className="err" id="prof-err" role="alert" /></>}
  </div><div className="mf">{canAdd && state.phase === "list" && <><Button data-act="profile-add" onClick={() => { closeModal(); (onAdd || openConnect)(); }}><Icon name="plus" />添加控制面…</Button><span className="sp" style={{ flex: 1 }} /></>}<Button data-act="modal-cancel" onClick={closeModal}>取消</Button></div></>;
}
