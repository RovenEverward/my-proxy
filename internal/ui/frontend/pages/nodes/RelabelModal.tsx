import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { FlagText, Icon } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { errorMessage, type Metadata } from "../../domain/nodes";
interface Plan { id: number; name: string; metadata: Metadata; [key: string]: unknown }
interface Row { id: number; name: string; old: string; new: string; note: string }
export function RelabelModal({ onChanged }: { onChanged: () => Promise<void> }) {
  const app = useApp();
  const [result, setResult] = useState<{ plans: Plan[]; rows: Row[] } | null>(null);
  const [busy, setBusy] = useState<"plan" | "apply" | null>(null);
  const [error, setError] = useState("");
  useEffect(() => { app.setModalLocked(busy === "apply"); return () => app.setModalLocked(false); }, [busy, app.setModalLocked]);
  const plan = async () => {
    setBusy("plan"); setError("");
    try { const packet = await write<{ plans?: Plan[]; rows?: Row[] }>("POST", "/local/relabel/plan", { all: true }); setResult({ plans: packet.plans || [], rows: packet.rows || [] }); }
    catch (reason) { setError(errorMessage(reason)); } finally { setBusy(null); }
  };
  const apply = async () => {
    if (!result?.plans.length) return;
    setBusy("apply"); setError("");
    try {
      const packet = await write<{ results?: { skipped?: boolean }[] }>("POST", "/local/relabel/apply", { plans: result.plans });
      const results = packet.results || [], skipped = results.filter(value => value.skipped).length;
      app.toast(`已写入 ${results.length - skipped} 个节点${skipped ? `,${skipped} 个已被改过、跳过` : ""}`); app.closeOverlays(); await onChanged();
    } catch (reason) { setError(errorMessage(reason)); setBusy(null); }
  };
  return <><div className="mbody"><h2>批量识别显示名</h2><p>对还没有显示名的节点各做一次识别(控制面按节点 IP 问 ipwho.is,<b>节点 IP 会发给这个第三方</b>)，先出旧名和新名的对照表。已经有显示名的节点会跳过，用户写过的值不会被覆盖。</p>
    <div id="rl-table">{result && (result.rows.length ? <div className="field"><label>对照表</label><div className="tw"><table><thead><tr><th className="num">ID</th><th>内部名</th><th>旧名</th><th>新名</th><th>依据</th></tr></thead><tbody>{result.rows.map(row => <tr key={row.id}><td className="num mono">{row.id}</td><td className="mono">{row.name}</td><td><FlagText>{row.old || "—"}</FlagText></td><td><b className="bold"><FlagText>{row.new}</FlagText></b></td><td className="sub">{row.note}</td></tr>)}</tbody></table></div></div> : <p className="sub">没有要处理的节点。</p>)}</div><div className="err" id="rl-err" role="alert">{error}</div></div>
    <div className="mf"><Button data-act="modal-cancel" disabled={busy === "apply"} onClick={app.closeModal}>取消</Button><Button id="rl-plan" disabled={!!busy} loading={busy === "plan"} onClick={() => void plan()}><Icon name="globe" />生成对照表</Button>{!!result?.plans.length && <Button variant="primary" id="rl-apply" disabled={!!busy} loading={busy === "apply"} onClick={() => void apply()}>写入这 {result.plans.length} 个节点</Button>}</div></>;
}
