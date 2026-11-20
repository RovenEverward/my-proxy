import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { DrawerBody, DrawerHeader, FlagText, Icon } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { cellText, verdictLong, verdictText, type ReachCell, type ReachNodeCheck } from "../../domain/reach";
import { nodeLabel, type NodeView } from "../../domain/nodes";
import { nodeSubject } from "../../domain/probes";
import { bandwidthText, bpsText, metricsMap, stabilityText, type MetricsResponse, type SubjectMetrics } from "../../domain/metrics";
import { api } from "../../runtime/api";

// 一个(入站,协议)格子:状态点 + 判定词 + 时延。
function ReachCellView({ cell }: { cell?: ReachCell }) {
  const c = cellText(cell);
  return <span className="reach-r"><span className="st"><span className={`dot ${c.tone}`} />{c.text}</span>{c.latency && <span className="lat">{c.latency}</span>}</span>;
}

function SourceGroup({ title, note, rows, side }: { title: string; note: string; rows: ReachNodeCheck["rows"]; side: "local" | "cp" }) {
  return <>
    <div className="reach-src-head"><b>{title}</b><span className="sub">{note}</span></div>
    {(rows || []).map(row => {
      const p = side === "local" ? row.local : row.cp;
      return <div className="reach-row" key={row.inbound_id} data-inbound={row.inbound_id}>
        <span className="inb"><span className="tag">{row.label || `#${row.inbound_id}`}</span><span className="cx-port">:{row.port}</span><b>{row.protocol}</b></span>
        <ReachCellView cell={p?.tcp} />
        <ReachCellView cell={p?.udp} />
      </div>;
    })}
  </>;
}

// ReachDrawer 是一台节点的触达明细:判定一句话 + 两来源×入站的 TCP/UDP 结果。
export function ReachDrawer({ node, check, running, onRecheck }: { node: NodeView; check: ReachNodeCheck; running: boolean; onRecheck: () => void }) {
  const app = useApp();
  const tone = check.verdict === "ok" ? "stone" : check.verdict === "unknown" ? "" : "bad";
  const icon = check.verdict === "ok" ? <Icon name="check" /> : <Icon name="warn" className={check.verdict === "unknown" ? "ico-warn" : "ico-bad"} />;
  return <><DrawerHeader title={<FlagText>{nodeLabel(node)}</FlagText>} description="触达检查 · 两来源 × 端口明细" onClose={app.closeOverlays} />
    <DrawerBody>
      <div className={`callout ${tone} mb`}>{icon}<div><b>{verdictText(check.verdict)}</b><div>{verdictLong[check.verdict]}</div></div></div>
      <SourceGroup title="本机" note="这台电脑直接探测，反映你所在的网络" rows={check.rows} side="local" />
      <SourceGroup title="控制面" note="控制面探测，反映节点本身是否活着" rows={check.rows} side="cp" />
      <MetricSection node={node} />
      <div className="reach-legend">UDP 超时可能是收了没回，判定仅供参考。检查于 {check.checked_at ? new Date(check.checked_at).toLocaleString() : "—"}</div>
    </DrawerBody>
    <div className="df"><button type="button" className="lnk" data-act="reach-open-node" onClick={() => app.navigate(`nodes/${node.id}`)}>打开节点</button><span className="sp" /><Button size="sm" data-act="reach-recheck" loading={running} onClick={onRecheck}><Icon name="refresh" />重新检查</Button></div>
  </>;
}

// MetricSection 是 UC-20 的稳定度+带宽段:独立拉一次 /api/metrics(抽屉
// 低频打开,不复用列表缓存),拿到就显示,404/失败静默不挡触达明细。
function MetricSection({ node }: { node: NodeView }) {
  const [m, setM] = useState<SubjectMetrics | null>(null);
  useEffect(() => {
    let cancelled = false;
    void api<MetricsResponse>("/api/metrics").then(d => {
      if (!cancelled) setM(metricsMap(d).get(nodeSubject(node.id)) || null);
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [node.id]);
  if (!m) return null;
  const st = stabilityText(m.stability);
  const bt = bandwidthText(m.bandwidth);
  return <div className="reach-src" data-act="metric-section">
    <div className="reach-src-head"><b>境内指标</b><span className="sub">稳定度按境内探测样本滚动聚合；带宽为最近一次实测</span></div>
    <div className="reach-row"><span className="inb"><b>稳定度</b></span><span className="reach-r" title={st.title}>{st.text}</span></div>
    <div className="reach-row"><span className="inb"><b>带宽</b></span><span className="reach-r" title={bt.title}>{bt.text}{m.bandwidth.measured_at ? ` · ${new Date(m.bandwidth.measured_at).toLocaleString()}` : ""}</span></div>
  </div>;
}
