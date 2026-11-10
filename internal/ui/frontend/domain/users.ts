import type { User as APIUser, Presence, SubPull } from "../runtime/types";

export interface UserPresence extends Omit<Presence, "online"> {
  online?: boolean;
  online_nodes?: number[];
  online_ips?: number;
}
export interface PresenceSummary extends Omit<Presence, "online"> { online?: number }
export type User = Omit<APIUser, "presence"> & { presence?: UserPresence };
export interface UserList { users: User[]; total: number; offset: number; limit?: number; presence?: PresenceSummary }
export interface Pull extends SubPull { format?: string }
export interface Activity {
  presence?: UserPresence;
  updated_at?: string;
  segments: { node_id: number; start: string; end?: string | null; bytes: number }[];
  ips: { ip: string; node_id: number; last_seen: string }[];
}
export type UserLinks = Record<string, string>;
export interface ModelUser {
  id: number; username: string; key?: string; sub_token?: string; enabled: boolean;
  data_limit: number; used_traffic: number; lifetime_used_traffic: number;
  expire_at?: string; reset_strategy: string; traffic_reset_at?: string;
  inbound_ids?: number[]; note?: string; created_at: string; plan_id?: number;
}
export const DAY = 86400000;
export const PAGE_SIZE = 50;
export const EXPIRED_KEEP_DAYS = 30;
export const EXPIRE_SOON_DAYS = 7;
export const RESET: Record<string, string> = { none: "不重置", daily: "每天", weekly: "每周", monthly: "每月" };
export const STATUS_TABS = [["", "全部"], ["active", "正常"], ["limited", "已超限"], ["expired", "已过期"], ["disabled", "已停用"]] as const;
export const USERNAME_RE = /^[A-Za-z0-9_.-]{1,64}$/;
export const userPath = (name: string) => "/api/users/" + encodeURIComponent(name);
export const userStateLabel = (status: string) => ({ active: "正常", disabled: "已停用", expired: "已过期", limited: "已超限" }[status] || status);
export const pad = (n: number) => String(n).padStart(2, "0");
export const ymd = (date: Date) => `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
export const hm = (date: Date) => `${pad(date.getHours())}:${pad(date.getMinutes())}`;
export const hms = (date: Date) => `${hm(date)}:${pad(date.getSeconds())}`;
export const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
export function fmtBytes(value: number | undefined | null) {
  const bytes = Number(value) || 0;
  if (bytes >= 2 ** 40) return (bytes / 2 ** 40).toFixed(2) + " TB";
  if (bytes >= 2 ** 30) return (bytes / 2 ** 30).toFixed(1) + " GB";
  if (bytes >= 2 ** 20) return (bytes / 2 ** 20).toFixed(1) + " MB";
  if (bytes >= 1024) return (bytes / 1024).toFixed(1) + " KB";
  return bytes + " B";
}
export function daysLeft(iso: string | null | undefined, now = new Date()): number | null {
  if (!iso) return null;
  const end = new Date(iso);
  return Math.round((+new Date(end.getFullYear(), end.getMonth(), end.getDate()) - +new Date(now.getFullYear(), now.getMonth(), now.getDate())) / DAY);
}
export const expiredFor = (user: { status?: string; expire_at?: string | null }) => user.status === "expired" && user.expire_at ? Math.max(0, -(daysLeft(user.expire_at) || 0)) : 0;
export function remain(user: { data_limit: number; used_traffic: number }) {
  const left = Math.max(0, user.data_limit - user.used_traffic);
  const rp = user.data_limit ? left / user.data_limit * 100 : 0;
  const over = user.used_traffic - user.data_limit;
  const cls = rp <= 10 ? "bad" : rp <= 30 ? "warn" : "ok";
  const pctText = left <= 0 ? "剩 0%" : rp < 1 ? "剩 <1%" : `剩 ${Math.floor(rp)}%`;
  const text = over > 0 ? `超 ${fmtBytes(over)} / ${fmtBytes(user.data_limit)}` : left <= 0 ? `剩 0 / ${fmtBytes(user.data_limit)}` : `剩 ${fmtBytes(left)} / ${fmtBytes(user.data_limit)}`;
  return { left, rp, cls, out: left <= 0, pctText, text, tip: `已用 ${fmtBytes(user.used_traffic)} / 上限 ${fmtBytes(user.data_limit)}` };
}
export function parseBytes(value: string) {
  let text = value.trim(), multiplier = 1;
  if (!text) throw new Error("大小不能为空");
  for (const [suffix, factor] of [["TB", 2 ** 40], ["GB", 2 ** 30], ["MB", 2 ** 20], ["KB", 2 ** 10], ["B", 1]] as const) {
    if (text.toUpperCase().endsWith(suffix)) { multiplier = factor; text = text.slice(0, -suffix.length).trim(); break; }
  }
  if (!text) throw new Error(`大小缺少数字： "${value}"`);
  if (!/^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(text) || !Number.isFinite(Number(text))) throw new Error(`无法解析大小 "${value}"，单位用 B/KB/MB/GB/TB`);
  const number = Number(text);
  if (number < 0) throw new Error(`大小不能是负数： "${value}"`);
  return /^[+-]?\d+$/.test(text) ? number * multiplier : Math.trunc(number * multiplier);
}
export function parseExpire(value: string) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value.trim());
  const date = match ? new Date(+match[1], +match[2] - 1, +match[3], 23, 59, 59) : null;
  if (!date || !match || date.getMonth() !== +match[2] - 1 || date.getDate() !== +match[3]) throw new Error(`到期日要写成 YYYY-MM-DD: "${value}"`);
  return date.toISOString();
}
export function renewTarget(current: string | null | undefined, days: number, now = new Date()) {
  const existing = current ? new Date(current) : null;
  const base = existing && existing > now ? existing : now;
  return new Date(base.getFullYear(), base.getMonth(), base.getDate() + days, 23, 59, 59).toISOString();
}
export function asModelUser(value: Partial<User> & { username: string }): ModelUser {
  const model: ModelUser = { id: value.id || 0, username: value.username, enabled: !!value.enabled, data_limit: value.data_limit || 0, used_traffic: value.used_traffic || 0, lifetime_used_traffic: value.lifetime_used_traffic || 0, reset_strategy: value.reset_strategy || "", created_at: value.created_at || "0001-01-01T00:00:00Z" };
  if (value.key) model.key = value.key;
  if (value.sub_token) model.sub_token = value.sub_token;
  if (value.expire_at) model.expire_at = value.expire_at;
  if (value.traffic_reset_at) model.traffic_reset_at = value.traffic_reset_at;
  if (value.inbound_ids?.length) model.inbound_ids = value.inbound_ids;
  if (value.note) model.note = value.note;
  return model;
}
export function relTime(iso: string) {
  const date = new Date(iso), now = new Date(), seconds = (+now - +date) / 1000;
  if (seconds < 60) return "刚刚";
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  if (sameDay(date, now)) return `今天 ${hm(date)}`;
  if (sameDay(date, new Date(+now - DAY))) return `昨天 ${hm(date)}`;
  return `${date.getFullYear() === now.getFullYear() ? "" : `${date.getFullYear()} 年 `}${date.getMonth() + 1} 月 ${date.getDate()} 日`;
}
export function agoShort(iso: string) {
  const seconds = Math.max(0, (Date.now() - +new Date(iso)) / 1000);
  return seconds < 60 ? `${Math.max(1, Math.round(seconds))} 秒前` : seconds < 3600 ? `${Math.floor(seconds / 60)} 分钟前` : relTime(iso);
}
export function absTime(iso: string) {
  const date = new Date(iso), offset = -new Date().getTimezoneOffset() / 60;
  return `${date.getFullYear()} 年 ${date.getMonth() + 1} 月 ${date.getDate()} 日 ${hms(date)}（UTC${offset >= 0 ? "+" : ""}${offset}）`;
}
export function spanText(segment: Activity["segments"][number]) {
  const start = new Date(segment.start), end = segment.end ? new Date(segment.end) : null;
  const head = relTime(segment.start).replace(/^(\d+ 分钟前|刚刚)$/, `今天 ${hm(start)}`);
  return `${/日$/.test(head) ? `${head} ${hm(start)}` : head} – ${end ? sameDay(start, end) ? hm(end) : relTime(segment.end!) : "现在"}`;
}
const CLIENT_NAMES: Record<string, string> = { "clash-verge": "Clash Verge", "clash.meta": "Clash Meta", mihomo: "Mihomo", clash: "Clash", shadowrocket: "Shadowrocket", stash: "Stash", "sing-box": "sing-box", sfa: "sing-box", sfi: "sing-box", sfm: "sing-box", v2rayn: "v2rayN", v2rayng: "v2rayNG", karing: "Karing", hiddify: "Hiddify", nekobox: "NekoBox", quantumult: "Quantumult X", surge: "Surge", loon: "Loon" };
export const clientLabel = (client: string) => !client || client === "unknown" ? "客户端" : CLIENT_NAMES[client.toLowerCase()] || client;
export const cat = (a: string, b: string) => /[A-Za-z0-9]$/.test(a) || /^[A-Za-z0-9]/.test(b) ? `${a} ${b}` : a + b;
export const pullText = (pull: Pull) => cat(`${clientLabel(pull.client)} ${relTime(pull.last_pulled_at)}`, "拉取过");
export const SEND_FORMATS = [["links", "分享链接", "一行一个节点，粘贴进 v2rayN、Shadowrocket"], ["base64-links", "Base64", "老客户端认的订阅格式"], ["clash-meta", "Clash Meta", "Clash Verge、Mihomo"], ["clash", "Clash", "老版 Clash 内核"], ["sing-box", "sing-box", "sing-box 官方客户端、Karing"], ["xray", "Xray", "Xray 内核的 JSON 配置"]] as const;
