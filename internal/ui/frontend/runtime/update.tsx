// 更新 pill 状态与一键更新弹窗。行为逐字移植自 assets/app.js 205–457:
// 30 秒静默取 /local/update;弹窗列出本机/控制面/节点三层并跟着 prefetch 状态走;
// 「重启以更新」发 POST /local/update/all 后增量拉事件,走到 restarting 改问 /ui/meta,
// 问通整页刷新;失败停在失败消息;隐藏时一拍不发,回到可见立刻恢复。
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { api, write } from "./api";
import { useApp } from "./app-context";
import { Button } from "../components/ui/button";
import { FlagText, Icon } from "../components/primitives";
import { nodeLabel, errorMessage } from "../domain/nodes";
import {
  InlineMd, nodeUpgradable, releaseHighlights, updLatest, updProgName, updSelfName, updTitle, updateAvailable,
  type UpdateAllSnapshot, type UpdateEvent, type UpdateInfo, type UpdateNode, type UpdatePart,
} from "../domain/releases";

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

// refreshUpdate 等价旧 refreshUpdatePill:失败静默保留上次结论;force=false 时 30 秒内不重问。
export async function refreshUpdate(force = true): Promise<UpdateInfo | null> {
  if (updateBusy) return updateData;
  if (!force && Date.now() - updateAt < POLL_MS) return updateData;
  updateBusy = true;
  try {
    const u = await api<UpdateInfo>("/local/update");
    updateAt = Date.now();
    updateData = u;
    emit();
  } catch { /* 失败静默:保留上次状态,不打扰用户 */ }
  finally { updateBusy = false; }
  return updateData;
}
// 「稍后」:把当前 latest 记进本会话,同版本不再出现;新版本再显示。
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
  // refresh 用节流档:打开 pill / App 挂载时拿的是 30 秒内已知结论,不立刻重取。
  // 强制取只走内部通道(interval、更新终态),与旧 refreshUpdatePill 语义一致。
  const refresh = useCallback(() => refreshUpdate(false), []);
  const latest = updLatest(data);
  return { data, refresh, available: updateAvailable(data) && !(latest && skipped === latest) };
}

/* ---------- 节点显示名 ---------- */

// NODE_LABELS:内部名 → 显示名。旧版在 /api/nodes 响应里顺手记下;这里弹窗打开时按需补拉一次。
const NODE_LABELS: Record<string, string> = {};
async function ensureNodeLabels(can: (method: string, path: string) => boolean): Promise<boolean> {
  if (Object.keys(NODE_LABELS).length || !can("GET", "/api/nodes")) return false;
  try {
    const nodes = await api<{ name?: string; display?: string }[]>("/api/nodes");
    for (const n of nodes || []) if (n.name) NODE_LABELS[n.name] = nodeLabel(n);
    return true;
  } catch { return false; } /* 原名兜底 */
}
const labelOfName = (name: string) => NODE_LABELS[name] || name;

/* ---------- 更新弹窗 ---------- */

interface RunState { since: number; events: UpdateEvent[]; delay: number; state: string }
const UPD_ALL_MIN = 1000, UPD_ALL_MAX = 5000, RESTART_WAIT_MS = 60000;
const UPD_STEP_NAMES: Record<string, string> = { control: "控制面", nodes: "节点", cli: "本机", restart: "重启" };
// 一轮进行中跨弹窗实例的状态:卸载弹窗不清,重新打开直接回进度视图。
let activeRun: RunState | null = null;
const restartWait = { active: false, timedOut: false, deadline: 0 };
const detachRun = () => { activeRun = null; };

