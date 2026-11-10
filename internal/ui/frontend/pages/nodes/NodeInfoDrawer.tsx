import { Button } from "../../components/ui/button";
import { DrawerBody, DrawerHeader, EmptyState, FlagText, Icon } from "../../components/primitives";
import { DrawerReadState } from "../../components/connections/EditorFrame";
import { useApp } from "../../runtime/app-context";
import { APIError } from "../../runtime/api";
import { usePageData } from "../../runtime/query";
import { nodeLabel, nodePath, type NodeView } from "../../domain/nodes";
interface Online { node: string; online: { user: string; count: number; ips?: string[] }[] }
export function NodeInfoDrawer({ node, kind, onApply }: { node: NodeView; kind: "online" | "config"; onApply: () => void }) {
  const app = useApp();
  const title = kind === "online" ? `${nodeLabel(node)} 在线` : `${nodeLabel(node)} 的配置预览`;
  const query = usePageData<{ online?: Online; config?: unknown }>(`drawer:node:${node.id}:${kind}`, async read => kind === "online" ? { online: await read<Online>(`${nodePath(node.id)}/online`) } : { config: await read<unknown>(`${nodePath(node.id)}/config`) }, { poll: 0 });
  if (!query.data && (query.loading || kind === "config" || !(query.error instanceof APIError))) return <DrawerReadState title={title} loading={query.loading} error={query.error} retry={() => void query.refresh()} />;
  const error = query.error;
  return <><DrawerHeader title={<FlagText>{title}</FlagText>} description={kind === "online" ? "现场读出的结果" : "「应用到节点」会推这一份"} onClose={app.closeOverlays} /><DrawerBody>
    {error && !query.data ? <div className="empty" role="alert"><div className="ill bad"><Icon name="warn" /></div><h4>读不到在线名单</h4>{error instanceof APIError && error.status === 409 ? <><p>{error.message}</p>{app.can("POST", "/api/nodes/x/sync") && <Button type="button" size="sm" className="mt" data-act="node-sync" onClick={onApply}>把配置应用到节点</Button>}</> : <><p>连不上<FlagText>{nodeLabel(node) || "这台节点"}</FlagText>，这次读不到在线名单。稍后再试，或者先「刷新状态」看看节点是不是在线。</p><details className="sum-raw"><summary>详情<Icon name="down" /></summary><div className="mono">{error.message}</div></details></>}<Button size="sm" className="mt" data-act="node-online" onClick={() => void query.refresh()}>重试</Button></div>
      : query.data?.online ? query.data.online.online.length ? <div className="tw"><table><thead><tr><th>用户</th><th className="num">IP 数</th><th>IP</th></tr></thead><tbody>{query.data.online.online.map(row => <tr key={row.user}><td><b className="bold">{row.user}</b></td><td className="num">{row.count}</td><td className="mono sub">{(row.ips || []).join(", ")}</td></tr>)}</tbody></table></div> : <EmptyState icon="users" title="没有在线用户" description="此刻没有用户连在这台节点上。" /> : <pre className="code">{query.data && "config" in query.data ? typeof query.data.config === "string" ? query.data.config : JSON.stringify(query.data.config, null, 2) : ""}</pre>}
  </DrawerBody></>;
}
