import { useLayoutEffect, useRef, useSyncExternalStore } from "react";
import type { AppContextValue } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { daysLeft, fmtBytes, parseExpire, renewTarget, userPath, ymd, type User } from "../../domain/users";
import { readModifyUser } from "./hooks";

interface Renewal { pick: number | "date"; date: string; reset: boolean; savedTarget: string | null; label: string }
function renewalState(user: User) {
  let value: Renewal = { pick: 30, date: ymd(new Date(renewTarget(user.expire_at, 30))), reset: false, savedTarget: null, label: "续期" };
  const listeners = new Set<() => void>();
  return { get: () => value, subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; }, update: (patch: Partial<Renewal>) => { value = { ...value, ...patch }; for (const listener of listeners) listener(); } };
}
type RenewalState = ReturnType<typeof renewalState>;
function RenewalLabel({ state }: { state: RenewalState }) { return <>{useSyncExternalStore(state.subscribe, state.get).label}</>; }
export function RenewOptions({ user, state }: { user: User; state: RenewalState }) {
  const value = useSyncExternalStore(state.subscribe, state.get);
  const dateInput = useRef<HTMLInputElement>(null);
  useLayoutEffect(() => { if (value.pick === "date") { dateInput.current?.focus(); dateInput.current?.setSelectionRange(value.date.length, value.date.length); } }, [value.pick]);
  const target = value.pick === "date" ? value.date : ymd(new Date(renewTarget(user.expire_at, value.pick)));
  const over = user.data_limit > 0 && user.used_traffic >= user.data_limit;
  const choose = (pick: number | "date") => {
    if (value.savedTarget) return;
    state.update({ pick, ...(pick === "date" && value.pick !== "date" ? { date: target } : {}) });
  };
  return <div className="rn"><div className="seg rn-seg" role="radiogroup" aria-label="续多久">
    {[30, 90, 365, "date"].map(pick => <button key={pick} type="button" role="radio" className={value.pick === pick ? "on" : ""} aria-checked={value.pick === pick} aria-disabled={value.savedTarget ? true : undefined} data-rn={pick} onClick={() => choose(pick === "date" ? "date" : Number(pick))}>{pick === "date" ? "指定日期" : `${pick} 天`}</button>)}
  </div><div className="rn-to" id="rn-to" hidden={value.pick === "date"}><span className="sub">新的到期日</span><b>{target}</b></div>
    <div className="field rn-date" id="rn-date" hidden={value.pick !== "date"}><label htmlFor="rn-in">到期日</label><input ref={dateInput} className="in mono" id="rn-in" value={value.date} placeholder="YYYY-MM-DD" autoComplete="off" readOnly={!!value.savedTarget} onChange={event => state.update({ date: event.target.value })} /><span className="h">当天 23:59:59 到期，按这台电脑的时区。</span></div>
    {over && <label className="chk rn-reset"><input type="checkbox" id="rn-reset" checked={value.reset} onChange={event => state.update({ reset: event.target.checked })} /><span>同时清零本期用量<span className="sub">本期已用 {fmtBytes(user.used_traffic)}，上限 {fmtBytes(user.data_limit)}。不清零的话，续期后他仍然是「已超限」，连不上。</span></span></label>}
  </div>;
}
export function openRenew(app: AppContextValue, user: User, onChanged: () => Promise<void> = app.refresh) {
  const state = renewalState(user), left = daysLeft(user.expire_at);
  app.confirmBox({ title: `给 ${user.username} 续期`, text: <>{user.expire_at ? <>现在的到期日是 <b className="c-ink">{ymd(new Date(user.expire_at))}</b>（{left! < 0 ? `已过 ${-left!} 天` : left === 0 ? "今天到期" : `还剩 ${left} 天`}）。</> : <>他现在永不过期。</>}{left === null || left < 0 ? "从今天起算。" : "从原到期日往后顺延。"}</>, ok: <RenewalLabel state={state} />, extra: <RenewOptions user={user} state={state} />, lock: true,
    run: async () => {
      let target = state.get().savedTarget;
      const selection = state.get();
      if (!target) {
        const date = selection.pick === "date" ? parseExpire(selection.date) : null;
        if (date && daysLeft(date)! < 1) throw new Error("新的到期日要晚于今天");
        await readModifyUser(user.username, model => { target = date || renewTarget(model.expire_at, selection.pick as number); model.expire_at = target; });
        state.update({ savedTarget: target });
      }
      if (selection.reset) {
        try { await write("POST", userPath(user.username) + "/reset-traffic"); }
        catch (error) { state.update({ label: "重试清零" }); throw new Error(`续期已保存，清零没成功：${error instanceof Error ? error.message : error}。重试只会清零，不会再次续期。`); }
      }
      await onChanged();
      app.toast(`已把用户 ${user.username} 的到期日设为 ${ymd(new Date(target!))}${selection.reset ? "，本期用量已清零" : ""}`);
    },
  });
}