export function UpdateDialog({ data, onClose }: { data: UpdateInfo; onClose: () => void }) {
  const app = useApp();
  const appRef = useRef(app);
  appRef.current = app;
  const [live, setLive] = useState<UpdateInfo>(data);
  const [kept, setKept] = useState(false); // 弹窗开过一轮轮询后省略「稍后」(轮询只换按钮排)
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState("");
  // run / restarting 挂模块级引用:弹窗关掉一轮还在跑,重开直接回进度视图。
  const [run, setRun] = useState<RunState | null>(() => activeRun ? { ...activeRun, events: [...activeRun.events] } : null);
  const [restarting, setRestarting] = useState<"" | "wait" | "timeout">(() => (restartWait.active ? (restartWait.timedOut ? "timeout" : "wait") : ""));
  const [labelTick, setLabelTick] = useState(0);
  void labelTick;
  const runTimer = useRef(0);
  const prefetchTimer = useRef(0);
  const stepsRef = useRef<HTMLDivElement>(null);
  const desktop = !!live.desktop;

  // 弹窗生命周期:一轮更新进行中锁弹窗(防误关),卸载一律解锁并停本地定时器。
  useEffect(() => {
    const unlock = () => appRef.current.setModalLocked(false);
    return () => {
      unlock();
      clearTimeout(runTimer.current);
      clearTimeout(prefetchTimer.current);
      detachRun();
    };
  }, []);

  // 节点行要显示名:还没拉过节点列表(比如直接停在设置页)就先拉一次。
  useEffect(() => {
    let alive = true;
    void ensureNodeLabels(app.can).then(got => { if (got && alive) setLabelTick(t => t + 1); });
    return () => { alive = false; };
  }, [app.can]);

  // props.data 变更(外层 refresh)同步进来;进行中的一轮以本地 live 为准。
  useEffect(() => { setLive(data); }, [data]);

  const refreshNow = useCallback(async () => {
    try {
      const u = await api<UpdateInfo>("/local/update");
      updateAt = Date.now();
      updateData = u;
      emit();
      setLive(u);
    } catch { /* 轮询失败静默 */ }
  }, []);

  // prefetch 后台下载:弹窗开着时每 2 秒问一次,只换按钮那一排的状态。
  const prefetch = live.prefetch;
  const downloading = prefetch?.state === "downloading";
  useEffect(() => {
    if (!downloading || run || restarting) return;
    const tick = async () => {
      await refreshNow();
      const p = updateData?.prefetch;
      if (p?.state !== "downloading") setKept(true);
      prefetchTimer.current = window.setTimeout(tick, 2000);
    };
    prefetchTimer.current = window.setTimeout(tick, 2000);
    return () => clearTimeout(prefetchTimer.current);
  }, [downloading, run, restarting, refreshNow]);

  // 一键更新轮询:有新事件回到 1 秒,没有就翻倍,上限 5 秒;页面隐藏一拍不发,
  // 回到可见由 visibilitychange 立刻补一次。
  useEffect(() => {
    if (!run) return;
    let alive = true;
    const poll = async () => {
      const a = activeRun;
      if (!alive || !a || document.hidden) return;
      let snap: UpdateAllSnapshot | null = null;
      try { snap = await api<UpdateAllSnapshot>("/local/update/all?since=" + a.since); }
      catch (e) {
        // 进度接口连不上:本机二进制正在被换,视同进入重启。
        if (e instanceof Error && "status" in e && (e as { status: number }).status === 0) { beginRestartWait(); return; }
      }
      if (!alive) return;
      if (activeRun !== a) { // 期间重开了一轮,旧的链子就此打住,新的已在自己轮询
        if (a.events.length) setRun({ ...a, events: [...a.events] });
        return;
      }
      if (snap) {
        const evs = Array.isArray(snap.events) ? snap.events : [];
        if (evs.length) {
          a.events.push(...evs);
          a.since = evs[evs.length - 1].seq;
          a.delay = UPD_ALL_MIN;
        } else {
          a.delay = Math.min(a.delay * 2, UPD_ALL_MAX);
        }
        if (snap.state === "failed" || snap.state === "done") {
          a.state = snap.state;
          setRun({ ...a, events: [...a.events] });
          appRef.current.setModalLocked(false);
          detachRun();
          restartWait.active = false;
          void refreshUpdate(true);
          return;
        }
        if (snap.state === "restarting") { beginRestartWait(); return; }
        a.state = snap.state || a.state;
        setRun({ ...a, events: [...a.events] });
      }
      if (document.hidden) return;
      runTimer.current = window.setTimeout(poll, a.delay);
    };
    void poll();
    const onVisible = () => {
      if (document.hidden || !activeRun) return;
      clearTimeout(runTimer.current);
      void poll();
    };
    document.addEventListener("visibilitychange", onVisible);
    return () => { alive = false; document.removeEventListener("visibilitychange", onVisible); };
    // run 只做进入信号;循环自己读模块态,不因对象变更重启。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [!!run]);

  // 进度列表滚到底。
  useEffect(() => {
    const box = stepsRef.current;
    if (box) box.scrollTop = box.scrollHeight;
  }, [run?.events.length, run?.state]);

  const startRun = () => {
    const a: RunState = { since: 0, events: [], delay: UPD_ALL_MIN, state: "" };
    activeRun = a;
    setRun({ ...a, events: [] });
  };
  function beginRestartWait() {
    clearTimeout(runTimer.current);
    detachRun();
    restartWait.active = true;
    restartWait.timedOut = false;
    restartWait.deadline = Date.now() + RESTART_WAIT_MS;
    setRun(null);
    setRestarting("wait");
    appRef.current.setModalLocked(false); // 重启等待不锁:弹窗可关,刷新发生在外层
  }

  // 重启等待:每秒问 /ui/meta,问通整页刷新;60 秒不起来换「手动刷新」提示。
  // 由 restarting 状态驱动:正常进入和弹窗重开都走这一条链。
  useEffect(() => {
    if (!restarting || restarting === "timeout") return;
    const tick = async () => {
      try {
        await api("/ui/meta");
        location.reload();
        return;
      } catch { /* 还没起来 */ }
      if (!restartWait.active) return;
      if (Date.now() >= restartWait.deadline) {
        restartWait.timedOut = true;
        setRestarting("timeout");
        return;
      }
      runTimer.current = window.setTimeout(tick, 1000);
    };
    runTimer.current = window.setTimeout(tick, 1000);
    return () => clearTimeout(runTimer.current);
    // 只在进入重启等待时装配一次;beginRestartWait 自己也会启同一拍,先到者胜。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [restarting]);

  const apply = async () => {
    if (applying) return;
    setApplying(true);
    setApplyError("");
    app.setModalLocked(true);
    try {
      try {
        await write("POST", "/local/update/all", {});
      } catch (e) {
        if (!(e instanceof Error && "status" in e && (e as { status: number }).status === 409)) throw e;
      }
      startRun();
    } catch (e) {
      setApplyError(errorMessage(e));
      app.setModalLocked(false);
    } finally {
      setApplying(false);
    }
  };

  const prefetchRetry = async () => {
    try {
      await write("POST", "/local/update/prefetch", {});
    } catch (e) {
      setApplyError(errorMessage(e));
      return;
    }
    setLive(previous => ({ ...previous, prefetch: { ...(previous.prefetch || {}), state: "downloading" } }));
    setKept(true);
  };

  const skip = () => { skipUpdate(live); onClose(); };
  const prefetchState = prefetch?.state;
  const downloadingPct = prefetch && prefetch.total ? " " + Math.min(100, Math.floor((prefetch.bytes || 0) * 100 / prefetch.total)) + "%" : "";
  const stepName = (step: string) => (step === "cli" && desktop ? "桌面程序" : UPD_STEP_NAMES[step] || step);

  /* ---- 视图:一轮进行中 / 重启等待 / 正常 ---- */

  if (run) {
    const groups: { step: string; evs: UpdateEvent[] }[] = [];
    for (const ev of run.events) {
      const last = groups[groups.length - 1];
      if (last && last.step === ev.step) last.evs.push(ev);
      else groups.push({ step: ev.step, evs: [ev] });
    }
    const failed = run.state === "failed" ? [...run.events].reverse().find(e => e.status === "failed") : undefined;
    const done = run.state === "done";
    const title = failed ? "更新失败" : done ? "已是最新" : "正在更新";
    return <>
      <div className="mbody"><h2>{title}</h2>
        <div className="upd-steps" ref={stepsRef}>{groups.map((group, gi) => <div key={gi}><div className="upd-step">{stepName(group.step)}</div>{group.evs.map((ev, i) => <div key={i} className={`upd-ev ${ev.status}`}>{ev.msg}</div>)}</div>)}</div>
        {failed && <div className="err" role="alert">{failed.msg}</div>}
      </div>
      {(failed || done) && <div className="mf"><Button variant="primary" data-act="modal-cancel" onClick={onClose}>关闭</Button></div>}
    </>;
  }
  if (restarting) {
    const title = updProgName(desktop) + " 正在重启";
    return <>
      <div className="mbody"><h2>{title}…</h2>
        <p className="sub">{restarting === "timeout" ? "等了一分钟还没起来，请手动刷新本页确认状态。" : "新版本起来后本页会自动刷新，会话沿用，无需重新登录。"}</p>
      </div>
      {restarting === "timeout" && <div className="mf"><Button variant="primary" data-act="modal-cancel" onClick={onClose}>关闭</Button></div>}
    </>;
  }

  const c = live.control || {}, l = live.cli || {};
  const nodes = Array.isArray(live.nodes) ? live.nodes : [];
  const highlights = releaseHighlights(live.notes);
  const row = (name: React.ReactNode, p: UpdatePart | undefined, key: string) => {
    const part = p || {};
    return <div className="upd-row" key={key}><b>{name}</b><span className="mono">{part.current || "未知"}</span><Icon name="arrow" className="upd-arrow" /><span className="mono">{part.latest || "—"}</span><span className="sp" />{part.available ? <span className="tag g">可升级</span> : <span className="sub">已是最新</span>}</div>;
  };
  const nodeRow = (n: UpdateNode) => {
    const up = nodeUpgradable(n);
    return <div className="upd-row" key={n.id}><b><FlagText>{labelOfName(n.name)}</FlagText></b><span className="sub">{n.core}</span>
      {up ? <><span className="mono">{n.current || "未知"}</span><Icon name="arrow" className="upd-arrow" /><span className="mono">{n.target}</span></> : <span className="mono">{n.current || "未知"}</span>}
      <span className="sp" />{up ? <span className="tag g">可升级</span> : <span className="sub">已是推荐版本</span>}</div>;
  };
  const skipButton = !kept && <Button data-act="update-skip" onClick={skip}>稍后</Button>;
  return <>
    <div className="mbody"><h2>{updTitle(live)}</h2>
      {row(updSelfName(desktop), l, "cli")}{row("控制面", c, "control")}{nodes.map(nodeRow)}
      {!!highlights.length && <div className="upd-notes"><div className="upd-hl-t">这次更新</div><ul className="upd-hl">{highlights.map((t, i) => <li key={i}><InlineMd text={t} /></li>)}</ul></div>}
      {(c.release_url || l.release_url) && <p><a className="u-link" target="_blank" rel="noopener" href={c.release_url || l.release_url}>查看发布说明</a></p>}
      <p className="sub mt">{desktop ? "将依次升级控制面、节点、桌面程序，完成后自动重启。" : "将依次升级控制面、节点、本机程序，完成后自动重启。"}</p>
      <div className="err" id="upd-err" role="alert">{applyError}</div>
    </div>
    <div className="mf">
      {skipButton}
      {prefetchState === "failed" && <><span className="sub">下载失败，稍后自动重试</span><Button variant="ghost" size="sm" id="upd-retry" data-act="update-prefetch-retry" onClick={() => void prefetchRetry()}>立即重试</Button></>}
      <Button variant="primary" id="upd-ok" data-act="update-apply" disabled={prefetchState === "downloading" || prefetchState === "failed"} loading={applying} onClick={() => void apply()}>
        {prefetchState === "downloading" ? `正在后台下载…${downloadingPct}` : "重启以更新"}
      </Button>
    </div>
  </>;
}
