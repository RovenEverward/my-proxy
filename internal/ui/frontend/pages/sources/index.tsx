import { Fragment, useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { Card, EmptyState, ErrorState, FlagText, Icon, LoadingTable, PageHeader, Tabs } from "../../components/primitives";
import { useApp, usePageTitle } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { usePageData } from "../../runtime/query";
import type { Settings, User } from "../../runtime/types";
import { NODE_TABS, sinceText } from "../../domain/nodes";
import { errorMessage, readSubscriptionUsers, sourcePath, type SubscriptionSource } from "../../domain/routing";
import { SourceFormatsSummary, SourceGrantsSummary, sourceInterval, SourceStatus } from "./components";
import { MixedPreviewModal } from "./MixedPreview";
import { SourceDrawer, type SourceDrawerTab } from "./SourceEditor";

interface SourcesData { sources: SubscriptionSource[]; users: User[] }

export function SourcesPage() {
  const app = useApp();
  usePageTitle([{ label: "节点", href: "#nodes" }, { label: "外部订阅" }]);
  const page = usePageData<SourcesData>("sources", async read => {
    const [sources, users] = await Promise.all([read<SubscriptionSource[]>("/api/sources"), readSubscriptionUsers(read)]);
    return { sources, users };
  });
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set());
  const [refreshing, setRefreshing] = useState<Set<number>>(() => new Set());

  useEffect(() => { app.setBusy(page.loading); return () => app.setBusy(false); }, [page.loading, app.setBusy]);

  const hasRefreshing = !!page.data?.sources.some(source => source.refreshing);
  useEffect(() => {
    if (!hasRefreshing) return;
    const timer = setInterval(() => { if (!document.hidden) void page.refresh(); }, 1000);
    return () => clearInterval(timer);
  }, [hasRefreshing, page.refresh]);

  function openSource(id?: number, initialTab: SourceDrawerTab = "config") {
    app.openDrawer(<SourceDrawer id={id} initialTab={initialTab} onChanged={() => page.refresh()} onCreated={createdId => openSource(createdId, "config")} />, { wide: true, key: "source:" + (id || "new") });
  }

  function openPreview() {
    app.openModal(<MixedPreviewModal users={page.data?.users || []} revision={JSON.stringify(page.data?.sources || [])} />);
  }

  function toggleExpand(id: number) {
    setExpanded(previous => {
      const next = new Set(previous);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  async function refreshSource(source: SubscriptionSource) {
    if (refreshing.has(source.id)) return;
    setRefreshing(previous => new Set(previous).add(source.id));
    try {
      await write("POST", sourcePath(source.id) + "/refresh");
      app.toast("已开始刷新，可在来源详情查看结果");
      await page.refresh();
    } catch (reason) {
      app.toast(errorMessage(reason), true);
    } finally {
      setRefreshing(previous => { const next = new Set(previous); next.delete(source.id); return next; });
    }
  }

  const canCreate = app.can("POST", "/api/sources");

  return <>
    <PageHeader
      title="节点"
      description="将外部订阅与自建节点合并分发，接收用户继续使用原订阅链接。"
      actions={<>
        <Button data-act="source-preview-open" disabled={!page.data} onClick={openPreview}><Icon name="eye" />预览混合订阅</Button>
        {canCreate && <Button variant="primary" data-act="source-new" onClick={() => openSource()}><Icon name="plus" />添加订阅</Button>}
      </>}
    />
    <Tabs items={NODE_TABS} value="sources" />
    {!page.data ? page.error ? <ErrorState title="外部订阅" error={page.error} onRetry={() => void page.refresh()} /> : <LoadingTable rows={4} columns={5} /> : <>
      {page.error && <p className="c-bad" role="alert">读取外部订阅失败：{page.error.message}<Button size="sm" data-act="reload" onClick={() => void page.refresh()}>重试</Button></p>}
      <Card className="source-list">
        {!page.data.sources.length ? <EmptyState
          icon="sub"
          title="还没有外部订阅"
          description="添加订阅地址后，可先检查导入与格式兼容结果，再选择接收用户。"
          actions={canCreate && <Button size="sm" variant="primary" data-act="source-new" onClick={() => openSource()}><Icon name="plus" />添加订阅</Button>}
        /> : <div className="tw">
          <table className="src-table">
            <thead>
              <tr>
                <th>来源</th>
                <th>状态</th>
                <th className="hide-m">接收用户</th>
                <th className="hide-m">格式兼容</th>
                <th className="num hide-m">最近成功</th>
                <th className="num" aria-label="操作" />
              </tr>
            </thead>
            <tbody>
              {page.data.sources.map(source => {
                const isOpen = expanded.has(source.id);
                const members = source.nodes || [];
                return <Fragment key={source.id}>
                  <tr className={"src-group source-row" + (isOpen ? " open" : "")} onClick={() => toggleExpand(source.id)}>
                    <td>
                      <div className="src-name-line">
                        <button
                          type="button"
                          className="src-chev"
                          data-act="source-expand"
                          data-id={source.id}
                          aria-expanded={isOpen}
                          aria-label={`${isOpen ? "收起" : "展开"}来源 ${source.name} 的节点`}
                          onClick={event => { event.stopPropagation(); toggleExpand(source.id); }}
                        ><Icon name="down" /></button>
                        <button
                          type="button"
                          className="lnk src-open-btn"
                          data-act="source-open"
                          data-id={source.id}
                          onClick={event => { event.stopPropagation(); openSource(source.id, "config"); }}
                        >{source.name}</button>
                      </div>
                      <div className="sub src-meta">{source.node_count} 个节点 · {sourceInterval(source)}{source.region ? ` · ${source.region}` : ""}</div>
                    </td>
                    <td>
                      <SourceStatus source={source} />
                      {source.last_error && <div className="sub c-bad src-row-err" title={source.last_error}>{source.last_error}</div>}
                    </td>
                    <td className="hide-m" onClick={event => { event.stopPropagation(); openSource(source.id, "config"); }}>
                      <SourceGrantsSummary grants={source.grants} plans={source.plan_grants} all={source.grant_all} />
                    </td>
                    <td className="hide-m" onClick={event => { event.stopPropagation(); openSource(source.id, "nodes"); }}>
                      <SourceFormatsSummary source={source} />
                    </td>
                    <td className="num sub hide-m">{source.last_good_at ? sinceText(source.last_good_at) : "尚未成功"}</td>
                    <td className="num">
                      <span className="src-row-acts">
                        <Button
                          size="icon"
                          title={"刷新 " + source.name}
                          aria-label={"刷新 " + source.name}
                          data-act="source-refresh"
                          data-id={source.id}
                          disabled={!source.enabled || source.refreshing || refreshing.has(source.id) || !canCreate}
                          loading={refreshing.has(source.id)}
                          onClick={event => { event.stopPropagation(); void refreshSource(source); }}
                        ><Icon name="refresh" /></Button>
                        <Button
                          size="icon"
                          title={"管理 " + source.name}
                          aria-label={"管理 " + source.name}
                          data-act="source-manage"
                          data-id={source.id}
                          onClick={event => { event.stopPropagation(); openSource(source.id, "config"); }}
                        ><Icon name="sliders" /></Button>
                      </span>
                    </td>
                  </tr>
                  {isOpen && (members.length ? members.map((member, index) => (
                    <tr className="src-member" key={`${source.id}:${index}`}>
                      <td>
                        <span className="src-member-name" title={member.raw_name && member.raw_name !== member.name ? `上游原名：${member.raw_name}` : undefined}><FlagText>{member.name}</FlagText></span>
                        <span className="tag src-member-proto-m">{member.protocol}</span>

                      </td>
                      <td><span className="sub">只读外部节点</span></td>
                      <td className="hide-m" />
                      <td className="hide-m"><span className="tag">{member.protocol}</span></td>
                      <td className="num sub hide-m" />
                      <td className="num" />
                    </tr>
                  )) : (
                    <tr className="src-member">
                      <td className="sub src-member-empty" colSpan={6}>这个来源还没有节点{source.last_error ? "（上次刷新失败）" : ""}。</td>
                    </tr>
                  ))}
                </Fragment>;
              })}
            </tbody>
          </table>
        </div>}
      </Card>
    </>}
  </>;
}

