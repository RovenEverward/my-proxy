import { NativeSelect } from "@/components/native-select";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button } from "../../components/ui/button";
import { Card, ErrorState, Icon, LoadingTable, PageHeader, Tabs } from "../../components/primitives";
import { useApp, usePageTitle } from "../../runtime/app-context";
import { usePageData } from "../../runtime/query";
import type { PageProps } from "../../runtime/types";
import { PAGE_SIZE, STATUS_TABS } from "../../domain/users";
import { sortedPlans } from "../../domain/plans";
import { readUsers, type UserFilters, type UsersData } from "./hooks";
import { PresenceSummary } from "./presence";
import { UserList } from "./list";
import { UserDetail } from "./detail";
import { UserEditor } from "./editor";
export { UserStatus, UsageBar } from "./components";
export { openRenew } from "./renew";
export { useUserActions } from "./actions";
let sessionFilters: UserFilters = { status: "", search: "", plan: "", offset: 0 };
let sessionDirection = -1;
export const USER_TABS = [{ key: "users", label: "用户", href: "#users" }, { key: "plans", label: "套餐", href: "#users/plans" }];
export function UsersPage({ route }: PageProps) {
  const app = useApp();
  const { navigate, closeOverlays, openDrawer, setBusy, navigationState } = app;
  const name = route[1] && route[1] !== "plans" ? route[1] : undefined;
  const navigationKey = JSON.stringify(navigationState);
  const initialFilters = () => navigationState.status !== undefined || navigationState.plan !== undefined ? { status: navigationState.status || "", plan: navigationState.plan || "", search: "", offset: 0 } : sessionFilters;
  const [filters, setFilters] = useState<UserFilters>(initialFilters);
  const [search, setSearch] = useState(filters.search), [dir, setDirection] = useState(sessionDirection);
  const [selected, setSelected] = useState(new Set<string>());
  const [editor, setEditor] = useState<string | null | undefined>(undefined);
  const [detailVersion, setDetailVersion] = useState(0), [created, setCreated] = useState("");
  const [writeVersion, setWriteVersion] = useState(0);
  const queryKey = "users" + JSON.stringify(filters);
  const query = usePageData<UsersData>(queryKey, read => readUsers(read, filters));
  const displayed = useRef<UsersData | null>(query.data);
  const observed = useRef({ key: queryKey, data: query.data, detail: !!name, writeVersion });
  const previous = observed.current;
  const changed = previous.data !== query.data;
  // Consume detail-time publications without applying them when the drawer closes.
  if (previous.key !== queryKey || !displayed.current || previous.writeVersion !== writeVersion || !name && !previous.detail && changed) displayed.current = query.data;
  observed.current = { key: queryKey, data: query.data, detail: !!name, writeVersion };
  const data = displayed.current;
  usePageTitle(name ? [{ label: "用户", href: "#users" }, { label: name }] : [{ label: "用户" }]);
  useEffect(() => { sessionFilters = filters; }, [filters]);
  useEffect(() => { sessionDirection = dir; }, [dir]);
  useEffect(() => {
    if (navigationState.status !== undefined || navigationState.plan !== undefined) {
      const next = { status: navigationState.status || "", plan: navigationState.plan || "", search: "", offset: 0 };
      setFilters(next); setSearch(""); setSelected(new Set());
    }
  }, [navigationKey]);
  useEffect(() => {
    const timeout = setTimeout(() => { const next = search.trim(); if (next !== filters.search) { setFilters(current => ({ ...current, search: next, offset: 0 })); setSelected(new Set()); } }, 250);
    return () => clearTimeout(timeout);
  }, [search, filters.search]);
  useEffect(() => { setBusy(query.loading); return () => setBusy(false); }, [query.loading, setBusy]);
  const onChanged = useCallback(async () => { await query.refresh(false); setWriteVersion(version => version + 1); }, [query.refresh]);
  const close = closeOverlays;
  const dismissed = useCallback(() => { setEditor(undefined); setCreated(""); if (name) navigate("users"); }, [name, navigate]);
  const saved = useCallback(async (username: string, isNew: boolean) => {
    await onChanged();
    setEditor(undefined); setCreated(isNew ? username : "");
    if (name === username) setDetailVersion(version => version + 1);
    else navigate("users/" + encodeURIComponent(username));
  }, [name, navigate, onChanged]);
  useLayoutEffect(() => {
    if (editor !== undefined) openDrawer(<UserEditor key={`editor/${editor || "new"}`} name={editor || undefined} onClose={close} onSaved={saved} />, { key: `user-editor/${editor || "new"}`, onClose: dismissed });
    else if (name) openDrawer(<UserDetail key={`${name}/${detailVersion}`} name={name} base={data} created={created === name} onEdit={() => setEditor(name)} onClose={close} onChanged={onChanged} />, { key: `user/${name}`, onClose: dismissed });
  }, [name, editor, data, detailVersion, created, openDrawer, close, dismissed, saved, onChanged]);
  const pickable = filters.status === "expired" && app.can("DELETE", "/api/users/x");
  useEffect(() => {
    const valid = new Set(data?.page.users.filter(user => user.status === "expired").map(user => user.username));
    setSelected(current => { const next = new Set([...current].filter(value => pickable && valid.has(value))); return next.size === current.size ? current : next; });
  }, [data, pickable]);
  const changeFilters = (patch: Partial<UserFilters>) => { setFilters(current => ({ ...current, ...patch, offset: patch.offset || 0 })); setSelected(new Set()); };
  const openNew = () => setEditor(null);
  return <><PageHeader title="用户" description="每个用户一条自己的订阅" actions={<><Button className="hide-m" data-act="user-export" onClick={() => app.confirmBox({ title: "导出 CSV", text: "导出用户清单暂不支持在窗口里操作。", ok: "知道了", run: () => {} })}><Icon name="dl" />导出 CSV</Button>{app.can("POST", "/api/users") && <Button variant="primary" data-act="user-new" onClick={openNew}><Icon name="plus" />添加用户</Button>}</>} /><Tabs items={USER_TABS} value="users" />
    <Card><div className="tb"><div className="seg" role="tablist" aria-label="用户状态">{STATUS_TABS.map(([status, label]) => <button type="button" role="tab" aria-selected={filters.status === status} className={filters.status === status ? "on" : ""} data-act="user-filter" data-status={status} key={status} onClick={() => changeFilters({ status })}>{label}<em>{data?.counts[status] ?? ""}</em></button>)}</div><span className="sp" style={{ flex: 1 }} />{data && <PresenceSummary data={data} />}<NativeSelect className="in w180" id="uf-plan" aria-label="按套餐筛选" value={filters.plan} onChange={event => changeFilters({ plan: event.target.value })}><option value="">全部套餐</option>{sortedPlans(data?.plans || []).map(plan => <option value={plan.id} key={plan.id}>{plan.name}</option>)}</NativeSelect><label className="search in-card"><Icon name="search" /><input type="search" id="user-search" placeholder="搜索用户名" value={search} autoComplete="off" onChange={event => setSearch(event.target.value)} /></label></div>
      {data && query.error && <ErrorState title="用户" error={query.error} onRetry={() => void query.refresh()} />}
      {!data ? query.error ? <ErrorState title="用户" error={query.error} onRetry={() => void query.refresh()} /> : <LoadingTable rows={8} columns={7} /> : <UserList data={data} filtered={!!(filters.status || filters.search || filters.plan)} pickable={pickable} selected={selected} onSelect={setSelected} onDeleted={username => setSelected(current => { const next = new Set(current); next.delete(username); return next; })} onChanged={onChanged} dir={dir} onSort={() => setDirection(current => -current)} onPage={direction => changeFilters({ offset: Math.max(0, filters.offset + PAGE_SIZE * direction) })} onOpen={username => navigate("users/" + encodeURIComponent(username))} canCreate={app.can("POST", "/api/users")} onNew={openNew} />}
    </Card></>;
}
