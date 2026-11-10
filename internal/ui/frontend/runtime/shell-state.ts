import { useSyncExternalStore } from "react";
import { requestEvents } from "./api";
import type { Node, Status, UserList } from "./types";

interface ShellState { counts: Record<string, number>; warning: boolean; online: boolean | null; at: Date | null; nodeNames: Record<string, string> }
let state: ShellState = { counts: {}, warning: false, online: null, at: null, nodeNames: {} };
const listeners = new Set<() => void>();
requestEvents.addEventListener("response", event => {
  const { path, value } = (event as CustomEvent<{ path: string; value: unknown }>).detail;
  if (!path.startsWith("/api/")) return;
  let next = { ...state, online: true, at: new Date() };
  if (path === "/api/status" && value) {
    const status = value as Status;
    next = { ...next, counts: { ...state.counts, ...status.counts }, warning: !!status.backoffs?.length };
  }
  if (/^\/api\/nodes(?:\?|$)/.test(path) && Array.isArray(value)) {
    const nodes = value as Node[];
    next = { ...next, counts: { ...next.counts, nodes: nodes.length }, nodeNames: Object.fromEntries(nodes.map(node => [node.name, node.display || node.name])) };
  }
  if (/^\/api\/users(?:\?|$)/.test(path) && value && !Array.isArray(value)) {
    const users = value as UserList;
    const query = new URLSearchParams(path.split("?")[1] || "");
    if (!["status", "search", "plan"].some(key => query.has(key)) && typeof users.total === "number") next = { ...next, counts: { ...next.counts, users: users.total } };
  }
  state = next;
  for (const listener of listeners) listener();
});
requestEvents.addEventListener("failure", event => {
  const { path, status } = (event as CustomEvent<{ path: string; status: number }>).detail;
  if (!path.startsWith("/api/") || status > 0 && status < 500 || state.online === false) return;
  state = { ...state, online: false };
  for (const listener of listeners) listener();
});
export function useShellState() { return useSyncExternalStore(listener => { listeners.add(listener); return () => { listeners.delete(listener); }; }, () => state); }
