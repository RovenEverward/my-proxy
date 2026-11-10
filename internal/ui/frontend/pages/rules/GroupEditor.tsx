import { NativeSelect } from "@/components/native-select";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "../../components/ui/button";
import { Card, DrawerBody, DrawerFooter, DrawerHeader, Field, FlagText, Icon, LoadingTable } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { api, batchWrites, write } from "../../runtime/api";
import { errorMessage, groupPath, previewInit, RULE_CAPS, RULE_REGIONS, type GroupPreview, type ProxyGroup, type RulesPageData, type SavedProxyGroup } from "../../domain/routing";
import { RuleWarning } from "./components";

type Conditions = "regions" | "protocols" | "capabilities";
const toggleValue = (values: string[], value: string) => values.includes(value) ? values.filter(item => item !== value) : [...values, value];
export function GroupEditor({ data, original }: { data: RulesPageData; original?: SavedProxyGroup }) {
  const app = useApp();
  const [draft, setDraft] = useState<ProxyGroup>(() => ({ ...(original || { name: "", icon: "", mode: "auto" }), regions: [...(original?.regions || [])], protocols: [...(original?.protocols || [])], capabilities: [...(original?.capabilities || [])], tags: [...(original?.tags || [])] }));
  const [origins, setOrigins] = useState<string[]>(() => [...(original ? data.origins[original.id] || [] : [])]);
  const initialOrigins = useRef(JSON.stringify(origins));
  const savedID = useRef(original?.id);
  const initialPreview = original ? data.previews[original.id] : null;
  const [preview, setPreview] = useState<GroupPreview[] | null>(initialPreview || null);
  const [choices, setChoices] = useState<GroupPreview[]>(initialPreview || []);
  const [user, setUser] = useState("");
  const [previewError, setPreviewError] = useState("");
  const [previewing, setPreviewing] = useState(true);
  const [previewRetry, setPreviewRetry] = useState(0);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const canWrite = app.can(original ? "PUT" : "POST", "/api/proxy-groups/x");
  const conditionsKey = JSON.stringify([draft.regions, draft.protocols, draft.capabilities, draft.tags, origins, user, previewRetry]);
  const currentDraft = useRef(draft); currentDraft.current = draft;
  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setPreviewing(true); setPreviewError("");
    const query = new URLSearchParams({ sources: origins.join(",") });
    if (user) query.set("user", user);
    void api<GroupPreview[]>("/api/proxy-groups/preview?" + query, { ...previewInit(currentDraft.current), signal: controller.signal }).then(result => {
      if (!active) return;
      setPreview(result);
      setChoices(previous => {
        const seen = new Set(previous.map(entry => `${entry.node_id}:${entry.inbound_id}:${entry.host_id}:${entry.source_id || 0}`));
        return [...previous, ...result.filter(entry => !seen.has(`${entry.node_id}:${entry.inbound_id}:${entry.host_id}:${entry.source_id || 0}`))];
      });
    }).catch((reason: unknown) => { if (active) setPreviewError(errorMessage(reason)); }).finally(() => { if (active) setPreviewing(false); });
    return () => { active = false; controller.abort(); };
  // Text fields do not affect membership.
  }, [conditionsKey]);
  const updateCondition = (key: Conditions, value: string) => setDraft(previous => ({ ...previous, [key]: toggleValue(previous[key] || [], value) }));
  const conditionChoices = (key: Conditions, candidates: string[], labels: Record<string, string>) => {
    const values = [...new Set([...candidates, ...(draft[key] || [])])];
    return values.length ? <div className="chips" data-key={key}>{values.map(value => <label className="chip" key={value}><input type="checkbox" value={value} checked={(draft[key] || []).includes(value)} onChange={() => updateCondition(key, value)} /><FlagText>{labels[value] || value}</FlagText></label>)}</div> : <span className="sub">还没有节点带这一项</span>;
  };
  const sourceChoices = [{ value: "managed", label: "自建节点" }, ...data.sources.map(source => ({ value: "source:" + source.id, label: source.name })), ...origins.filter(value => value !== "managed" && !data.sources.some(source => "source:" + source.id === value)).map(value => ({ value, label: "来源已删除 · " + value }))];
  async function save(event: FormEvent) {
    event.preventDefault();
    if (saving || !canWrite) return;
    if (!draft.name.trim()) { setError("名字不能为空"); return; }
    setSaving(true); setError("");
    try {
      await batchWrites(async () => {
        const body = { ...draft, name: draft.name.trim(), icon: draft.icon?.trim(), ...(savedID.current ? { id: savedID.current } : {}) };
        const saved = await write<SavedProxyGroup>(savedID.current ? "PUT" : "POST", savedID.current ? groupPath(savedID.current) : "/api/proxy-groups", body);
        savedID.current = saved.id;
        if (JSON.stringify(origins) !== initialOrigins.current) {
          try { await write("PUT", groupPath(saved.id) + "/sources", { sources: origins }); }
          catch (reason) { throw new Error("组配置已保存，来源条件未保存。重试只更新这个组：" + errorMessage(reason)); }
        }
      });
      app.toast(`策略组「${draft.name.trim()}」已保存`); app.closeOverlays();
    } catch (reason) { setError(errorMessage(reason)); }
    finally { setSaving(false); }
  }
  function remove() {
    if (!original) return;
    app.confirmBox({ title: `删除策略组 ${original.name}？`, text: "删除后订阅里不再有这个组。", ok: "删除", danger: true,
      run: async () => { await write("DELETE", groupPath(original.id)); app.toast(`策略组「${original.name}」已删除`); app.closeOverlays(); } });
  }
  const count = preview?.filter(entry => entry.admitted).length || 0;
  return <form id="gp-form" noValidate onSubmit={event => void save(event)}>
    <DrawerHeader title={original?.name || "新建策略组"} onClose={app.closeOverlays} />
    <DrawerBody>
      <Field label="名字" htmlFor="gp-name" error={!draft.name.trim() && error ? error : undefined}><div className="flex8"><input className="in" style={{ width: 64, flex: "none", textAlign: "center" }} id="gp-icon" value={draft.icon || ""} placeholder="🌐" aria-label="图标" onChange={event => setDraft(previous => ({ ...previous, icon: event.target.value }))} /><input className="in" style={{ flex: 1, minWidth: 0 }} id="gp-name" value={draft.name} placeholder="比如 日本节点" autoComplete="off" onChange={event => setDraft(previous => ({ ...previous, name: event.target.value }))} /></div></Field>
      <Field label={<>收哪些节点 <span className="sub">· 各类之间要同时满足，不选就不限</span></>}><></></Field>
      <Field label={<span className="sub">来源（任一，不选表示全部）</span>}><div className="chips">{sourceChoices.map(choice => <label className="chip" key={choice.value}><input type="checkbox" data-source-choice="" value={choice.value} checked={origins.includes(choice.value)} onChange={() => setOrigins(previous => toggleValue(previous, choice.value))} />{choice.label}</label>)}</div></Field>
      <Field label={<span className="sub">地区(任一)</span>}>{conditionChoices("regions", choices.flatMap(entry => entry.region ? [entry.region] : []), RULE_REGIONS)}</Field>
      <Field label={<span className="sub">协议(任一)</span>}>{conditionChoices("protocols", choices.map(entry => entry.protocol).filter(Boolean), {})}</Field>
      <Field label={<span className="sub">能力检测(全部通过)</span>}>{conditionChoices("capabilities", Object.keys(RULE_CAPS), RULE_CAPS)}</Field>
      {!!draft.tags?.length && <div id="gp-oldtags"><RuleWarning>这个策略组还留着旧的「标签」条件({draft.tags.join("、")})。从 v0.7.0 起节点不再带标签，这个条件什么都匹配不上，组里会一直是空的。<div className="mt6"><Button size="sm" id="gp-cleartags" onClick={() => setDraft(previous => ({ ...previous, tags: [] }))}>去掉旧的标签条件</Button></div></RuleWarning></div>}
      <Field label="怎么选" hint="自动：客户端定时测网页延迟，挑最快的。手动：用户在客户端里自己选。"><div className="seg" id="gp-mode">{(["auto", "manual"] as const).map(mode => <Button key={mode} className={draft.mode === mode ? "on" : ""} data-m={mode} aria-pressed={draft.mode === mode} onClick={() => setDraft(previous => ({ ...previous, mode }))}>{mode === "auto" ? "自动选最快" : "我手动选"}</Button>)}</div></Field>
      <Field label="预览用户" htmlFor="gp-user" hint="选择用户后，预览该用户已获授权的外部节点。"><NativeSelect className="in" id="gp-user" value={user} onChange={event => setUser(event.target.value)}><option value="">全部自建节点</option>{data.users.map(name => <option key={name} value={name}>{name}</option>)}</NativeSelect></Field>
      <Card className="mt"><div className="ch">现在会收进来<small id="gp-cnt">{preview && `· ${count} / ${preview.length} 个节点`}</small></div><div id="gp-pv" aria-busy={previewing}>
        {previewError ? <div className="pad16 c-bad" role="alert">{previewError}<Button size="sm" onClick={() => setPreviewRetry(value => value + 1)}>重试预览</Button></div> : !preview ? <LoadingTable rows={3} columns={2} /> : preview.length ? preview.map((entry, index) => <div className={"row pp-pvr " + (entry.admitted ? "" : "miss")} key={`${entry.node_id}:${entry.inbound_id}:${entry.host_id}:${entry.source_id || 0}:${index}`}><span className={"pp-mk " + (entry.admitted ? "ok" : "")}><Icon name={entry.admitted ? "check" : "x"} /></span><span className="grow"><FlagText>{entry.name}</FlagText></span><span className="sub">{entry.admitted ? (entry.capabilities || []).map(value => RULE_CAPS[value] || value).join(" · ") : (entry.reasons || []).join("、")}</span></div>) : <div className="pad16 sub">还没有节点。</div>}
      </div></Card>
      <div className="err" id="gp-err" role="alert">{error}</div>
      {original && app.can("DELETE", "/api/proxy-groups/x") && <Card className="mt22"><div className="row"><div><b className="bold">删除策略组</b><div className="sub">还有规则指向它时删不掉，先改掉那些规则。</div></div><span className="sp" /><Button size="sm" data-act="gp-rm" data-id={original.id} data-name={original.name} onClick={remove}>删除</Button></div></Card>}
    </DrawerBody>
    <DrawerFooter><Button data-act="close" onClick={app.closeOverlays}>取消</Button>{canWrite && <Button type="submit" variant="primary" id="gp-save" loading={saving}>保存</Button>}</DrawerFooter>
  </form>;
}
