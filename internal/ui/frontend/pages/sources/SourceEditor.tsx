import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { Button } from "../../components/ui/button";
import { DrawerBody, DrawerFooter, ErrorState, Field, FlagText, Icon, LoadingTable } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import type { User } from "../../runtime/types";
import { errorMessage, readSubscriptionUsers, sourcePath, type SubscriptionSource } from "../../domain/routing";
import type { Plan } from "../../domain/plans";
import { SourceFormatDetails, SourceRuntime, sourceIssueCount } from "./components";
import { useSourceRuntime } from "./hooks";

export type SourceDrawerTab = "config" | "nodes";
interface SourcePacket { source: SubscriptionSource; grants: string[]; planGrants: number[]; users: User[]; plans: Plan[] }
interface DrawerProps { id?: number; initialTab?: SourceDrawerTab; onChanged: () => Promise<void>; onCreated: (id: number) => void }

export function SourceDrawer({ id, initialTab = "config", onChanged, onCreated }: DrawerProps) {
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
      api<{ users: string[]; plans?: { id: number; name: string }[] }>(sourcePath(id) + "/grants", { signal: controller.signal }),
      readSubscriptionUsers((path, init) => api(path, { ...init, signal: controller.signal })),
      api<Plan[]>("/api/plans", { signal: controller.signal }).catch((): Plan[] => []),
    ]).then(([source, grants, users, plans]) => { if (active) setPacket({ source, grants: grants.users, planGrants: (grants.plans || []).map(p => p.id), users, plans }); })
      .catch((reason: unknown) => { if (active) setError(errorMessage(reason)); });
    return () => { active = false; controller.abort(); };
  }, [id, attempt]);

  if (!id) return <CreateSourceEditor onChanged={onChanged} onCreated={onCreated} />;
  if (packet) return <ExistingSourceEditor key={id} initial={packet} initialTab={initialTab} onChanged={onChanged} />;
  return <div className="source-drawer-frame">
    <SourceDrawerHeader title="订阅详情" onClose={app.closeOverlays} />
    <DrawerBody>{error ? <ErrorState title="订阅详情" error={error} onRetry={() => setAttempt(previous => previous + 1)} /> : <LoadingTable rows={4} columns={2} />}</DrawerBody>
  </div>;
}

function SourceDrawerHeader({ title, onClose }: { title: string; onClose: () => void }) {
  return <div className="dh">
    <h2 id="source-drawer-title" className="ell" tabIndex={-1}>{title}</h2>
    <span className="sp" />
    <Button size="icon" data-act="close" aria-label="关闭" onClick={onClose}><Icon name="x" /></Button>
  </div>;
}

