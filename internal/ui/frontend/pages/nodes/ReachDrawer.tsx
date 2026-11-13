import { Button } from "../../components/ui/button";
import { DrawerBody, DrawerHeader, FlagText, Icon } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { cellText, verdictLong, verdictText, type ReachCell, type ReachNodeCheck } from "../../domain/reach";
import { nodeLabel, type NodeView } from "../../domain/nodes";

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
      <div className="reach-legend">UDP 超时可能是收了没回，判定仅供参考。检查于 {check.checked_at ? new Date(check.checked_at).toLocaleString() : "—"}</div>
    </DrawerBody>
    <div className="df"><button type="button" className="lnk" data-act="reach-open-node" onClick={() => app.navigate(`nodes/${node.id}`)}>打开节点</button><span className="sp" /><Button size="sm" data-act="reach-recheck" loading={running} onClick={onRecheck}><Icon name="refresh" />重新检查</Button></div>
  </>;
}
