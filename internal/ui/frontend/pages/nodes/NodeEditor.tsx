import { useState } from "react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { NativeSelect } from "../../components/native-select";
import { Label } from "../../components/ui/label";
import { DrawerBody, DrawerFooter, DrawerHeader, Field, FlagText } from "../../components/primitives";
import { DrawerReadState, ErrorMessage } from "../../components/connections/EditorFrame";
import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import { usePageData } from "../../runtime/query";
import { asModelNode, autoNodeName, errorMessage, metadataGroup, nodeLabel, nodePath, previewDisplay, type Metadata, type NodeView } from "../../domain/nodes";

interface Props { nodeId?: number; title?: string; onChanged: () => Promise<void> }
export function NodeEditor(props: Props) {
  if (!props.nodeId) return <NodeForm {...props} node={null} metadata={{}} />;
  return <ExistingNodeEditor {...props} nodeId={props.nodeId} />;
}
function ExistingNodeEditor(props: Props & { nodeId: number }) {
  const query = usePageData(`drawer:node:${props.nodeId}`, async read => {
    const [node, metadata] = await Promise.all([read<NodeView>(nodePath(props.nodeId)), read<Metadata>(`${nodePath(props.nodeId)}/metadata`)]);
    return { node, metadata };
  }, { poll: 0 });
  if (!query.data) return <DrawerReadState title={`编辑 ${props.title || "节点"}`} loading={query.loading} error={query.error} retry={() => void query.refresh()} />;
  return <NodeForm {...props} node={query.data.node} metadata={query.data.metadata} />;
}
const NODE_FIELDS = ["name", "address", "ssh_port", "ssh_user", "api_port", "config_path", "service_name", "outbound_ip"] as const;
function NodeForm({ nodeId, node, metadata, onChanged }: Props & { node: NodeView | null; metadata: Metadata }) {
  const app = useApp();
  const [draft, setDraft] = useState<Record<string, string>>(() => ({ ...Object.fromEntries(NODE_FIELDS.map(key => [key, key === "outbound_ip" && node?.outbound_ip === "v4v6" ? "" : node?.[key] ? String(node[key]) : ""])), region: metadata.region || "", city: metadata.city || "", letter: metadata.letter || "" }));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [locating, setLocating] = useState(false);
  const [locateMessage, setLocateMessage] = useState("按节点 IP 问 ipwho.is 给建议，只填进表单，保存才写入;节点 IP 会发给这个第三方。");
  const isNew = !node;
  const currentMetadata = { region: draft.region.trim().toUpperCase(), city: draft.city.trim(), letter: draft.letter.trim().toUpperCase() };
  const preview = { ...currentMetadata };
  if (preview.letter === (metadata.letter || "") && metadataGroup(preview) !== metadataGroup(metadata)) preview.letter = "";
  const input = (key: string, id: string, label: string, placeholder = "", hint?: string, numeric = false) => <Field label={label} htmlFor={id} hint={hint}><Input className="in mono" id={id} name={key} value={draft[key]} placeholder={placeholder} inputMode={numeric ? "numeric" : undefined} autoComplete="off" onChange={event => setDraft({ ...draft, [key]: event.target.value })} /></Field>;
  const locate = async () => {
    setLocating(true);
    try {
      const result = await write<{ region: string; city?: string; city_en?: string; display: string; source: string; via: string; ip: string }>("POST", `${nodePath(nodeId!)}/locate`);
      setDraft(previous => ({ ...previous, region: result.region, city: result.city || "" }));
      setLocateMessage(`建议：${result.display}(${result.source}，依据${result.via === "node" ? "节点出口" : "节点"} IP ${result.ip})${!result.city && result.city_en ? `;${result.city_en} 不在对照表里，可手填城市` : ""}`);
    } catch (reason) { setError(errorMessage(reason)); } finally { setLocating(false); }
  };
  const save = async () => {
    if (busy) return;
    const errors: string[] = [];
    for (const [key, label] of [["ssh_port", "SSH 端口"], ["api_port", "API 端口"]]) if (draft[key].trim() && !/^\d+$/.test(draft[key].trim())) errors.push(`${label} 要是整数`);
    if (isNew && !draft.address.trim()) errors.push("必须给对外地址");
    if (!isNew && !draft.name.trim()) errors.push("内部名不能为空");
    if (!isNew && currentMetadata.letter && !/^[A-Z]{1,2}$/.test(currentMetadata.letter)) errors.push("字母要是 1–2 位");
    setError(errors.join("\n")); if (errors.length) return;
    setBusy(true);
    try {
      const fields = { name: draft.name.trim(), address: draft.address.trim(), ssh_port: Number(draft.ssh_port.trim()) || 0, ssh_user: draft.ssh_user.trim(), api_port: Number(draft.api_port.trim()) || 0, config_path: draft.config_path.trim(), service_name: draft.service_name.trim(), outbound_ip: draft.outbound_ip.trim() };
      if (isNew) {
        fields.name ||= await autoNodeName();
        const body = { id: 0, ...fields, draining: false };
        if (!body.outbound_ip) delete (body as { outbound_ip?: string }).outbound_ip;
        const created = await write<NodeView>("POST", "/api/nodes", body);
        app.toast(`已添加节点 ${created.display || created.address}`); app.closeOverlays(); app.navigate(`nodes/${created.id}`);
      } else {
        const metaChanged = currentMetadata.region !== (metadata.region || "") || currentMetadata.city !== (metadata.city || "") || currentMetadata.letter !== (metadata.letter || "");
        const nodeChanged = NODE_FIELDS.some(key => fields[key] !== (node[key] || (key.endsWith("_port") ? 0 : "")));
        if (metaChanged) {
          const fresh = await api<Metadata>(`${nodePath(node.id)}/metadata`);
          const body: Metadata = { ...fresh, city: fresh.city || "", letter: fresh.letter || "" };
          if (currentMetadata.region !== (metadata.region || "")) body.region = currentMetadata.region;
          if (currentMetadata.city !== (metadata.city || "")) body.city = currentMetadata.city;
          if (currentMetadata.letter !== (metadata.letter || "")) body.letter = currentMetadata.letter;
          else if (metadataGroup(body) !== metadataGroup(fresh)) body.letter = "";
          const saved = await write<Metadata>("PUT", `${nodePath(node.id)}/metadata`, body);
          if (!nodeChanged) { app.toast(`显示名已改为 ${previewDisplay(saved) || "(没有地区，不显示)"}`); app.closeOverlays(); await onChanged(); return; }
        }
        const fresh = asModelNode(await api<NodeView>(nodePath(node.id)));
        const body: ReturnType<typeof asModelNode> = { ...fresh, ...fields }; if (!body.outbound_ip) delete body.outbound_ip;
        await write("PUT", nodePath(node.id), body);
        app.toast(`已更新节点 ${nodeLabel(node)}`); app.closeOverlays(); await onChanged();
      }
    } catch (reason) { setError(errorMessage(reason)); setBusy(false); }
  };
  return <form id="node-form" noValidate onSubmit={event => { event.preventDefault(); void save(); }}><DrawerHeader title={<FlagText>{isNew ? "添加节点" : `编辑 ${nodeLabel(node)}`}</FlagText>} onClose={app.closeOverlays} /><DrawerBody>
    {input("address", "n-address", "对外地址", "203.0.113.10", `连接方式没填对外地址时，订阅里用它${isNew ? ";显示名按它自动识别" : ""}`)}
    <div className="two">{input("ssh_port", "n-ssh-port", "SSH 端口", "22", undefined, true)}{input("ssh_user", "n-ssh-user", "SSH 用户", "root")}</div>
    {!isNew && <div className="field"><Label>显示名<span className="sub" id="n-display">{preview.region && <> · <FlagText>{previewDisplay(preview)}</FlagText>{!preview.letter && "(字母保存时自动分配)"}</>}</span></Label><div className="three">{[["region", "地区", "地区 US"], ["city", "城市", "城市，留空用国家名"], ["letter", "字母", "字母，留空自动"]].map(([key, label, placeholder]) => <Input key={key} className={`in ${key !== "city" ? "mono" : ""}`} id={`n-${key}`} name={key} value={draft[key]} placeholder={placeholder} aria-label={label} autoComplete="off" onChange={event => setDraft({ ...draft, [key]: event.target.value })} />)}</div><div className="flex8 mt"><Button type="button" size="sm" id="n-locate" loading={locating} onClick={() => void locate()}>重新识别</Button><span className="h" id="n-locate-msg"><FlagText>{locateMessage}</FlagText></span></div></div>}
    <details className="adv"><summary>高级</summary>{input("name", "n-name", "内部名", isNew ? "留空自动生成" : "", "只在命令行和数据里用;界面和订阅里显示的是显示名")}<div className="two">{input("api_port", "n-api-port", "xray API 端口", "默认", undefined, true)}{input("service_name", "n-service", "服务名", "mpx-xray")}</div>{input("config_path", "n-config", "配置路径", "/etc/mpx/xray/config.json")}
      <Field label="出口 IP" htmlFor="n-outbound-ip" hint="节点直连出口的 IP 版本;改完要「应用到节点」才生效"><NativeSelect className="in" id="n-outbound-ip" name="outbound_ip" value={draft.outbound_ip === "v4v6" ? "" : draft.outbound_ip} onChange={event => setDraft({ ...draft, outbound_ip: event.target.value })}><option value="">默认(先 IPv4)</option><option value="v4">仅 IPv4</option><option value="v6">仅 IPv6</option><option value="asis">不指定(AsIs)</option></NativeSelect></Field></details>
    <p className="sub">{isNew ? "这里只登记已能免密 SSH 的机器。要用 SSH 密码添加，请用「添加节点」向导。" : "改地址会影响订阅里没单独填对外地址的连接方式;改 SSH 参数只影响控制面以后怎么连它。都不会自动应用到节点。"}</p><ErrorMessage id="n-err">{error}</ErrorMessage>
  </DrawerBody><DrawerFooter><Button type="button" data-act="close" onClick={app.closeOverlays}>取消</Button><Button type="submit" variant="primary" id="n-save" loading={busy}>{isNew ? "创建" : "保存"}</Button></DrawerFooter></form>;
}