function CreateSourceEditor({ onChanged, onCreated }: { onChanged: () => Promise<void>; onCreated: (id: number) => void }) {
  const app = useApp();
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [interval, setIntervalValue] = useState("6");
  const [region, setRegion] = useState("");
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
    else if (!url.trim()) field = "url";
    else if (interval === "" || !Number.isSafeInteger(seconds) || seconds < 0) field = "interval";
    if (field) { setError("请填写来源名称、有效订阅地址和非负刷新间隔。"); setInvalid(field); inputs[field].current?.focus(); return; }
    setError(""); setInvalid(null); setSaving(true);
    const body = { name: name.trim(), url: url.trim(), interval_seconds: seconds, region: region.trim() };
    try {
      const saved = await write<SubscriptionSource>("POST", "/api/sources", body);
      if (!mounted.current) return;
      app.toast("来源已添加，正在拉取节点");
      onCreated(saved.id);
      await onChanged();
    } catch (reason) {
      if (mounted.current) { setError(errorMessage(reason)); setInvalid("url"); }
    } finally {
      if (mounted.current) setSaving(false);
    }
  }

  return <div className="source-drawer-frame">
    <SourceDrawerHeader title="添加外部订阅" onClose={app.closeOverlays} />
    <form id="source-form" noValidate onSubmit={event => void save(event)}>
      <DrawerBody>
        <Field label="来源名称" htmlFor="source-name">
          <input className="in" id="source-name" name="name" autoFocus maxLength={64} value={name} required ref={inputs.name} aria-invalid={invalid === "name" || undefined} aria-describedby="source-err" onChange={event => setName(event.target.value)} disabled={saving} />
        </Field>
        <Field label="订阅地址" htmlFor="source-url">
          <input className="in mono" id="source-url" name="url" type="url" autoComplete="off" spellCheck={false} aria-describedby="source-url-hint source-err" aria-invalid={invalid === "url" || undefined} placeholder="https://" value={url} ref={inputs.url} disabled={saving} onChange={event => setURL(event.target.value)} />
          <span className="sub" id="source-url-hint">支持 Clash、sing-box 与分享链接订阅。添加后默认不分配给用户。</span>
        </Field>
        <div className="two">
          <Field label="刷新间隔（小时）" htmlFor="source-interval">
            <input className="in" id="source-interval" name="interval" type="number" min="0" step="any" value={interval} ref={inputs.interval} aria-describedby="source-interval-hint source-err" aria-invalid={invalid === "interval" || undefined} disabled={saving} onChange={event => setIntervalValue(event.target.value)} />
            <span className="sub" id="source-interval-hint">0 表示仅手动刷新。</span>
          </Field>
          <Field label="默认地区（选填）" htmlFor="source-region">
            <input className="in" id="source-region" name="region" maxLength={2} value={region} placeholder="如 JP" disabled={saving} onChange={event => setRegion(event.target.value)} />
          </Field>
        </div>
        <div className="err" id="source-err" role="alert">{error}</div>
      </DrawerBody>
      <DrawerFooter>
        <Button variant="ghost" data-act="close" disabled={saving} onClick={app.closeOverlays}>取消</Button>
        <Button type="submit" variant="primary" id="source-save" loading={saving}>添加并拉取</Button>
      </DrawerFooter>
    </form>
  </div>;
}

const sameUsers = (a: string[], b: string[]) => {
  const sa = new Set(a), sb = new Set(b);
  return sa.size === sb.size && [...sa].every(item => sb.has(item));
};

