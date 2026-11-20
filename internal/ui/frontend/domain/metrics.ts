// UC-20:境内稳定度 + 带宽指标的领域形状,与 GET /api/metrics 对齐。

export interface Stability {
  window_days: number;
  rounds: number;
  success_rate: number; // 0–1
  latency_ms: number;
  sources: number;
  insufficient: boolean;
}
export interface Bandwidth {
  status: string; // untested / ok / failed / unsupported
  bytes_per_sec?: number;
  measured_at?: string;
  source_id?: string;
  error?: string;
}
export interface SubjectMetrics {
  subject_key: string;
  stability: Stability;
  bandwidth: Bandwidth;
}
export interface MetricsResponse {
  window_days: number;
  subjects: SubjectMetrics[];
}

export function metricsMap(d?: MetricsResponse | null): Map<string, SubjectMetrics> {
  return new Map((d?.subjects || []).map(m => [m.subject_key, m]));
}

// bpsText 把字节/秒摆成人读的吞吐。
export function bpsText(bps?: number): string {
  if (!bps || bps <= 0) return "";
  const mbps = bps * 8 / 1_000_000;
  if (mbps >= 100) return Math.round(mbps) + " Mbps";
  if (mbps >= 1) return mbps.toFixed(1) + " Mbps";
  return Math.round(bps * 8 / 1000) + " Kbps";
}

// stabilityText 是「境内」列的主文案:成功率·时延,样本不足明说。
export function stabilityText(s?: Stability): { text: string; title?: string } {
  if (!s || s.rounds === 0) return { text: "—", title: "还没有境内探测样本" };
  if (s.insufficient) return { text: "样本不足", title: `${s.rounds} 轮样本，凑够 3 轮才出数` };
  const pct = Math.round(s.success_rate * 100) + "%";
  const lat = s.latency_ms > 0 ? ` · ${s.latency_ms}ms` : "";
  return { text: pct + lat, title: `${s.window_days} 天内 ${s.rounds} 轮 · ${s.sources} 个探测源` };
}

// bandwidthText 是带宽展示值:有效值 + 状态占位。
export function bandwidthText(b?: Bandwidth): { text: string; title?: string } {
  if (!b || b.status === "untested") return { text: "未测" };
  if (b.status === "unsupported") return { text: "不支持", title: b.error };
  const at = b.measured_at ? `测于 ${new Date(b.measured_at).toLocaleString()}` : "";
  // failed:展示沿用最近一次有效实测值(带宽测出来就一直用,复测失败
  // 只是这次没测成),提示里说清最近这次失败。
  const v = bpsText(b.bytes_per_sec);
  if (b.status === "failed") return { text: v || "失败", title: [at, "最近一次复测失败：" + (b.error || "")].filter(Boolean).join(" · ") };
  return { text: v || "—", title: [at, b.error].filter(Boolean).join(" · ") };
}
