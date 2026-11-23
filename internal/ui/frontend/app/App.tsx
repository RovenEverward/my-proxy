import { AppShell } from "./AppShell";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentType, } from "react";
import { AppContext, type AppContextValue, type Crumb, type LeaveGuard } from "@/runtime/app-context";
import { APIError, api, requestEvents } from "@/runtime/api";
import { refreshPages, useQueryActivity } from "@/runtime/query";
import { canonicalKey, navigation, routeOf } from "@/runtime/navigation";
import { logout } from "@/runtime/auth";
import { useOverlayManager } from "@/components/overlays";
import type { Meta, PageProps } from "@/runtime/types";
import { UsersPage } from "@/pages/users";
import { PlansPage } from "@/pages/plans";
import { NodesPage } from "@/pages/nodes";
import { SubscriptionPage } from "@/pages/sub";
import { SystemPage } from "@/pages/system";
import { RulesPage } from "@/pages/rules";
import { DoctorPage } from "@/pages/doctor";
import { OverviewPage } from "@/pages/overview";
import { LoginPage } from "@/pages/login";
import { ErrorState, LoadingTable, PageHeader } from "@/components/primitives";
import { useUpdateState } from "@/runtime/update";
import { UpdateDialog } from "@/components/UpdateDialog";

const pages: Record<string, ComponentType<PageProps>> = {
  overview: OverviewPage, users: UsersPage, plans: PlansPage, nodes: NodesPage, sub: SubscriptionPage,
  system: SystemPage, rules: RulesPage, doctor: DoctorPage, login: LoginPage,
};
const titleFor = (key: string) => navigation.find(item => item.key === key)?.label || (key === "plans" ? "套餐" : "MPX");

// App 的会话状态机(UC-26):启动先取 /api/admin/meta。
//   - 200:已登录,进工作区。
//   - 401:未登录,进登录页(hash 置 #login;登录成功由 login() 整页重载)。
//   - 其他错:控制面连不上,给重试。
// 运行中任何 /api 响应 401 都会广播 session-expired(api.ts),这里收到同样
// 回登录页 —— 改密、吊销、过期三条路汇成一条。
export function App() {
  const [meta, setMeta] = useState<Meta | null>(null);
  const [metaError, setMetaError] = useState<Error | null>(null);
  const [loggedOut, setLoggedOut] = useState(() => canonicalKey(location.hash) === "login");
  const load = useCallback(async () => {
    try {
      const m = await api<Meta>("/api/admin/meta");
      setMeta(m);
      setMetaError(null);
      setLoggedOut(false);
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) { setMeta(null); setLoggedOut(true); }
      else setMetaError(cause instanceof Error ? cause : new Error(String(cause)));
    }
  }, []);
  useEffect(() => { void load(); }, [load]);
  // session-expired 由 api.ts 在 401 时广播;登录页自己的提交 401 已被过滤,
  // 这里只管「原来登录着、现在失效了」这一种情形。
  useEffect(() => {
    const onExpired = () => setLoggedOut(true);
    requestEvents.addEventListener("session-expired", onExpired);
    return () => requestEvents.removeEventListener("session-expired", onExpired);
  }, []);

  if (loggedOut) return <LoginGate />;
  if (metaError) return <div className="page"><PageHeader title="MPX" /><ErrorState title="控制面" error={metaError} onRetry={() => { setMetaError(null); void load(); }} /></div>;
  if (!meta) return <div className="page"><PageHeader title="MPX" /><LoadingTable /></div>;
  return <Shell meta={meta} />;
}

// LoginGate 让 hash 与登录态保持一致:进登录页时把 #login 写进地址栏(可分享、
// 刷新停在登录页),登录后由 login() 清空 hash 并重载,回到总览。
function LoginGate() {
  useEffect(() => {
    if (location.hash !== "#login") history.replaceState(null, "", "#login");
  }, []);
  return <div className="page"><LoginPage route={["login"]} /></div>;
}

