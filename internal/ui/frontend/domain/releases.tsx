// 更新检查的数据类型与发布说明解析。逐字移植自 assets/app.js 更新区段与 release helpers,
// 不含 DOM 与网络;React 版 inline markdown 用节点代替 innerHTML。
import type { ReactNode } from "react";

export interface UpdatePart {
  current?: string;
  latest?: string;
  behind?: number;
  available?: boolean;
  release_url?: string;
  error?: string;
}
export interface UpdatePrefetch {
  version?: string;
  state?: string; // downloading | failed | ready(或其它就绪态)
  bytes?: number;
  total?: number;
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
  cli?: UpdatePart;
  nodes?: UpdateNode[];
  notes?: string;
  desktop?: boolean;
  prefetch?: UpdatePrefetch;
}
export interface UpdateEvent { seq: number; step: string; status: string; msg: string }
export interface UpdateAllSnapshot { state?: string; step?: string; seq?: number; events?: UpdateEvent[] }

// 本机那一层与「正在运行的程序」的叫法:桌面构建(desktop 为 true)都叫「桌面程序」,
// 浏览器模式是「本机程序」与「本机界面」。
export const updSelfName = (desktop: boolean) => (desktop ? "桌面程序" : "本机程序");
export const updProgName = (desktop: boolean) => (desktop ? "桌面程序" : "本机界面");

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
// updLatest 取 cli/control 里较新且非空的 latest;都空时返回 ""。skip 以它为准。
export function updLatest(u: UpdateInfo | null): string {
  let out = "";
  for (const p of [u?.control, u?.cli]) if (p && p.latest && (!out || cmpVer(p.latest, out) > 0)) out = p.latest;
  return out;
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
// 有可升级项时 pill 才出现。可升级 = 本机 / 控制面有新发布,或任一节点核心不在推荐版本上。
export function updateAvailable(u: UpdateInfo | null): boolean {
  if (!u) return false;
  const c = u.control || {}, l = u.cli || {};
  const nodes = Array.isArray(u.nodes) ? u.nodes : [];
  const cAvail = !!c.available && !isDevVer(c.current), lAvail = !!l.available && !isDevVer(l.current);
  return cAvail || lAvail || nodes.some(nodeUpgradable);
}

// 弹窗标题:「更新到 控制面 vX / 本机 vY」,只有一版或同版时「更新到 vX」。
export function updTitle(u: UpdateInfo): string {
  const c = u.control || {}, l = u.cli || {};
  const ver = c.latest && l.latest && c.latest !== l.latest ? `控制面 ${c.latest} / 本机 ${l.latest}` : c.latest || l.latest || "";
  return `更新到 ${ver}`.trim();
}

/* ---------- 发布说明 ---------- */

// releaseNotesBody:发布说明里只留「这次改了什么」。v0.9.4 起正文开头是「我该下哪个」下载表和
// 折叠的文件清单(给发布页看的),更早的版本末尾是「## 下载」一节;更新弹窗里都不需要。
export function releaseNotesBody(md: string | undefined): string {
  let s = String(md || "");
  s = s.replace(/^## 我该下哪个[\s\S]*?<\/details>\s*/m, "");
  s = s.replace(/^## 下载\s*$[\s\S]*/m, "");
  return s.trim();
}

// releaseHighlights:更新弹窗只列这一版的要点,不展示整份发布说明。v0.9.5 起发布说明开头是
// 「## 更新要点」一节(取自 CHANGELOG 的「### 要点」);更早的发布没有这一节,就从下载表 /
// 「## 下载」之前的正文里取前几条列表项。下载表、安装说明、折叠的文件清单一律不要。
const HL_MAX = 8;
export function releaseHighlights(md: string | undefined): string[] {
  const s = String(md || "").replace(/\r\n/g, "\n");
  const items = (text: string) => text.split("\n").map(l => /^\s{0,1}[-*]\s+(.+)$/.exec(l)).filter((m): m is RegExpExecArray => !!m).map(m => m[1].trim());
  const sec = /^##\s+(?:v[\d.]+\s+)?更新要点[ \t]*$([\s\S]*?)(?=^#{1,6}\s|(?![\s\S]))/m.exec(s);
  if (sec) return items(sec[1]).slice(0, HL_MAX);
  const body = releaseNotesBody(s).replace(/^##\s+我该下哪个[\s\S]*$/m, "");
  const li = items(body);
  if (li.length) return li.slice(0, HL_MAX);
  // 连列表都没有(很老的发布):取前几行正文,标题、表格、代码围栏、HTML 都跳过。
  return body.split("\n").map(l => l.trim()).filter(l => l && !/^(#|\||```|<)/.test(l)).slice(0, 3);
}

// InlineMd:要点里只认 `代码` 和 **粗体**,其余按纯文本渲染(等价旧版的转义后替换)。
export function InlineMd({ text }: { text: string }) {
  const out: ReactNode[] = [];
  // 先按 `code` 切段,再在普通段里按 **b** 切,顺序与旧版 replace 链一致。
  const codeRe = /`([^`]+)`/g;
  let last = 0, key = 0;
  const pushBold = (plain: string) => {
    const boldRe = /\*\*([^*]+)\*\*/g;
    let from = 0;
    for (const m of plain.matchAll(boldRe)) {
      const idx = m.index ?? 0;
      if (idx > from) out.push(plain.slice(from, idx));
      out.push(<b key={`b${key++}`}>{m[1]}</b>);
      from = idx + m[0].length;
    }
    if (from < plain.length) out.push(plain.slice(from));
  };
  for (const m of text.matchAll(codeRe)) {
    const idx = m.index ?? 0;
    if (idx > last) pushBold(text.slice(last, idx));
    out.push(<code key={`c${key++}`}>{m[1]}</code>);
    last = idx + m[0].length;
  }
  if (last < text.length) pushBold(text.slice(last));
  return <>{out}</>;
}