function ExistingSourceEditor({ initial, initialTab, onChanged }: { initial: SourcePacket; initialTab: SourceDrawerTab; onChanged: () => Promise<void> }) {
  const app = useApp();
  const runtime = useSourceRuntime(initial.source, onChanged);
  const [tab, setTab] = useState<SourceDrawerTab>(initialTab);
  const [savedSource, setSavedSource] = useState(initial.source);
  const [savedGrants, setSavedGrants] = useState(initial.grants);
  const [savedPlanGrants, setSavedPlanGrants] = useState(initial.planGrants);
  const [name, setName] = useState(initial.source.name);
  const [url, setURL] = useState("");
  const [interval, setIntervalValue] = useState(String(initial.source.interval_seconds / 3600));
  const [region, setRegion] = useState(initial.source.region || "");
  const [grantAll, setGrantAll] = useState(Boolean(initial.source.grant_all));
  const [selected, setSelected] = useState(initial.grants);
  const [selectedPlans, setSelectedPlans] = useState(initial.planGrants);
  const [error, setError] = useState("");
  const [actionError, setActionError] = useState("");
  const [invalid, setInvalid] = useState<"name" | "url" | "interval" | null>(null);
  const [saving, setSaving] = useState(false);
  const [busy, setBusy] = useState("");

  const inputs = { name: useRef<HTMLInputElement>(null), url: useRef<HTMLInputElement>(null), interval: useRef<HTMLInputElement>(null) };
  const tabRefs = { config: useRef<HTMLButtonElement>(null), nodes: useRef<HTMLButtonElement>(null) };
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  const canPatch = app.can("PATCH", "/api/sources");
  const canGrant = app.can("PUT", "/api/sources/x/grants");
  const canRefresh = app.can("POST", "/api/sources");
  const canDelete = app.can("DELETE", "/api/sources");
  const writable = canPatch || canGrant;
  const issueCount = sourceIssueCount(runtime.source);
  const latestRevision = Math.max(savedSource.revision, runtime.source.revision);
  const members = runtime.source.nodes || [];

  function switchTab(next: SourceDrawerTab) {
    setTab(next);
    requestAnimationFrame(() => tabRefs[next].current?.focus());
  }

  function onTabKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.key === "ArrowRight" || event.key === "ArrowLeft") {
      event.preventDefault();
      switchTab(tab === "config" ? "nodes" : "config");
    } else if (event.key === "Home") {
      event.preventDefault();
      switchTab("config");
    } else if (event.key === "End") {
      event.preventDefault();
      switchTab("nodes");
    }
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    if (saving || !!busy || !writable) return;
    const trimmedName = name.trim();
    const trimmedURL = url.trim();
    const trimmedRegion = region.trim();
    const seconds = Number(interval) * 3600;
    let field: typeof invalid = null;
    if (!trimmedName) field = "name";
    else if (interval === "" || !Number.isSafeInteger(seconds) || seconds < 0) field = "interval";
    if (field) {
      setError("请填写来源名称和非负刷新间隔。");
      setInvalid(field);
      setTab("config");
      requestAnimationFrame(() => inputs[field].current?.focus());
      return;
    }

    const configChanged = canPatch && (
      trimmedName !== savedSource.name ||
      Boolean(trimmedURL) ||
      seconds !== savedSource.interval_seconds ||
      trimmedRegion !== (savedSource.region || "") ||
      grantAll !== Boolean(savedSource.grant_all)
    );
    const planChanged = !sameUsers(selectedPlans.map(String), savedPlanGrants.map(String));
    const grantsChanged = canGrant && (!sameUsers(selected, savedGrants) || planChanged);
    if (!configChanged && !grantsChanged) {
      app.closeOverlays();
      return;
    }

    setError(""); setInvalid(null); setSaving(true);
    let configSavedNow = false;
    try {
      if (configChanged) {
        const body = {
          name: trimmedName,
          interval_seconds: seconds,
          region: trimmedRegion,
          grant_all: grantAll,
          revision: latestRevision,
          ...(trimmedURL ? { url: trimmedURL } : {}),
        };
        const updatedSource = await write<SubscriptionSource>("PATCH", sourcePath(savedSource.id), body);
        if (!mounted.current) return;
        setSavedSource(updatedSource);
        setURL("");
        configSavedNow = true;
      }
      if (grantsChanged) {
        try {
          const updatedGrants = await write<{ users: string[]; plans?: { id: number; name: string }[] }>("PUT", sourcePath(savedSource.id) + "/grants", { users: selected, ...(planChanged ? { plan_ids: selectedPlans } : {}) });
          if (!mounted.current) return;
          setSavedGrants(updatedGrants.users);
          setSavedPlanGrants((updatedGrants.plans || []).map(p => p.id));
        } catch (grantReason) {
          if (!mounted.current) return;
          if (configSavedNow) {
            setError(`基本配置已保存，但保存接收用户失败：${errorMessage(grantReason)}`);
            await onChanged();
          } else {
            setError(errorMessage(grantReason));
          }
          setTab("config");
          return;
        }
      }
      if (!mounted.current) return;
      app.toast(configChanged && grantsChanged ? "来源配置与接收用户已保存" : grantsChanged ? "接收用户已保存，客户端刷新订阅后生效" : "来源配置已保存");
      app.closeOverlays();
      await onChanged();
    } catch (reason) {
      if (mounted.current) {
        setError(errorMessage(reason));
        setInvalid("url");
        setTab("config");
      }
    } finally {
      if (mounted.current) setSaving(false);
    }
  }

  async function refresh() {
    if (busy || saving || !canRefresh) return;
    setBusy("refresh"); setActionError("");
    try {
      await write("POST", sourcePath(initial.source.id) + "/refresh");
      app.toast("已开始刷新，可在来源详情查看结果");
      await runtime.refresh();
    } catch (reason) {
      setActionError(errorMessage(reason));
    } finally {
      if (mounted.current) setBusy("");
    }
  }

  async function toggle() {
    if (busy || saving || !canPatch) return;
    setBusy("toggle"); setActionError("");
    try {
      await write("PATCH", sourcePath(initial.source.id), { enabled: !runtime.source.enabled });
      app.closeOverlays();
      app.toast(runtime.source.enabled ? "已停用来源，后续订阅不再输出这些节点" : "已恢复来源");
      await onChanged();
    } catch (reason) {
      setActionError(errorMessage(reason));
    } finally {
      if (mounted.current) setBusy("");
    }
  }

  function remove() {
    app.confirmBox({
      title: `删除订阅来源 ${savedSource.name}？`,
      text: "删除来源、快照及用户授权。其他来源和自建节点不受影响。已保存的上游凭证仍可能可用。",
      ok: "删除",
      danger: true,
      run: async () => {
        await write("DELETE", sourcePath(initial.source.id));
        app.closeOverlays();
        app.toast("已删除来源");
        await onChanged();
      },
    });
  }

  return <div className="source-drawer-frame">
    <SourceDrawerHeader title={savedSource.name} onClose={app.closeOverlays} />
    <form id="source-form" noValidate onSubmit={event => void save(event)}>
      <DrawerBody>
        <div id="source-runtime" className="src-rt-bar" aria-live="polite" aria-busy={runtime.reading || undefined}>
          <div className="src-rt-main">
            {runtime.error ? <>
              <p className="c-bad" role="alert">读取刷新结果失败：{runtime.error}</p>
              <Button size="sm" data-act="source-open" data-id={runtime.source.id} onClick={() => void runtime.refresh()}>重试读取</Button>
            </> : <SourceRuntime source={runtime.source} />}
          </div>
          <div className="src-rt-acts">
            <Button size="sm" data-act="source-refresh" data-id={initial.source.id} disabled={!runtime.source.enabled || runtime.source.refreshing || !!busy || saving || !canRefresh} loading={busy === "refresh"} onClick={() => void refresh()}>
              <Icon name="refresh" />刷新
            </Button>
            <Button size="sm" data-act="source-toggle" data-id={initial.source.id} data-enabled={runtime.source.enabled ? "0" : "1"} disabled={!!busy || saving || !canPatch} loading={busy === "toggle"} onClick={() => void toggle()}>
              {runtime.source.enabled ? "停用" : "恢复"}
            </Button>
          </div>
        </div>
        {actionError && <div className="err" role="alert">{actionError}</div>}

        <div className="src-tabs" role="tablist" aria-label="来源管理">
          <button
            ref={tabRefs.config}
            type="button"
            role="tab"
            id="src-tab-config"
            aria-selected={tab === "config"}
            tabIndex={tab === "config" ? 0 : -1}
            data-act="source-tab"
            data-tab="config"
            onClick={() => setTab("config")}
            onKeyDown={onTabKeyDown}
          >配置与授权</button>
          <button
            ref={tabRefs.nodes}
            type="button"
            role="tab"
            id="src-tab-nodes"
            aria-selected={tab === "nodes"}
            tabIndex={tab === "nodes" ? 0 : -1}
            data-act="source-tab"
            data-tab="nodes"
            onClick={() => setTab("nodes")}
            onKeyDown={onTabKeyDown}
          >节点与格式<em>{runtime.source.node_count}</em>{issueCount > 0 && <span className="src-tab-dot" title={`${issueCount} 条兼容提示`} />}</button>
        </div>

        <div role="tabpanel" aria-labelledby="src-tab-config" hidden={tab !== "config"}>
          <Field label="来源名称" htmlFor="source-name">
            <input className="in" id="source-name" name="name" autoFocus maxLength={64} value={name} required ref={inputs.name} aria-invalid={invalid === "name" || undefined} aria-describedby="source-err" onChange={event => setName(event.target.value)} disabled={saving || !canPatch} />
          </Field>
          <Field label="替换订阅地址" htmlFor="source-url">
            <input className="in mono" id="source-url" name="url" type="url" autoComplete="off" spellCheck={false} aria-describedby="source-url-hint source-err" aria-invalid={invalid === "url" || undefined} placeholder={savedSource.url_masked || "https://"} value={url} ref={inputs.url} disabled={saving || !canPatch} onChange={event => setURL(event.target.value)} />
            <span className="sub" id="source-url-hint">留空保留当前地址。新地址刷新成功前，继续使用上次结果。</span>
          </Field>
          <div className="two">
            <Field label="刷新间隔（小时）" htmlFor="source-interval">
              <input className="in" id="source-interval" name="interval" type="number" min="0" step="any" value={interval} ref={inputs.interval} aria-describedby="source-interval-hint source-err" aria-invalid={invalid === "interval" || undefined} disabled={saving || !canPatch} onChange={event => setIntervalValue(event.target.value)} />
              <span className="sub" id="source-interval-hint">0 表示仅手动刷新。</span>
            </Field>
            <Field label="默认地区（选填）" htmlFor="source-region">
              <input className="in" id="source-region" name="region" maxLength={2} value={region} placeholder="如 JP" disabled={saving || !canPatch} onChange={event => setRegion(event.target.value)} />
            </Field>
          </div>
          <section className="src-grants-sec">
            <h3>接收用户</h3>
            <p className="sub">这些用户共享上游凭证。撤回授权后，不再提供这些节点。已保存的凭证仍需由上游轮换。</p>
            <label className="chk" id="source-grant-all-row">
              <input
                type="checkbox"
                name="grant_all"
                checked={grantAll}
                disabled={saving || !canPatch}
                onChange={event => setGrantAll(event.target.checked)}
              />
              授权给所有用户（含之后新建的用户）
            </label>
            <div className="two source-user-list" id="source-grants-form">
              {initial.users.length ? initial.users.map(user => (
                <label className="chk" key={user.username}>
                  <input
                    type="checkbox"
                    name="user"
                    value={user.username}
                    checked={selected.includes(user.username)}
                    disabled={saving || !canGrant || grantAll}
                    onChange={event => setSelected(previous => event.target.checked ? [...previous, user.username] : previous.filter(value => value !== user.username))}
                  />
                  {user.username}
                </label>
              )) : <p className="sub">还没有用户。添加用户后可分配。</p>}
            </div>
            {initial.plans.length > 0 && <>
              <p className="sub" id="source-plans-label">按套餐授权：套餐下的用户（含之后挂进来的）都能看到这个来源。</p>
              <div className="two source-user-list" id="source-plan-grants-form">
                {initial.plans.map(plan => (
                  <label className="chk" key={plan.id}>
                    <input
                      type="checkbox"
                      name="plan"
                      value={plan.id}
                      checked={selectedPlans.includes(plan.id)}
                      disabled={saving || !canGrant || grantAll}
                      onChange={event => setSelectedPlans(previous => event.target.checked ? [...previous, plan.id] : previous.filter(value => value !== plan.id))}
                    />
                    {plan.name}{plan.is_default ? "（默认）" : ""}
                  </label>
                ))}
              </div>
            </>}
            {grantAll && <p className="sub" id="source-grants-note">全员授权已开启，按人与按套餐名单保留但不决定可见性。</p>}
          </section>
          <div className="err" id="source-err" role="alert">{error}</div>
        </div>

        <div role="tabpanel" aria-labelledby="src-tab-nodes" hidden={tab !== "nodes"}>
          <section className="src-panel-sec">
            <h3>格式兼容</h3>
            <div id="source-format-results" aria-busy={runtime.reading || undefined}>
              <SourceFormatDetails source={runtime.source} />
              {runtime.error && <p className="c-bad" role="alert">读取刷新结果失败：{runtime.error}<Button size="sm" onClick={() => void runtime.refresh()}>重试读取</Button></p>}
            </div>
          </section>
          <section className="src-panel-sec">
            <h3>快照节点<span className="sub fw4"> · {members.length} 个（只读）</span></h3>
            <div id="source-snapshot-nodes" className="src-snap-list">
              {members.length ? members.map((member, index) => (
                <div className="src-snap-row" key={`${initial.source.id}:${index}`}>
                  <span className="src-snap-idx mono sub">{index + 1}</span>
                  <span className="grow src-snap-name"><FlagText>{member.name}</FlagText></span>
                  <span className="tag">{member.protocol}</span>
                </div>
              )) : <p className="sub">当前快照里没有可用节点{runtime.source.last_error ? "，上一次刷新失败" : ""}。</p>}
            </div>
          </section>
        </div>
      </DrawerBody>
      <DrawerFooter>
        {canDelete && <Button variant="ghost" className="c-bad" data-act="source-delete" data-id={initial.source.id} data-name={savedSource.name} disabled={saving || !!busy} onClick={remove}><Icon name="trash" />删除</Button>}
        <span className="sp" />
        <Button variant="ghost" data-act="close" disabled={saving || !!busy} onClick={app.closeOverlays}>{writable ? "取消" : "关闭"}</Button>
        {writable && <Button type="submit" variant="primary" id="source-save" disabled={!!busy} loading={saving}>保存</Button>}
      </DrawerFooter>
    </form>
  </div>;
}
