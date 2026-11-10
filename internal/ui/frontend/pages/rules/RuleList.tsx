import { NativeSelect } from "@/components/native-select";
import { useEffect, useRef, useState } from "react";
import { Button } from "../../components/ui/button";
import { Card, FlagText, Icon } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { errorMessage, finalPath, RULE_REGIONS, RULE_TYPES, ruleValueText, targetName, type RouteRule, type RulesPageData, type RuleType } from "../../domain/routing";
import { TargetOptions, TargetPill } from "./components";

export interface RuleDraft { rule: RouteRule; index: number | null }
export function RuleList({ data, scope, draft, onDraft, onSave, onBuiltin }: {
  data: RulesPageData; scope: string; draft: RuleDraft | null; onDraft: (draft: RuleDraft | null) => void;
  onSave: (rules: RouteRule[], version: number, message: string) => Promise<void>; onBuiltin: () => void;
}) {
  const app = useApp();
  const canWrite = app.can("PUT", "/api/rules");
  const ownRules = scope ? data.rules.user_rules || [] : data.rules.global;
  const version = scope ? data.rules.user_version || 0 : data.rules.global_version;
  const [busyRows, setBusyRows] = useState<Set<number>>(() => new Set());
  const [moving, setMoving] = useState(false);
  const [drag, setDrag] = useState<number | null>(null);
  const [over, setOver] = useState<number | null>(null);
  const [finalBusy, setFinalBusy] = useState(false);
  function add() { onDraft({ index: null, rule: { type: "suffix", value: "", target: data.rules.final || (data.groups[0] ? "group:" + data.groups[0].id : "DIRECT"), enabled: true } }); }
  async function move(from: number, to: number) {
    if (from === to || !ownRules[from] || !ownRules[to] || moving) return;
    const list = ownRules.slice(); const [item] = list.splice(from, 1); list.splice(to, 0, item);
    setMoving(true);
    try { await onSave(list, version, `已移到第 ${to + 1} 条`); }
    catch (reason) { app.toast(errorMessage(reason), true); }
    finally { setMoving(false); }
  }
  async function toggle(rule: RouteRule, index: number) {
    if (busyRows.has(index)) return;
    setBusyRows(previous => new Set(previous).add(index));
    try { await write("PATCH", "/api/rules/" + rule.id, { enabled: !rule.enabled }); app.toast(rule.enabled ? "已停用，客户端更新订阅后生效" : "已启用"); }
    catch (reason) { app.toast(errorMessage(reason), true); }
    finally { setBusyRows(previous => { const next = new Set(previous); next.delete(index); return next; }); }
  }
  async function setFinal(target: string) {
    setFinalBusy(true);
    try { await write("PUT", finalPath(scope), { target }); app.toast(`其他全部改走 ${target ? targetName(target, data.groups) : "跟随全局"}`); }
    catch (reason) { app.toast(errorMessage(reason), true); }
    finally { setFinalBusy(false); }
  }
  function row(rule: RouteRule, index: number, own: boolean, editable: boolean) {
    if (editable && draft?.index === index) return <RuleEditor key={"edit:" + index} data={data} draft={draft} onDraft={onDraft} rules={ownRules} version={version} onSave={onSave} />;
    const entry = rule.type === "set" ? data.catalog.find(item => item.key === rule.value) : null;
    const value = ruleValueText(rule, data.catalog);
    return <div key={(own ? "own:" : "global:") + (rule.id || index)} className={["rp-row", !rule.enabled && "off", own && scope && "own", drag === index && "drag", over === index && "over"].filter(Boolean).join(" ")} draggable={editable ? true : undefined} data-i={index}
      onDragStart={editable ? event => { event.dataTransfer.setData("text/plain", String(index)); setDrag(index); } : undefined}
      onDragEnd={editable ? () => { setDrag(null); setOver(null); } : undefined}
      onDragOver={editable ? event => { event.preventDefault(); setOver(index); } : undefined}
      onDragLeave={editable ? () => setOver(null) : undefined}
      onDrop={editable ? event => { event.preventDefault(); setOver(null); setDrag(null); void move(Number(event.dataTransfer.getData("text/plain")), index); } : undefined}>
      <span className="rp-grip" title="拖动排序">{editable && <Icon name="grip" />}</span><span className="rp-n">{index + 1}</span>
      <span className="rp-tyc"><span className="rp-ty">{RULE_TYPES[rule.type] || rule.type}</span></span><span className={"rp-v " + (rule.type === "set" ? "" : "mono")} title={value}><FlagText>{value}</FlagText>{entry && <> <span className="sub">{entry.desc}</span></>}{rule.note && <> <span className="sub">· {rule.note}</span></>}</span>
      <span className="rp-a"><Icon name="arrow" /></span><TargetPill target={rule.target} groups={data.groups} /><span className="sp" />{own && scope && <span className="pill on">仅 {scope}</span>}
      {editable && <><label className="sw" title={rule.enabled ? "停用" : "启用"}><input type="checkbox" data-act="rp-toggle" data-i={index} aria-label={`启用第 ${index + 1} 条`} checked={rule.enabled} disabled={busyRows.has(index)} aria-busy={busyRows.has(index) || undefined} onChange={() => void toggle(rule, index)} /><i /></label><Button size="icon-sm" data-act="rp-edit" data-i={index} title="编辑" aria-label={`编辑第 ${index + 1} 条`} onClick={() => onDraft({ index, rule: { ...rule } })}><Icon name="more" /></Button></>}
    </div>;
  }
  const finalValue = scope ? data.rules.user_final || "" : data.rules.final;
  return <Card className="rp-card" aria-busy={moving || undefined}>
    {!scope && <div className="ch"><span className="rp-instruction">从上往下匹配，第一条对上的生效</span><span className="sp" />{canWrite && <><Button size="sm" data-act="rp-builtin" onClick={onBuiltin}><Icon name="plus" />内置规则组</Button><Button variant="primary" size="sm" data-act="rp-add" onClick={add}><Icon name="plus" />添加规则</Button></>}</div>}
    {!scope && draft?.index === null && <RuleEditor data={data} draft={draft} onDraft={onDraft} rules={ownRules} version={version} onSave={onSave} />}
    <div className="rp-list">
      {scope && <><div className="rp-sec">只对 {scope} 生效 · 排在全局规则前面<span className="sp" />{canWrite && <Button size="sm" data-act="rp-add" onClick={add}><Icon name="plus" />给 {scope} 加一条</Button>}</div>
        {draft?.index === null && <RuleEditor data={data} draft={draft} onDraft={onDraft} rules={ownRules} version={version} onSave={onSave} />}
        {ownRules.length ? ownRules.map((rule, index) => row(rule, index, true, canWrite)) : <div className="rp-row sub">还没有专属规则，{scope} 完全跟随全局。</div>}<div className="rp-sec">全局规则(只读，去「全部用户」里改)</div></>}
      {data.rules.global.map((rule, index) => row(rule, index + (scope ? ownRules.length : 0), !scope, !scope && canWrite))}
      <div className="rp-row final"><span className="rp-grip" /><span className="rp-n"><Icon name="infinity" /></span><span className="rp-tyc"><span className="rp-ty">兜底</span></span><span className="rp-v"><b>其他全部</b> <span className="sub">上面都没对上的</span></span><span className="rp-a"><Icon name="arrow" /></span>
        {canWrite ? <NativeSelect className={"in w220" + (finalBusy ? " busy" : "")} id="rp-final" aria-label="其他全部的去向" value={finalValue} disabled={finalBusy} aria-busy={finalBusy || undefined} onChange={event => void setFinal(event.target.value)}><TargetOptions data={data} follow={scope ? `跟随全局(${targetName(data.rules.final, data.groups)})` : undefined} /></NativeSelect> : <TargetPill target={finalValue || data.rules.final} groups={data.groups} />}<span className="sp" />
      </div>
    </div>
  </Card>;
}
function RuleEditor({ data, draft, onDraft, rules, version, onSave }: {
  data: RulesPageData; draft: RuleDraft; onDraft: (value: RuleDraft | null) => void; rules: RouteRule[]; version: number;
  onSave: (rules: RouteRule[], version: number, message: string) => Promise<void>;
}) {
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const input = useRef<HTMLInputElement | HTMLSelectElement | null>(null);
  useEffect(() => { input.current?.focus(); }, [draft.index, draft.rule.type]);
  const update = (patch: Partial<RouteRule>) => onDraft({ ...draft, rule: { ...draft.rule, ...patch } });
  async function save(remove = false) {
    const value = draft.rule.value.trim();
    if (!remove && !value) { setError("先填匹配的内容"); input.current?.focus(); return; }
    setSaving(true); setError("");
    const list = rules.slice();
    if (remove && draft.index !== null) list.splice(draft.index, 1);
    else if (draft.index === null) list.unshift({ ...draft.rule, value });
    else list[draft.index] = { ...draft.rule, value };
    try { await onSave(list, version, remove ? "已删除" : draft.index === null ? "已添加，客户端更新订阅后生效" : "已保存"); onDraft(null); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setSaving(false); }
  }
  return <div className="rp-row editing"><span className="rp-grip" /><span className="rp-n">{draft.index === null ? "新" : <Icon name="edit" />}</span>
    <NativeSelect className="in w140" id="re-type" aria-label="类型" value={draft.rule.type} onChange={event => { const type = event.target.value as RuleType; update({ type, value: type === "set" ? data.catalog[0]?.key || "" : type === "geoip" ? "CN" : "" }); }} disabled={saving}>{Object.entries(RULE_TYPES).map(([type, label]) => <option key={type} value={type}>{label}</option>)}</NativeSelect>
    <div className="grow">{draft.rule.type === "set" || draft.rule.type === "geoip" ? <NativeSelect className="in" id="re-val" aria-label="匹配内容" ref={element => { input.current = element; }} value={draft.rule.value} onChange={event => update({ value: event.target.value })} disabled={saving}>{draft.rule.type === "set" ? data.catalog.map(entry => <option key={entry.key} value={entry.key}>{entry.name} · {entry.desc}</option>) : Object.entries(RULE_REGIONS).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</NativeSelect> : <input className="in mono" id="re-val" aria-label="匹配内容" aria-invalid={!!error || undefined} aria-describedby="re-err" ref={element => { input.current = element; }} value={draft.rule.value} autoComplete="off" spellCheck={false} disabled={saving} placeholder={draft.rule.type === "suffix" ? "example.com(含子域名)" : draft.rule.type === "keyword" ? "google" : "91.108.4.0/22"} onChange={event => update({ value: event.target.value })} onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); void save(); } }} />}</div>
    <span className="rp-a"><Icon name="arrow" /></span><NativeSelect className="in w220" id="re-to" aria-label="去向" value={draft.rule.target} disabled={saving} onChange={event => update({ target: event.target.value })}><TargetOptions data={data} /></NativeSelect>
    <Button variant="primary" size="sm" data-act="rp-save" loading={saving} onClick={() => void save()}>{draft.index === null ? "添加" : "保存"}</Button><Button variant="ghost" size="sm" data-act="rp-cancel" disabled={saving} onClick={() => onDraft(null)}>取消</Button>{draft.index !== null && <Button size="icon-sm" data-act="rp-del" title="删除" aria-label="删除这条" disabled={saving} onClick={() => void save(true)}><Icon name="x" /></Button>}
    <div className="err" id="re-err" role="alert" style={{ flexBasis: "100%" }}>{error}</div>
  </div>;
}
