import { Icon } from "../../components/primitives";
import { DAY, SEND_FORMATS, absTime, agoShort, cat, clientLabel, fmtBytes, hm, pullText, relTime, spanText, type Pull, type User, type UserPresence } from "../../domain/users";
import { planNodeName } from "../../domain/plans";
import { DetailReadError, NodeName, Tip } from "./components";
import type { DetailPart, UserDetailData, UsersData } from "./hooks";
export const presenceKnown = (data: UsersData | null) => !!data?.page.presence?.updated_at;
const presenceStale = (data: UsersData | null) => presenceKnown(data) && Date.now() - +new Date(data!.page.presence!.updated_at!) > 90000;
const nodeText = (data: UsersData | null, id?: number) => { const node = data?.nodes.find(n => n.id === id); return node ? planNodeName(node) : `节点 #${id}`; };
const bareNode = (text: string) => text.replace(/^[\u{1F1E6}-\u{1F1FF}]{2} /u, "");
const staleHint = (data: UsersData | null, presence: UserPresence) => {
  const stale = data?.page.presence?.stale_nodes?.find(s => s.node_id === presence.last_node_id);
  return stale && presence.last_active_at && +new Date(presence.last_active_at) >= +new Date(stale.since) - 120000 ? stale : null;
};
const activeTip = (data: UsersData | null, presence: UserPresence) => "最后一次有流量：" + (presence.last_active_at ? absTime(presence.last_active_at) : "") + (presence.last_node_id ? " · " + bareNode(nodeText(data, presence.last_node_id)) : "");
const nodesText = (data: UsersData | null, ids: number[] = []) => !ids.length ? "" : ids.length === 1 ? nodeText(data, ids[0]) : `${nodeText(data, ids[0])} 等 ${ids.length} 个节点`;
export function comparePresence(data: UsersData, dir: number) {
  const key = (user: User) => presenceKnown(data) && user.presence?.online ? 4e12 + (user.presence.online_ips || 0) : user.presence?.last_active_at ? +new Date(user.presence.last_active_at) : -4e12 + +new Date(user.created_at || 0) / 1000;
  return (a: User, b: User) => Number(!!b.presence?.last_active_at) - Number(!!a.presence?.last_active_at) || dir * (key(a) - key(b)) || a.username.localeCompare(b.username);
}
export function PresenceCell({ user, data }: { user: User; data: UsersData }) {
  const p = user.presence || {}, pull = data.pulls.find(x => x.username === user.username);
  if (!presenceKnown(data)) return <div className="pres">{p.last_active_at && <span className={`pres-l ${Date.now() - +new Date(p.last_active_at) > DAY ? "old" : ""}`}>{relTime(p.last_active_at)}</span>}</div>;
  if (p.online && !presenceStale(data)) return <Tip className="pres" tip={activeTip(data, p) + ((p.online_nodes?.length || 0) > 1 ? " · " + p.online_nodes!.map(id => bareNode(nodeText(data, id))).join("、") : "")}><span className="pres-l on"><span className="pdot" />在线</span><div className="sub ell">{p.online_ips ? `${p.online_ips} 个 IP · ` : ""}<NodeName name={nodesText(data, p.online_nodes)} /></div></Tip>;
  if (p.online) return <Tip className="pres" tip={activeTip(data, p)}><span className="pres-l"><span className="pdot hollow" />{agoShort(data.page.presence!.updated_at!)}在线</span><div className="sub ell"><NodeName name={nodesText(data, p.online_nodes)} /></div></Tip>;
  if (!p.last_active_at) return <Tip className="pres" tip={pull ? `${clientLabel(pull.client)} ${absTime(pull.last_pulled_at)} 拉取过订阅，之后没有流量` : undefined}><span className="pres-l never">从未连接</span>{pull && <div className="sub ell">导入过订阅，还没连上</div>}</Tip>;
  return <Tip className="pres" tip={activeTip(data, p)}><span className={`pres-l ${Date.now() - +new Date(p.last_active_at) > DAY ? "old" : ""}`}>{relTime(p.last_active_at)}</span>{staleHint(data, p) && <div className="sub ell">{bareNode(nodeText(data, p.last_node_id))} 连不上，可能还在线</div>}</Tip>;
}
export function PresenceSummary({ data }: { data: UsersData }) {
  if (!presenceKnown(data)) return <Tip inline className="pres-sum stale-t" tip="控制面这次没算出在线状态，刷新页面再试"><Icon name="clock" />在线状态读不到</Tip>;
  const at = data.page.presence!.updated_at!;
  return presenceStale(data) ? <Tip inline className="pres-sum stale-t" tip={`上一次拿到在线状态：${absTime(at)}`}><Icon name="clock" />在线状态 {agoShort(at)}更新</Tip> : <Tip inline className="pres-sum" tip={`在线状态 ${agoShort(at)}更新，每 30 秒一次`}><span className="pdot" />{data.page.presence!.online} 人在线</Tip>;
}
export function StalePresenceNote({ data }: { data: UsersData }) {
  const stale = data.page.presence?.stale_nodes?.[0];
  return stale ? <div className="pres-note"><Icon name="clock" /><span><NodeName name={nodeText(data, stale.node_id)} /> 从 {hm(new Date(stale.since))} 起连不上，这台上的在线状态停在 {agoShort(stale.since)}。</span><a className="lnk" href={`#nodes/${stale.node_id}`}>看节点</a></div> : null;
}
export function RecentConnections({ user, base, detail, pull, retry }: { user: User; base: UsersData | null; detail: UserDetailData; pull?: Pull; retry: (part: DetailPart) => void }) {
  const p = user.presence || {}, activity = detail.activity || { segments: [], ips: [] }, known = presenceKnown(base), stale = presenceStale(base), hint = staleHint(base, p);
  const at = hint?.since || base?.page.presence?.updated_at;
  const segmentError = detail.errors.activity;
  return <section className="ud-pres" id="ud-pres"><div className="udp-h"><h3>最近连接</h3>{known && at && <Tip inline className="sub udp-upd" tip={absTime(at)}>{(hint || stale) && <Icon name="clock" />}{hint ? `${bareNode(nodeText(base, hint.node_id))} 的` : ""}在线状态 {agoShort(at)}更新</Tip>}</div>
    <div className="udp-now">{known && p.online && !stale ? <><span className="pres-l on"><span className="pdot" />在线</span><span className="sub">{p.online_ips ? `${p.online_ips} 个 IP · ` : ""}<NodeName name={nodesText(base, p.online_nodes)} /></span></> : p.last_active_at ? <><span className="pres-l">{relTime(p.last_active_at)}</span><span className="sub">最后一次有流量 · <NodeName name={nodeText(base, p.last_node_id)} />{hint ? "（这台现在连不上，可能还在线）" : ""}</span></> : <span className="pres-l never">还没连接过</span>}</div>
    {segmentError ? <><p className="sub udp-foot">读不到最近连接</p><details className="sum-raw"><summary>详情<Icon name="down" /></summary><ul className="summary mono"><li>{segmentError}</li></ul></details></> : <>{activity.segments?.length > 0 && <div className="udp-list">{activity.segments.slice(0, 4).map((segment, index) => <Tip className="udp-r" key={`${segment.start}/${index}`} tip={`${absTime(segment.start)} 起`}><span className="udp-t">{spanText(segment)}</span><span className="udp-n ell"><NodeName name={nodeText(base, segment.node_id)} /></span><span className="udp-b">{fmtBytes(segment.bytes)}</span></Tip>)}</div>}{known && p.online && !stale && <div className="udp-ips"><span className="sub">此刻的 IP</span>{activity.ips?.map(ip => <span className="udp-ip" key={ip.ip}><span className="mono">{ip.ip}</span><span className="sub">{agoShort(ip.last_seen)}</span></span>)}</div>}</>}
    {!p.last_active_at && (pull ? <div className="udp-hint"><Icon name="info" /><div><p>{cat(`${clientLabel(pull.client)} ${relTime(pull.last_pulled_at)}`, "拉取过订阅")}，但之后一直没有流量。多半是客户端里没打开代理，或者连不上节点。</p><a className="btn sm" href="#doctor"><Icon name="pulse" />去体检</a></div></div> : <p className="sub udp-foot">把上面的二维码或链接发给他，他用起来以后会显示在这里。</p>)}
    {detail.pending.activity && !detail.activity && <p className="sub" role="status">正在读取最近连接…</p>}<DetailReadError label="最近连接" part="activity" error={segmentError} onRetry={() => retry("activity")} />
    {p.last_active_at && <p className="sub udp-foot">按节点上的流量算，每 30 秒更新一次。<br />客户端开着但没有流量，不算在线。</p>}
    {(p.last_active_at || !pull) && <div className="udp-pull"><Icon name="sub" /><div><b>订阅导入</b><span className="sub">{pull ? `${pullText(pull)} · ${SEND_FORMATS.find(format => format[0] === pull.format)?.[1] || pull.format || ""}` : "还没有客户端导入过"}</span></div></div>}
  </section>;
}
