// Two-stage upgrade dialog: upgrade node cores via incremental job events,
// then trigger control-plane upgrade and poll /api/admin/meta until back online.
import { useCallback, useEffect, useRef, useState } from "react";
import { api, write } from "@/runtime/api";
import { useApp } from "@/runtime/app-context";
import { publishUpdate, skipUpdate } from "@/runtime/update";
import { Button } from "@/components/ui/button";
import { Icon } from "@/components/primitives";
import { nodeLabel } from "@/domain/nodes";
import { isDevVer, nodeUpgradable, updTitle, updateAvailable, type UpdateInfo, type UpdateNode, type UpdatePart } from "@/domain/releases";

const NODE_LABELS: Record<string, string> = {};
async function ensureNodeLabels(): Promise<boolean> {
  if (Object.keys(NODE_LABELS).length) return false;
  try {
    const nodes = await api<{ name?: string; display?: string }[]>("/api/nodes");
    for (const n of nodes || []) if (n.name) NODE_LABELS[n.name] = nodeLabel(n);
    return true;
  } catch { return false; }
}
const labelOfName = (name: string) => NODE_LABELS[name] || name;

interface NodeEvent { seq: number; node: string; core: string; status: string; msg: string }
interface NodeProgress { job?: number; state?: string; seq?: number; events?: NodeEvent[] }
type RunPhase = "nodes" | "control" | "restart" | "done" | "failed";
interface RunState { phase: RunPhase; since: number; events: NodeEvent[]; error: string }
const RESTART_WAIT_MS = 120000;
let activeRun: RunState | null = null;
const restartWait = { active: false, timedOut: false };
const detachRun = () => { activeRun = null; };

const NODE_EVENT_TEXT: Record<string, string> = {
  start: "正在升级", ok: "完成", skip: "已是目标版本", rolled_back: "已回滚", failed: "失败",
};
function nodeEventText(e: NodeEvent): string {
  const label = `${labelOfName(e.node)} · ${e.core}`;
  const word = NODE_EVENT_TEXT[e.status] || e.status;
  return e.msg ? `${label}:${word} — ${e.msg}` : `${label}:${word}`;
}

