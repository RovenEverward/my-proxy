import { useSyncExternalStore } from "react";
import { useApp } from "../../runtime/app-context";
import { batchWrites, write } from "../../runtime/api";
import { Button } from "../../components/ui/button";
import { Icon } from "../../components/primitives";
import { EXPIRED_KEEP_DAYS, expiredFor, userPath, type User } from "../../domain/users";
function progressState(initial: string) {
  let label = initial;
  const listeners = new Set<() => void>();
  return { get: () => label, subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; }, set: (value: string) => { label = value; for (const listener of listeners) listener(); } };
}
function BulkDeleteLabel({ progress }: { progress: ReturnType<typeof progressState> }) { return <>{useSyncExternalStore(progress.subscribe, progress.get)}</>; }
export function BulkDeleteBar({ users, selected, onSelect, onDeleted, onChanged }: { users: User[]; selected: Set<string>; onSelect: (names: Set<string>) => void; onDeleted: (name: string) => void; onChanged: () => Promise<void> }) {
  const app = useApp(), old = users.filter(user => expiredFor(user) > EXPIRED_KEEP_DAYS).map(user => user.username);
  const selectOld = old.some(name => !selected.has(name));
  const remove = () => {
    const rows = users.filter(user => user.status === "expired" && selected.has(user.username)).sort((a, b) => expiredFor(b) - expiredFor(a));
    if (!rows.length || !app.can("DELETE", "/api/users/x")) return;
    const names = rows.map(user => user.username), pending = new Set(names), count = names.length;
    const progress = progressState(`删除 ${count} 个用户`);
    app.confirmBox({ title: `删除 ${count} 个已过期用户？`, text: <>不可恢复：他们的协议凭证与订阅 token 一起删掉，订阅地址立刻 404。只想让人连不上，过期本身已经做到了。<span className="rm-list">{rows.slice(0, 8).map(user => <span className="tag" key={user.username}>{user.username}<span className="sub">已过 {expiredFor(user)} 天</span></span>)}{count > 8 && <span className="sub">等 {count} 个</span>}</span></>, ok: <BulkDeleteLabel progress={progress} />, danger: true, typed: `删除 ${count} 个用户`, lock: true,
      run: async () => {
        const failed: string[] = [], attempt = [...pending], done = count - attempt.length;
        await batchWrites(async () => {
          for (const [index, name] of attempt.entries()) {
            progress.set(`正在删除 ${done + index + 1} / ${count}`);
            try { await write("DELETE", userPath(name)); pending.delete(name); onDeleted(name); }
            catch (error) { failed.push(`${name}（${error instanceof Error ? error.message : error}）`); }
          }
        });
        await onChanged();
        if (failed.length) { progress.set(`重试 ${failed.length} 个`); throw new Error(`删掉了 ${count - pending.size} 个，${pending.size} 个没删掉：${failed.join("、")}`); }
        app.toast(`已删除 ${count} 个用户`);
      },
    });
  };
  return <div className={`bulk${selected.size ? " on" : ""}`} role="region" aria-label="批量操作">{selected.size ? <b>已选 {selected.size} 个</b> : <span className="sub">勾选要清理的用户，可以一次删掉。</span>}{selectOld && <button type="button" className="lnk" data-act="pick-old" onClick={() => onSelect(new Set([...selected, ...old]))}>选中过期超过 {EXPIRED_KEEP_DAYS} 天的 {old.length} 个</button>}{selected.size > 0 && <><span className="sp" /><Button size="sm" variant="ghost" data-act="pick-clear" onClick={() => onSelect(new Set())}>取消选择</Button><Button size="sm" className="dg-t" data-act="user-rm-many" onClick={remove}><Icon name="trash" />批量删除</Button></>}</div>;
}
