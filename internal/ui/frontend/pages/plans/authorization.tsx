import { Fragment, useState } from "react";
import { FlagText, Icon, StatusBadge } from "../../components/primitives";
import { activeOccupancy, planCountText, planInboundLabel, planNodeName, type PlanData, type PlanDraft, type PlanSelection } from "../../domain/plans";
import type { User } from "../../domain/users";
import { Pickbox } from "../users/components";
export function PlanAuthorization({ data, draft, writable, onChange }: { data: PlanData; draft: PlanDraft; writable: boolean; onChange: (selection: PlanSelection) => void }) {
  const [expanded, setExpanded] = useState(new Set(Object.keys(draft.selection).map(Number).filter(id => Array.isArray(draft.selection[id]))));
  const occupancy = activeOccupancy(data.inbounds, data.users, data.plans);
  const toggleNode = (nodeID: number, checked: boolean) => { const next = { ...draft.selection }; if (checked) next[nodeID] = "all"; else delete next[nodeID]; onChange(next); };
  const toggleInbound = (nodeID: number, id: number, checked: boolean) => {
    const selection = draft.selection[nodeID];
    const existing = selection === "all" ? data.inbounds.filter(i => i.node_id === nodeID).map(i => i.id) : selection || [];
    const chosen = checked ? [...existing, id] : existing.filter(value => value !== id);
    const next = { ...draft.selection };
    if (chosen.length) next[nodeID] = data.inbounds.filter(i => i.node_id === nodeID && chosen.includes(i.id)).map(i => i.id);
    else delete next[nodeID];
    onChange(next);
  };
  return <div className={`pl-nodes ${draft.all_nodes ? "off" : ""}`}>{data.nodes.map(node => {
    const selection = draft.all_nodes ? "all" : draft.selection[node.id], partial = Array.isArray(selection), open = expanded.has(node.id);
    const inbounds = data.inbounds.filter(i => i.node_id === node.id);
    const status = !node.status ? node.draining ? ["info", "已暂停分配"] : ["", "未体检"] : !node.status.reachable ? ["bad", "连不上"] : node.draining ? ["info", "已暂停分配"] : !node.status.active ? ["warn", "代理程序未运行"] : ["ok", "运行中"];
    return <Fragment key={node.id}><div className={`pl-n ${open ? "exp" : ""}`}><label><Pickbox data-node={node.id} checked={!!selection} mixed={partial} disabled={draft.all_nodes || !writable} onChange={event => toggleNode(node.id, event.target.checked)} /><b><FlagText>{planNodeName(node)}</FlagText></b><StatusBadge tone={status[0]}>{status[1]}</StatusBadge></label><span className="tail">{!selection ? "" : partial ? `已选 ${selection.length} / ${inbounds.length} 个连接方式` : "含以后新加的连接方式"}</span><button type="button" className="ib sm" data-act="pl-exp" data-id={node.id} aria-label="展开连接方式" aria-expanded={open} onClick={() => setExpanded(current => { const next = new Set(current); if (next.has(node.id)) next.delete(node.id); else next.add(node.id); return next; })}><Icon name="arrow" /></button></div>
      {open && <div className="pl-ibs">{inbounds.length ? inbounds.map(inbound => <label className="pl-i" key={inbound.id}><input type="checkbox" data-ib={inbound.id} checked={selection === "all" || partial && selection.includes(inbound.id)} disabled={draft.all_nodes || !writable} onChange={event => toggleInbound(node.id, inbound.id, event.target.checked)} /><span>{planInboundLabel(inbound)}</span><span className="mono">:{inbound.port}</span>{!!inbound.max_users && occupancy[inbound.id] >= inbound.max_users && <span className="tag w">满员</span>}</label>) : <div className="pl-i sub">这台节点还没有连接方式</div>}</div>}
    </Fragment>;
  })}</div>;
}
export function PlanPreview({ data, draft, users }: { data: PlanData; draft: PlanDraft; users: User[] }) {
  const authorized = data.inbounds.filter(inbound => draft.all_nodes || draft.selection[inbound.node_id] === "all" || Array.isArray(draft.selection[inbound.node_id]) && (draft.selection[inbound.node_id] as number[]).includes(inbound.id));
  const occupancy = activeOccupancy(data.inbounds, data.users, data.plans), nodeIDs = [...new Set(authorized.map(i => i.node_id))];
  const reason = (inbound: PlanData["inbounds"][number]) => data.nodes.find(node => node.id === inbound.node_id)?.draining ? "暂停分配" : inbound.max_users && occupancy[inbound.id] >= inbound.max_users ? "满员" : "";
  return <div className="pl-pv"><h5>挂这个套餐的用户会拿到<small>{authorized.length ? `· ${nodeIDs.length} 个节点、${authorized.length} 个连接方式` : ""}</small></h5>{authorized.length ? nodeIDs.map(id => { const name = planNodeName(data.nodes.find(node => node.id === id)) || "节点"; return <div className="pl-g" key={id}><b><FlagText>{name}</FlagText></b>{authorized.filter(inbound => inbound.node_id === id).map(inbound => { const why = reason(inbound); return <div className={`pl-e ${why ? "dim" : ""}`} key={inbound.id}><span className={`pp-mk ${why ? "" : "ok"}`}><Icon name={why ? "ban" : "check"} /></span><FlagText>{`${name} · ${planInboundLabel(inbound)}`}</FlagText><span className="sp" /><span className="why">{why}</span></div>; })}</div>; }) : <div className="pl-none"><span className="ill"><Icon name="ban" /></span><b>无可用节点</b><span>{users.length ? `${planCountText(users.length, users.filter(user => user.status === "active").length)} 个用户将断开` : "挂上的用户什么都用不了"}</span></div>}
    {users.length > 0 && <div className="pl-sub"><div className="pl-sec">在用的用户</div><div className="pl-users">{users.slice(0, 12).map(user => <span className="tag" key={user.username}>{user.username}</span>)}{users.length > 12 && <span className="tag g">+{users.length - 12}</span>}</div></div>}
  </div>;
}
