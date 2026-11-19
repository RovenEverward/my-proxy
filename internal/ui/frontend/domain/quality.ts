// IP 质量评级(UC-14):控制面经 SSH 在节点上探测出口 IP 的质量并打分。
// 分数只反映 IP 信誉(机房/VPN/代理/Tor/滥用记录),不代表速度。
export interface QualityStep { name: string; status: "ok" | "failed" | "skipped" | string; reason?: string }
export interface QualityDeduction { flag: string; points: number; reason: string }
export interface QualityReport {
  egress_ip?: string; region?: string; city_en?: string; city?: string;
  asn?: string; org?: string;
  flags?: Record<string, boolean>;
  abuse_score?: number;
  score?: number;
  deductions?: QualityDeduction[];
  steps?: QualityStep[];
  error?: string;
}
export type QualityVerdict = "ok" | "partial" | "untested";
export interface NodeQuality {
  node_id: number; checked_at: string; verdict: QualityVerdict; score: number;
  report?: QualityReport;
}
export interface RegradeRow {
  // kind 区分托管节点与外部端点(UC-17);外部行带 source_id/source_key,id 为 0。
  kind?: "node" | "external";
  id: number; source_id?: number; source_key?: string;
  name: string; display?: string;
  region?: string; city?: string;
  score?: number; verdict: string;
  old?: string; new?: string;
  note?: string; changed?: boolean;
}
export interface RegradePacket { rows?: RegradeRow[]; plans?: unknown[]; applied?: boolean }

export const QUALITY_VERDICT: Record<QualityVerdict, string> = { ok: "正常", partial: "部分失败", untested: "未测" };

// 分数分档:80 起是干净 IP,50–79 有扣分项但可用,<50 风险高。
export function scoreTone(score: number, verdict?: QualityVerdict): string {
  if (!verdict || verdict === "untested") return "";
  if (score >= 80) return "ok";
  if (score >= 50) return "warn";
  return "bad";
}
export function scoreText(q?: NodeQuality): string {
  if (!q || q.verdict === "untested") return "未测";
  return String(q.score);
}
const FLAG_LABEL: Record<string, string> = {
  is_datacenter: "机房 IP", is_vpn: "VPN", is_proxy: "代理",
  is_tor: "Tor", is_abuser: "有滥用记录", is_bogon: "bogon",
};
export function flagText(flags?: Record<string, boolean>): string {
  const out = Object.keys(FLAG_LABEL).filter(k => flags?.[k]).map(k => FLAG_LABEL[k]);
  return out.join("、");
}
// qualityTip 是分数徽标上的悬停说明。
export function qualityTip(q?: NodeQuality): string {
  if (!q || q.verdict === "untested") return "还没测过这台节点的 IP 质量";
  const rep = q.report || {};
  const parts = [`出口 ${rep.egress_ip || "?"}`, rep.region || ""];
  const f = flagText(rep.flags);
  if (f) parts.push(f);
  if (rep.abuse_score) parts.push(`滥用分 ${rep.abuse_score}`);
  if (q.verdict === "partial") parts.push("部分探测失败，分数可能偏高");
  return parts.filter(Boolean).join(" · ");
}
