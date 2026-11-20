import { useCallback, useEffect, useRef, useState } from "react";
import { api, write } from "../../runtime/api";
import type { Bandwidth } from "../../domain/metrics";

export interface BandwidthJob {
  id?: string;
  subject?: string;
  stage: string; // measure / save / done / error / none
  started_at?: string;
  result?: { status: string; bytes_per_sec?: number; interface?: string; error?: string };
  error?: string;
}

// useBandwidth 驱动一次手动带宽测量(UC-20):POST 起本地 job
// (/local/bandwidth/measure),1.5s 轮询到 done / error 后回调刷新。
// 同一时刻一个对象一个任务;同一对象重复点返回在跑的那个。
export function useBandwidth(onDone: (job: BandwidthJob) => void) {
  const [job, setJob] = useState<BandwidthJob | null>(null);
  const poll = useRef<ReturnType<typeof setInterval> | null>(null);
  const done = useRef(onDone);
  done.current = onDone;

  const stop = useCallback(() => {
    if (poll.current) { clearInterval(poll.current); poll.current = null; }
  }, []);
  useEffect(() => stop, [stop]);

  const watch = useCallback(() => {
    stop();
    poll.current = setInterval(async () => {
      try {
        const snap = await api<BandwidthJob>("/local/bandwidth/measure");
        if (snap.stage === "done" || snap.stage === "error") {
          stop();
          setJob(null);
          done.current(snap);
          return;
        }
        setJob(snap.stage === "none" ? null : snap);
      } catch { /* 本地服务抖一下不算结束,下轮再试 */ }
    }, 1500);
  }, [stop]);

  const start = useCallback(async (subject: string) => {
    if (job && job.subject === subject && job.stage !== "done" && job.stage !== "error") return;
    try {
      const snap = await write<BandwidthJob>("POST", "/local/bandwidth/measure", { subject });
      if (snap.stage === "done" || snap.stage === "error") { setJob(null); done.current(snap); }
      else { setJob(snap); watch(); }
    } catch (error) {
      done.current({ stage: "error", subject, error: error instanceof Error ? error.message : String(error) });
    }
  }, [job, watch]);

  return { job, measuring: job?.subject && job.stage !== "done" && job.stage !== "error" ? job.subject : null, start };
}
