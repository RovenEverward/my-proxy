import { useState } from "react";
import { Button } from "../../components/ui/button";
import { DrawerBody, DrawerFooter, DrawerHeader, ErrorState, Field, Icon } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { usePageData } from "../../runtime/query";
import { write } from "../../runtime/api";
import { RESET, USERNAME_RE, asModelUser, parseBytes, parseExpire, userPath, ymd, type User } from "../../domain/users";
import { planInboundLabel, planNodeCount, planNodeName, sortedPlans, type Inbound, type Node, type Plan } from "../../domain/plans";
import { NodeName } from "./components";
import { readModifyUser } from "./hooks";
interface EditorData { user: User | null; inbounds: Inbound[]; nodes: Node[]; plans: Plan[] }
export function UserEditor({ name, onClose, onSaved }: { name?: string; onClose: () => void; onSaved: (name: string, created: boolean) => Promise<void> }) {
  const query = usePageData<EditorData>(`users/editor/${name || "new"}`, async read => {
    const [user, inbounds, nodes, plans] = await Promise.all([name ? read<User>(userPath(name)) : Promise.resolve(null), read<Inbound[]>("/api/inbounds"), read<Node[]>("/api/nodes"), read<Plan[]>("/api/plans")]);
    return { user, inbounds: inbounds || [], nodes: nodes || [], plans: plans || [] };
  }, { poll: 0 });
  if (!query.data) return <><DrawerHeader title={name ? `编辑 ${name}` : "添加用户"} onClose={onClose} /><DrawerBody aria-busy={query.loading || undefined}>{query.error ? <ErrorState title="用户编辑资料" error={query.error} onRetry={() => void query.refresh()} /> : <p role="status">正在读取编辑资料…</p>}</DrawerBody></>;
  return <UserForm initial={query.data} onClose={onClose} onSaved={onSaved} />;
}
export function UserForm({ initial, onClose, onSaved }: { initial: EditorData; onClose: () => void; onSaved: (name: string, created: boolean) => Promise<void> }) {
  const app = useApp(), user = initial.user, isNew = !user;
  const planList = sortedPlans(initial.plans), currentPlan = user?.plan_id != null ? String(user.plan_id) : "";
  const [username, setUsername] = useState(user?.username || "");
  const [planID, setPlanID] = useState(isNew ? String(planList.find(p => p.is_default)?.id || "") : currentPlan);
  const [limit, setLimit] = useState(user?.data_limit ? user.data_limit % 2 ** 30 === 0 ? `${user.data_limit / 2 ** 30}GB` : `${user.data_limit}B` : "");
  const [expire, setExpire] = useState(user?.expire_at ? ymd(new Date(user.expire_at)) : "");
  const [reset, setReset] = useState(user?.reset_strategy || "none");
  const [selected, setSelected] = useState(new Set(user?.inbound_ids || []));
  const [note, setNote] = useState(user?.note || ""), [disabled, setDisabled] = useState(false);
  const [busy, setBusy] = useState(false), [errors, setErrors] = useState<Record<string, string>>({});
  const plan = planList.find(p => String(p.id) === planID), allNodes = !!plan?.all_nodes;
  const allPlan = planList.find(p => p.all_nodes && planNodeCount(p, initial.nodes, initial.inbounds));
  const empty = !!plan && !planNodeCount(plan, initial.nodes, initial.inbounds);
  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (busy || isNew && empty) return;
    const problems: Record<string, string> = {};
    const name = isNew ? username.trim() : user!.username;
    if (isNew && !USERNAME_RE.test(name)) problems.username = "用户名只能用字母、数字、下划线、点和减号，长度 1-64";
    let dataLimit = 0, expiry: string | null = null;
    try { dataLimit = limit.trim() ? parseBytes(limit.trim()) : 0; } catch (error) { problems.limit = (error as Error).message; }
    try { expiry = expire.trim() ? parseExpire(expire.trim()) : null; } catch (error) { problems.expire = (error as Error).message; }
    setErrors(problems);
    if (Object.keys(problems).length) return;
    setBusy(true);
    const inbounds = allNodes ? [] : [...selected], planChanged = isNew ? planID !== "" : planID !== "" && planID !== currentPlan;
    try {
      if (isNew) {
        const body = asModelUser({ username: name, enabled: !disabled, reset_strategy: reset, note, data_limit: dataLimit, expire_at: expiry || undefined, inbound_ids: inbounds });
        if (planChanged) body.plan_id = +planID;
        await write("POST", "/api/users", body);
      } else {
        await readModifyUser(name, model => {
          model.data_limit = dataLimit;
          if (expiry) model.expire_at = expiry; else delete model.expire_at;
          model.reset_strategy = reset;
          if (inbounds.length) model.inbound_ids = inbounds; else delete model.inbound_ids;
          if (note) model.note = note; else delete model.note;
          if (planChanged) model.plan_id = +planID;
        });
        app.toast(`已更新用户 ${name}`);
      }
      await onSaved(name, isNew);
    } catch (error) { setErrors({ server: (error as Error).message }); setBusy(false); }
  };
  const toggleInbound = (id: number, checked: boolean) => setSelected(current => { const next = new Set(current); if (checked) next.add(id); else next.delete(id); return next; });
  return <form id="user-form" noValidate onSubmit={event => void submit(event)}><DrawerHeader title={isNew ? "添加用户" : `编辑 ${user!.username}`} onClose={busy ? undefined : onClose} /><DrawerBody>
    <Field label="用户名" htmlFor="f-name" hint={`字母、数字、下划线、点和减号，1–64 位${isNew ? "" : ";建好后不能改"}`}><input className="in" id="f-name" name="username" value={username} disabled={!isNew || busy} autoFocus={isNew} placeholder="alice" autoComplete="off" aria-invalid={!!errors.username} aria-describedby={errors.username ? "f-err" : "f-name-hint"} onChange={event => setUsername(event.target.value)} /></Field>
    <div className="field"><label id="f-plan-l">套餐</label><div className="plans" role="radiogroup" aria-labelledby="f-plan-l" id="f-plans">{!isNew && user!.plan_id == null && <label className="plan"><input type="radio" name="plan" value="" checked={planID === ""} disabled readOnly /><span className="rd" /><span className="plan-t"><b>未挂套餐</b><span className="sub">单独授权;选一个套餐后不能再移出</span></span></label>}{planList.map(p => { const count = planNodeCount(p, initial.nodes, initial.inbounds); return <label className="plan" key={p.id}><input type="radio" name="plan" value={p.id} checked={planID === String(p.id)} disabled={busy} onChange={() => setPlanID(String(p.id))} /><span className="rd" /><span className="plan-t"><b>{p.name}</b>{(p.note || p.is_default) && <span className="sub">{p.note || "新用户默认挂这个"}</span>}</span><span className={`plan-n ${count ? "" : "zero"}`}>{count ? `${count} 个节点` : "没有节点"}</span></label>; })}</div>
      {planList.filter(p => !planNodeCount(p, initial.nodes, initial.inbounds)).map(p => <div className="block" role="alert" data-plan={p.id} hidden={planID !== String(p.id)} key={p.id}><Icon name="warn" /><div><b>「{p.name}」里还没有节点</b><p>挂在它上面的用户什么都连不上。先给套餐加节点，或者换一个套餐。</p><div className="block-a"><a className="btn sm" href="#users/plans"><Icon name="plus" />给「{p.name}」加节点</a>{allPlan && <Button size="sm" variant="ghost" data-act="f-plan-all" onClick={() => setPlanID(String(allPlan.id))}>换成「{allPlan.name}」</Button>}</div></div></div>)}
    </div>
    <div className="two"><Field label="流量上限" htmlFor="f-limit" hint="B / KB / MB / GB / TB，按 1024 进位"><input className="in" id="f-limit" name="limit" value={limit} placeholder="100GB，留空不限" disabled={busy} aria-invalid={!!errors.limit} aria-describedby={errors.limit ? "f-err" : "f-limit-hint"} onChange={event => setLimit(event.target.value)} /></Field><Field label="到期日" htmlFor="f-expire" hint="本机时区当天 23:59:59"><input className="in" id="f-expire" name="expire" value={expire} placeholder="YYYY-MM-DD，留空永不" disabled={busy} aria-invalid={!!errors.expire} aria-describedby={errors.expire ? "f-err" : "f-expire-hint"} onChange={event => setExpire(event.target.value)} /></Field></div>
    <details className="fmts more" id="f-more" open={!isNew && (!!user?.note || !!user?.inbound_ids?.length || !!user?.reset_strategy && user.reset_strategy !== "none") || undefined}><summary><Icon name="arrow" className="car" />更多选项<span className="sub">清零周期、额外授权、备注</span></summary><div className="more-b"><div className="field"><label>流量重置</label><div className="chips">{Object.entries(RESET).map(([key, label]) => <label className="chip" key={key}><input type="radio" name="reset" value={key} checked={reset === key} disabled={busy} onChange={() => setReset(key)} />{label}</label>)}</div></div>
      <div className="field"><label id="f-inb-label">{!plan ? "可用连接方式" : allNodes ? "额外授权" : "额外授权(可选)"}</label><div className="chips">{initial.inbounds.length ? initial.inbounds.map(inbound => <label className="chip" key={inbound.id}><input type="checkbox" name="inb" value={inbound.id} checked={selected.has(inbound.id)} disabled={allNodes || busy} onChange={event => toggleInbound(inbound.id, event.target.checked)} /><NodeName name={planNodeName(initial.nodes.find(n => n.id === inbound.node_id)) || "节点"} /> · {planInboundLabel(inbound)}</label>) : <span className="sub">还没有连接方式</span>}</div><span className="h" id="f-inb-h">{!plan ? "一个都不选 = 全部连接方式，含以后新加的" : allNodes ? "全部节点套餐不需要额外授权" : "在套餐之外再给这个用户开放的连接方式"}</span></div>
      <Field label="备注" htmlFor="f-note"><input className="in" id="f-note" name="note" value={note} placeholder="可选" disabled={busy} onChange={event => setNote(event.target.value)} /></Field>{isNew && <label className="chk"><input type="checkbox" name="disabled" checked={disabled} disabled={busy} onChange={event => setDisabled(event.target.checked)} /> 先不启用(建好后手动启用)</label>}</div></details><div className="err" id="f-err" role="alert">{Object.values(errors).join("\n")}</div>
  </DrawerBody><DrawerFooter><span className="sub df-why" id="f-why" hidden={!isNew || !empty}><Icon name="warn" />选的套餐没有节点</span><span className="sp" style={{ flex: 1 }} /><Button data-act="close" disabled={busy} onClick={onClose}>取消</Button><Button type="submit" variant="primary" id="f-save" loading={busy} disabled={isNew && empty}>{isNew ? "创建" : "保存"}</Button></DrawerFooter></form>;
}
