import { Avatar, DrawerBody, Icon } from "../../components/primitives";
import { Button } from "../../components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../../components/ui/dropdown-menu";
import { useApp } from "../../runtime/app-context";
import { RESET, fmtBytes, remain, ymd, type User } from "../../domain/users";
import { planInboundLabel, planNodeCount } from "../../domain/plans";
import { DetailReadError, ExpireCell, UserStatus } from "./components";
import { useUserActions } from "./actions";
import { useUserDetail, type UsersData } from "./hooks";
import { RecentConnections } from "./presence";
import { SendSubscription } from "./send";
function UserMenu({ user, onChanged, onClose }: { user: User; onChanged: () => Promise<void>; onClose: () => void }) {
  const { can } = useApp();
  const actions = useUserActions(user, onChanged, { afterDelete: onClose });
  const items = [
    { act: "user-renew", label: "续期", icon: "clock", run: actions.renew, permitted: can("PUT", "/api/users/x") },
    { act: "user-reset", label: "清零本期用量", icon: "undo", run: actions.reset, permitted: can("POST", "/api/users/x/reset-traffic") },
    { act: "user-toggle", label: user.enabled ? "停用" : "启用", icon: "pause", run: actions.toggle, permitted: can("PUT", "/api/users/x") },
    { act: "user-revoke", label: "换一条新链接", icon: "key", run: actions.revoke, permitted: can("POST", "/api/users/x/revoke-sub") },
    { act: "user-rm", label: "删除用户", icon: "trash", run: actions.remove, permitted: can("DELETE", "/api/users/x") },
  ].filter(item => item.permitted);
  if (!items.length) return null;
  return <span className="mw"><DropdownMenu modal={false}><DropdownMenuTrigger asChild><button type="button" className="ib" title="更多" aria-label="更多" aria-haspopup="menu" data-act="user-menu"><Icon name="more" /></button></DropdownMenuTrigger><DropdownMenuContent className="menu ui-menu" id="user-menu" align="end">{items.map(item => <DropdownMenuItem key={item.act} asChild onSelect={item.run}><button type="button" className={item.act === "user-rm" ? "dg" : ""} data-act={item.act} data-name={user.username} data-on={item.act === "user-toggle" ? user.enabled ? "1" : "" : undefined}><Icon name={item.icon} />{item.label}</button></DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu></span>;
}
export function UserDetail({ name, base, created = false, onEdit, onClose, onChanged }: { name: string; base: UsersData | null; created?: boolean; onEdit: () => void; onClose: () => void; onChanged: () => Promise<void> }) {
  const app = useApp(), detail = useUserDetail(name, base), user = detail.user;
  const refresh = async () => { await Promise.all([onChanged(), detail.refresh()]); };
  if (!user) return <><div className="dh"><h2>{name}</h2><span className="sp" /><Button size="icon" data-act="close" aria-label="关闭" onClick={onClose}><Icon name="x" /></Button></div><DrawerBody aria-busy={detail.data.pending.user || undefined}>{detail.data.errors.user ? <DetailReadError label="用户资料" part="user" error={detail.data.errors.user} onRetry={() => void detail.retry("user")} /> : <p role="status">正在读取用户资料…</p>}</DrawerBody></>;
  const plan = base?.plans.find(p => p.id === user.plan_id), count = planNodeCount(plan, base?.nodes || [], base?.inbounds || []);
  const remaining = user.data_limit ? remain(user) : null;
  const connected = !!user.presence?.last_active_at && !created;
  const pull = base?.pulls.find(p => p.username === name);
  const presence = <RecentConnections user={user} base={base} detail={detail.data} pull={pull} retry={detail.retry} />;
  const send = <SendSubscription user={user} detail={detail.data} pull={connected ? undefined : pull} pullsKnown={!connected && base?.pullsKnown} retry={detail.retry} />;
  return <><div className="dh ud-h"><Avatar name={name} /><div className="ud-t"><h2 className="ell">{name}</h2><UserStatus status={user.status} /></div><span className="sp" style={{ flex: 1 }} />{app.can("PUT", "/api/users/x") && <button type="button" className="ib" title="编辑" aria-label="编辑" data-act="user-edit" data-name={name} onClick={onEdit}><Icon name="pen" /></button>}<UserMenu user={user} onChanged={refresh} onClose={onClose} /><button type="button" className="ib" data-act="close" aria-label="关闭" onClick={onClose}><Icon name="x" /></button></div>
    <DrawerBody className="ud-b" aria-busy={Object.values(detail.data.pending).some(Boolean) || undefined}>{created && <div className="created"><Icon name="check" /><span>已创建 <b>{name}</b>。把下面的二维码或链接发给他就行。</span></div>}{connected ? <>{presence}{send}</> : <>{send}{presence}</>}
      <section className="ud-use"><div className="sub">{remaining ? "本期剩余" : "本期用量"}</div><div className={`ud-v${remaining?.out ? " c-bad" : ""}`}>{remaining ? remaining.out ? user.used_traffic > user.data_limit ? `超出 ${fmtBytes(user.used_traffic - user.data_limit)}` : "已用完" : fmtBytes(remaining.left) : fmtBytes(user.used_traffic)}</div>{remaining ? <><div className="bar w100 tall"><i className={remaining.cls} style={{ width: `${remaining.rp.toFixed(1)}%` }} /></div><div className="sub mt8">已用 {fmtBytes(user.used_traffic)} / 上限 {fmtBytes(user.data_limit)}{user.reset_strategy && user.reset_strategy !== "none" ? ` · ${RESET[user.reset_strategy]}清零` : ""}</div></> : <div className="sub mt8">不限流量</div>}</section>
      <dl className="kv flat ud-kv"><dt>套餐</dt><dd>{plan ? <>{plan.name} <span className="sub">· {count ? `${count} 个节点` : "没有节点"}</span></> : user.inbound_ids?.length ? `单独授权 · ${user.inbound_ids.length} 个连接方式` : "全部连接方式"}</dd>{!!user.inbound_ids?.length && <><dt>额外授权</dt><dd>{user.inbound_ids.map(id => <span key={id} className="tag">{planInboundLabel(base?.inbounds.find(i => i.id === id) || { id })}</span>)}</dd></>}<dt>到期</dt><dd><ExpireCell user={user} /></dd><dt>创建于</dt><dd>{user.created_at ? ymd(new Date(user.created_at)) : "—"}</dd>{detail.data.errors.user && <><dt>资料读取</dt><dd><DetailReadError label="用户资料" part="user" error={detail.data.errors.user} onRetry={() => void detail.retry("user")} /></dd></>}{user.note && <><dt>备注</dt><dd>{user.note}</dd></>}</dl>
    </DrawerBody></>;
}
