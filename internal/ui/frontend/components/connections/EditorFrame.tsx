import type { ReactNode } from "react";
import { Button } from "../ui/button";
import { DrawerBody, DrawerHeader, ErrorState, FlagText, LoadingTable } from "../primitives";
import { useApp } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { errorMessage, plainNodeName, type NodeView } from "../../domain/nodes";

export function DrawerReadState({ title, loading, error, retry }: { title: string; loading: boolean; error: Error | null; retry: () => void }) {
  const { closeOverlays } = useApp();
  return <><DrawerHeader title={<FlagText>{title}</FlagText>} onClose={closeOverlays} /><DrawerBody><div aria-busy={loading}>{error ? <ErrorState title={title} error={error} onRetry={retry} /> : <LoadingTable rows={4} columns={2} />}</div></DrawerBody></>;
}
export const restartNote = (node: NodeView) => `会重启${plainNodeName(node)} 上的代理，已连着的用户会断开几秒`;
export function ApplyFooter({ node, need, isNew, busy, id = "i-save", save }: { node: NodeView; need: boolean; isNew?: boolean; busy: boolean; id?: string; save: (apply: boolean) => void }) {
  const verb = isNew ? "创建" : "保存";
  return <div className="df df-apply"><div className="df-row"><Button type="button" id="x-later" hidden={!need} style={!need ? { display: "none" } : undefined} disabled={busy} onClick={() => save(false)}>{`只${verb}，稍后应用`}</Button><Button type="submit" variant="primary" id={id} loading={busy}>{need ? `${verb}并应用到节点` : verb}</Button></div><p className="df-note" id="x-note" hidden={!need}>{restartNote(node)}</p></div>;
}
export function useConnectionFinish(onChanged: () => Promise<void>) {
  const { toast, closeOverlays } = useApp();
  return async (node: NodeView, done: string, need: boolean, apply: boolean, tail = "随时可以应用") => {
    if (need && apply) {
      try { await write("POST", `/api/nodes/${node.id}/sync`); toast(`${done}，已应用到 ${plainNodeName(node)}`); }
      catch (error) { toast(`${done}，但应用到 ${plainNodeName(node)} 失败：${errorMessage(error)}`, true); }
    } else if (need) toast(`${done}；${plainNodeName(node)} 显示「有改动未应用」，${tail}`);
    else toast(done);
    closeOverlays();
    await onChanged();
  };
}
export function ErrorMessage({ id, children }: { id: string; children: ReactNode }) { return <div className="err" id={id} role="alert">{children}</div>; }
