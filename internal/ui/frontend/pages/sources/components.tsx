import { Fragment } from "react";
import { StatusBadge } from "../../components/primitives";
import { SOURCE_FORMATS, type SourceIssue, type SubscriptionSource } from "../../domain/routing";

export function SourceStatus({ source }: { source: SubscriptionSource }) {
  const [tone, label] = !source.enabled ? ["off", "已停用"] : source.refreshing ? ["warn", "刷新中"] : source.last_error ? ["warn", source.node_count ? "使用上次结果" : "未就绪"] : source.node_count ? ["ok", "可用"] : ["warn", "未就绪"];
  return <StatusBadge tone={tone}>{label}</StatusBadge>;
}
export const sourceInterval = (source: SubscriptionSource) => source.interval_seconds ? `每 ${+(source.interval_seconds / 3600).toFixed(2)} 小时` : "仅手动刷新";
export function SourceIssues({ issues }: { issues?: SourceIssue[] }) {
  if (!issues?.length) return null;
  return <details className="mt"><summary>查看 {issues.length} 条兼容说明</summary>{issues.map((issue, index) => <div className="row sub" key={index}><span>{issue.index ? "第 " + issue.index + " 条 · " : ""}{issue.name}</span><span>{issue.reason}</span></div>)}</details>;
}
export function SourceRuntime({ source }: { source: SubscriptionSource }) {
  return <><div className="flex8"><SourceStatus source={source} /><span className="sub">{source.node_count} 个节点 · {source.input_format || "尚未识别格式"}</span></div>
    {source.last_error && <p className="c-bad" role="alert">{source.last_error}{source.last_good_at ? "；正在使用 " + new Date(source.last_good_at).toLocaleString() + " 的结果。" : ""}</p>}
    {source.snapshot_stale && <p className="sub">旧地址结果，等待新地址刷新。</p>}
  </>;
}
export function SourceFormatDetails({ source }: { source: SubscriptionSource }) {
  return <>{SOURCE_FORMATS.map(([format, label]) => <Fragment key={format}><div className="row"><b className="grow">{label}</b><span className="sub">{source.formats[format]?.count || 0} 个节点</span></div><SourceIssues issues={source.formats[format]?.issues} /></Fragment>)}<SourceIssues issues={source.issues} /></>;
}
