import { FlagText, Icon } from "../../components/primitives";
import { planCountText, planInboundLabel, planNodeName, planSelection, sortedPlans, type Plan, type PlanData } from "../../domain/plans";
function PlanIncludedTags({ plan, data }: { plan: Plan; data: PlanData }) {
  if (plan.all_nodes) return <span className="tag all"><Icon name="globe" />全部节点(含以后新加的)</span>;
  const selection = planSelection(plan, data.inbounds);
  if (!Object.keys(selection).length) return <span className="tag none"><Icon name="ban" />无可用节点</span>;
  return <>{Object.entries(selection).map(([id, value]) => { const name = planNodeName(data.nodes.find(node => node.id === +id)) || "节点"; return <span className="tag" key={id}><FlagText>{value === "all" ? name : `${name} · 仅 ${value.map(inboundID => { const inbound = data.inbounds.find(i => i.id === inboundID); return inbound ? planInboundLabel(inbound) : "连接方式"; }).join("、")}`}</FlagText></span>; })}</>;
}
function PlanWarnings({ plan, data }: { plan: Plan; data: PlanData }) {
  const empty = !plan.all_nodes && !plan.node_ids?.length && !plan.inbound_ids?.length;
  const ids = new Set(plan.node_ids || []);
  for (const inbound of data.inbounds) if (plan.inbound_ids?.includes(inbound.id)) ids.add(inbound.node_id);
  return <>{empty && plan.users > 0 && <span className="tag none"><Icon name="warn" />挂着的 {plan.users} 个用户什么都用不了</span>}{!plan.all_nodes && data.nodes.some(node => node.draining && ids.has(node.id)) && <span className="tag g">含暂停分配的节点</span>}</>;
}
export function PlanList({ data, onEdit, onUsers }: { data: PlanData; onEdit: (id: number) => void; onUsers: (id: number) => void }) {
  const plans = sortedPlans(data.plans);
  const only = plans.length === 1 && plans[0].is_default;
  return <><div className="card">{plans.map(plan => <div className="pl-row" key={plan.id} data-act="pl-edit" data-id={plan.id} role="button" tabIndex={0} aria-label={`编辑套餐 ${plan.name}`} onClick={() => onEdit(plan.id)} onKeyDown={event => { if ((event.key === "Enter" || event.key === " ") && event.target === event.currentTarget) { event.preventDefault(); onEdit(plan.id); } }}><div className="pl-name"><b title={plan.name}>{plan.name}{plan.is_default && <> <span className="pl-def">默认</span></>}</b>{plan.note && <span title={plan.note}>{plan.note}</span>}</div><div className="pl-inc"><PlanIncludedTags plan={plan} data={data} /><PlanWarnings plan={plan} data={data} /></div><a className={`pl-uc ${plan.users ? "" : "zero"}`} href="#users" data-act="pl-users" data-id={plan.id} onClick={event => { event.preventDefault(); event.stopPropagation(); onUsers(plan.id); }}><Icon name="users" />{planCountText(plan.users, plan.active_users)}</a><Icon name="arrow" /></div>)}</div>{only && <div className="callout stone mt"><Icon name="globe" /><div>所有 {planCountText(plans[0].users, plans[0].active_users)} 个用户都在默认套餐，能用全部节点。要分开，就新建套餐。</div></div>}</>;
}
