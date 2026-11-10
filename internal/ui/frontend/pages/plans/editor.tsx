import { NativeSelect } from "@/components/native-select";
import { useState } from "react";
import { Button } from "../../components/ui/button";
import { DrawerBody, DrawerFooter, Field, Icon } from "../../components/primitives";
import { useApp, type AppContextValue } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { planBody, planCountText, planSelection, sortedPlans, type Plan, type PlanData, type PlanDraft } from "../../domain/plans";
import { PlanAuthorization, PlanPreview } from "./authorization";
function ReassignPlan({ plans, choice }: { plans: Plan[]; choice: { id: string } }) {
  const [value, setValue] = useState(choice.id);
  return <div className="field"><label htmlFor="pl-to">这些用户改挂到</label><NativeSelect className="in" id="pl-to" value={value} onChange={event => { choice.id = event.target.value; setValue(event.target.value); }}>{plans.map(plan => <option value={plan.id} key={plan.id}>「{plan.name}」</option>)}</NativeSelect></div>;
}
export function deletePlan(app: AppContextValue, plan: Plan, data: PlanData, onDeleted: () => Promise<void>) {
  const users = data.users.filter(user => user.plan_id === plan.id), others = sortedPlans(data.plans.filter(item => item.id !== plan.id));
  const choice = { id: String((others.find(item => item.is_default) || others[0])?.id || "") };
  app.confirmBox({ title: `删除「${plan.name}」`, danger: true, ok: "删除", text: users.length ? `${planCountText(users.length, users.filter(user => user.status === "active").length)} 个用户挂着这个套餐。` : "没有用户挂着这个套餐。", extra: users.length ? <ReassignPlan plans={others} choice={choice} /> : undefined,
    run: async () => {
      if (users.length && !choice.id) throw new Error("请先选择这些用户改挂到的套餐");
      await write("DELETE", `/api/plans/${plan.id}${users.length ? `?reassign=${choice.id}` : ""}`);
      await onDeleted();
      app.toast(`已删除「${plan.name}」`);
    },
  });
}
export function PlanEditor({ plan, data, onClose, onSaved }: { plan: Plan | null; data: PlanData; onClose: () => void; onSaved: () => Promise<void> }) {
  const app = useApp();
  const [draft, setDraft] = useState<PlanDraft>({ name: plan?.name || "", note: plan?.note || "", all_nodes: !!plan?.all_nodes, selection: planSelection(plan, data.inbounds) });
  const [busy, setBusy] = useState(false), [error, setError] = useState("");
  const writable = app.can(plan ? "PUT" : "POST", "/api/plans");
  const users = plan ? data.users.filter(user => user.plan_id === plan.id) : [];
  const empty = !draft.all_nodes && !Object.keys(draft.selection).length;
  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!writable || busy) return;
    if (!draft.name.trim()) { setError("名字不能为空"); return; }
    setBusy(true); setError("");
    try {
      await write(plan ? "PUT" : "POST", plan ? `/api/plans/${plan.id}` : "/api/plans", planBody(plan, draft));
      await onSaved(); app.toast(`套餐「${draft.name.trim()}」已保存`); onClose();
    } catch (failure) { setError((failure as Error).message); setBusy(false); }
  };
  return <form id="pl-form" onSubmit={event => void save(event)}><div className="dh"><h2 className="ell">{plan?.name || "新建套餐"}</h2>{plan?.is_default && <span className="pl-def">默认</span>}{plan && <span className={`pill ${users.length ? "on" : ""}`}>{planCountText(plan.users, plan.active_users)} 个用户在用</span>}<span style={{ flex: 1 }} /><button type="button" className="ib" data-act="close" aria-label="关闭" disabled={busy} onClick={onClose}><Icon name="x" /></button></div>
    <DrawerBody><div id="pl-warn">{empty && <div className="callout mb"><Icon name="warn" className="ico-warn" /><div><b>空套餐</b>:{plan?.is_default ? "默认套餐里的用户和以后新建的用户都将无法使用任何节点" : "挂着的用户将无法使用任何节点"}</div></div>}</div><div className="pl-ed"><div><div className="two"><Field label="名字" htmlFor="pl-name"><input className={`in ${error === "名字不能为空" ? "bad" : ""}`} id="pl-name" value={draft.name} readOnly={plan?.is_default} placeholder="美日" autoComplete="off" disabled={!writable || busy} aria-invalid={error === "名字不能为空"} aria-describedby={error ? "pl-err" : undefined} onChange={event => setDraft(current => ({ ...current, name: event.target.value }))} /></Field><Field label="备注" htmlFor="pl-note"><input className="in" id="pl-note" value={draft.note} placeholder="可选" autoComplete="off" disabled={!writable || busy} onChange={event => setDraft(current => ({ ...current, note: event.target.value }))} /></Field></div><div className="pl-all"><div className="grow"><b>全部节点</b><span>包括以后新加的节点</span></div><label className="sw"><input type="checkbox" id="pl-all" aria-label="全部节点" checked={draft.all_nodes} disabled={!writable || busy} onChange={event => setDraft(current => ({ ...current, all_nodes: event.target.checked }))} /><i /></label></div><div className="pl-sec">节点<small id="pl-cnt">{draft.all_nodes ? "" : `· 已选 ${Object.keys(draft.selection).length} / ${data.nodes.length}`}</small></div><div id="pl-nodes"><PlanAuthorization data={data} draft={draft} writable={writable && !busy} onChange={selection => setDraft(current => ({ ...current, selection }))} /></div></div><div id="pl-pv"><PlanPreview data={data} draft={draft} users={users} /></div></div><div className="err" id="pl-err" role="alert">{error}</div></DrawerBody>
    <DrawerFooter>{plan?.is_default ? <span className="sub">新用户自动进这里 · 不能删除</span> : plan && app.can("DELETE", "/api/plans") && <Button variant="ghost" className="c-bad" data-act="pl-del" data-id={plan.id} disabled={busy} onClick={() => deletePlan(app, plan, data, async () => { await onSaved(); onClose(); })}><Icon name="trash" />删除</Button>}<span style={{ flex: 1 }} />{writable ? <><Button variant="ghost" data-act="close" disabled={busy} onClick={onClose}>取消</Button><Button type="submit" variant="primary" id="pl-save" loading={busy}>保存</Button></> : <Button data-act="close" onClick={onClose}>关闭</Button>}</DrawerFooter>
  </form>;
}
