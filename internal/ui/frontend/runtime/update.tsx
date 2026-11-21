// 更新 pill 状态与一键更新弹窗(UC-26 二层版):30 秒静默取 GET /api/update;
// 弹窗列「控制面 + 节点核心」两层,先起 POST /api/update/nodes 跟
// /api/update/nodes?since= 的事件流,节点侧收尾后再 POST /api/update/control
// 并轮询 /api/admin/meta,控制面起来即整页刷新。没有本机/桌面一层 ——
// 管理台伺服在控制面自己身上。
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { api, write } from "./api";
import { useApp } from "./app-context";
import { Button } from "../components/ui/button";
import { Icon } from "../components/primitives";
import { nodeLabel } from "../domain/nodes";
import { isDevVer, nodeUpgradable, updLatest, updTitle, updateAvailable, type UpdateInfo, type UpdateNode, type UpdatePart } from "../domain/releases";

/* ---------- 共享状态 ---------- */

const UPD_SKIP_KEY = "mpx-upd-skip";
const POLL_MS = 30000;
let updateData: UpdateInfo | null = null;
let updateBusy = false;
let updateAt = 0;
let skipVersion = "";
const listeners = new Set<() => void>();
const emit = () => { for (const listener of listeners) listener(); };

try { skipVersion = sessionStorage.getItem(UPD_SKIP_KEY) || ""; } catch { /* 存储不可用就每次问 */ }

// refreshUpdate:失败静默保留上次结论;force=false 时 30 秒内不重问。
export async function refreshUpdate(force = true): Promise<UpdateInfo | null> {
  if (updateBusy) return updateData;
  if (!force && Date.now() - updateAt < POLL_MS) return updateData;
  updateBusy = true;
  try {
    const u = await api<UpdateInfo>("/api/update");
    updateAt = Date.now();
    updateData = u;
    emit();
  } catch { /* 失败静默:保留上次状态,不打扰用户 */ }
  finally { updateBusy = false; }
  return updateData;
}

export function skipUpdate(u: UpdateInfo | null) {
  const v = updLatest(u);
  if (!v) return;
  try { sessionStorage.setItem(UPD_SKIP_KEY, v); } catch { /* 不可写也只影响下次启动 */ }
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

/* ---------- 节点显示名 ---------- */

const NODE_LABELS: Record<string, string> = {};
async function ensureNodeLabels(): Promise<boolean> {
  if (Object.keys(NODE_LABELS).length) return false;
  try {
    const nodes = await api<{ name?: string; display?: string }[]>("/api/nodes");
    for (const n of nodes || []) if (n.name) NODE_LABELS[n.name] = nodeLabel(n);
    return true;
  } catch { return false; } /* 原名兜底 */
}
const labelOfName = (name: string) => NODE_LABELS[name] || name;

/* ---------- 更新弹窗 ---------- */

// 一轮跑的两段:先节点核心(后台 job,GET /api/update/nodes 跟事件),
// 再控制面(POST /api/update/control 写请求文件,root 侧接力升级并重启)。
// 控制面重启期间 /api 全断,等 /api/admin/meta 问通就整页刷新。
interface NodeEvent { seq: number; node: string; core: string; status: string; msg: string }
interface NodeProgress { job?: number; state?: string; seq?: number; events?: NodeEvent[] }
type RunPhase = "nodes" | "control" | "restart" | "done" | "failed";
interface RunState { phase: RunPhase; since: number; events: NodeEvent[]; error: string }
const RESTART_WAIT_MS = 120000;
let activeRun: RunState | null = null;
const restartWait = { active: false, timedOut: false };
const detachRun = () => { activeRun = null; };

// nodeEventText 把 (节点,核心,状态) 折成一行可读的事件。
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

  // 一轮进行中锁弹窗(防误关);卸载一律解锁、停本地定时器。
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

  const refreshNow = useCallback(async () => {
    try {
      const u = await api<UpdateInfo>("/api/update");
      updateAt = Date.now();
      updateData = u;
      emit();
      setLive(u);
    } catch { /* 轮询失败静默 */ }
  }, []);

  /* ----- 轮询与阶段推进 ----- */

  const push = (patch: Partial<RunState>) => {
    activeRun = { phase: "nodes", since: 0, events: [], error: "", ...activeRun, ...patch };
    setRun({ ...activeRun, events: [...activeRun.events] });
  };

  // 控制面升级请求已写下:每秒问 /api/admin/meta,问通整页刷新;
  // 120 秒还没起来换「手动刷新」提示(会话在库里,重启不失效)。
  const waitRestart = useCallback(() => {
    restartWait.active = true;
    setRestarting("wait");
    const deadline = Date.now() + RESTART_WAIT_MS;
    const tick = async () => {
      try {
        await fetch("/api/admin/meta", { credentials: "same-origin", headers: { Accept: "application/json" } }).then(r => { if (!r.ok) throw new Error(); });
        location.reload();
        return;
      } catch { /* 还没起来 */ }
      if (Date.now() > deadline) { restartWait.timedOut = true; setRestarting("timeout"); return; }
      timer.current = window.setTimeout(tick, 1000);
    };
    void tick();
  }, []);

  // 节点 job 轮询:?since 增量取事件;终态(done/failed)推进到控制面段。
  const watchNodes = useCallback(() => {
    const tick = async () => {
      let snap: NodeProgress | null = null;
      try { snap = await api<NodeProgress>("/api/update/nodes?since=" + (activeRun?.since ?? 0)); } catch { /* 抖一下下轮再试 */ }
      if (!activeRun || activeRun.phase !== "nodes") return;
      if (snap) {
        const events = [...(activeRun.events), ...(snap.events || [])];
        push({ events, since: snap.seq ?? activeRun.since });
        if (snap.state === "done" || snap.state === "failed") {
          if (snap.state === "failed") { push({ phase: "failed", error: "节点升级任务失败，见上面事件" }); return; }
          // 节点侧完事:发起控制面升级。
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

  // apply 只在「有可更新项且没在跑」时点亮:节点有货就先起节点 job,
  // 只有控制面可升时直接写升级请求。
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
      updateAt = Date.now();
      updateData = u;
      emit();
      setLive(u);
    } catch (cause) { appRef.current.toast(cause instanceof Error ? cause.message : "检查失败", true); }
    finally { setChecking(false); }
  }, []);

  /* ----- 渲染 ----- */

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
    {!!nodes.length === false && <p className="sub">还没有节点更新信息。</p>}
    {(c.release_url) && <p><a className="u-link" target="_blank" rel="noopener" href={c.release_url}>查看发布说明</a></p>}
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