export function UpdateDialog({ data, onClose }: { data: UpdateInfo; onClose: () => void }) {
  const app = useApp();
  const appRef = useRef(app);
  appRef.current = app;
  const [live, setLive] = useState<UpdateInfo>(data);
  const [checking, setChecking] = useState(false);
  const [applying, setApplying] = useState(false);
  const [run, setRun] = useState<RunState | null>(() => activeRun ? { ...activeRun, events: [...activeRun.events] } : null);
  const [restarting, setRestarting] = useState<"" | "wait" | "timeout">(() => (restartWait.active ? (restartWait.timedOut ? "timeout" : "wait") : ""));
  const [labelTick, setLabelTick] = useState(0);
  void labelTick;
  const timer = useRef(0);

  useEffect(() => {
    const unlock = () => appRef.current.setModalLocked(false);
    return () => { unlock(); clearTimeout(timer.current); detachRun(); };
  }, []);
  useEffect(() => {
    let alive = true;
    void ensureNodeLabels().then(got => { if (got && alive) setLabelTick(t => t + 1); });
    return () => { alive = false; };
  }, []);
  useEffect(() => { setLive(data); }, [data]);

  const push = (patch: Partial<RunState>) => {
    activeRun = { phase: "nodes", since: 0, events: [], error: "", ...activeRun, ...patch };
    setRun({ ...activeRun, events: [...activeRun.events] });
  };

  const waitRestart = useCallback(() => {
    restartWait.active = true;
    setRestarting("wait");
    const deadline = Date.now() + RESTART_WAIT_MS;
    const tick = async () => {
      try {
        await fetch("/api/admin/meta", { credentials: "same-origin", headers: { Accept: "application/json" } }).then(r => { if (!r.ok) throw new Error(); });
        location.reload();
        return;
      } catch { /* Still restarting. */ }
      if (Date.now() > deadline) { restartWait.timedOut = true; setRestarting("timeout"); return; }
      timer.current = window.setTimeout(tick, 1000);
    };
    void tick();
  }, []);

  const watchNodes = useCallback(() => {
    const tick = async () => {
      let snap: NodeProgress | null = null;
      try { snap = await api<NodeProgress>("/api/update/nodes?since=" + (activeRun?.since ?? 0)); } catch { /* Retry on next tick. */ }
      if (!activeRun || activeRun.phase !== "nodes") return;
      if (snap) {
        const events = [...activeRun.events, ...(snap.events || [])];
        push({ events, since: snap.seq ?? activeRun.since });
        if (snap.state === "done" || snap.state === "failed") {
          if (snap.state === "failed") { push({ phase: "failed", error: "节点升级任务失败，见上面事件" }); return; }
          push({ phase: "control" });
          try {
            await write("POST", "/api/update/control", { action: "upgrade" });
            push({ phase: "restart" });
            waitRestart();
          } catch (cause) {
            push({ phase: "failed", error: cause instanceof Error ? cause.message : String(cause) });
          }
          return;
        }
      }
      timer.current = window.setTimeout(tick, 1500);
    };
    void tick();
  }, [waitRestart]);

  const apply = useCallback(async () => {
    if (applying || run) return;
    setApplying(true);
    const nodes = (live.nodes || []).filter(nodeUpgradable);
    try {
      if (nodes.length) {
        push({ phase: "nodes" });
        await write("POST", "/api/update/nodes", {});
        appRef.current.setModalLocked(true);
        watchNodes();
      } else {
        push({ phase: "control" });
        await write("POST", "/api/update/control", { action: "upgrade" });
        appRef.current.setModalLocked(true);
        push({ phase: "restart" });
        waitRestart();
      }
    } catch (cause) {
      push({ phase: "failed", error: cause instanceof Error ? cause.message : String(cause) });
    } finally { setApplying(false); }
  }, [applying, run, live.nodes, watchNodes, waitRestart]);

  const checkNow = useCallback(async () => {
    setChecking(true);
    try {
      const u = await api<UpdateInfo>("/api/update?refresh=1");
      publishUpdate(u);
      setLive(u);
    } catch (cause) { appRef.current.toast(cause instanceof Error ? cause.message : "检查失败", true); }
    finally { setChecking(false); }
  }, []);

  const c = live.control || {}, nodes = Array.isArray(live.nodes) ? live.nodes : [];
  const row = (label: string, part: UpdatePart, kind: "control" | "cli" | "node") => {
    const cur = part.current || "—", latest = part.latest || "—", err = part.error;
    const state = err ? <span className="c-bad">{err}</span>
      : part.available && !isDevVer(cur) ? <b className="c-ok">{latest}</b>
      : <span className="sub">已是最新</span>;
    return <div className="upd-row" key={kind + label}><b>{label}</b><span className="mono">{cur}</span><Icon name="arrow" className="upd-arrow" />{state}</div>;
  };
  const nodeRow = (n: UpdateNode) => {
    const up = nodeUpgradable(n);
    return <div className="upd-row" key={n.id + ":" + n.core}><b>{labelOfName(n.name)}</b><span className="sub">{n.core}</span><span className="mono">{n.current || "—"}</span><Icon name="arrow" className="upd-arrow" />{up ? <b className="c-ok">{n.target}</b> : <span className="sub">已是最新</span>}</div>;
  };

  if (run) {
    return <><div className="mbody"><h2>正在更新</h2>
      <div className="upd-steps">
        {run.phase !== "failed" && <div className="upd-step">节点</div>}
        {run.events.map((e, i) => <div key={i} className={"upd-ev" + (e.status === "failed" ? " failed" : e.status === "ok" ? " ok" : "")}>{nodeEventText(e)}</div>)}
        {run.phase === "nodes" && !run.events.length && <div className="upd-ev">等待任务启动…</div>}
        {(run.phase === "control" || run.phase === "restart") && <><div className="upd-step">控制面</div><div className="upd-ev">已提交升级请求，控制面正在重启…</div></>}
      </div>
      {run.phase === "failed" && <div className="err" role="alert">{run.error}</div>}
      {restarting === "timeout" && <p className="sub">控制面还没起来。可以稍等再刷新，或到服务器上看 mpx 进程状态。</p>}
    </div>
      <div className="mf">{restarting === "timeout" ? <Button variant="primary" onClick={() => location.reload()}>刷新页面</Button> : <Button variant="ghost" onClick={() => { detachRun(); appRef.current.setModalLocked(false); setRun(null); onClose(); }}>关闭</Button>}</div></>;
  }

  const anyUpgradable = updateAvailable(live);
  return <><div className="mbody"><h2>{anyUpgradable ? updTitle(live) : "检查更新"}</h2>
    {row("控制面", c, "control")}
    {nodes.map(nodeRow)}
    {!nodes.length && <p className="sub">还没有节点更新信息。</p>}
    {c.release_url && <p><a className="u-link" target="_blank" rel="noopener" href={c.release_url}>查看发布说明</a></p>}
    {anyUpgradable && <p className="sub mt">将先升级各节点，再升级控制面并重启；期间页面会短暂断开。</p>}
  </div>
    <div className="mf">
      <Button variant="ghost" size="sm" id="upd-check" data-act="update-check" loading={checking} onClick={() => void checkNow()}><Icon name="refresh" />检查更新</Button>
      <span className="sp" />
      {anyUpgradable && <Button variant="ghost" size="sm" id="upd-skip" onClick={() => { skipUpdate(live); onClose(); }}>稍后</Button>}
      <Button variant="primary" id="upd-ok" data-act="update-apply" disabled={!anyUpgradable} loading={applying} onClick={() => void apply()}>
        {anyUpgradable ? "开始更新" : "已是最新"}
      </Button>
    </div></>;
}
