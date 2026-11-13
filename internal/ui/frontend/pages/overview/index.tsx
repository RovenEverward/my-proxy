import { useEffect, useState, type ReactNode } from "react";
import { useApp, usePageTitle } from "@/runtime/app-context";
import { usePageData } from "@/runtime/query";
import { write } from "@/runtime/api";
import type { PageProps, Settings, Status, SubPull } from "@/runtime/types";
import { type NodeView, nodeLabel, nodeStateLabel, nodeStateTone, errorMessage } from "@/domain/nodes";
import { type User, type UserList, type PresenceSummary, daysLeft, expiredFor, remain, fmtBytes, clientLabel, EXPIRED_KEEP_DAYS, EXPIRE_SOON_DAYS, hm } from "@/domain/users";
import { Button } from "@/components/ui/button";
import { ActionMenu, Avatar, EmptyState, ErrorState, FlagText, Icon, PageHeader, StatusBadge } from "@/components/primitives";
import { UserEditor } from "@/pages/users/editor";
import { UserStatus } from "@/pages/users/components";
import { useUserActions } from "@/pages/users/actions";
import { shortVersion } from "@/runtime/navigation";
import { isDevVer } from "@/domain/releases";
import { StartChecklist, useChecklist } from "./start-checklist";

export interface OverviewData { status: Status; nodes: NodeView[]; settings: Settings; users: UserList; pulls: SubPull[] | null; presence: PresenceSummary | null }
let entered = false;
const nodeRank = (node: NodeView) => !node.status ? 3 : !node.status.reachable ? 0 : node.status.certificates?.some(cert => cert.not_after && cert.days_left <= 14) ? 1 : !node.status.active ? 2 : node.draining ? 4 : 5;
const duration = (value: number) => value >= 86400 ? `${Math.floor(value / 86400)} 天` : value >= 3600 ? `${Math.floor(value / 3600)} 小时` : `${Math.floor(value / 60)} 分钟`;
const parts = (items: [number, string][]) => items.filter(([number], index) => index === 0 || number > 0).map(([number, label]) => `${number}\u00a0${label}`).join(" · ");
const isDevelopment = isDevVer;
export function OverviewPage(_props: PageProps) {
  const app = useApp();
  usePageTitle([{ label: "总览" }]);
  const query = usePageData<OverviewData>("overview", async read => {
    const [status, nodes, settings, users, pulls, presence] = await Promise.all([
      read<Status>("/api/status"), read<NodeView[]>("/api/nodes"), read<Settings>("/api/settings"), read<UserList>("/api/users?limit=1000"),
      read<SubPull[]>("/api/sub-pulls").catch(() => null), read<UserList>("/api/users?limit=1").catch(() => null),
    ]);
    return { status, nodes, settings, users, pulls, presence: presence?.presence || null };
  });
  const [animate] = useState(!entered);
  useEffect(() => { if (query.data) entered = true; }, [query.data]);
  const checklist = useChecklist(query.data);
  const [retrying, setRetrying] = useState<number | null>(null);
  // retryBackoff 人工重试一台退避中的节点:服务端当场跑一轮采样与收敛,
  // 通了退避就清零;不通它照样记一次失败,错误原样说给操作者听。
  const retryBackoff = async (backoff: { node_id: number; node_name: string }) => {
    setRetrying(backoff.node_id);
    const label = labels[backoff.node_name] || backoff.node_name;
    try {
      await write("POST", `/api/nodes/${backoff.node_id}/backoff/retry`);
      app.toast(`${label} 重试成功，退避已清零`);
    } catch (error) {
      app.toast(`${label} 重试未通：${errorMessage(error)}`, true);
    }
    setRetrying(null);
    // 退避状态在 /api/status 里,不走 nodes 的失效映射:这里要全量重读,
    // 用(false)拿到的还是写前那份 status,banner 不会消。
    await query.refresh();
  };
  const addUser = () => app.openDrawer(<UserEditor onClose={app.closeOverlays} onSaved={async username => { app.closeOverlays(); app.navigate("users/" + encodeURIComponent(username)); }} />);
  if (!query.data) return <div className="ov"><PageHeader title="总览" />{query.error ? <ErrorState title="总览数据" error={query.error} onRetry={() => void query.refresh()} /> : <><div className="grid g4">{[0, 1, 2, 3].map(key => <Stat key={key} loading />)}</div><div className="grid g2 ov-main"><div className="card"><div className="ch">需要处理的用户</div><SkeletonRows /></div><div className="card"><div className="ch">节点</div><SkeletonRows /></div></div></>}</div>;
  const data = query.data, { status, nodes, users, settings, pulls } = data, list = users.users;
  const counts = (statusName: string) => list.filter(user => user.status === statusName).length;
  const soon = (user: User) => user.status === "active" && !!user.expire_at && (daysLeft(user.expire_at) ?? Infinity) <= EXPIRE_SOON_DAYS;
  const usageThreshold = settings.notify_usage_percent || 80;
  const usageWarning = (user: User) => user.status !== "expired" && !!user.data_limit && user.used_traffic / user.data_limit * 100 >= usageThreshold;
  const expired = list.filter(user => user.status === "expired"), stale = expired.filter(user => expiredFor(user) > EXPIRED_KEEP_DAYS);
  const attention = [...list.filter(user => user.status === "limited"), ...expired.filter(user => expiredFor(user) <= EXPIRED_KEEP_DAYS).sort((a, b) => expiredFor(a) - expiredFor(b)), ...list.filter(soon), ...list.filter(user => user.status === "active" && usageWarning(user) && !soon(user))];
  const shown = nodes.map((node, index) => ({ node, index })).sort((a, b) => nodeRank(a.node) - nodeRank(b.node) || a.index - b.index).slice(0, 6).map(item => item.node);
  const backs = status.backoffs || [], labels = Object.fromEntries(nodes.map(node => [node.name, nodeLabel(node)]));
  const backoffTitle = backs.map(backoff => `${labels[backoff.node_name] || backoff.node_name}：连续失败 ${backoff.failures} 次，${hm(new Date(backoff.skip_until))} 前跳过采样与收敛。`).join(" ");
  const allDone = checklist.steps.every(step => step.done), version = status.update || {};
  return <div className={"ov" + (animate ? " home-enter" : "")}>
    <PageHeader title="总览" description={!allDone ? "欢迎用 MPX。跟着下面几步，把第一条订阅发出去。" : undefined} actions={<>{allDone && !checklist.hidden && <div className="ov-done" id="start-done"><Icon name="check" /><span><b>开始使用</b><span className="ov-done-rest"> · 4 步都完成了</span></span><Button size="sm" variant="ghost" data-act="cl-open" onClick={checklist.toggle}>{checklist.open ? "收起" : "再看一眼"}</Button><Button size="icon-sm" title="不再显示" aria-label="不再显示" data-act="cl-hide" onClick={checklist.hide}><Icon name="x" /></Button></div>}{app.can("POST", "/api/users") && <Button variant="primary" className="home-add" data-act="user-new" onClick={addUser}><Icon name="plus" />添加用户</Button>}</>} />
    {(!allDone || checklist.open && !checklist.hidden) && <div className="mb"><StartChecklist steps={checklist.steps} onSkip={checklist.skip} onAddUser={addUser} /></div>}
    {backs.length > 0 && <div className="callout mb" title={backoffTitle}><Icon name="warn" className="ico-warn" /><div>{backs.length === 1 ? <><b><FlagText>{labels[backs[0].node_name] || backs[0].node_name}</FlagText> 在退避中</b><span className="ov-backoff-detail"> · 连续失败 {backs[0].failures} 次，{hm(new Date(backs[0].skip_until))} 前跳过采样与收敛。修好了可立即重试。</span></> : <><b>{backs.length} 个节点在退避中</b> · <FlagText>{backs.map(backoff => labels[backoff.node_name] || backoff.node_name).join("、")}</FlagText>：连续失败，暂时跳过采样与收敛。</>}</div>{backs.length === 1 && <span className="ov-backoff-link">{app.can("POST", "/api/nodes/x/backoff/retry") && <Button size="sm" variant="ghost" data-act="node-retry" loading={retrying === backs[0].node_id} disabled={retrying !== null} onClick={() => void retryBackoff(backs[0])}>立即重试</Button>}<a href={"#nodes/" + backs[0].node_id} className="btn ghost sm">查看节点 <Icon name="arrow" /></a></span>}</div>}
    <div className="grid g4"><Stat label="用户" value={users.total} description={<>{parts([[counts("active"), "正常"], [counts("limited"), "超限"], [counts("expired"), "过期"], [counts("disabled"), "停用"]])}{users.total > list.length && <span className="stale">· 只统计了前 {list.length} 个</span>}</>} /><Stat label="节点" value={status.counts.nodes} description={parts([[nodes.filter(node => node.status?.reachable && !node.draining).length, "运行"], [nodes.filter(node => node.draining).length, "暂停分配"], [nodes.filter(node => node.status && !node.status.reachable).length, "连不上"]])} />
      {data.presence ? <Stat label="近 24 小时在用" value={<>{data.presence.active_24h ?? 0}<small>人</small></>} description="按节点流量算" /> : pulls ? <Stat label="近 24 小时拉过订阅" value={<>{pulls.filter(pull => Date.now() - +new Date(pull.last_pulled_at) < 86400000).length}<small>人</small></>} description="客户端拉取过订阅" /> : <Stat label="连接方式" value={<>{status.counts.inbounds}<small>/ {status.counts.hosts}</small></>} description={`${status.counts.inbounds} 条连接 · ${status.counts.hosts} 个对外地址`} />}
      <Stat label="控制面" dot="ink" value={<span className="lead ell" title={status.version}>{shortVersion(status.version) || "未知版本"}</span>} description={(version.behind || 0) > 0 && !isDevelopment(status.version) ? <><span className="tag lab">有新版本 {version.latest}</span> · 运行 {duration(status.uptime_seconds)}</> : `${isDevelopment(status.version) ? "开发版，不比较版本" : version.latest ? "已是最新" : "没查到新版本信息"} · 运行 ${duration(status.uptime_seconds)}`} title={status.last_tick_at ? `上一轮 ${new Date(status.last_tick_at).toLocaleTimeString("zh-CN", { hour12: false })}，用时 ${(status.last_tick_seconds || 0).toFixed(2)} 秒` : "还没跑过一轮"} /></div>
    <div className="grid g2 ov-main"><div className="card"><div className="ch">节点<small>{nodes.length ? `· ${nodes.length}` : ""}</small><span className="sp" /><a className="btn ghost sm" href="#nodes">全部 <Icon name="arrow" /></a></div>{nodes.length ? shown.map(node => { const cert = node.status?.certificates?.filter(item => item.not_after && item.days_left <= 14).sort((a, b) => a.days_left - b.days_left)[0]; return <a key={node.id} className="row ov-n" href={"#nodes/" + node.id}><span className="ov-node-id"><b className="bold ell" title={nodeLabel(node)}><FlagText>{nodeLabel(node)}</FlagText></b><span className="sub mono ov-addr">{node.address}</span></span>{cert && <span className={"tag " + (cert.days_left < 0 ? "c-bad" : "c-warn")}>证书{cert.days_left < 0 ? "已过期" : `剩 ${cert.days_left} 天`}</span>}<StatusBadge tone={nodeStateTone(node)}>{nodeStateLabel(node)}</StatusBadge></a>; }) : <EmptyState icon="nodes" title="还没有节点" description="登记一台能从控制面 SSH 上去的服务器。节点上不装任何 mpx 组件。到「节点」页点「添加节点」开始。" />}</div>
      <div className="card"><div className="ch">需要处理的用户<small>{attention.length ? `· ${attention.length}` : ""}</small><span className="sp" /><a className="btn ghost sm" href="#users">全部用户 <Icon name="arrow" /></a></div>{attention.length ? <table className="ov-t"><tbody>{attention.slice(0, 6).map(user => <AttentionRow key={user.username} user={user} soon={soon(user)} usageWarning={usageWarning(user)} onChanged={() => query.refresh(false)} />)}</tbody></table> : <EmptyState icon={users.total ? "check" : "users"} title={users.total ? "没有要处理的用户" : "还没有用户"} description={users.total ? `没有超限、${stale.length ? EXPIRED_KEEP_DAYS + " 天内" : ""}过期、${EXPIRE_SOON_DAYS} 天内到期或用量过 ${usageThreshold}% 的用户。` : "加一个用户，他就有自己的订阅链接。"} />}{stale.length > 0 && <div className="ov-stale sub">另有 {stale.length} 个用户过期超过 {EXPIRED_KEEP_DAYS} 天，不再列在这里。<button type="button" className="lnk" data-act="go-expired" onClick={() => app.navigate("users", { status: "expired" })}>到「已过期」里清理</button></div>}</div></div>
    <div className="ov-note" id="ov-notify"><span className="ov-note-h"><Icon name="bell" />通知</span><span className="ov-kv"><span className="sub">Webhook</span>{settings.webhook_url ? <span className="mono ell" title={settings.webhook_url}>{settings.webhook_url}</span> : <span className="sub">未配置，不发通知</span>}</span><span className="ov-kv"><span className="sub">到期提醒</span>{settings.notify_days_left ? `剩 ${settings.notify_days_left} 天时` : "关闭"}</span><span className="ov-kv"><span className="sub">用量提醒</span>{settings.notify_usage_percent ? `用到 ${settings.notify_usage_percent}% 时` : "关闭"}</span><span className="sp" /><a className="btn ghost sm" href="#system">设置 <Icon name="arrow" /></a></div>
    {query.error && <ErrorState title="总览数据" error={query.error} onRetry={() => void query.refresh()} />}
  </div>;
}
function Stat({ label, value, description, dot, title, loading }: { label?: string; value?: ReactNode; description?: ReactNode; dot?: string; title?: string; loading?: boolean }) { return <div className="card stat" title={title}><div className="l">{dot && <span className={"dot " + dot} />}{label}</div><div className="v">{loading ? <span className="sk h26 w60" /> : value}</div><div className="s" title={typeof description === "string" ? description : undefined}>{loading ? <span className="sk w80" /> : description}</div></div>; }
function SkeletonRows() { return <table><tbody>{[0, 1, 2, 3].map(row => <tr key={row}>{[0, 1, 2].map(column => <td key={column}><span className="sk" style={{ width: "70%" }} /></td>)}</tr>)}</tbody></table>; }
function AttentionRow({ user, soon, usageWarning, onChanged }: { user: User; soon: boolean; usageWarning: boolean; onChanged: () => Promise<void> }) {
  const app = useApp(), actions = useUserActions(user, onChanged, { afterDelete: () => { void onChanged(); } });
  const expired = user.status === "expired", left = remain(user);
  const go = () => app.navigate("users/" + encodeURIComponent(user.username));
  return <tr className={expired ? "is-exp" : ""} data-href={"#users/" + encodeURIComponent(user.username)} tabIndex={0} onClick={event => { if (!(event.target as Element).closest("button,a,input,[role=menuitem]")) go(); }} onKeyDown={event => { if (event.key === "Enter" && event.target === event.currentTarget) go(); }}><td><div className="u"><Avatar name={user.username} /><b className="bold ell" title={user.username}>{user.username}</b></div></td><td>{expired ? <StatusBadge tone="warn">已过期 {expiredFor(user)} 天</StatusBadge> : user.status !== "active" ? <UserStatus status={user.status} /> : <StatusBadge tone="warn">{soon ? "快到期" : "快用完"}</StatusBadge>}</td>{expired ? <><td /><td className="ov-act" colSpan={2}>{app.can("PUT", "/api/users/x") && <Button size="sm" data-act="user-renew" data-name={user.username} onClick={actions.renew}>续期</Button>}{(app.can("PUT", "/api/users/x") || app.can("DELETE", "/api/users/x")) && <ActionMenu name={user.username} items={[{ label: "查看详情", icon: "user", onSelect: go }, ...(app.can("DELETE", "/api/users/x") ? [{ label: "删除用户", icon: "trash", danger: true, onSelect: actions.remove }] : [])]} />}</td></> : <><td>{usageWarning && <span className="ov-use" title={left.text + " · " + left.tip}>{user.data_limit ? <><span className="bar"><i className={left.cls} style={{ width: left.rp.toFixed(1) + "%" }} /></span><span className={"sub" + (left.out ? " c-bad" : "")}>{left.pctText}</span></> : <span className="sub">{fmtBytes(user.used_traffic)} · 不限</span>}</span>}</td><td className="num sub">{user.expire_at && `剩 ${daysLeft(user.expire_at)} 天`}</td><td className="ov-act" /></>}</tr>;
}
