import { useCallback, useEffect, useRef, useState } from "react";
import { nodeLabel } from "../../domain/nodes";
import { api } from "../../runtime/api";
import { useApp } from "../../runtime/app-context";
import type { Node } from "../../runtime/types";
import type { DoctorReport } from "./report";

const DOCTOR_TIMEOUT_MS = 10 * 60 * 1000;
interface DoctorSnapshot {
  report: DoctorReport | null;
  error: string;
  labels: Record<string, string>;
}
// Keep completed results when the route unmounts. Active requests belong to this visit.
let snapshot: DoctorSnapshot = { report: null, error: "", labels: {} };

export function useDoctor() {
  const { can } = useApp();
  const [data, setData] = useState(() => snapshot);
  const [running, setRunning] = useState(false);
  const active = useRef<{ controller: AbortController; timer: ReturnType<typeof setTimeout> } | null>(null);

  useEffect(() => () => {
    if (!active.current) return;
    const request = active.current;
    active.current = null;
    clearTimeout(request.timer);
    request.controller.abort();
  }, []);

  const run = useCallback(async () => {
    if (active.current) return;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), DOCTOR_TIMEOUT_MS);
    const request = { controller, timer };
    active.current = request;
    setRunning(true);
    snapshot = { ...snapshot, error: "" };
    setData(snapshot);
    try {
      const report = await api<DoctorReport>("/api/doctor", { method: "GET", signal: controller.signal });
      if (active.current !== request) return;
      snapshot = { ...snapshot, report, error: "" };
      let labels = snapshot.labels;
      if (!Object.keys(labels).length && can("GET", "/api/nodes")) {
        try {
          const nodes = await api<Node[] | null>("/api/nodes", { method: "GET", signal: controller.signal });
          labels = Object.fromEntries((nodes || []).map(node => [node.name, nodeLabel(node)]));
        } catch { /* Keep internal node names if labels are unavailable. */ }
      }
      if (active.current !== request) return;
      snapshot = { report, error: "", labels };
      setData(snapshot);
    } catch (error) {
      if (active.current !== request) return;
      snapshot = { ...snapshot, report: null, error: error instanceof Error ? error.message : String(error) };
      setData(snapshot);
    } finally {
      clearTimeout(timer);
      if (active.current === request) {
        active.current = null;
        setRunning(false);
      }
    }
  }, [can]);

  return { ...data, running, run };
}
