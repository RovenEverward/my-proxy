import { Field } from "../primitives";
import { Input } from "../ui/input";
import { NativeSelect } from "../native-select";
import { NETWORKS, OPTION_NAMES, PROTOCOLS, SECURITIES, isQUIC, splitCSV, type ConnectionInbound } from "../../domain/connections";
import { nodeLabel, type NodeView } from "../../domain/nodes";

type FieldDefinition = { key: string; label: string; placeholder?: string; kind?: "integer" | "list" | "boolean"; show?: (draft: InboundDraft) => boolean };
export type InboundDraft = Record<string, string | boolean>;
export const INBOUND_FIELDS: FieldDefinition[] = [
  { key: "tag", label: "标签", placeholder: "留空按「协议-端口」生成" }, { key: "listen", label: "监听地址", placeholder: "留空 0.0.0.0" },
  { key: "ws_path", label: "WS 路径", placeholder: "/ws", show: draft => draft.network === "ws" },
  { key: "grpc_service", label: "gRPC serviceName", show: draft => draft.network === "grpc" },
  { key: "xhttp_path", label: "xhttp 路径", show: draft => ["xhttp", "splithttp", "httpupgrade"].includes(String(draft.network)) },
  { key: "xhttp_host", label: "xhttp Host", show: draft => ["xhttp", "splithttp", "httpupgrade"].includes(String(draft.network)) },
  { key: "xhttp_mode", label: "xhttp mode", placeholder: "auto / packet-up / stream-up / stream-one", show: draft => draft.network === "xhttp" },
  { key: "sni", label: "SNI", placeholder: "www.cloudflare.com", show: draft => draft.security !== "none" },
  { key: "reality_dest", label: "REALITY 回落目标", placeholder: "www.cloudflare.com:443", show: draft => draft.security === "reality" },
  { key: "short_ids", label: "Short ID", placeholder: "逗号分隔", kind: "list", show: draft => draft.security === "reality" },
  { key: "alpn", label: "ALPN", placeholder: "h2,http/1.1", kind: "list", show: draft => draft.security === "tls" },
  { key: "cert_file", label: "证书路径(节点上)", placeholder: "/etc/ssl/fullchain.pem", show: draft => draft.security === "tls" },
  { key: "key_file", label: "私钥路径(节点上)", placeholder: "/etc/ssl/privkey.pem", show: draft => draft.security === "tls" },
  { key: "fingerprint", label: "uTLS 指纹", placeholder: "留空 chrome", show: draft => draft.security !== "none" },
  { key: "flow", label: "VLESS flow", placeholder: "xtls-rprx-vision", show: draft => draft.protocol === "vless" },
  { key: "wg_address", label: "wireguard 隧道地址", placeholder: "10.0.0.1/24", show: draft => draft.protocol === "wireguard" },
  { key: "wg_mtu", label: "wireguard MTU", placeholder: "1420", kind: "integer", show: draft => draft.protocol === "wireguard" },
  { key: "congestion_control", label: "TUIC 拥塞控制", placeholder: "bbr / cubic / new_reno", show: draft => draft.protocol === "tuic" },
  { key: "max_users", label: "用户数上限", placeholder: "0 不限", kind: "integer" },
  { key: "mux", label: "订阅里开 mux", kind: "boolean" }, { key: "fragment", label: "订阅里开分片(仅 xray)", kind: "boolean" },
];
export function inboundDraft(base: ConnectionInbound): InboundDraft {
  const draft: InboundDraft = { node_id: String(base.node_id), port: base.port ? String(base.port) : "", protocol: base.protocol, network: base.network || "tcp", security: base.security || "none" };
  for (const field of INBOUND_FIELDS) {
    const value = base[field.key];
    draft[field.key] = field.kind === "boolean" ? !!value : Array.isArray(value) ? value.join(",") : value ? String(value) : "";
  }
  return draft;
}
export function collectInbound(draft: InboundDraft, base: ConnectionInbound): { value: ConnectionInbound; errors: string[] } {
  const port = String(draft.port).trim(), errors: string[] = [];
  const value: ConnectionInbound = { id: base.id, node_id: Number(draft.node_id), protocol: String(draft.protocol), network: String(draft.network), security: String(draft.security), port: /^\d+$/.test(port) ? Number(port) : 0 };
  if (!(value.port >= 1 && value.port <= 65535)) errors.push("端口必须在 1-65535 之间");
  for (const field of INBOUND_FIELDS) {
    const text = String(draft[field.key] ?? "").trim();
    if (field.kind === "integer" && text && !/^\d+$/.test(text)) errors.push(`${field.label} 要是整数`);
    Object.assign(value, { [field.key]: field.kind === "boolean" ? !!draft[field.key] : field.kind === "integer" ? Number(text) || 0 : field.kind === "list" ? splitCSV(text) : text });
  }
  return { value, errors };
}
export function InboundFields({ draft, onChange, nodes, isNew }: { draft: InboundDraft; onChange: (draft: InboundDraft) => void; nodes: NodeView[]; isNew: boolean }) {
  const update = (key: string, value: string | boolean) => {
    const next = { ...draft, [key]: value };
    if (key === "protocol" && isQUIC(String(value)) && !isQUIC(String(draft.protocol))) { next.network = "udp"; next.security = "tls"; }
    onChange(next);
  };
  const select = (key: string, label: string, choices: string[]) => <Field label={label} htmlFor={`i-${key}`}><NativeSelect className="in" id={`i-${key}`} name={key} value={String(draft[key])} onChange={event => update(key, event.target.value)}>{choices.map(value => <option key={value} value={value}>{OPTION_NAMES[value] || value}</option>)}</NativeSelect></Field>;
  const renderField = (field: FieldDefinition) => <div key={field.key} data-f={field.key} hidden={field.show ? !field.show(draft) : false}>{field.kind === "boolean" ? <label className="chk"><input type="checkbox" name={field.key} checked={!!draft[field.key]} onChange={event => update(field.key, event.target.checked)} /> {field.label}</label> : <Field label={field.label} htmlFor={`i-${field.key}`}><Input className="in mono" id={`i-${field.key}`} name={field.key} value={String(draft[field.key])} placeholder={field.placeholder} autoComplete="off" inputMode={field.kind === "integer" ? "numeric" : undefined} onChange={event => update(field.key, event.target.value)} /></Field>}</div>;
  const advanced = new Set(["tag", "listen", "max_users", "mux", "fragment"]);
  return <>
    <div className="two"><Field label="节点" htmlFor="i-node"><NativeSelect className="in" id="i-node" name="node_id" disabled={!isNew} value={String(draft.node_id)} onChange={event => update("node_id", event.target.value)}>{nodes.map(node => <option key={node.id} value={node.id}>{nodeLabel(node)}</option>)}</NativeSelect></Field><Field label="端口" htmlFor="i-port"><Input className="in mono" id="i-port" name="port" value={String(draft.port)} placeholder="443" inputMode="numeric" onChange={event => update("port", event.target.value)} /></Field></div>
    {select("protocol", "协议", PROTOCOLS)}<div className="two">{select("network", "传输", NETWORKS)}{select("security", "安全层", SECURITIES)}</div>
    {INBOUND_FIELDS.filter(field => !advanced.has(field.key)).map(renderField)}
    <details className="adv"><summary>更多</summary>{INBOUND_FIELDS.filter(field => advanced.has(field.key) && field.kind !== "boolean").map(renderField)}<div className="stack8 mb">{INBOUND_FIELDS.filter(field => field.kind === "boolean").map(renderField)}</div></details>
  </>;
}