function Shell({ meta }: { meta: Meta }) {
  const [key, setKey] = useState(() => canonicalKey(location.hash));
  const [navigationState, setNavigationState] = useState<Record<string, string>>({});
  const [crumbs, setCrumbs] = useState<Crumb[]>(() => [{ label: titleFor(routeOf(key)[0]) }]);
  const [busy, setBusy] = useState(false);
  const overlays = useOverlayManager();
  const overlayRef = useRef(overlays); overlayRef.current = overlays;
  const keyRef = useRef(key); keyRef.current = key;
  const guardRef = useRef<LeaveGuard | null>(null);
  const activity = useQueryActivity();
  const update = useUpdateState(true);
  const updateRef = useRef(update); updateRef.current = update;
  const route = useMemo(() => routeOf(key), [key]);
  const setLeaveGuard = useCallback((guard: LeaveGuard | null) => { guardRef.current = guard; }, []);
  const commit = useCallback((next: string, state: Record<string, string> = {}) => {
    const previous = keyRef.current;
    if (overlayRef.current.isLocked()) return;
    if (next !== previous) overlayRef.current.dismissOverlays();
    keyRef.current = next;
    setNavigationState(state);
    setKey(next);
    setCrumbs([{ label: titleFor(routeOf(next)[0]) }]);
    if (location.hash !== "#" + next) location.hash = next;
  }, []);
  const navigate = useCallback((to: string, state: Record<string, string> = {}) => {
    const next = canonicalKey(to);
    const apply = () => commit(next, state);
    if (overlayRef.current.isLocked()) return;
    // Subscription anchors retain the current draft and component instance.
    if (keyRef.current.startsWith("sub") && next.split("/")[0] === "sub") { apply(); return; }
    const allowed = guardRef.current?.(apply) ?? true;
    if (allowed instanceof Promise) void allowed.then(value => { if (value) apply(); });
    else if (allowed) apply();
  }, [commit]);
  const guardedAction = useCallback((action: () => void) => {
    if (overlayRef.current.isLocked()) return;
    const allowed = guardRef.current?.(action) ?? true;
    if (allowed instanceof Promise) void allowed.then(value => { if (value) action(); });
    else if (allowed) action();
  }, []);
  const openUpdate = useCallback(() => {
    void updateRef.current.refresh().then(data => {
      if (data) overlayRef.current.openModal(<UpdateDialog data={data} onClose={overlayRef.current.closeModal} />);
    });
  }, []);
  // UC-26:管理台没有 mpx ui 的能力白名单,登录进来的就是管理员,can 恒真。
  // 签名保留(~40 个调用点),语义改成「已登录 = 全允许」。
  const can = useCallback((_method: string, _path: string) => true, []);
  const context = useMemo<AppContextValue>(() => ({
    meta, route, navigationState, can, navigate, runGuarded: guardedAction, refresh: refreshPages, setCrumbs, setBusy, setLeaveGuard,
    openDrawer: overlays.openDrawer, closeOverlays: overlays.closeOverlays, closeModal: overlays.closeModal,
    openModal: overlays.openModal, setModalLocked: overlays.setModalLocked, confirmBox: overlays.confirmBox,
    toast: overlays.toast, openUpdate, logout: () => void logout(),
  }), [meta, route, navigationState, can, navigate, guardedAction, setLeaveGuard, overlays.openDrawer, overlays.closeOverlays, overlays.closeModal, overlays.openModal, overlays.setModalLocked, overlays.confirmBox, overlays.toast, openUpdate]);
  useEffect(() => {
    const onHash = () => {
      const next = canonicalKey(location.hash);
      if (next === keyRef.current) { if (location.hash !== "#" + next) history.replaceState(null, "", "#" + next); return; }
      navigate(next);
      if (keyRef.current !== next) history.replaceState(null, "", "#" + keyRef.current);
    };
    const onLink = (event: globalThis.MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      const anchor = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>('a[href^="#"]') : null;
      if (!anchor || anchor.target || anchor.hasAttribute("download")) return;
      event.preventDefault();
      if (anchor.hash && anchor.hash !== "#") navigate(anchor.hash);
    };
    addEventListener("hashchange", onHash); document.addEventListener("click", onLink);
    return () => { removeEventListener("hashchange", onHash); document.removeEventListener("click", onLink); };
  }, [navigate]);
  useLayoutEffect(() => {
    // workspace 是全应用的统一主题;登录页不在这里(它在 App 外层 LoginGate)。
    document.documentElement.dataset.ui = "workspace";
    if (canonicalKey(location.hash) !== location.hash.slice(1)) history.replaceState(null, "", "#" + key);
  }, [key, route]);
  useEffect(() => {
    if (activity.refreshing) document.documentElement.dataset.loading = "1";
    else delete document.documentElement.dataset.loading;
  }, [activity.refreshing]);
  useEffect(() => {
    const notify = (event: Event) => overlays.toast((event as CustomEvent<Error>).detail.message, true);
    requestEvents.addEventListener("background-error", notify);
    return () => requestEvents.removeEventListener("background-error", notify);
  }, [overlays.toast]);
  const Page = pages[route[0]] || OverviewPage;
  return <AppContext.Provider value={context}>
    <AppShell crumbs={crumbs} busy={busy || activity.loading} updateAvailable={update.available}><Page route={route} /></AppShell>
    {overlays.layers}
    {(busy || activity.refreshing) && <div id="ui-progress" className="ui-progress" role="status">正在处理…</div>}
  </AppContext.Provider>;
}
