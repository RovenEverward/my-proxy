import { useCallback, useEffect, useRef, useState } from "react";
import { api, APIError, write } from "@/runtime/api";
import type { InstallStep, SetupDelta, SetupParams, SetupResult, SetupStatus } from "./model";

export interface InstallationState {
  phase: "idle" | "running" | "failed" | "done";
  steps: InstallStep[]; lines: string[]; elapsed: number; error: string; result: SetupResult | null;
}
const emptyState: InstallationState = { phase: "idle", steps: [], lines: [], elapsed: 0, error: "", result: null };
export function useInstallation() {
  const [state, setState] = useState<InstallationState>(emptyState);
  const [job, setJob] = useState<{ id: string; generation: number } | null>(null);
  const [starting, setStarting] = useState(false);
  const params = useRef<SetupParams | null>(null);
  const launching = useRef(false);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; params.current = null; }; }, []);
  const resume = useCallback((id: string) => {
    setState({ ...emptyState, phase: "running" });
    setJob(previous => ({ id, generation: (previous?.generation || 0) + 1 }));
  }, []);
  const start = useCallback(async (next?: SetupParams) => {
    if (launching.current) return;
    if (next) params.current = { ...next };
    if (!params.current) return;
    launching.current = true; setStarting(true);
    try {
      let id = "";
      try {
        const response = await write<{ id?: string }>("POST", "/local/setup/run", params.current);
        id = response?.id || "";
      } catch (error) {
        if (!(error instanceof APIError) || error.status !== 409) throw error;
        // The read API keeps the current job after a concurrent start.
        const status = await api<SetupStatus>("/local/setup");
        id = status.job?.id || "";
        if (!id) throw error;
      }
      if (!id) throw new Error("安装启动失败");
      if (mounted.current) resume(id);
    } finally {
      launching.current = false;
      if (mounted.current) setStarting(false);
    }
  }, [resume]);
  useEffect(() => {
    if (!job) return;
    let disposed = false, busy = false, ended = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    let since = 0, idle = 0, delay = 2000;
    let progress: InstallationState = { ...emptyState, phase: "running" };
    const controller = new AbortController();
    const clearTimer = () => { if (timer !== null) clearTimeout(timer); timer = null; };
    const schedule = (wait = delay) => {
      clearTimer();
      if (disposed || ended || document.hidden) return;
      timer = setTimeout(() => { timer = null; void poll(); }, wait);
    };
    const poll = async () => {
      clearTimer();
      if (disposed || ended || busy || document.hidden) return;
      busy = true;
      let response: SetupDelta;
      try {
        response = await api<SetupDelta>(`/local/setup/jobs/${encodeURIComponent(job.id)}?since=${since}`, { signal: controller.signal });
      } catch {
        busy = false;
        delay = Math.min(delay * 2, 10000);
        schedule();
        return;
      }
      busy = false;
      if (disposed) return;
      if (response.seq != null) since = response.seq;
      if (response.elapsed != null) progress = { ...progress, elapsed: response.elapsed };
      const changed = !!response.steps?.length || !!response.lines?.length;
      if (response.steps?.length) progress = { ...progress, steps: response.steps };
      if (response.lines?.length) progress = { ...progress, lines: [...progress.lines, ...response.lines].slice(-50) };
      if (changed) { idle = 0; delay = 2000; }
      else { idle++; if (idle >= 6) delay = 10000; else if (idle >= 3) delay = 5000; }
      if (response.state === "done") {
        ended = true;
        if (params.current) params.current.device_password = "";
        progress = { ...progress, phase: "done", result: response.result || progress.result };
      } else if (response.state === "failed") {
        ended = true;
        progress = { ...progress, phase: "failed", error: response.error || "" };
      }
      setState(progress);
      if (!ended) schedule(response.more ? 0 : delay);
    };
    const visibilityChanged = () => {
      if (document.hidden) clearTimer();
      else if (!ended && timer === null) void poll();
    };
    document.addEventListener("visibilitychange", visibilityChanged);
    void poll();
    return () => {
      disposed = true; clearTimer(); controller.abort();
      document.removeEventListener("visibilitychange", visibilityChanged);
    };
  }, [job]);
  return { state, starting, start, resume, canRetry: params.current !== null };
}
