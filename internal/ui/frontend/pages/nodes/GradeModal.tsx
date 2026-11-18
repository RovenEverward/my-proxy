import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { FlagText, Icon, StatusBadge } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import { errorMessage } from "../../domain/nodes";
import { scoreTone, type RegradePacket, type RegradeRow } from "../../domain/quality";

interface Props { nodes: { id: number; name: string; display?: string }[]; onChanged: () => Promise<void> }

// GradeModal 两步:先探测打分(或直接预览),再确认写入改名。
// 探测与控制面 regrade 是两条链:探测写 node_quality,regrade 只读分数重排字母。
export function GradeModal({ nodes, onChanged }: Props) {
  const app = useApp();
  const [rows, setRows] = useState<RegradeRow[] | null>(null);
  const [busy, setBusy] = useState<"probe" | "preview" | "apply" | null>(null);
  const [progress, setProgress] = useState("");
  const [error, setError] = useState("");
  useEffect(() => { app.setModalLocked(busy === "apply"); return () => app.setModalLocked(false); }, [busy, app.setModalLocked]);

  const preview = async (): Promise<RegradeRow[] | null> => {
    const packet = await write<RegradePacket>("POST", "/api/regrade");
    return packet.rows || [];
  };
  // probeAll 逐台探测并打分,全部完成后刷新预览。节点上没有 curl 或探测失败
  // 只让那一台标记未测,不打断剩下的。
  const probeAll = async () => {
    setBusy("probe"); setError("");
    try {
      let done = 0;
      for (const n of nodes) {
        setProgress(`${++done}/${nodes.length} ${n.display || n.name}`);
        try { await api(`/api/nodes/${n.id}/quality`, { method: "POST" }); } catch { /* 单个失败记入该行判定 */ }
      }
      setRows(await preview());
    } catch (reason) { setError(errorMessage(reason)); } finally { setBusy(null); setProgress(""); }
  };
  const justPreview = async () => {
    setBusy("preview"); setError("");
    try { setRows(await preview()); } catch (reason) { setError(errorMessage(reason)); } finally { setBusy(null); }
  };
  const apply = async () => {
    if (!rows?.some(r => r.changed)) return;
    setBusy("apply"); setError("");
    try {
      const packet = await write<RegradePacket>("POST", "/api/regrade", { apply: true });
      const changed = (packet.rows || []).filter(r => r.changed).length;
      app.toast(changed ? `已重排 ${changed} 个节点的显示名` : "字母顺序没有变化");
      app.closeOverlays();
      await onChanged();
    } catch (reason) { setError(errorMessage(reason)); setBusy(null); }
  };

  const changed = (rows || []).filter(r => r.changed).length;
  const tested = (rows || []).filter(r => r.score !== undefined).length;
  return <><div className="mbody"><h2>IP 质量评级</h2>
    <p>按 IP 信誉给节点打分并重新排字母：分数高的在同地区里拿更靠前的字母。分数来自节点出口 IP 的风险标记（机房/VPN/代理/Tor/滥用记录），<b>节点 IP 会发给 ipwho.is、ipapi.is 等第三方</b>。改名字母会反映在订阅里的节点名。</p>
    {busy === "probe" && <div className="callout stone mb" role="status"><Icon name="refresh" /><div>正在探测 {progress}。一台最多半分钟，可以等它跑完再预览。</div></div>}
    <div id="rg-table">{rows && (rows.length ? <div className="field"><label>对照表{tested ? `(测过 ${tested}/${rows.length})` : ""}</label><div className="tw"><table><thead><tr><th className="num">ID</th><th>节点</th><th className="num">分数</th><th>旧名</th><th>新名</th><th>备注</th></tr></thead><tbody>{rows.map(row => <tr key={row.id}><td className="num mono">{row.id}</td><td className="mono">{row.name}</td><td className="num">{row.score === undefined ? <span className="sub">—</span> : <StatusBadge tone={scoreTone(row.score, row.verdict === "untested" ? "untested" : "ok")}>{row.score}</StatusBadge>}</td><td><FlagText>{row.display || row.name}</FlagText></td><td><b className="bold"><FlagText>{row.new || "—"}</FlagText></b></td><td className="sub">{row.note || ""}</td></tr>)}</tbody></table></div></div> : <p className="sub">没有节点。</p>)}</div>
    <div className="err" id="rg-err" role="alert">{error}</div></div>
    <div className="mf"><Button data-act="modal-cancel" disabled={busy === "apply"} onClick={app.closeModal}>取消</Button><Button id="rg-preview" disabled={!!busy} loading={busy === "preview"} onClick={() => void justPreview()}>用现有分数预览</Button><Button id="rg-probe" disabled={!!busy} loading={busy === "probe"} onClick={() => void probeAll()}><Icon name="flask" />{rows ? "重新探测" : "探测全部节点"}</Button>{!!changed && <Button variant="primary" id="rg-apply" disabled={!!busy} loading={busy === "apply"} onClick={() => void apply()}>写入这 {changed} 个改名</Button>}</div></>;
}
