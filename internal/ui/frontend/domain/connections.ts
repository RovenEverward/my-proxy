import { api } from "../runtime/api";
import type { Host, Inbound, Read } from "../runtime/types";
import { nodeLabel, type Metadata, type NodeView } from "./nodes";

export interface ConnectionInbound extends Inbound {
  ws_path?: string; grpc_service?: string; xhttp_path?: string; xhttp_host?: string; xhttp_mode?: string;
  wg_address?: string; wg_mtu?: number; sni?: string; alpn?: string[]; fingerprint?: string;
  cert_file?: string; key_file?: string; reality_dest?: string; reality_pub?: string; short_ids?: string[];
  congestion_control?: string; flow?: string; mux?: boolean; fragment?: boolean;
}
export interface ConnectionHost extends Host { sni?: string; host?: string; path?: string; alpn?: string[] }
export interface ConnectionData {
  inbounds: ConnectionInbound[]; nodes: NodeView[]; hosts: Record<number, ConnectionHost[]>;
  total: number; all: ConnectionInbound[]; pending: number[];
}
export interface InboundModel extends ConnectionInbound {
  id: number; node_id: number; tag: string; protocol: string; listen: string; port: number; network: string; security: string;
}
export interface HostModel extends ConnectionHost { id: number; inbound_id: number; remark: string; address: string; port: number }
export const inbPath = (id: number) => `/api/inbounds/${id}`;
export const splitCSV = (value: string) => value.split(",").map(part => part.trim()).filter(Boolean);
export const PROTO_NAMES: Record<string, string> = { vless: "VLESS", vmess: "VMess", trojan: "Trojan", shadowsocks: "SS", wireguard: "WireGuard", hysteria2: "Hysteria2", tuic: "TUIC" };
export const NET_NAMES: Record<string, string> = { ws: "WS", grpc: "gRPC", xhttp: "XHTTP", splithttp: "SplitHTTP", httpupgrade: "HTTPUpgrade" };
export const PROTOCOLS = ["vless", "vmess", "trojan", "shadowsocks", "wireguard", "hysteria2", "tuic"];
export const NETWORKS = ["tcp", "ws", "grpc", "xhttp", "splithttp", "httpupgrade", "udp"];
export const SECURITIES = ["none", "tls", "reality"];
export const OPTION_NAMES: Record<string, string> = { ...PROTO_NAMES, shadowsocks: "Shadowsocks", tcp: "TCP", ws: "WebSocket", grpc: "gRPC", xhttp: "XHTTP", splithttp: "SplitHTTP", httpupgrade: "HTTPUpgrade", udp: "UDP", none: "无", tls: "TLS", reality: "REALITY" };
export const isQUIC = (protocol: string) => protocol === "hysteria2" || protocol === "tuic";
export const inbTag = (inbound: Inbound) => inbound.tag || `${inbound.protocol}-${inbound.port}`;
export const inbKind = (inbound: Inbound) => [PROTO_NAMES[inbound.protocol] || inbound.protocol, NET_NAMES[inbound.network || ""], inbound.security === "reality" ? "Reality" : inbound.security === "tls" ? "TLS" : ""].filter(Boolean).join(" ");
export const inbShortKind = (inbound: Inbound) => inbound.security === "reality" ? "Reality" : NET_NAMES[inbound.network || ""] || PROTO_NAMES[inbound.protocol] || inbound.protocol;
export const inbTitle = (inbound: Inbound, node?: { name?: string; display?: string }) => !inbound.tag || inbound.tag === `${inbound.protocol}-${inbound.port}` ? `${node ? nodeLabel(node) + " · " : ""}${inbKind(inbound)}:${inbound.port}` : inbTag(inbound);
export const nameOfHost = (inbound: Inbound, host: Host | null | undefined, node: NodeView) => host ? host.sub_name || host.remark || nodeLabel(node) : inbound.sub_name || nodeLabel(node);
export const addrOfHost = (inbound: Inbound, host: Host | null | undefined, node: NodeView) => host ? `${host.effective?.address || node.address}:${host.effective?.port || inbound.port}` : `${node.address}:${inbound.port}`;
export const customName = (host?: Host | null) => !!host && !host.auto_name && !!host.remark?.trim();
export const customAddr = (host?: Host | null) => !!host?.address?.trim();
const OPTIONAL_INBOUND_KEYS = ["ws_path", "grpc_service", "xhttp_path", "xhttp_host", "xhttp_mode", "wg_address", "wg_mtu", "sni", "alpn", "fingerprint", "cert_file", "key_file", "reality_dest", "reality_pub", "short_ids", "congestion_control", "flow", "mux", "fragment", "max_users"] as const;
export function asModelInbound(value: ConnectionInbound): InboundModel {
  const result: InboundModel = { id: value.id || 0, node_id: value.node_id, tag: value.tag || "", protocol: value.protocol, listen: value.listen || "", port: value.port || 0, network: value.network || "", security: value.security || "" };
  for (const key of OPTIONAL_INBOUND_KEYS) {
    const item = value[key];
    if (Array.isArray(item) ? item.length : item) Object.assign(result, { [key]: item });
  }
  return result;
}
export function asModelHost(value: ConnectionHost): HostModel {
  const result: HostModel = { id: value.id || 0, inbound_id: value.inbound_id, remark: value.remark || "", address: value.address || "", port: value.port || 0 };
  for (const key of ["sni", "host", "path", "alpn"] as const) {
    const item = value[key];
    if (Array.isArray(item) ? item.length : item) Object.assign(result, { [key]: item });
  }
  return result;
}
export function inbNeedsApply(before: ConnectionInbound, after: ConnectionInbound): boolean {
  const strip = (value: ConnectionInbound) => { const model = asModelInbound(value); delete model.max_users; delete model.fingerprint; return JSON.stringify(model); };
  return strip(before) !== strip(after);
}
export async function loadConnections(nodeIds: number[], read: Read = api): Promise<ConnectionData> {
  const [all, rawNodes] = await Promise.all([read<ConnectionInbound[]>("/api/inbounds"), read<NodeView[]>("/api/nodes")]);
  const wanted = new Set(nodeIds);
  const inbounds = wanted.size ? all.filter(inbound => wanted.has(inbound.node_id)) : all;
  const selected = wanted.size ? rawNodes.filter(node => wanted.has(node.id)) : rawNodes;
  const [hosts, metadata, pending] = await Promise.all([
    Promise.all(inbounds.map(inbound => read<ConnectionHost[]>(`${inbPath(inbound.id)}/hosts`))),
    Promise.all(selected.map(node => read<Metadata>(`/api/nodes/${node.id}/metadata`).catch(() => null))),
    read<{ node_id: number }[]>("/api/nodes/pending").catch(() => []),
  ]);
  return { inbounds, all, total: all.length, nodes: selected.map((node, index) => ({ ...node, region: metadata[index]?.region || "", city: metadata[index]?.city || "" })), hosts: Object.fromEntries(inbounds.map((inbound, index) => [inbound.id, hosts[index]])), pending: (pending || []).map(item => item.node_id) };
}
const PLACE_CODES = [["美国", "US"], ["日本", "JP"], ["香港", "HK"], ["新加坡", "SG"], ["台湾", "TW"], ["韩国", "KR"], ["英国", "GB"], ["德国", "DE"], ["法国", "FR"], ["荷兰", "NL"], ["加拿大", "CA"], ["澳大利亚", "AU"], ["俄罗斯", "RU"], ["印度", "IN"], ["东京", "JP"], ["大阪", "JP"], ["洛杉矶", "US"], ["圣何塞", "US"], ["硅谷", "US"], ["纽约", "US"], ["西雅图", "US"], ["芝加哥", "US"], ["达拉斯", "US"], ["伦敦", "GB"], ["法兰克福", "DE"], ["首尔", "KR"], ["台北", "TW"], ["阿姆斯特丹", "NL"]];
const REGION_ZH: Record<string, string> = Object.fromEntries(PLACE_CODES.slice(0, 14).map(([place, code]) => [code, place]));
export function nameMismatch(name: string, node: NodeView): string | null {
  const region = node.region?.trim().toUpperCase();
  if (!name.trim() || !region) return null;
  const flag = name.match(/[\u{1f1e6}-\u{1f1ff}]{2}/u)?.[0];
  let code = "", said = "";
  if (flag) {
    code = [...flag].map(character => String.fromCharCode(character.codePointAt(0)! - 0x1f1e6 + 65)).join("");
    said = REGION_ZH[code] || code;
  } else {
    const place = PLACE_CODES.filter(([word]) => name.includes(word)).sort(([a], [b]) => name.indexOf(a) - name.indexOf(b))[0];
    if (!place) return null;
    [said, code] = place;
  }
  return code === region ? null : `名称写的是${said}，节点在${node.city || REGION_ZH[region] || region}`;
}
