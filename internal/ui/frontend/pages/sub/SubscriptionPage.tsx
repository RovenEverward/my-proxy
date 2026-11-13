import { NativeSelect } from "@/components/native-select";
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { ErrorState, Icon, LoadingTable, PageHeader } from "../../components/primitives";
import { Button } from "../../components/ui/button";
import { APIError } from "../../runtime/api";
import { useApp, useLeaveGuard, usePageTitle } from "../../runtime/app-context";
import { usePageData } from "../../runtime/query";
import type { ClientRule, PageProps, Settings, Status, SubPull, User } from "../../runtime/types";
import { CLIENT_APPS, FORMATS, OTHER_UA, TEMPLATE_FORMATS, TEMPLATE_META, changedFields, changedPatch, changes, clientFormat, clientGroups, effectiveRules, emptyPreview, modifySettings, recentPreviewUser, sameRules, subscriptionLink, subscriptionPreview, subscriptionTemplates, subscriptionUsers, templateBytes, templateLines, templateSize, type SubscriptionDraft, type SubscriptionPreview, type TemplateFormat } from "../../domain/settings";
import { RulesDrawer } from "./RulesDrawer";
import { TemplateDrawer } from "./TemplateDrawer";
import { TitleField } from "./TitleField";

interface SubscriptionData { settings: Settings; builtin: ClientRule[]; users: User[]; preview: SubscriptionPreview; warnings: string[] }
interface DraftState { original: SubscriptionDraft; draft: SubscriptionDraft; error: string; fieldErrors: Partial<Record<keyof Settings, string>>; saving: boolean }
let advancedPreference = false;
const initialState = (settings: SubscriptionDraft): DraftState => ({ original: structuredClone(settings), draft: structuredClone(settings), error: "", fieldErrors: {}, saving: false });
function Tip({ children }: { children: string }) { return <span className="r-tip" tabIndex={0} role="note" aria-label={children} data-tip={children}><Icon name="info" /></span>; }
function SectionHeading({ title, children }: { title: string; children?: ReactNode }) { return <div className="r-sh"><h2>{title}</h2>{children}</div>; }
function LeaveDialog({ labels, onStay, onDrop, onSave }: { labels: string[]; onStay: () => void; onDrop: () => void; onSave: () => void }) {
  return <><div className="mbody"><h2 id="sub-leave-title">有 {labels.length} 处改动还没保存</h2><p>离开「订阅」页，这些改动就会丢：</p><ul className="s5-leave-list">{labels.map(label => <li key={label}>{label}</li>)}</ul><div className="err" id="modal-err" role="alert" /></div>
    <div className="mf"><Button data-act="sub-leave-stay" onClick={onStay}>留下</Button><Button data-act="sub-leave-drop" onClick={onDrop}>不保存</Button><Button variant="primary" id="sub-leave-ok" data-act="sub-leave-save" autoFocus onClick={onSave}>保存并离开</Button></div></>;
}
export function SubscriptionPage({ route }: PageProps) {
  const { setBusy, toast } = useApp();
  const selected = useRef<string>();
  usePageTitle([{ label: "订阅" }]);
  const query = usePageData<SubscriptionData>("sub", async read => {
    const warnings: string[] = [];
    const [settings, builtin, users, pulls] = await Promise.all([
      read<Settings>("/api/settings"), read<ClientRule[]>("/api/settings/builtin-rules"), subscriptionUsers(read), read<SubPull[]>("/api/sub-pulls").catch(() => []),
      read<Status>("/api/status").catch(() => null),
    ]);
    const user = recentPreviewUser(users, pulls, selected.current);
    const preview = await subscriptionPreview(user, read).catch((error: Error) => { warnings.push(error.message); return { ...emptyPreview(user?.username), token: "" }; });
    return { settings, builtin, users, preview, warnings };
  });
  const routeKey = route.join("/");
  const previousRoute = useRef(routeKey);
  useEffect(() => {
    if (previousRoute.current === routeKey) return;
    previousRoute.current = routeKey;
    void query.refresh(true);
  }, [routeKey, query.refresh]);
  useEffect(() => { setBusy(query.loading); return () => setBusy(false); }, [query.loading, setBusy]);
  useEffect(() => { query.data?.warnings.forEach(message => toast(message, true)); }, [query.data, toast]);
  if (!query.data) return <><PageHeader title="订阅" className="r-ph" />{query.error ? <ErrorState title="订阅" error={query.error} onRetry={() => void query.refresh()} /> : <LoadingTable rows={6} columns={2} />}</>;
  return <SubscriptionWorkspace route={route} data={query.data} selected={selected} refresh={() => query.refresh(false)} readError={query.error} onReadRetry={() => void query.refresh()} />;
}
function SubscriptionWorkspace({ route, data, selected, refresh, readError, onReadRetry }: { route: string[]; data: SubscriptionData; selected: { current: string | undefined }; refresh: () => Promise<void>; readError: Error | null; onReadRetry: () => void }) {
  const { meta, can, toast, navigate, openDrawer, openModal, closeModal } = useApp();
  const [state, setState] = useState<DraftState>(() => initialState(data.settings));
  const current = useRef(state); current.current = state;
  const [preview, setPreview] = useState(data.preview), previewSequence = useRef(0);
  const [advanced, setAdvanced] = useState(advancedPreference || route[1] === "templates");
  const [prefixHover, setPrefixHover] = useState(false), [prefixFocus, setPrefixFocus] = useState(false);
  const mounted = useRef(true), saving = useRef(false);
  const writable = can("PUT", "/api/settings"), labels = changes(state.draft, state.original), dirty = labels.length > 0;
  const originalSettings = useRef(data.settings);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    if (originalSettings.current !== data.settings) {
      originalSettings.current = data.settings;
      setState(previous => changes(previous.draft, previous.original).length || saving.current ? previous : initialState(data.settings));
    }
    if (selected.current && !data.users.some(user => user.username === selected.current)) selected.current = data.preview.user;
    if (!selected.current || data.preview.user === selected.current) setPreview(data.preview);
  }, [data, selected]);
  useEffect(() => { if (route[1] === "templates") { advancedPreference = true; setAdvanced(true); } }, [route]);
  const routeKey = route.join("/");
  useEffect(() => {
    const section = ({ link: "sec-link", info: "sec-info", look: "sec-info", clients: "sec-clients", templates: "sec-adv" } as Record<string, string>)[route[1] || ""];
    if (!section) return;
    const frame = requestAnimationFrame(() => document.getElementById(section)?.scrollIntoView({ block: "start" }));
    return () => cancelAnimationFrame(frame);
  }, [routeKey, route, advanced]);
  const edit = useCallback(<K extends keyof SubscriptionDraft>(key: K, value: SubscriptionDraft[K]) => setState(previous => { const fieldErrors = { ...previous.fieldErrors }; delete fieldErrors[key]; return { ...previous, draft: { ...previous.draft, [key]: value }, error: "", fieldErrors }; }), []);
  const save = useCallback(async (): Promise<boolean> => {
    const snapshot = current.current, changed = changedFields(snapshot.draft, snapshot.original), changedRules = !sameRules(snapshot.draft.rules, snapshot.original.rules), count = changes(snapshot.draft, snapshot.original).length;
    if (!writable || saving.current || !count) return false;
    if (!/^\d+$/.test(String(snapshot.draft.update_interval)) || !Number.isSafeInteger(+snapshot.draft.update_interval)) {
      setState(previous => ({ ...previous, error: "刷新间隔要是非负整数", fieldErrors: { update_interval: "刷新间隔要是非负整数" } })); return false;
    }
    saving.current = true; setState(previous => ({ ...previous, saving: true, error: "", fieldErrors: {} }));
    try {
      await modifySettings(settings => {
        for (const [key] of changed) { if (key === "update_interval") settings[key] = +snapshot.draft[key]; else settings[key] = snapshot.draft[key]; }
        if (changedRules) settings.rules = sameRules(snapshot.draft.rules, data.builtin) ? null : structuredClone(snapshot.draft.rules);
      });
      const saved = { ...snapshot.draft, update_interval: +snapshot.draft.update_interval };
      if (changed.some(([key]) => key === "sub_url_prefix")) { saved.sub_url_prefix = saved.sub_url_prefix.replace(/\/+$/, ""); setPreview(previous => ({ ...previous, auto: "", appliedPrefix: saved.sub_url_prefix || previous.appliedPrefix })); }
      const next = { ...snapshot, original: structuredClone(saved), draft: structuredClone(saved), saving: false, error: "", fieldErrors: {} };
      current.current = next; if (mounted.current) setState(next);
      saving.current = false; toast(`已保存 ${count} 处改动`); await refresh(); return true;
    } catch (cause) {
      const error = cause instanceof Error ? cause : new Error(String(cause));
      const prefixError = cause instanceof APIError && cause.status === 400 && (cause.field === "sub_url_prefix" || error.message.includes("订阅地址") || error.message.includes("链接开头"));
      if (mounted.current) setState(previous => ({ ...previous, saving: false, error: prefixError ? "没保存上：链接开头的格式不对，改好再保存。" : `没保存上：${error.message.replace(/[。.]$/, "")}。改动还在，可以再点一次保存。`, fieldErrors: prefixError ? { sub_url_prefix: error.message.replaceAll("订阅地址", "链接开头") } : cause instanceof APIError && cause.field ? { [cause.field]: error.message } : {} }));
      if (prefixError) requestAnimationFrame(() => document.getElementById("f-prefix")?.focus());
      saving.current = false; return false;
    }
  }, [data.builtin, refresh, toast, writable]);
  const guard = useCallback((next: string | (() => void)) => {
    const snapshot = current.current, changed = changes(snapshot.draft, snapshot.original);
    if (!changed.length || typeof next === "string" && next.replace(/^#/, "").split("/")[0] === "sub") return true;
    const proceed = () => { if (typeof next === "function") next(); else navigate(next); };
    openModal(<LeaveDialog labels={changed} onStay={closeModal} onDrop={() => { const reset = initialState(current.current.original); current.current = reset; setState(reset); closeModal(); proceed(); }} onSave={() => { closeModal(); void save().then(success => { if (success) proceed(); }); }} />);
    return false;
  }, [closeModal, navigate, openModal, save]);
  useLeaveGuard(guard);
  const discard = () => {
    if (saving.current) return;
    const snapshot = current.current, patch = changedPatch(snapshot.draft, snapshot.original);
    setState(previous => ({ ...previous, draft: structuredClone(previous.original), fieldErrors: {}, error: "" }));
    toast("已放弃改动", false, { label: "撤销", run: () => { if (mounted.current) setState(previous => ({ ...previous, draft: { ...previous.draft, ...structuredClone(patch) }, fieldErrors: {}, error: "" })); } });
  };
  const chooseUser = async (username: string) => {
    selected.current = username;
    const sequence = ++previewSequence.current, user = data.users.find(candidate => candidate.username === username);
    setPreview({ user: username, token: "", auto: "", appliedPrefix: "", getAuto: false });
    try { const next = await subscriptionPreview(user); if (mounted.current && sequence === previewSequence.current) setPreview(next); } catch (cause) { if (mounted.current && sequence === previewSequence.current) toast(cause instanceof Error ? cause.message : String(cause), true); }
  };
  const rules = effectiveRules(state.draft.rules, data.builtin), originalRules = effectiveRules(state.original.rules, data.builtin);
  const fallback = meta.server || location.origin, link = subscriptionLink(state.draft, state.original, preview, fallback), split = link.lastIndexOf("/sub/"), base = link.slice(0, split), tail = link.slice(split);
  const prefixBase = state.draft.sub_url_prefix || preview.appliedPrefix || fallback;
  const readOnly = !writable || state.saving;
  return <><PageHeader title="订阅" className="r-ph" actions={<><Button asChild><a href="#sources">管理外部订阅</a></Button>{data.users.length ? <label className="b5-pick"><span className="b5-pick-l">预览用户</span><NativeSelect className="in" id="pv-user" value={preview.user} onChange={event => void chooseUser(event.target.value)}>{data.users.map(user => <option key={user.username} value={user.username}>{user.username}</option>)}</NativeSelect></label> : <div className="b5-pick" title="还没有用户，下面用示例数据"><span className="b5-pick-l">预览用户</span><b>{preview.user}</b><span className="tag lab">示例</span></div>}</>} />
    {readError && <ErrorState title="订阅" error={readError} onRetry={onReadRetry} />}
    <div className="b5 r-page">
      <section className="r-sec b5-sec" id="sec-link"><SectionHeading title="订阅链接" /><div id="lk" className={prefixHover || prefixFocus ? "hl" : ""}><div className="r-link"><code className="r-url"><span className="lk-pre">{base}</span><span className="lk-tail">{preview.token ? tail : "/sub/…"}</span></code><Button className="ton r-copy" data-act="copy" data-text={link} disabled={!preview.token || !data.users.length} title={!data.users.length ? "加了用户才能复制" : undefined} onClick={() => void navigator.clipboard.writeText(link).then(() => toast("已复制"), (cause: Error) => toast(cause.message, true))}><Icon name="copy" />复制链接</Button></div></div>
        <div className="field r-pfx" onMouseEnter={() => setPrefixHover(true)} onMouseLeave={() => setPrefixHover(false)} onFocus={() => setPrefixFocus(true)} onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setPrefixFocus(false); }}><label htmlFor="f-prefix">链接开头</label><input className="in mono" id="f-prefix" data-k="sub_url_prefix" value={state.draft.sub_url_prefix} onChange={event => edit("sub_url_prefix", event.target.value)} placeholder="例如 https://sub.example.com" autoComplete="off" spellCheck={false} readOnly={readOnly} aria-invalid={!!state.fieldErrors.sub_url_prefix} aria-describedby="pfx-err pfx-slot" /><span className="s5-field-err" id="pfx-err" role="alert">{state.fieldErrors.sub_url_prefix || ""}</span><div id="pfx-slot"><span className="h">{state.draft.sub_url_prefix ? "客户端要能访问到这个地址。" : `没填，链接用服务器 IP（${prefixBase.replace(/^https?:\/\//, "").replace(/:\d+$/, "")}）开头，拿到链接的人都能看到。`}</span></div></div>
      </section>
      <section className="r-sec b5-sec" id="sec-info"><SectionHeading title="订阅信息" /><div className="r-info"><div className="field"><label htmlFor="f-title">订阅名<Tip>在客户端的订阅列表里显示的名字。「用户名」会换成每个人自己的用户名，不填就只显示用户名。</Tip></label><TitleField value={state.draft.profile_title} readOnly={readOnly} onChange={value => edit("profile_title", value)} /></div>
        <div className="field"><label htmlFor="f-int">刷新间隔<Tip>客户端多久自动更新一次订阅，单位小时。填 0 由客户端决定。</Tip></label><div className="s5-unit"><input className="in mono" id="f-int" data-k="update_interval" value={state.draft.update_interval} onChange={event => { const value = event.target.value; edit("update_interval", /^\d+$/.test(value.trim()) ? +value.trim() : value); }} inputMode="numeric" autoComplete="off" readOnly={readOnly} aria-invalid={!!state.fieldErrors.update_interval} aria-describedby="int-err" /><span>小时</span></div><span className="s5-field-err" id="int-err" role="alert">{state.fieldErrors.update_interval || ""}</span></div>
        <div className="field"><label htmlFor="f-help">帮助链接</label><input className="in mono" id="f-help" data-k="support_link" value={state.draft.support_link} onChange={event => edit("support_link", event.target.value)} placeholder="例如 https://help.example.com" autoComplete="off" spellCheck={false} readOnly={readOnly} /></div></div>
      </section>
      <section className="r-sec b5-sec" id="sec-clients"><SectionHeading title="客户端格式"><Tip>按各客户端常见的标识推算，实际以控制面判断为准。</Tip><span className="sp" />{!sameRules(rules, data.builtin) && <span className="sub b5-cust">已改过识别规则</span>}{writable && <Button size="sm" className="ton" data-act="s5-rules" disabled={state.saving} onClick={() => openDrawer(<RulesDrawer rules={rules} builtin={data.builtin} onDone={value => edit("rules", value)} />, { wide: true })}><Icon name="sliders" />识别规则</Button>}</SectionHeading>
        <div className="b5-tbl r-tbl" id="b5-tbl"><div className="b5-th"><span>客户端</span><span>拿到</span></div><div className="b5-tb">{clientGroups(rules).map(([format, apps]) => <ClientRow key={format} apps={apps.join("、")} format={format} changed={!apps.every(name => clientFormat(originalRules, CLIENT_APPS.find(([app]) => app === name)![1]) === format)} />)}<ClientRow apps={<>其他客户端<span className="sub">包括用浏览器打开</span></>} format={clientFormat(rules, OTHER_UA)} changed={clientFormat(rules, OTHER_UA) !== clientFormat(originalRules, OTHER_UA)} other /></div></div>
      </section>
      <section className={"r-sec r-adv " + (advanced ? "open" : "")} id="sec-adv"><button type="button" className="r-sum" data-act="s5-adv" aria-expanded={advanced} onClick={() => setAdvanced(previous => { advancedPreference = !previous; return !previous; })}><h2>高级</h2><span className="sp" /><span className="sub">订阅底稿 · {TEMPLATE_FORMATS.filter(format => data.settings.templates?.[format]).length ? `${TEMPLATE_FORMATS.filter(format => data.settings.templates?.[format]).length} 种自定义` : "都用内置"}</span><Icon name="down" className="s5-car" /></button>{advanced && <><div className="r-sh r-sh2"><h3>订阅底稿</h3><Tip>分流规则、策略组和 DNS 写在底稿里。节点会自动放进去，其余部分原样发给所有人。</Tip></div><TemplateList settings={data.settings} routeKey={routeKey} onOpen={format => openDrawer(<TemplateDrawer format={format} custom={!!data.settings.templates?.[format]} onSaved={() => { void refresh(); }} />, { wide: true, key: "template-" + format })} /></>}</section>
    </div><div className="s5-sb" id="sb">{writable && dirty && <div className="s5-bar" role="region" aria-label="未保存的改动"><span className="s5-bar-t"><b>有 {labels.length} 处改动</b><span className="sub ell" title={labels.join("、")}>{labels.join("、")}</span></span>{state.error && <span className="s5-bar-err" role="alert">{state.error}</span>}<span className="sp" /><Button data-act="s5-discard" disabled={state.saving} onClick={discard}>放弃</Button><Button variant="primary" data-act="s5-save" loading={state.saving} onClick={() => void save()}>保存</Button></div>}</div>
  </>;
}
function ClientRow({ apps, format, changed, other }: { apps: ReactNode; format: string; changed: boolean; other?: boolean }) { const metadata = FORMATS[format] || FORMATS.links; return <div className={"b5-tr " + (other ? "b5-other" : "")}><span className="b5-apps">{apps}</span><span className="b5-fmt"><b>{metadata[0]}</b><span className="sub">{metadata[1]}</span>{changed && <span className="tag lab">改过</span>}</span></div>; }
function TemplateList({ settings, routeKey, onOpen }: { settings: Settings; routeKey: string; onOpen: (format: TemplateFormat) => void }) {
  const { toast } = useApp();
  const query = usePageData("sub-templates", subscriptionTemplates, { poll: 0 });
  const previousRoute = useRef(routeKey);
  useEffect(() => {
    if (previousRoute.current === routeKey) return;
    previousRoute.current = routeKey;
    void query.refresh(true);
  }, [routeKey, query.refresh]);
  useEffect(() => { if (query.error) toast(query.error.message, true); }, [query.error, toast]);
  return <div className="zebra r-zebra">{TEMPLATE_FORMATS.map(format => { const body = query.data?.[format] || "", custom = !!settings.templates?.[format]; return <button key={format} type="button" className="zr zr-tpl" data-act="tpl-open" data-f={format} onClick={() => onOpen(format)}><span className="zr-c"><b>{TEMPLATE_META[format].name}</b></span><span className={"pill " + (custom ? "on" : "")}>{custom ? "自定义" : "内置"}</span><span className="sub mono hide-m">{body ? `${templateLines(body)} 行 · ${templateSize(templateBytes(body))}` : ""}</span><span className="btn sm ton"><Icon name="tpl" />打开</span></button>; })}</div>;
}
