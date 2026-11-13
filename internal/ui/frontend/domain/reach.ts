// 触达检查(UC-06):本机与控制面各对每个入站探一次 TCP/UDP,合并成节点级判定。
// 判定的显示口径与原型 G 一致:双通 / 境内不通 / 都不通 / 未知。
export interface ReachCell { status: "reachable" | "timeout" | "refused" | "unreachable" | "unknown"; latency_ms?: number; error?: string }
export interface ReachProtos { tcp?: ReachCell; udp?: ReachCell }
export interface ReachSource { probed?: boolean; reachable?: boolean; failed?: boolean; latency_ms?: number }
export type ReachVerdict = "ok" | "blocked" | "down" | "unknown";
export interface ReachRow {
  inbound_id: number; label: string; address: string; port: number;
  protocol: string; network: string;
  local?: ReachProtos; cp?: ReachProtos;
}
export interface ReachNodeCheck {
  node_id: number; node_name: string; checked_at: string; verdict: ReachVerdict;
  local?: ReachSource; cp?: ReachSource; rows?: ReachRow[];
}
export interface ReachLast { checked_at: string; counts: Record<string, number>; nodes: ReachNodeCheck[] }

export const REACH_VERDICTS: [ReachVerdict, string][] = [
  ["ok", "双通"], ["blocked", "境内不通"], ["down", "都不通"], ["unknown", "未知"],
];
export const verdictText = (v?: ReachVerdict) => REACH_VERDICTS.find(([k]) => k === v)?.[1] || "未知";

// 来源列的一句话:可达带最快时延;探过但确定不通说"不通";探不出来只说"未知"。
export function sourceText(s?: ReachSource): { text: string; tone: string; latency?: string } {
  if (!s || !s.probed) return { text: "—", tone: "info" };
  if (s.reachable) return { text: "可达", tone: "ok", latency: s.latency_ms ? `${s.latency_ms} ms` : undefined };
  if (s.failed) return { text: "不通", tone: "bad" };
  return { text: "未知", tone: "info" };
}

export function cellText(c?: ReachCell): { text: string; tone: string; latency?: string } {
  if (!c) return { text: "—", tone: "info" };
  switch (c.status) {
    case "reachable": return { text: "可达", tone: "ok", latency: c.latency_ms ? `${c.latency_ms} ms` : undefined };
    case "timeout": return { text: "超时", tone: "bad" };
    case "refused": return { text: "拒绝", tone: "bad" };
    case "unreachable": return { text: "不可达", tone: "bad" };
    default: return { text: "未知", tone: "info" };
  }
}

export const verdictLong: Record<ReachVerdict, string> = {
  ok: "本机和控制面都能触达这台节点的端口。",
  blocked: "本机探测不通，控制面可以触达——节点活着，本机这条路径被封了。",
  down: "本机和控制面都连不上，更像是节点或机房故障，不只是境内路径问题。",
  unknown: "至少一侧探测没有结果，不下结论。UDP 超时可能是对方收了没回，属于这一类。",
};

// jobSnapshot 是 /local/reach/check 的任务快照(POST 与 GET 同形)。
export interface ReachJob {
  id?: string; stage: "none" | "cp" | "local" | "save" | "done" | "error";
  nodes?: number; counts?: Record<string, number>; error?: string;
}
