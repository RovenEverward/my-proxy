import { useLayoutEffect, useRef, useState, type HTMLAttributes, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { FlagText, Icon, StatusBadge } from "../../components/primitives";
import { EXPIRE_SOON_DAYS, daysLeft, fmtBytes, remain, userStateLabel, ymd, type User } from "../../domain/users";
import { extrasOf, type Inbound, type Plan } from "../../domain/plans";

export function UserStatus({ status, children }: { status: string; children?: ReactNode }) {
  return <StatusBadge tone={status === "active" ? "ok" : status === "expired" || status === "limited" ? "warn" : ""}>{children || userStateLabel(status)}</StatusBadge>;
}
export function UsageBar({ user, wide = false }: { user: Pick<User, "data_limit" | "used_traffic">; wide?: boolean }) {
  if (!user.data_limit) return <div className="use"><span className="keep">{fmtBytes(user.used_traffic)} <span className="c-faint">· 不限</span></span></div>;
  const remaining = remain(user);
  return <div className="use" title={remaining.tip}><div className={`bar${wide ? " w100" : ""}`}><i className={remaining.cls} style={{ width: `${remaining.rp.toFixed(1)}%` }} /></div><span className={remaining.out ? "c-bad" : ""}>{remaining.text}</span></div>;
}
export function ExpireCell({ user }: { user: Pick<User, "expire_at"> }) {
  if (!user.expire_at) return <span className="sub">永不过期</span>;
  const days = daysLeft(user.expire_at)!;
  return <>{ymd(new Date(user.expire_at))}<div className={`sub ${days < 0 ? "c-bad" : days <= EXPIRE_SOON_DAYS ? "c-warn" : ""}`}>{days < 0 ? `已过 ${-days} 天` : days === 0 ? "今天到期" : `剩 ${days} 天`}</div></>;
}
export function UserPlanCell({ user, plans, inbounds }: { user: User; plans: Plan[]; inbounds: Inbound[] }) {
  const plan = plans.find(p => p.id === user.plan_id);
  if (!plan) return user.inbound_ids?.length ? <span className="pl-ptag"><span className="tag g" title={`单独授权的 ${user.inbound_ids.length} 个连接方式`}>单独授权 · {user.inbound_ids.length} 个连接方式</span></span> : <span className="sub">全部连接方式</span>;
  const extra = extrasOf(user, plan, inbounds);
  return <span className="pl-ptag"><span className={`tag ${plan.is_default ? "g" : ""}`}>{plan.name}</span>{extra.length > 0 && <span className="sub" title={`额外授权：${extra.map(i => i.tag || `${i.protocol}:${i.port}`).join("、")}`}>+{extra.length}</span>}</span>;
}
export function Pickbox({ mixed = false, ...props }: React.InputHTMLAttributes<HTMLInputElement> & { mixed?: boolean }) {
  const ref = useRef<HTMLInputElement>(null);
  useLayoutEffect(() => { if (ref.current) ref.current.indeterminate = mixed; }, [mixed]);
  return <input ref={ref} type="checkbox" {...props} />;
}
export function NodeName({ name }: { name: string }) { return <FlagText>{name}</FlagText>; }
export function Tip({ tip, inline = false, children, ...props }: HTMLAttributes<HTMLElement> & { tip?: string; inline?: boolean }) {
  const [bounds, setBounds] = useState<DOMRect | null>(null);
  const [position, setPosition] = useState({ left: 8, top: 8 });
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (!bounds || !ref.current) return;
    const box = ref.current.getBoundingClientRect();
    setPosition({ left: Math.max(8, Math.min(bounds.left, innerWidth - box.width - 8)), top: bounds.bottom + 6 + box.height > innerHeight ? bounds.top - 6 - box.height : bounds.bottom + 6 });
  }, [bounds, tip]);
  const shared = { ...props, "data-tip": tip, onMouseEnter: (e: React.MouseEvent<HTMLElement>) => tip && setBounds(e.currentTarget.getBoundingClientRect()), onMouseLeave: () => setBounds(null), onFocus: (e: React.FocusEvent<HTMLElement>) => tip && setBounds(e.currentTarget.getBoundingClientRect()), onBlur: () => setBounds(null), onClick: () => setBounds(null) };
  return <>{inline ? <span {...shared}>{children}</span> : <div {...shared}>{children}</div>}{bounds && tip && createPortal(<div className="ftip" ref={ref} style={position}>{tip}</div>, document.body)}</>;
}
export function DetailReadError({ label, part, error, onRetry }: { label: string; part: string; error?: string; onRetry: () => void }) {
  if (!error) return null;
  return <><p className="sub" role="alert">读不到{label}：{error}</p><button type="button" className="btn sm" data-act="user-detail-retry" data-part={part} onClick={onRetry}><Icon name="refresh" />重试</button></>;
}
