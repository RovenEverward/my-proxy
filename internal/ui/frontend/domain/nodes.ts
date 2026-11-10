import { api } from "../runtime/api";
import type { Node, NodeMetadata, NodeStatus } from "../runtime/types";

export interface ExtendedNodeStatus extends NodeStatus {
  observed_hash?: string;
  certificates?: { not_after?: string; days_left: number; error?: string; name?: string; inbound_name?: string }[];
}
export interface NodeView extends Node {
  template_drift?: boolean;
  locate_note?: string;
  region?: string;
  city?: string;
  status?: ExtendedNodeStatus;
}
export interface Metadata extends NodeMetadata { [key: string]: unknown }
export interface NodeModel {
  id: number; name: string; address: string; ssh_port: number; ssh_user: string;
  api_port: number; config_path: string; service_name: string; outbound_ip?: string;
  deployed_hash?: string; deployed_at?: string; draining: boolean;
}
export const nodePath = (id: number) => `/api/nodes/${id}`;
export const nodeLabel = (node?: { display?: string; name?: string } | null): string => node?.display || node?.name || "";
export function nodeStateLabel(node: Pick<Node, "status" | "draining">): string {
  if (!node.status) return node.draining ? "已暂停分配" : "未体检";
  if (!node.status.reachable) return "连不上";
  if (node.draining) return "已暂停分配";
  return node.status.active ? "运行中" : "代理程序未运行";
}
export function nodeStateTone(node: Pick<Node, "status" | "draining">): string {
  if (!node.status) return node.draining ? "info" : "";
  if (!node.status.reachable) return "bad";
  if (node.draining) return "info";
  return node.status.active ? "ok" : "warn";
}
export function asModelNode(node: Node): NodeModel {
  return {
    id: node.id, name: node.name, address: node.address,
    ssh_port: node.ssh_port || 0, ssh_user: node.ssh_user || "",
    api_port: node.api_port || 0, config_path: node.config_path || "", service_name: node.service_name || "",
    ...(node.outbound_ip ? { outbound_ip: node.outbound_ip } : {}),
    ...(node.deployed_hash ? { deployed_hash: node.deployed_hash } : {}),
    ...(node.deployed_at ? { deployed_at: node.deployed_at } : {}), draining: !!node.draining,
  };
}
export function previewDisplay(metadata: NodeMetadata): string {
  const region = (metadata.region || "").trim().toUpperCase();
  if (!region) return "";
  const iso = /^[A-Z]{2}$/.test(region);
  const flag = iso ? String.fromCodePoint(...[...region].map(ch => 0x1f1e6 + ch.charCodeAt(0) - 65)) : "";
  let place = (metadata.city || "").trim();
  if (!place) {
    place = ({ HK: "香港", MO: "澳门" } as Record<string, string>)[region] || region;
    if (iso && !/^(HK|MO)$/.test(region)) {
      try { place = new Intl.DisplayNames(["zh-Hans"], { type: "region" }).of(region) || region; } catch { /* Keep the region code. */ }
    }
  }
  return [flag, place, metadata.letter || ""].filter(Boolean).join(" ");
}
export const metadataGroup = (metadata: NodeMetadata) => `${(metadata.region || "").toUpperCase()}\0${metadata.city || ""}`;
export const sshText = (node: Node) => `${node.ssh_user || "root"}@${node.address || "?"}:${node.ssh_port || 22}`;
export function coreText(status?: NodeStatus): string {
  if (!status) return "还没检查过";
  if (status.cores?.length) return status.cores.map(core => `${core.core} ${core.active ? "运行中" : "未运行"}${core.version ? " · " + core.version : ""}`).join(" / ");
  return status.version ? `xray ${status.active ? "运行中" : "未运行"}` : "没装代理程序";
}
export function driftState(node: NodeView): { tone: string; text: string } {
  if (!node.deployed_hash) return { tone: "", text: "还没应用过" };
  if (!node.status?.observed_hash) return { tone: "", text: "未知" };
  if (node.template_drift) return { tone: "warn", text: "需要应用" };
  return node.status.observed_hash === node.deployed_hash ? { tone: "ok", text: "已是最新" } : { tone: "warn", text: "有改动未应用" };
}
export const plainNodeName = (node: { name?: string; display?: string }) => nodeLabel(node).replace(/[\u{1f1e6}-\u{1f1ff}]{2}\s*/gu, "").trim() || node.name || "节点";
export async function autoNodeName(): Promise<string> {
  let names = new Set<string>();
  try { names = new Set((await api<Node[]>("/api/nodes")).map(node => node.name)); } catch { /* Let the control plane report a name conflict. */ }
  for (let index = 1; ; index++) if (!names.has(`node-${index}`)) return `node-${index}`;
}
export const errorMessage = (error: unknown) => error instanceof Error ? error.message : String(error);
export const dateTime = (value?: string) => value ? new Date(value).toLocaleString("sv-SE", { hour12: false }).slice(0, 16) : "";
export const hourMinute = (value: string) => new Date(value).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false });
export function sinceText(value?: string): string {
  if (!value) return "";
  const seconds = Math.max(0, (Date.now() - new Date(value).getTime()) / 1000);
  return seconds < 60 ? "刚刚" : seconds < 3600 ? `${Math.floor(seconds / 60)} 分钟前` : seconds < 86400 ? `${Math.floor(seconds / 3600)} 小时前` : `${Math.floor(seconds / 86400)} 天前`;
}
