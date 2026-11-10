import { useApp } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { userPath, type User } from "../../domain/users";
import { readModifyUser } from "./hooks";
import { openRenew } from "./renew";
export function useUserActions(user: User, onChanged?: () => Promise<void>, options: { afterDelete?: () => void } = {}) {
  const app = useApp(), refresh = onChanged || app.refresh;
  const reset = () => app.confirmBox({ title: `重置 ${user.username} 的本期流量？`, text: "本期用量清零，累计用量不变。超限的用户会在下一轮收敛时恢复。", ok: "重置流量", run: async () => { await write("POST", userPath(user.username) + "/reset-traffic"); await refresh(); app.toast(`已重置用户 ${user.username} 的流量`); } });
  const toggle = () => {
    const off = user.enabled, verb = off ? "停用" : "启用";
    const run = async () => { await readModifyUser(user.username, model => { model.enabled = !off; }); await refresh(); app.toast(`已${verb}用户 ${user.username}`); };
    if (off) app.confirmBox({ title: `停用 ${user.username}？`, text: "下一轮收敛(最多 30 秒)把他从所有节点上摘掉，订阅里只剩「已停用」占位节点。记录保留，随时可以再启用。", ok: "停用", run });
    else void run().catch(error => app.toast(error.message, true));
  };
  const revoke = () => app.confirmBox({ title: `给 ${user.username} 换一条新链接？`, text: "旧链接马上失效，他的客户端要重新扫码或粘贴新链接才能更新订阅。已经连着的不会断。", ok: "换一条新链接", run: async () => { await write("POST", userPath(user.username) + "/revoke-sub"); await refresh(); app.toast("已换成新链接，旧链接已失效"); } });
  const remove = () => app.confirmBox({ title: `删除 ${user.username}？`, text: "不可恢复：协议凭证与订阅 token 一起删掉，已经连上的客户端会断开，订阅地址立刻 404。只想让人连不上，用「停用」。", ok: "删除用户", danger: true, typed: user.username, run: async () => { await write("DELETE", userPath(user.username)); app.toast(`已删除用户 ${user.username}`); options.afterDelete?.(); if (!options.afterDelete) await refresh(); } });
  return { renew: () => openRenew(app, user, refresh), reset, toggle, revoke, remove };
}
