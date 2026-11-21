// 更新检查的数据类型与比较逻辑。形状与 internal/model/update.go 一致:
// GET /api/update → UpdateSnapshot{control, nodes[]},
// GET /api/update/nodes → UpdateNodesProgress{job, state, seq, events[]}(在
// runtime/update.tsx 里定义,跟着弹窗轮询走)。
export interface UpdatePart {
  current?: string;
  latest?: string;
  behind?: number;
  available?: boolean;
  release_url?: string;
  error?: string;
}
export interface UpdateNode {
  id: number;
  name: string;
  display?: string;
  core: string;
  current?: string;
  target?: string;
}
export interface UpdateInfo {
  control?: UpdatePart;
  nodes?: UpdateNode[];
}

// cmpVer 给版本号做粗略排序:按数字段比,段数不够的算小。"v" 前缀与预发布后缀不计。
export function cmpVer(a: string, b: string): number {
  const seg = (v: string) => String(v || "").replace(/^v/i, "").split(/[.\-+]/).map(x => (/^\d+$/.test(x) ? +x : 0));
  const A = seg(a), B = seg(b);
  for (let i = 0; i < Math.max(A.length, B.length); i++) { const d = (A[i] || 0) - (B[i] || 0); if (d) return d; }
  return 0;
}
// isDevVer:开发版(v0.0.0-…、带 dev 的、空的)没有可比的发布号,不提示「有新版本」。
export function isDevVer(v: string | undefined): boolean {
  const s = String(v || "").trim().toLowerCase();
  return !s || /^v?0\.0\.0(\b|[-+.]|$)/.test(s) || /(^|[^a-z])dev([^a-z]|$)/.test(s);
}
// updLatest 取 control.latest;空时返回 ""。skip 以它为准。
export function updLatest(u: UpdateInfo | null): string {
  return u?.control?.latest || "";
}
// sameVer:同一版本的粗判,忽略空白与打头的 v —— 节点核心报的 current / target
// 一个带 v 一个不带时,直接字符串比会把同一版判成两版。
export function sameVer(a: string | undefined, b: string | undefined): boolean {
  const x = String(a || "").trim().replace(/^v/, "");
  const y = String(b || "").trim().replace(/^v/, "");
  return x !== "" && x === y;
}
// 节点核心这一行有没有得升:target 为空(没查到或没钉版本)、还不知道节点上是哪一版(没巡检过)、
// 或已等于 target 都算没有 —— 不知道现状就不催人更新。
export function nodeUpgradable(n: UpdateNode): boolean {
  return !!n.target && !!String(n.current || "").trim() && !sameVer(n.current, n.target);
}
// 有可升级项时 pill 才出现。可升级 = 控制面有新发布,或任一节点核心不在推荐版本上。
export function updateAvailable(u: UpdateInfo | null): boolean {
  if (!u) return false;
  const c = u.control || {};
  const nodes = Array.isArray(u.nodes) ? u.nodes : [];
  return (!!c.available && !isDevVer(c.current)) || nodes.some(nodeUpgradable);
}

// 弹窗标题:「更新到 vX」。
export function updTitle(u: UpdateInfo): string {
  const ver = u.control?.latest || "";
  return `更新到 ${ver}`.trim();
}
