export interface DoctorCheck {
  name: string;
  status: string;
  detail: string;
  hint?: string;
  ui_hint?: string;
  node_id?: number;
  ui_action?: string;
}
export interface DoctorReport { checks?: DoctorCheck[] | null }

export const DOCTOR_STATUSES = [
  { status: "ok", tone: "ok", label: "正常", description: "没有毛病" },
  { status: "warn", tone: "warn", label: "警告", description: "该看一眼，不算坏" },
  { status: "fail", tone: "bad", label: "失败", description: "要看，系统确实有毛病" },
] as const;

export function doctorPlain(value?: string): string {
  return String(value || "")
    .replace(/下发配置/g, "应用到节点")
    .replace(/下发/g, "应用到节点")
    .replace(/排水/g, "暂停分配")
    .replace(/入站/g, "连接方式")
    .replace(/入口/g, "对外地址")
    .replace(/巡检/g, "体检")
    .replace(/核心/g, "代理程序");
}

export function doctorApplyDetail(detail: string): string {
  const xray = "配置和下发的不一致(节点上被改过)";
  const singbox = "sing-box 配置未读取或与下发内容不一致";
  const parts = String(detail).split(";");
  const hasXray = parts.some(part => part.includes(xray));
  return parts.map(part => {
    if (part.includes(xray)) return part.split(xray).join("节点上的配置和控制面不一致");
    if (part.includes(singbox) && !hasXray) return part.split(singbox).join("节点上的 sing-box 配置和控制面不一致");
    return part;
  }).join(";");
}
