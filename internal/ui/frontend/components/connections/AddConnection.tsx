import { useState } from "react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { NativeSelect } from "../native-select";
import { DrawerBody, DrawerHeader, Field, FlagText, Icon } from "../primitives";
import { useApp } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { asModelInbound, inbTitle, type ConnectionData, type ConnectionInbound } from "../../domain/connections";
import { errorMessage, nodeLabel } from "../../domain/nodes";
import { ApplyFooter, ErrorMessage, useConnectionFinish } from "./EditorFrame";
import { ConnectionEditor } from "./ConnectionEditor";

export function AddConnection({ data, presetNode, onChanged }: { data: ConnectionData; presetNode: number; onChanged: () => Promise<void> }) {
  const app = useApp();
  const finish = useConnectionFinish(onChanged);
  const [draft, setDraft] = useState({ nodeId: presetNode || data.nodes[0]?.id, protocol: "reality", port: "", touched: false });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const node = data.nodes.find(value => value.id === draft.nodeId) || data.nodes[0];
  if (!node) return <><DrawerHeader title="添加连接" onClose={app.closeOverlays} /><DrawerBody>先添加一台节点</DrawerBody></>;
  const wanted = draft.protocol === "reality" ? 8443 : 2087;
  const used = new Set(data.inbounds.filter(inbound => inbound.node_id === node.id).map(inbound => inbound.port));
  let auto = wanted; while (used.has(auto)) auto++;
  const port = draft.touched ? draft.port : String(auto);
  const save = async (apply: boolean) => {
    if (busy) return;
    if (!/^\d+$/.test(port) || !(Number(port) >= 1 && Number(port) <= 65535)) { setError("端口必须在 1-65535 之间"); return; }
    setBusy(true); setError("");
    try {
      const reality = draft.protocol === "reality";
      const created = await write<ConnectionInbound>("POST", "/api/inbounds", asModelInbound({ id: 0, node_id: node.id, protocol: "vless", port: Number(port), network: reality ? "tcp" : "ws", security: reality ? "reality" : "none", flow: reality ? "xtls-rprx-vision" : "", ws_path: reality ? "" : "/ws", ...(reality ? { sni: "www.cloudflare.com", reality_dest: "www.cloudflare.com:443" } : {}) }));
      await finish(node, `已添加 ${inbTitle(created, node)}`, true, apply);
    } catch (reason) { setError(errorMessage(reason)); setBusy(false); }
  };
  return <form id="add-form" noValidate onSubmit={event => { event.preventDefault(); void save(true); }}><DrawerHeader title="添加连接" description="加好后会出现在订阅里" onClose={app.closeOverlays} /><DrawerBody>
    <Field label="节点" htmlFor="a-node"><NativeSelect className="in" id="a-node" value={node.id} onChange={event => setDraft({ ...draft, nodeId: Number(event.target.value), touched: false })}>{data.nodes.map(value => <option key={value.id} value={value.id}>{nodeLabel(value)}　{value.address}</option>)}</NativeSelect></Field>
    <Field label="协议"><div className="opts" role="radiogroup" aria-label="协议">{[["reality", "VLESS Reality", "不需要域名和证书，伪装成访问普通网站，最省心"], ["ws", "VLESS WS", "可以套 CDN 隐藏节点地址，需要一个域名"]].map(([key, title, hint]) => <button type="button" key={key} className={`opt pcard ${draft.protocol === key ? "on" : ""}`} data-act="conn-add-proto" data-k={key} role="radio" aria-checked={draft.protocol === key} onClick={() => setDraft({ ...draft, protocol: key, touched: false })}><span className="rd" /><div><b>{title}{key === "reality" && <span className="rec">推荐</span>}</b><span className="h">{hint}</span></div></button>)}</div></Field>
    <Field label="端口" htmlFor="a-port"><Input className="in mono" id="a-port" value={port} inputMode="numeric" onChange={event => setDraft({ ...draft, touched: true, port: event.target.value })} /><span className="h hi" id="a-port-h"><Icon name="info" /><span>{draft.touched ? "已手动修改" : auto !== wanted ? `这台节点上 ${wanted} 已经在用，自动换成 ${auto}` : "自动选的常用端口，可以改"}</span></span></Field>
    <div className="pv"><span className="pv-l">订阅里会显示成</span><div className="pv-r"><b id="pv-n"><FlagText>{`${nodeLabel(node)} · ${draft.protocol === "reality" ? "Reality" : "WS"}`}</FlagText></b><span className="mono" id="pv-a">{node.address}:{port}</span></div><span className="pv-h">名称跟着节点名走，节点改名这里一起变</span></div>
    <p className="sub"><Button type="button" variant="ghost" className="lnk" data-act="conn-other" data-node={node.id} onClick={() => app.openDrawer(<ConnectionEditor presetNode={node.id} full onChanged={onChanged} />, { key: `inbound:new:${node.id}` })}>其他协议…</Button></p><ErrorMessage id="add-err">{error}</ErrorMessage>
  </DrawerBody><ApplyFooter node={node} need isNew busy={busy} id="a-save" save={apply => void save(apply)} /></form>;
}
