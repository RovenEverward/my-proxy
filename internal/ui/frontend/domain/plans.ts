import type { Inbound, Node, Plan } from "../runtime/types";
import type { User } from "./users";
export type { Inbound, Node, Plan } from "../runtime/types";
export interface PlanData { plans: Plan[]; nodes: Node[]; inbounds: Inbound[]; users: User[] }
export type PlanSelection = Record<number, "all" | number[]>;
export interface PlanDraft { name: string; note: string; all_nodes: boolean; selection: PlanSelection }
export const planNodeName = (node: Node | undefined) => node?.display || node?.name || "";
const PROTOCOLS: Record<string, string> = { vless: "VLESS", vmess: "VMess", trojan: "Trojan", shadowsocks: "SS", wireguard: "WireGuard", hysteria2: "Hysteria2", tuic: "TUIC" };
const NETWORKS: Record<string, string> = { ws: "WS", grpc: "gRPC", xhttp: "XHTTP", splithttp: "SplitHTTP", httpupgrade: "HTTPUpgrade" };
export function planInboundLabel(inbound: Pick<Inbound, "id"> & Partial<Inbound>) {
  if (!inbound.protocol) return `#${inbound.id}`;
  if (inbound.tag && inbound.tag !== `${inbound.protocol}-${inbound.port}`) return inbound.tag;
  const kind = [PROTOCOLS[inbound.protocol] || inbound.protocol, NETWORKS[inbound.network || ""], inbound.security === "reality" ? "Reality" : inbound.security === "tls" ? "TLS" : ""].filter(Boolean).join(" ");
  return `${kind}:${inbound.port}`;
}
export const sortedPlans = (plans: Plan[]) => plans.slice().sort((a, b) => Number(b.is_default) - Number(a.is_default) || a.id - b.id);
export const planCountText = (users: number, active: number) => `${users}（活跃 ${active}）`;
export function planEffectiveSet(plan: Plan, inbounds: Inbound[]): Set<number> | null {
  if (plan.all_nodes) return null;
  const set = new Set(plan.inbound_ids || []), nodes = new Set(plan.node_ids || []);
  for (const inbound of inbounds) if (nodes.has(inbound.node_id)) set.add(inbound.id);
  return set;
}
export function extrasOf(user: Pick<User, "inbound_ids">, plan: Plan, inbounds: Inbound[]) {
  const effective = planEffectiveSet(plan, inbounds);
  if (!effective) return [];
  return (user.inbound_ids || []).filter(id => !effective.has(id)).flatMap(id => { const inbound = inbounds.find(i => i.id === id); return inbound ? [inbound] : []; });
}
export function userEffectiveSet(user: Pick<User, "plan_id" | "inbound_ids">, plans: Plan[], inbounds: Inbound[]): Set<number> | null {
  const plan = plans.find(p => p.id === user.plan_id);
  if (!plan) return user.inbound_ids?.length ? new Set(user.inbound_ids) : null;
  const effective = planEffectiveSet(plan, inbounds);
  if (!effective) return null;
  for (const id of user.inbound_ids || []) effective.add(id);
  return effective;
}
export function activeOccupancy(inbounds: Inbound[], users: User[], plans: Plan[]) {
  const occupancy: Record<number, number> = Object.fromEntries(inbounds.map(i => [i.id, 0]));
  for (const user of users) {
    if (user.status !== "active") continue;
    const set = userEffectiveSet(user, plans, inbounds);
    for (const inbound of inbounds) if (!set || set.has(inbound.id)) occupancy[inbound.id]++;
  }
  return occupancy;
}
export function planNodeCount(plan: Plan | undefined, nodes: Node[], inbounds: Inbound[]) {
  if (!plan) return 0;
  const existing = new Set(nodes.map(n => n.id));
  if (plan.all_nodes) return existing.size;
  const ids = new Set((plan.node_ids || []).filter(id => existing.has(id)));
  for (const id of plan.inbound_ids || []) {
    const inbound = inbounds.find(i => i.id === id);
    if (inbound && existing.has(inbound.node_id)) ids.add(inbound.node_id);
  }
  return ids.size;
}
export function planSelection(plan: Plan | null, inbounds: Inbound[]): PlanSelection {
  const selection: PlanSelection = {};
  for (const id of plan?.node_ids || []) selection[id] = "all";
  for (const id of plan?.inbound_ids || []) {
    const inbound = inbounds.find(i => i.id === id);
    if (!inbound || selection[inbound.node_id] === "all") continue;
    const current = selection[inbound.node_id];
    selection[inbound.node_id] = [...(Array.isArray(current) ? current : []), id];
  }
  return selection;
}
export function planBody(plan: Plan | null, draft: PlanDraft): Plan {
  const selection = draft.all_nodes ? {} : draft.selection;
  const nodeIDs = Object.keys(selection).map(Number).filter(id => selection[id] === "all").sort((a, b) => a - b);
  const inboundIDs = Object.values(selection).flatMap(value => value === "all" ? [] : value).sort((a, b) => a - b);
  const body: Plan = plan ? { id: plan.id, name: draft.name.trim(), all_nodes: draft.all_nodes, is_default: plan.is_default, users: plan.users, active_users: plan.active_users, extra_auth: plan.extra_auth } : { id: 0, name: draft.name.trim(), all_nodes: draft.all_nodes, is_default: false, users: 0, active_users: 0, extra_auth: 0 };
  if (draft.note.trim()) body.note = draft.note.trim();
  if (nodeIDs.length) body.node_ids = nodeIDs;
  if (inboundIDs.length) body.inbound_ids = inboundIDs;
  return body;
}
