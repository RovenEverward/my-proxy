import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";
import { api, readEpoch, requestEvents } from "./api";
import type { PageData, Read } from "./types";

type Snapshot<T> = Omit<PageData<T>, "refresh">;
interface Entry<T> {
  snapshot: Snapshot<T>;
  reads: Map<string, { path: string; value: unknown }>;
  listeners: Set<() => void>;
  loader?: (read: Read) => Promise<T>;
  promise?: Promise<void>;
  epoch?: number;
  fresh?: boolean;
  background?: boolean;
  generation: number;
}
const entries = new Map<string, Entry<unknown>>();
const activityListeners = new Set<() => void>();
let activity = { loading: false, refreshing: false };
function updateActivity() {
  const active = [...entries.values()].filter(entry => entry.listeners.size);
  const loading = active.some(entry => entry.snapshot.loading);
  const refreshing = active.some(entry => entry.snapshot.refreshing);
  if (activity.loading === loading && activity.refreshing === refreshing) return;
  activity = { loading, refreshing };
  for (const listener of activityListeners) listener();
}
export function useQueryActivity() {
  return useSyncExternalStore(listener => { activityListeners.add(listener); return () => { activityListeners.delete(listener); }; }, () => activity);
}
const copy = <T,>(value: T): T => structuredClone(value);
const resource = (path: string) => new URL(path, location.origin).pathname.split("/")[2];
const dependencies: Record<string, string[]> = {
  users: ["users", "plans", "status", "sub-pulls"], plans: ["plans", "users"],
  nodes: ["nodes", "inbounds", "hosts", "proxy-groups", "plans"],
  inbounds: ["inbounds", "hosts", "nodes", "plans", "users", "proxy-groups"],
  hosts: ["hosts", "inbounds", "nodes", "plans", "users", "proxy-groups"],
  settings: ["settings", "users"], sources: ["sources", "users", "proxy-groups", "routing"],
  rules: ["rules"], "proxy-groups": ["proxy-groups", "routing", "users"],
  devices: ["devices"], "device-password": ["devices"],
};
function affected(path: string, target: string) {
  if (resource(path) === "settings") {
    const sourcePath = new URL(path, location.origin).pathname;
    const targetPath = new URL(target, location.origin).pathname;
    return sourcePath.startsWith("/api/settings/templates/")
      ? targetPath === "/api/settings" || targetPath === sourcePath
      : targetPath.startsWith("/api/settings") || /^\/api\/users\/[^/]+\/links$/.test(targetPath);
  }
  const resources = dependencies[resource(path)];
  return !resources || resources.includes(resource(target));
}
requestEvents.addEventListener("invalidate", event => {
  const path = (event as CustomEvent<string>).detail;
  for (const entry of entries.values()) for (const [key, read] of entry.reads) if (affected(path, read.path)) entry.reads.delete(key);
});
requestEvents.addEventListener("written", event => {
  const detail = (event as CustomEvent<string | string[]>).detail;
  const paths = Array.isArray(detail) ? detail : [detail];
  for (const entry of entries.values()) {
    if (entry.listeners.size && entry.loader) void refreshEntry(entry, false, true);
    else if ([...entry.reads.values()].some(read => paths.some(path => affected(path, read.path)))) entry.snapshot = { ...entry.snapshot, at: null };
  }
});
function entryFor<T>(key: string, seed?: T | null): Entry<T> {
  let entry = entries.get(key) as Entry<T> | undefined;
  if (!entry) {
    entry = { snapshot: { data: seed ? copy(seed) : null, error: null, loading: true, refreshing: false, at: null }, reads: new Map(), listeners: new Set(), generation: 0 };
    entries.set(key, entry as Entry<unknown>);
  } else { entries.delete(key); entries.set(key, entry as Entry<unknown>); }
  // Successful inactive entries are bounded for the current session.
  if (entries.size > 16) for (const [oldKey, old] of entries) if (oldKey !== key && !old.listeners.size) { entries.delete(oldKey); break; }
  return entry;
}
function publish<T>(entry: Entry<T>, snapshot: Snapshot<T>) {
  entry.snapshot = snapshot;
  for (const listener of entry.listeners) listener();
  updateActivity();
}
async function refreshEntry<T>(entry: Entry<T>, fresh: boolean, background = false): Promise<void> {
  if (!entry.loader || !entry.listeners.size) return;
  if (entry.promise) {
    if (entry.epoch !== readEpoch || fresh && !entry.fresh || !background && entry.background) return entry.promise.then(() => refreshEntry(entry, fresh, background));
    return entry.promise;
  }
  const generation = ++entry.generation;
  const epoch = readEpoch;
  entry.epoch = epoch; entry.fresh = fresh; entry.background = background;
  const previous = entry.reads;
  const collected = new Map<string, { path: string; value: unknown }>();
  const requests = new Map<string, Promise<unknown>>();
  const read: Read = <V,>(path: string, init: RequestInit & { asText?: boolean } = {}): Promise<V> => {
    const signature = JSON.stringify([path, init.method || "GET", init.body || "", !!init.asText]);
    const cached = !fresh && previous.get(signature);
    if (cached) { collected.set(signature, cached); return Promise.resolve(copy(cached.value) as V); }
    if (!requests.has(signature)) requests.set(signature, api<V>(path, init).then(value => { collected.set(signature, { path, value: copy(value) }); return value; }));
    return requests.get(signature)!.then(value => copy(value) as V);
  };
  publish(entry, { ...entry.snapshot, loading: !entry.snapshot.data, refreshing: true });
  const pending = entry.loader(read).then(data => {
    if (entry.generation !== generation || readEpoch !== epoch) return;
    entry.reads = collected;
    publish(entry, { data: copy(data), error: null, loading: false, refreshing: false, at: new Date() });
  }).catch((error: Error) => {
    if (entry.generation !== generation || readEpoch !== epoch) return;
    if (!entry.listeners.size) { publish(entry, { ...entry.snapshot, loading: false, refreshing: false }); return; }
    if (background && entry.snapshot.data) {
      publish(entry, { ...entry.snapshot, loading: false, refreshing: false });
      requestEvents.dispatchEvent(new CustomEvent("background-error", { detail: error }));
    } else publish(entry, { ...entry.snapshot, error, loading: false, refreshing: false });
  }).finally(() => {
    if (entry.promise === pending) entry.promise = undefined;
    if (entry.generation === generation && epoch !== readEpoch && entry.listeners.size) void refreshEntry(entry, false, true);
  });
  entry.promise = pending;
  return pending;
}
export function usePageData<T>(key: string, loader: (read: Read) => Promise<T>, options: { poll?: number; seed?: T | null } = {}): PageData<T> {
  const entry = entryFor<T>(key, options.seed);
  const loaderRef = useRef(loader);
  const firstEntry = useRef(true);
  loaderRef.current = loader;
  const subscribe = useCallback((listener: () => void) => { entry.listeners.add(listener); updateActivity(); return () => { entry.listeners.delete(listener); updateActivity(); }; }, [entry]);
  const snapshot = useSyncExternalStore(subscribe, () => entry.snapshot);
  useEffect(() => {
    entry.loader = read => loaderRef.current(read);
    const background = firstEntry.current && !!entry.snapshot.at;
    firstEntry.current = false;
    void refreshEntry(entry, true, background);
    const interval = options.poll === 0 ? undefined : setInterval(() => {
      if (!document.hidden && !document.querySelector("#drawer.open, #modal.open")) void refreshEntry(entry, true, true);
    }, options.poll || 30000);
    return () => { clearInterval(interval); };
  }, [entry, options.poll]);
  const refresh = useCallback((fresh = true) => refreshEntry(entry, fresh), [entry]);
  return { ...snapshot, refresh };
}
export function cachedPage<T>(key: string): T | null { const data = entries.get(key)?.snapshot.data; return data ? copy(data) as T : null; }
export function refreshPages(fresh = true) { return Promise.all([...entries.values()].filter(entry => entry.listeners.size).map(entry => refreshEntry(entry, fresh))).then(() => {}); }
