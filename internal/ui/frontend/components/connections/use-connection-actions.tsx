import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import { asModelHost, inbPath, inbTag, inbTitle, type ConnectionHost, type ConnectionInbound } from "../../domain/connections";
import { type NodeView } from "../../domain/nodes";
import { restartNote, useConnectionFinish } from "./EditorFrame";

export function useConnectionActions(onChanged: () => Promise<void>) {
  const { confirmBox } = useApp();
  const finish = useConnectionFinish(onChanged);
  const removeInbound = (inbound: ConnectionInbound, node: NodeView) => {
    const title = inbTitle(inbound, node);
    const remove = async (apply: boolean) => { await write("DELETE", inbPath(inbound.id)); await finish(node, `已删除连接方式「${title}」`, true, apply, "应用后节点才停止监听"); };
    confirmBox({ title: `删除连接「${title}」？`, text: <>不可恢复：挂在它上面的对外地址一起删掉，用它的客户端下次刷新订阅后就没了这个对外地址。<br /><span className="sub">{restartNote(node)}</span></>, ok: "删除并应用到节点", danger: true, typed: inbTag(inbound), alt: { label: "只删除，稍后应用", run: () => remove(false) }, run: () => remove(true) });
  };
  const restoreName = async (inbound: ConnectionInbound, host: ConnectionHost, node: NodeView) => {
    const current = (await api<ConnectionHost[]>(`${inbPath(inbound.id)}/hosts`)).find(item => item.id === host.id);
    if (!current) throw new Error("对外地址已经不存在了");
    await write("PUT", `/api/hosts/${host.id}`, asModelHost({ ...current, remark: "" }));
    await finish(node, "已改回跟随节点名", false, false);
  };
  const removeHost = (host: ConnectionHost, node: NodeView) => {
    const name = host.sub_name || host.remark || "对外地址";
    confirmBox({ title: `删除对外地址「${name}」？`, text: "客户端下次刷新订阅后这个地址就没了;节点和已连着的用户不受影响。", ok: "删除对外地址", danger: true, run: async () => { await write("DELETE", `/api/hosts/${host.id}`); await finish(node, `已删除对外地址「${name}」`, false, false); } });
  };
  return { removeInbound, restoreName, removeHost };
}
