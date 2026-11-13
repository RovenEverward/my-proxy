import { AppShell } from "./AppShell";
import { reportTheme, useTitlebar } from "@/runtime/platform";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ComponentType, } from "react";
import { AppContext, type AppContextValue, type Crumb, type LeaveGuard } from "@/runtime/app-context";
import { requestEvents } from "@/runtime/api";
import { refreshPages, useQueryActivity } from "@/runtime/query";
import { canonicalKey, navigation, routeOf } from "@/runtime/navigation";
import { useOverlayManager } from "@/components/overlays";
import type { Meta, PageProps } from "@/runtime/types";
import { UsersPage } from "@/pages/users";
import { PlansPage } from "@/pages/plans";
import { NodesPage } from "@/pages/nodes";
import { SubscriptionPage } from "@/pages/sub";
import { SystemPage } from "@/pages/system";
import { RulesPage } from "@/pages/rules";
import { DoctorPage } from "@/pages/doctor";
import { SourcesPage } from "@/pages/sources";
import { SetupPage, ConnectForm } from "@/pages/setup";
import { ProfilesDialog } from "@/runtime/profiles";
import { OverviewPage } from "@/pages/overview";
import { UpdateDialog, useUpdateState } from "@/runtime/update";

const pages: Record<string, ComponentType<PageProps>> = {
  overview: OverviewPage, users: UsersPage, plans: PlansPage, nodes: NodesPage, sub: SubscriptionPage,
  system: SystemPage, rules: RulesPage, doctor: DoctorPage, sources: SourcesPage,
};
const titleFor = (key: string) => navigation.find(item => item.key === key)?.label || (key === "plans" ? "套餐" : key === "sources" ? "外部订阅" : "MPX");
export function App({ meta }: { meta: Meta }) {
  const [key, setKey] = useState(() => canonicalKey(location.hash));
  const [navigationState, setNavigationState] = useState<Record<string, string>>({});
  const [crumbs, setCrumbs] = useState<Crumb[]>(() => [{ label: titleFor(routeOf(key)[0]) }]);
  const [busy, setBusy] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const overlays = useOverlayManager();
  const overlayRef = useRef(overlays); overlayRef.current = overlays;
  const keyRef = useRef(key); keyRef.current = key;
  const guardRef = useRef<LeaveGuard | null>(null);
  const activity = useQueryActivity();
  const update = useUpdateState(meta.mode !== "setup");
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
  const openConnect = useCallback(() => { if (!overlayRef.current.isLocked()) { overlayRef.current.dismissOverlays(); setConnecting(true); } }, []);
  const openProfiles = useCallback(() => { if (!overlayRef.current.isLocked()) overlayRef.current.openModal(<ProfilesDialog onAdd={openConnect} />); }, [openConnect]);
  const openUpdate = useCallback(() => {
    void updateRef.current.refresh().then(data => {
      if (data) overlayRef.current.openModal(<UpdateDialog data={data} onClose={overlayRef.current.closeModal} />);
    });
  }, []);
  const can = useCallback((method: string, path: string) => meta.allow.some(rule => {
    const [allowedMethod, prefix] = rule.split(" ");
    return method === allowedMethod && (path === prefix || path.startsWith(prefix.replace(/\/$/, "") + "/"));
  }), [meta.allow]);
  const context = useMemo<AppContextValue>(() => ({
    meta, route, navigationState, can, navigate, runGuarded: guardedAction, refresh: refreshPages, setCrumbs, setBusy, setLeaveGuard,
    openDrawer: overlays.openDrawer, closeOverlays: overlays.closeOverlays, closeModal: overlays.closeModal,
    openModal: overlays.openModal, setModalLocked: overlays.setModalLocked, confirmBox: overlays.confirmBox,
    toast: overlays.toast, openUpdate, openProfiles, openConnect,
  }), [meta, route, navigationState, can, navigate, guardedAction, setLeaveGuard, overlays.openDrawer, overlays.closeOverlays, overlays.closeModal, overlays.openModal, overlays.setModalLocked, overlays.confirmBox, overlays.toast, openUpdate, openProfiles, openConnect]);
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
    // workspace 是全应用的统一主题,不随路由切:总览之外也用它,否则 side/top/
    // btn/page 外壳与面板底色会回到 classic 遗留值(ISS-11)。setup/connecting 仍走
    // classic——向导页没迁到新体系。
    document.documentElement.dataset.ui = meta.mode !== "setup" && !connecting ? "workspace" : "classic";
    if (canonicalKey(location.hash) !== location.hash.slice(1)) history.replaceState(null, "", "#" + key);
  }, [key, route, meta.mode, connecting]);
  useEffect(() => { reportTheme(document.documentElement.dataset.theme || "light"); }, []);
  useEffect(() => {
    if (activity.refreshing) document.documentElement.dataset.loading = "1";
    else delete document.documentElement.dataset.loading;
  }, [activity.refreshing]);
  useEffect(() => {
    const notify = (event: Event) => overlays.toast((event as CustomEvent<Error>).detail.message, true);
    requestEvents.addEventListener("background-error", notify);
    return () => requestEvents.removeEventListener("background-error", notify);
  }, [overlays.toast]);
  useTitlebar();
  const Page = pages[route[0]] || OverviewPage;
  return <AppContext.Provider value={context}>
    {meta.mode === "setup" ? <SetupPage /> : <AppShell crumbs={crumbs} busy={busy || activity.loading} updateAvailable={update.available}><Page route={route} /></AppShell>}
    {connecting && <ConnectForm mode="add" onBack={() => setConnecting(false)} />}
    {overlays.layers}
    {(busy || activity.refreshing) && <div id="ui-progress" className="ui-progress" role="status">正在处理…</div>}
  </AppContext.Provider>;
}
