import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { Card, EmptyState, ErrorState, Icon, LoadingTable, PageHeader } from "../../components/primitives";
import { useApp, usePageTitle } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { usePageData } from "../../runtime/query";
import type { PageProps, User } from "../../runtime/types";
import { sinceText } from "../../domain/nodes";
import { errorMessage, readSubscriptionUsers, sourcePath, type SavedProxyGroup, type SubscriptionSource } from "../../domain/routing";
import { sourceInterval, SourceStatus } from "./components";
import { MixedPreview } from "./MixedPreview";
import { SourceDrawer } from "./SourceEditor";

interface SourcesData { sources: SubscriptionSource[]; users: User[]; groups: SavedProxyGroup[] }
export function SourcesPage({ route: _route }: PageProps) {
  const app = useApp();
  usePageTitle([{ label: "订阅", href: "#sub" }, { label: "外部订阅" }]);
  const page = usePageData<SourcesData>("sources", async read => {
    const [sources, users, groups] = await Promise.all([read<SubscriptionSource[]>("/api/sources"), readSubscriptionUsers(read), read<SavedProxyGroup[]>("/api/proxy-groups")]);
    return { sources, users, groups };
  });
  const [refreshing, setRefreshing] = useState<Set<number>>(() => new Set());
  useEffect(() => { app.setBusy(page.loading); return () => app.setBusy(false); }, [page.loading, app.setBusy]);
  const hasRefreshing = !!page.data?.sources.some(source => source.refreshing);
  useEffect(() => {
    if (!hasRefreshing) return;
    const timer = setInterval(() => { if (!document.hidden) void page.refresh(); }, 1000);
    return () => clearInterval(timer);
  }, [hasRefreshing, page.refresh]);
  function openSource(id?: number) {
    app.openDrawer(<SourceDrawer id={id} onChanged={() => page.refresh()} onCreated={openSource} />, { wide: true, key: "source:" + (id || "new") });
  }
  async function refreshSource(source: SubscriptionSource) {
    if (refreshing.has(source.id)) return;
    setRefreshing(previous => new Set(previous).add(source.id));
    try { await write("POST", sourcePath(source.id) + "/refresh"); app.toast("已开始刷新，可在来源详情查看结果"); }
    catch (reason) { app.toast(errorMessage(reason), true); }
    finally { setRefreshing(previous => { const next = new Set(previous); next.delete(source.id); return next; }); }
  }
  return <>
    <PageHeader title="外部订阅" description="将已有订阅与自建节点合并，继续使用原来的订阅链接。" actions={app.can("POST", "/api/sources") && <Button variant="primary" data-act="source-new" onClick={() => openSource()}><Icon name="plus" />添加订阅</Button>} />
    {!page.data ? page.error ? <ErrorState title="外部订阅" error={page.error} onRetry={() => void page.refresh()} /> : <LoadingTable rows={4} columns={3} /> : <>
      {page.error && <p className="c-bad" role="alert">读取外部订阅失败：{page.error.message}<Button size="sm" data-act="reload" onClick={() => void page.refresh()}>重试</Button></p>}
      <Card className="source-list">{page.data.sources.length ? page.data.sources.map(source => <div className="row source-row" key={source.id}><div className="grow source-name"><button type="button" className="lnk" data-act="source-open" data-id={source.id} onClick={() => openSource(source.id)}>{source.name}</button><div className="sub">{sourceInterval(source)} · {source.last_good_at ? "最近成功 " + sinceText(source.last_good_at) : "尚未成功刷新"}</div></div><span className="sub">{source.node_count} 个节点</span><SourceStatus source={source} /><Button size="icon" title={"刷新 " + source.name} aria-label={"刷新 " + source.name} data-act="source-refresh" data-id={source.id} disabled={!source.enabled || source.refreshing || refreshing.has(source.id) || !app.can("POST", "/api/sources")} loading={refreshing.has(source.id)} onClick={() => void refreshSource(source)}><Icon name="refresh" /></Button></div>) : <EmptyState icon="sub" title="还没有外部订阅" description="添加订阅地址后，先检查导入结果，再选择接收用户。" />}</Card>
      <MixedPreview users={page.data.users} revision={JSON.stringify(page.data.sources)} />
    </>}
  </>;
}
