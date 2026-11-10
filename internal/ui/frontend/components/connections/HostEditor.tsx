import { useState } from "react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { DrawerBody, DrawerFooter, DrawerHeader, Field, FlagText } from "../primitives";
import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import { usePageData } from "../../runtime/query";
import { asModelHost, inbPath, inbTitle, splitCSV, type ConnectionHost, type ConnectionInbound } from "../../domain/connections";
import { errorMessage, type NodeView } from "../../domain/nodes";
import { DrawerReadState, ErrorMessage, useConnectionFinish } from "./EditorFrame";
import { useConnectionActions } from "./use-connection-actions";

interface Props { inbound: ConnectionInbound; node: NodeView; hostId?: number; onChanged: () => Promise<void> }
export function HostEditor(props: Props) {
  const query = usePageData(`drawer:host:${props.inbound.id}:${props.hostId || "new"}`, async read => {
    const hosts = await read<ConnectionHost[]>(`${inbPath(props.inbound.id)}/hosts`);
    const host = props.hostId ? hosts.find(value => value.id === props.hostId) : null;
    if (props.hostId && !host) throw new Error(`连接方式 ${props.inbound.id} 上没有 id 为 ${props.hostId} 的对外地址`);
    return { host: host || null };
  }, { poll: 0 });
  if (!query.data) return <DrawerReadState title={props.hostId ? "编辑对外地址" : "添加对外地址"} loading={query.loading} error={query.error} retry={() => void query.refresh()} />;
  return <HostForm {...props} host={query.data.host} />;
}
const FIELDS = [
  ["remark", "客户端看到的名字", "香港 · 直连", "留空用节点显示名;可用 {SERVER_IP} {NODE_NAME} {NODE_DISPLAY} {PROTOCOL} {TRANSPORT}"],
  ["address", "对外地址", "留空用节点地址", ""], ["port", "对外端口", "留空用连接方式端口", ""],
  ["sni", "SNI", "", ""], ["host", "Host 头", "ws / grpc", ""], ["path", "路径", "ws", ""], ["alpn", "ALPN", "逗号分隔", ""],
];
function HostForm({ inbound, node, hostId, host, onChanged }: Props & { host: ConnectionHost | null }) {
  const app = useApp();
  const finish = useConnectionFinish(onChanged);
  const { removeHost } = useConnectionActions(onChanged);
  const [draft, setDraft] = useState<Record<string, string>>(() => Object.fromEntries(FIELDS.map(([key]) => { const value = host?.[key]; return [key, Array.isArray(value) ? value.join(",") : value ? String(value) : ""]; })));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const name = host?.sub_name || host?.remark || "对外地址";
  const save = async () => {
    const port = draft.port.trim();
    if (port && !/^\d+$/.test(port)) { setError("对外端口要是整数"); return; }
    setBusy(true); setError("");
    try {
      const current = hostId ? (await api<ConnectionHost[]>(`${inbPath(inbound.id)}/hosts`)).find(value => value.id === hostId) : null;
      if (hostId && !current) throw new Error(`连接方式 ${inbound.id} 上没有 id 为 ${hostId} 的对外地址`);
      const body = asModelHost({ ...current, id: hostId || 0, inbound_id: inbound.id, remark: draft.remark.trim(), address: draft.address.trim(), port: Number(port) || 0, sni: draft.sni.trim(), host: draft.host.trim(), path: draft.path.trim(), alpn: splitCSV(draft.alpn) });
      await write(hostId ? "PUT" : "POST", hostId ? `/api/hosts/${hostId}` : `${inbPath(inbound.id)}/hosts`, body);
      await finish(node, hostId ? `已更新对外地址「${body.remark || name}」` : body.remark ? `已创建对外地址「${body.remark}」` : "已创建对外地址，名字跟随节点名", false, false);
    } catch (reason) { setError(errorMessage(reason)); setBusy(false); }
  };
  return <form id="host-form" noValidate onSubmit={event => { event.preventDefault(); void save(); }}>
    <DrawerHeader title={<FlagText>{hostId ? `编辑对外地址「${name}」` : `给「${inbTitle(inbound, node)}」加对外地址`}</FlagText>} onClose={app.closeOverlays} />
    <DrawerBody>{FIELDS.map(([key, label, placeholder, hint]) => <Field key={key} label={label} htmlFor={`h-${key}`} hint={hint || undefined}><Input className={`in ${key === "remark" ? "" : "mono"}`} id={`h-${key}`} name={key} value={draft[key]} placeholder={placeholder} autoComplete="off" inputMode={key === "port" ? "numeric" : undefined} onChange={event => setDraft({ ...draft, [key]: event.target.value })} /></Field>)}
      {host ? <p className="sub">现在发出去的是 <code>{host.effective?.address || node.address}:{host.effective?.port || inbound.port}</code>。改地址或端口，用户下次刷新订阅就跟着变;不用应用到节点。</p> : <p className="sub">对外地址只影响订阅里写什么，不用应用到节点。</p>}
      <p className="sub">可用第二条对外地址填 IPv6，订阅会出现两行；纯 V6 时监听地址勿留空，填 <code>::</code>。</p><ErrorMessage id="h-err">{error}</ErrorMessage>
      {host && app.can("DELETE", "/api/hosts/x") && <div className="card mt"><div className="row"><div><b className="bold">删除对外地址</b><div className="sub">只影响订阅;删掉最后一个对外地址，订阅回落到连接方式自己的地址。</div></div><span className="sp" /><Button size="sm" type="button" data-act="conn-host-rm" data-id={host.id} onClick={() => removeHost(host, node)}>删除</Button></div></div>}
    </DrawerBody><DrawerFooter><Button type="button" data-act="close" onClick={app.closeOverlays}>取消</Button><Button variant="primary" type="submit" id="h-save" loading={busy}>{hostId ? "保存" : "创建"}</Button></DrawerFooter>
  </form>;
}
