// Shared update availability polling state (GET /api/update every 30s).
import { useCallback, useEffect, useSyncExternalStore } from "react";
import { api } from "./api";
import { updLatest, updateAvailable, type UpdateInfo } from "@/domain/releases";

const UPD_SKIP_KEY = "mpx-upd-skip";
const POLL_MS = 30000;
let updateData: UpdateInfo | null = null;
let updateBusy = false;
let updateAt = 0;
let skipVersion = "";
const listeners = new Set<() => void>();
const emit = () => { for (const listener of listeners) listener(); };

try { skipVersion = sessionStorage.getItem(UPD_SKIP_KEY) || ""; } catch { /* Storage unavailable. */ }

export function publishUpdate(u: UpdateInfo) {
  updateAt = Date.now();
  updateData = u;
  emit();
}

export async function refreshUpdate(force = true): Promise<UpdateInfo | null> {
  if (updateBusy) return updateData;
  if (!force && Date.now() - updateAt < POLL_MS) return updateData;
  updateBusy = true;
  try {
    const u = await api<UpdateInfo>("/api/update");
    publishUpdate(u);
  } catch { /* Keep previous cached state on silent background failure. */ }
  finally { updateBusy = false; }
  return updateData;
}

export function skipUpdate(u: UpdateInfo | null) {
  const v = updLatest(u);
  if (!v) return;
  try { sessionStorage.setItem(UPD_SKIP_KEY, v); } catch { /* Ignore storage error. */ }
  skipVersion = v;
  emit();
}

export function useUpdateState(enabled = true): { data: UpdateInfo | null; refresh: () => Promise<UpdateInfo | null>; available: boolean } {
  const subscribe = useCallback((listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; }, []);
  const data = useSyncExternalStore(subscribe, () => updateData);
  const skipped = useSyncExternalStore(subscribe, () => skipVersion);
  useEffect(() => {
    if (!enabled) return;
    void refreshUpdate(false);
    const timer = setInterval(() => { if (!document.hidden) void refreshUpdate(true); }, POLL_MS);
    return () => clearInterval(timer);
  }, [enabled]);
  const refresh = useCallback(() => refreshUpdate(false), []);
  const latest = updLatest(data);
  return { data, refresh, available: updateAvailable(data) && !(latest && skipped === latest) };
}
