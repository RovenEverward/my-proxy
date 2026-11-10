import { useCallback, useEffect, useLayoutEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { ErrorState, Icon, LoadingTable, PageHeader, Tabs } from "../../components/primitives";
import { useApp, usePageTitle } from "../../runtime/app-context";
import { usePageData } from "../../runtime/query";
import type { PageProps } from "../../runtime/types";
import type { UserList } from "../../domain/users";
import type { PlanData } from "../../domain/plans";
import { PlanEditor } from "./editor";
import { PlanList } from "./list";
const tabs = [{ key: "users", label: "用户", href: "#users" }, { key: "plans", label: "套餐", href: "#users/plans" }];
export function PlansPage({ route: _route }: PageProps) {
  const app = useApp(), { openDrawer, closeOverlays, setBusy, navigate } = app;
  const [editor, setEditor] = useState<number | null | undefined>(undefined);
  const query = usePageData<PlanData>("plans", async read => {
    const [plans, nodes, inbounds, users] = await Promise.all([read<PlanData["plans"]>("/api/plans"), read<PlanData["nodes"]>("/api/nodes"), read<PlanData["inbounds"]>("/api/inbounds"), read<UserList>("/api/users?limit=1000")]);
    return { plans: plans || [], nodes: nodes || [], inbounds: inbounds || [], users: users.users || [] };
  });
  usePageTitle([{ label: "用户", href: "#users" }, { label: "套餐" }]);
  useEffect(() => { setBusy(query.loading); return () => setBusy(false); }, [query.loading, setBusy]);
  const close = useCallback(() => { setEditor(undefined); closeOverlays(); }, [closeOverlays]);
  const saved = useCallback(() => query.refresh(false), [query.refresh]);
  useLayoutEffect(() => {
    if (editor === undefined || !query.data) return;
    const plan = query.data.plans.find(item => item.id === editor) || null;
    openDrawer(<PlanEditor key={editor || "new"} plan={plan} data={query.data} onClose={close} onSaved={saved} />, { wide: true, key: `plan/${editor || "new"}`, onClose: () => setEditor(undefined) });
  }, [editor, query.data, openDrawer, close, saved]);
  return <><PageHeader title="用户" description="套餐决定用户能用哪些节点。挂了套餐的用户只拿得到套餐里的节点" actions={app.can("POST", "/api/plans") ? <Button variant="primary" data-act="pl-edit" data-id="" disabled={!query.data} onClick={() => setEditor(null)}><Icon name="plus" />新建套餐</Button> : undefined} /><Tabs items={tabs} value="plans" />{query.data && query.error && <ErrorState title="套餐" error={query.error} onRetry={() => void query.refresh()} />}{!query.data ? query.error ? <ErrorState title="套餐" error={query.error} onRetry={() => void query.refresh()} /> : <LoadingTable rows={4} columns={3} /> : <PlanList data={query.data} onEdit={setEditor} onUsers={id => navigate("users", { plan: String(id) })} />}</>;
}
