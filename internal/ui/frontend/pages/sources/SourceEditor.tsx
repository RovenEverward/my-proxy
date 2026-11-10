import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "../../components/ui/button";
import { DrawerBody, ErrorState, Field, Icon, LoadingTable } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import type { User } from "../../runtime/types";
import { errorMessage, readSubscriptionUsers, sourcePath, type SubscriptionSource } from "../../domain/routing";
import { SourceFormatDetails, SourceRuntime } from "./components";
import { useSourceRuntime } from "./hooks";

interface SourcePacket { source: SubscriptionSource; grants: string[]; users: User[] }
interface DrawerProps { id?: number; onChanged: () => Promise<void>; onCreated: (id: number) => void }
export function SourceDrawer({ id, onChanged, onCreated }: DrawerProps) {
  const app = useApp();
  const [packet, setPacket] = useState<SourcePacket | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!id) return;
    const controller = new AbortController(); let active = true;
    setError("");
    void Promise.all([
      api<SubscriptionSource>(sourcePath(id), { signal: controller.signal }),
      api<{ users: string[] }>(sourcePath(id) + "/grants", { signal: controller.signal }),
      readSubscriptionUsers((path, init) => api(path, { ...init, signal: controller.signal })),
    ]).then(([source, grants, users]) => { if (active) setPacket({ source, grants: grants.users, users }); })
      .catch((reason: unknown) => { if (active) setError(errorMessage(reason)); });
    return () => { active = false; controller.abort(); };
  }, [id, attempt]);
  if (!id) return <SourceEditor onChanged={onChanged} onCreated={onCreated} />;
  if (packet) return <ExistingSourceEditor key={id} initial={packet} onChanged={onChanged} onCreated={onCreated} />;
  return <div className="source-drawer-frame"><SourceDrawerHeader title="订阅详情" onClose={app.closeOverlays} /><DrawerBody>{error ? <ErrorState title="订阅详情" error={error} onRetry={() => setAttempt(previous => previous + 1)} /> : <LoadingTable rows={4} columns={2} />}</DrawerBody></div>;
}
function SourceDrawerHeader({ title, onClose }: { title: string; onClose: () => void }) {
  return <div className="dh"><h2 id="source-drawer-title" tabIndex={-1}>{title}</h2><span className="sp" /><Button size="icon" data-act="close" aria-label="关闭" onClick={onClose}><Icon name="x" /></Button></div>;
}
type RuntimeState = ReturnType<typeof useSourceRuntime>;
function ExistingSourceEditor({ initial, onChanged, onCreated }: { initial: SourcePacket; onChanged: () => Promise<void>; onCreated: (id: number) => void }) {
  const runtime = useSourceRuntime(initial.source, onChanged);
  return <SourceEditor initial={initial} runtime={runtime} onChanged={onChanged} onCreated={onCreated} />;
}
function SourceEditor({ initial, runtime, onChanged, onCreated }: { initial?: SourcePacket; runtime?: RuntimeState; onChanged: () => Promise<void>; onCreated: (id: number) => void }) {
  const app = useApp();
  const source = initial?.source;
  const [name, setName] = useState(source?.name || "");
  const [url, setURL] = useState("");
  const [interval, setIntervalValue] = useState(String(source ? source.interval_seconds / 3600 : 6));
  const [region, setRegion] = useState(source?.region || "");
  const [error, setError] = useState("");
  const [invalid, setInvalid] = useState<"name" | "url" | "interval" | null>(null);
  const [saving, setSaving] = useState(false);
  const inputs = { name: useRef<HTMLInputElement>(null), url: useRef<HTMLInputElement>(null), interval: useRef<HTMLInputElement>(null) };
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  async function save(event: FormEvent) {
    event.preventDefault();
    if (saving) return;
    const seconds = Number(interval) * 3600;
    let field: typeof invalid = null;
    if (!name.trim()) field = "name";
    else if (!source && !url.trim()) field = "url";
    else if (interval === "" || !Number.isSafeInteger(seconds) || seconds < 0) field = "interval";
    if (field) { setError("请填写来源名称、有效订阅地址和非负刷新间隔。"); setInvalid(field); inputs[field].current?.focus(); return; }
    setError(""); setInvalid(null); setSaving(true);
    const body = { name: name.trim(), interval_seconds: seconds, region: region.trim(), ...(source ? { revision: source.revision } : {}), ...(url.trim() ? { url: url.trim() } : {}) };
    try {
      const saved = await write<SubscriptionSource>(source ? "PATCH" : "POST", source ? sourcePath(source.id) : "/api/sources", body);
      if (!mounted.current) return;
      setURL(""); app.toast(source ? "来源配置已保存" : "来源已添加，正在拉取节点");
      if (source) app.closeOverlays(); else onCreated(saved.id);
      await onChanged();
    } catch (reason) { if (mounted.current) { setError(errorMessage(reason)); setInvalid("url"); } }
    finally { if (mounted.current) setSaving(false); }
  }
  return <div className="source-drawer-frame"><SourceDrawerHeader title={source?.name || "添加外部订阅"} onClose={app.closeOverlays} /><DrawerBody>
    {runtime && <SourceRuntimePanel runtime={runtime} />}
    <form id="source-form" noValidate onSubmit={event => void save(event)}>
      <Field label="来源名称" htmlFor="source-name"><input className="in" id="source-name" name="name" autoFocus maxLength={64} value={name} required ref={inputs.name} aria-invalid={invalid === "name" || undefined} aria-describedby="source-err" onChange={event => setName(event.target.value)} disabled={saving} /></Field>
      <Field label={source ? "替换订阅地址" : "订阅地址"} htmlFor="source-url"><input className="in mono" id="source-url" name="url" type="url" autoComplete="off" spellCheck={false} aria-describedby="source-url-hint source-err" aria-invalid={invalid === "url" || undefined} placeholder={source?.url_masked || "https://"} value={url} ref={inputs.url} disabled={saving} onChange={event => setURL(event.target.value)} /><span className="sub" id="source-url-hint">{source ? "留空保留当前地址。新地址刷新成功前，继续使用上次结果。" : "支持 Clash、sing-box 与分享链接订阅。添加后默认不分配给用户。"}</span></Field>
      <div className="two"><Field label="刷新间隔（小时）" htmlFor="source-interval"><input className="in" id="source-interval" name="interval" type="number" min="0" step="any" value={interval} ref={inputs.interval} aria-describedby="source-interval-hint source-err" aria-invalid={invalid === "interval" || undefined} disabled={saving} onChange={event => setIntervalValue(event.target.value)} /><span className="sub" id="source-interval-hint">0 表示仅手动刷新。</span></Field><Field label="默认地区（选填）" htmlFor="source-region"><input className="in" id="source-region" name="region" maxLength={2} value={region} placeholder="如 JP" disabled={saving} onChange={event => setRegion(event.target.value)} /></Field></div>
      <div className="err" id="source-err" role="alert">{error}</div><div className="flex8 end"><Button type="submit" variant="primary" id="source-save" loading={saving}>{source ? "保存配置" : "添加并拉取"}</Button></div>
    </form>
    {initial && runtime && <><SourceGrants source={initial.source} users={initial.users} initialGrants={initial.grants} />
      <SourceResultAndActions initial={initial.source} runtime={runtime} />
    </>}
  </DrawerBody></div>;
}
function SourceGrants({ source, users, initialGrants }: { source: SubscriptionSource; users: User[]; initialGrants: string[] }) {
  const app = useApp();
  const [selected, setSelected] = useState(initialGrants);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  async function save(event: FormEvent) {
    event.preventDefault(); if (saving) return;
    setSaving(true); setError("");
    try { await write("PUT", sourcePath(source.id) + "/grants", { users: selected }); app.toast("接收用户已保存，客户端刷新订阅后生效"); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setSaving(false); }
  }
  return <section className="mt"><h3>接收用户</h3><p className="sub">这些用户共享上游凭证。撤回授权后，不再提供这些节点。已保存的凭证仍需由上游轮换。</p><form id="source-grants-form" onSubmit={event => void save(event)}><div className="two source-user-list">{users.length ? users.map(user => <label className="chk" key={user.username}><input type="checkbox" name="user" value={user.username} checked={selected.includes(user.username)} disabled={saving} onChange={event => setSelected(previous => event.target.checked ? [...previous, user.username] : previous.filter(value => value !== user.username))} />{user.username}</label>) : <p className="sub">还没有用户。添加用户后可分配。</p>}</div><div className="err" id="source-grants-err" role="alert">{error}</div><div className="flex8 end mt8"><Button type="submit" id="source-grants-save" loading={saving}>保存接收用户</Button></div></form></section>;
}
function SourceRuntimePanel({ runtime }: { runtime: RuntimeState }) {
  return <div id="source-runtime" aria-live="polite" aria-busy={runtime.reading || undefined}>{runtime.error ? <><p className="c-bad" role="alert">读取刷新结果失败：{runtime.error}</p><Button data-act="source-open" data-id={runtime.source.id} onClick={() => void runtime.refresh()}>重试读取</Button></> : <SourceRuntime source={runtime.source} />}</div>;
}
function SourceResultAndActions({ initial, runtime }: { initial: SubscriptionSource; runtime: RuntimeState }) {
  const app = useApp();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  async function refresh() {
    if (busy) return; setBusy("refresh"); setError("");
    try { await write("POST", sourcePath(initial.id) + "/refresh"); app.toast("已开始刷新，可在来源详情查看结果"); await runtime.refresh(); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setBusy(""); }
  }
  async function toggle() {
    if (busy) return; setBusy("toggle"); setError("");
    try { await write("PATCH", sourcePath(initial.id), { enabled: !runtime.source.enabled }); app.closeOverlays(); app.toast(runtime.source.enabled ? "已停用来源，后续订阅不再输出这些节点" : "已恢复来源"); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setBusy(""); }
  }
  function remove() { app.confirmBox({ title: `删除订阅来源 ${initial.name}？`, text: "删除来源、快照及用户授权。其他来源和自建节点不受影响。已保存的上游凭证仍可能可用。", ok: "删除", danger: true, run: async () => { await write("DELETE", sourcePath(initial.id)); app.closeOverlays(); app.toast("已删除来源"); } }); }
  return <><section className="mt"><h3>格式兼容</h3><div id="source-format-results" aria-busy={runtime.reading || undefined}><SourceFormatDetails source={runtime.source} />{runtime.error && <p className="c-bad" role="alert">读取刷新结果失败：{runtime.error}<Button onClick={() => void runtime.refresh()}>重试读取</Button></p>}</div></section>
    <div className="err" role="alert">{error}</div><div className="flex8 mt"><Button data-act="source-refresh" data-id={initial.id} disabled={!runtime.source.enabled || runtime.source.refreshing || !!busy} loading={busy === "refresh"} onClick={() => void refresh()}><Icon name="refresh" />刷新来源</Button><Button data-act="source-toggle" data-id={initial.id} data-enabled={runtime.source.enabled ? "0" : "1"} disabled={!!busy} loading={busy === "toggle"} onClick={() => void toggle()}>{runtime.source.enabled ? "停用" : "恢复"}</Button><span className="sp" /><Button variant="danger" data-act="source-delete" data-id={initial.id} data-name={initial.name} disabled={!!busy} onClick={remove}>删除</Button></div>
  </>;
}
