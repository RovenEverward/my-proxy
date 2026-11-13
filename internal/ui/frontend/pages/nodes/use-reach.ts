import { useCallback, useEffect, useRef, useState } from "react";
import { api, write } from "../../runtime/api";
import type { ReachJob } from "../../domain/reach";

// useReachCheck 驱动一次触达检查:POST 起任务(对 /local/reach/check,由本机编排),
// 之后每 1.5s 拉一次进展,跑到 done / error 交给 onDone 刷新列表。
// 检查起在本进程,关页面不取消任务;同一时刻只跑一轮,重复点会拿到在跑的那个任务。
export function useReachCheck(onDone: (job: ReachJob) => void) {
  const [job, setJob] = useState<ReachJob | null>(null);
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
        const snap = await api<ReachJob>("/local/reach/check");
        if (snap.stage === "done" || snap.stage === "error") {
          stop();
          setJob(null); // 完成即归零:失败走 toast,在跑状态不留着挡下一轮
          done.current(snap);
          return;
        }
        setJob(snap.stage === "none" ? null : snap);
      } catch { /* 本地服务抖一下不算结束,下轮再试 */ }
    }, 1500);
  }, [stop]);

  // 进页面时若上一轮还在跑(别的窗口或关页前起的),接上继续等。
  useEffect(() => {
    let cancelled = false;
    void api<ReachJob>("/local/reach/check").then(snap => {
      if (cancelled) return;
      if (snap.stage === "cp" || snap.stage === "local" || snap.stage === "save") { setJob(snap); watch(); }
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [watch]);

  const start = useCallback(async (nodeIDs?: number[]) => {
    if (job && job.stage !== "done" && job.stage !== "error") return;
    try {
      const snap = await write<ReachJob>("POST", "/local/reach/check", nodeIDs?.length ? { node_ids: nodeIDs } : {});
      if (snap.stage === "done" || snap.stage === "error") { setJob(null); done.current(snap); }
      else { setJob(snap); watch(); }
    } catch (error) {
      done.current({ stage: "error", error: error instanceof Error ? error.message : String(error) });
    }
  }, [job, watch]);

  return { job, running: !!job && job.stage !== "done" && job.stage !== "error", start };
}
