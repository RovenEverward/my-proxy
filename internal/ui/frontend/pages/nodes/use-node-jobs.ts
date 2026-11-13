import { useCallback, useEffect, useRef, useState } from "react";
import { useApp } from "../../runtime/app-context";
import { api } from "../../runtime/api";
import { nodeLabel, nodePath, type NodeView } from "../../domain/nodes";

export interface NodeJob {
  id: number; kind: "diagnose" | "install"; node_id: number; status: string; started_at: string; finished_at?: string;
  core?: string; version?: string; ui_hint?: string; error?: string; result?: { core?: string; version?: string };
}
interface Snapshot { active: NodeJob[]; last: NodeJob | null; loading: boolean }
export function useNodeJobs(node: NodeView | null, onChanged: () => Promise<void>) {
  const { toast } = useApp();
  const [snapshot, setSnapshot] = useState<Snapshot>({ active: [], last: null, loading: true });
  const running = useRef(new Map<number, NodeJob>());
  const acceptedVersion = useRef(0);
  const refreshRef = useRef(onChanged); refreshRef.current = onChanged;
  const toastRef = useRef(toast); toastRef.current = toast;
  const nodeRef = useRef(node); nodeRef.current = node;
  const reload = useRef<() => void>(() => {});
  useEffect(() => {
    if (!node?.id) return;
    const id = node.id;
    let live = true, pending = false, timer: ReturnType<typeof setTimeout> | undefined;
    running.current = new Map(); setSnapshot({ active: [], last: null, loading: true });
    const poll = async () => {
      if (!live || pending || document.hidden) return;
      pending = true; clearTimeout(timer);
      const version = acceptedVersion.current;
      try {
        const packet = await api<{ jobs: NodeJob[] }>(`${nodePath(id)}/jobs?active=1&limit=5`).catch(() => null);
        if (!live || version !== acceptedVersion.current) return;
        if (!Array.isArray(packet?.jobs)) { setSnapshot(previous => ({ ...previous, loading: false })); return; }
        const active = packet.jobs.filter(job => job.status === "running").map(job => ({ ...job, core: job.core || job.result?.core || "" }));
        const finished = [...running.current.values()].filter(job => !active.some(current => current.id === job.id));
        let last: NodeJob | null = null;
        if (!active.length && packet) {
          const recent = await api<{ jobs: NodeJob[] }>(`${nodePath(id)}/jobs?limit=1`).catch(() => null);
          const job = recent?.jobs?.[0];
          if (job?.status === "interrupted" && Date.now() - new Date(job.finished_at || job.started_at).getTime() < 86400000) last = job;
        }
        if (!live || version !== acceptedVersion.current) return;
        running.current = new Map(active.map(job => [job.id, job]));
        setSnapshot({ active, last, loading: false });
        for (const previous of finished) {
          const job = await api<NodeJob>(`/api/jobs/${previous.id}`).catch(() => previous);
          if (!live) return;
          if (job.status === "running") {
            running.current.set(job.id, job);
            setSnapshot(previous => ({ ...previous, active: [job, ...previous.active.filter(value => value.id !== job.id)] }));
            continue;
          }
          if (job.status === "done") toastRef.current(job.kind === "diagnose" ? "体检完成" : `已在 ${nodeLabel(nodeRef.current)} 上装好并启动 ${job.result?.core || job.core || previous.core || ""} ${job.result?.version || job.version || ""}`.trim());
          else if (job.status !== "interrupted") toastRef.current(`${job.kind === "diagnose" ? "体检" : "安装"}没做成${job.ui_hint || job.error ? `：${job.ui_hint || job.error}` : ""}`, true);
          await api(`${nodePath(id)}?fresh=1`).catch(() => null);
          if (live) await refreshRef.current();
        }
      } finally {
        pending = false;
        if (live && running.current.size && !document.hidden) timer = setTimeout(() => void poll(), 3000);
      }
    };
    reload.current = () => { if (!pending) void poll(); };
    const visible = () => { clearTimeout(timer); if (!document.hidden) void poll(); };
    document.addEventListener("visibilitychange", visible); void poll();
    return () => { live = false; clearTimeout(timer); document.removeEventListener("visibilitychange", visible); reload.current = () => {}; };
  }, [node?.id]);
  const accept = useCallback((job: NodeJob) => {
    acceptedVersion.current++;
    running.current.set(job.id, job);
    setSnapshot(previous => ({ active: [job, ...previous.active.filter(value => value.id !== job.id)], last: null, loading: false }));
    reload.current();
  }, []);
  return { ...snapshot, accept };
}
