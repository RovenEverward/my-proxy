import { api, write } from "../runtime/api";
import type { ClientRule, Read, Settings, SubPull, User, UserList } from "../runtime/types";

export const TEMPLATE_FORMATS = ["clash-meta", "sing-box", "xray"] as const;
export type TemplateFormat = typeof TEMPLATE_FORMATS[number];
export const TEMPLATE_META = {
  "clash-meta": { language: "yaml", label: "YAML", extension: ".yaml", name: "Clash Meta", how: "节点放进 proxies，分流规则、策略组和 DNS 原样带出。必须有顶层 rule-providers 段。" },
  "sing-box": { language: "json", label: "JSON", extension: ".json", name: "sing-box", how: "节点放进 outbounds，proxy、auto、direct、block 四个占位换成这次的节点。" },
  xray: { language: "json", label: "JSON", extension: ".json", name: "Xray", how: "每条连接方式生成一份完整配置，节点是第一条 outbound，其余原样。" },
} as const;
export function asModelSettings(value: Settings): Settings {
  return { sub_url_prefix: value.sub_url_prefix || "", profile_title: value.profile_title || "", support_link: value.support_link || "", update_interval: value.update_interval || 0,
    webhook_url: value.webhook_url || "", notify_days_left: value.notify_days_left || 0, notify_usage_percent: value.notify_usage_percent || 0, ruleset_mode: value.ruleset_mode || "", rules: value.rules ?? null, templates: value.templates ?? null };
}
export async function modifySettings(change: (settings: Settings) => void): Promise<unknown> {
  const settings = asModelSettings(await api<Settings>("/api/settings"));
  change(settings);
  return write("PUT", "/api/settings", settings);
}
export const SUB_FIELDS = [["sub_url_prefix", "链接开头"], ["profile_title", "订阅名"], ["update_interval", "刷新间隔"], ["support_link", "帮助链接"]] as const;
export type SubscriptionField = typeof SUB_FIELDS[number][0];
export type SubscriptionDraft = Omit<Settings, "update_interval"> & { update_interval: string | number };
export type DraftPatch = Partial<Pick<SubscriptionDraft, SubscriptionField | "rules">>;
export const sameRules = (a: ClientRule[] | null, b: ClientRule[] | null) => JSON.stringify((a || []).map(({ pattern, result }) => ({ pattern, result }))) === JSON.stringify((b || []).map(({ pattern, result }) => ({ pattern, result })));
export function changedFields(draft: SubscriptionDraft, original: SubscriptionDraft) { return SUB_FIELDS.filter(([key]) => String(draft[key] ?? "") !== String(original[key] ?? "")); }
export function changes(draft: SubscriptionDraft, original: SubscriptionDraft): string[] { const labels: string[] = changedFields(draft, original).map(([, label]) => label); if (!sameRules(draft.rules, original.rules)) labels.push("识别规则"); return labels; }
export function changedPatch(draft: SubscriptionDraft, original: SubscriptionDraft): DraftPatch {
  const patch: DraftPatch = {};
  for (const [key] of changedFields(draft, original)) { if (key === "update_interval") patch.update_interval = draft[key]; else patch[key] = draft[key]; }
  if (!sameRules(draft.rules, original.rules)) patch.rules = structuredClone(draft.rules);
  return patch;
}
export const FORMATS: Record<string, readonly [string, string]> = {
  "clash-meta": ["Clash Meta 配置", "带完整分流和策略组"], clash: ["Clash 配置", "精简版，只有节点和基本规则"], "sing-box": ["sing-box 配置", "带完整分流"], xray: ["Xray 配置", "每个节点一份完整配置"],
  "base64-links": ["节点链接 · Base64", "只有节点，没有分流"], links: ["节点链接", "每行一条，只有节点"], block: ["不给订阅", "返回 404"],
};
export const FORMAT_ORDER = ["clash-meta", "clash", "sing-box", "xray", "base64-links", "links", "block"];
export function toRegExp(pattern: string): RegExp | null { const flags = /^\(\?([ims]+)\)/.exec(pattern); try { return new RegExp(flags ? pattern.slice(flags[0].length) : pattern, flags ? [...new Set(flags[1])].join("") : ""); } catch { return null; } }
export function ruleHit(rules: ClientRule[], ua: string): number { if (!ua) return -1; const index = rules.findIndex(rule => toRegExp(rule.pattern)?.test(ua)); return index < 0 ? rules.length : index; }
export const effectiveRules = (rules: ClientRule[] | null, builtin: ClientRule[]) => rules?.length ? rules : builtin;
export function parsePattern(pattern: string) { const ci = /^\(\?i\)/.test(pattern), body = ci ? pattern.slice(4) : pattern; return { ci, body, all: body === ".*", regexp: !/^[^\\^$()[\]{}*+?.]*$/.test(body.replace(/\\\./g, "")) }; }
export function describePattern(pattern: string) { const parsed = parsePattern(pattern); if (parsed.all) return "其他所有客户端"; const parts = parsed.regexp ? [parsed.body] : parsed.body.split("|").map(value => value.replace(/\\\./g, ".")); return `${parsed.regexp ? "客户端标识符合" : "客户端标识里含"} ${parts.join(" 或 ")}`; }
export const CLIENT_APPS = [
  ["Clash Verge", "clash-verge/v2.0.3"], ["Mihomo Party", "mihomo.party/v1.5.12 (clash.meta)"], ["FlClash", "FlClash/v0.8.80 clash.meta"], ["Stash", "Stash/2.7.0 Clash/1.11.0"], ["Karing", "karing/1.1.2"],
  ["sing-box", "SFI/1.11.4 (sing-box 1.11.4)"], ["SFA", "SFA/1.11.4 (sing-box 1.11.4; Android)"], ["Hiddify", "HiddifyNext/2.5.7 (sing-box)"], ["Shadowrocket", "Shadowrocket/2070 CFNetwork/1494"], ["v2rayN", "v2rayN/7.4.2"], ["v2rayNG", "v2rayNG/1.9.30"],
  ["Quantumult X", "Quantumult%20X/1.4.2"], ["Surge", "Surge iOS/3021"], ["Loon", "Loon/3.2.4"],
] as const;
export const OTHER_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari/605.1.15";
export function clientFormat(rules: ClientRule[], ua: string): string { const index = ruleHit(rules, ua); return index >= 0 && index < rules.length ? rules[index].result : "links"; }
export function clientGroups(rules: ClientRule[]): [string, string[]][] { const groups = new Map<string, string[]>(); for (const [name, ua] of CLIENT_APPS) { const format = clientFormat(rules, ua); groups.set(format, [...groups.get(format) || [], name]); } return [...groups].sort(([a], [b]) => FORMAT_ORDER.indexOf(a) - FORMAT_ORDER.indexOf(b)); }

export const userPath = (username: string) => "/api/users/" + encodeURIComponent(username);
export async function subscriptionUsers(read: Read = api): Promise<User[]> { const users: User[] = []; for (;;) { const page = await read<UserList>(`/api/users?limit=1000&offset=${users.length}`); users.push(...page.users); if (!page.users.length || users.length >= page.total) return users; } }
export interface SubscriptionPreview { user: string; token: string; auto: string; appliedPrefix: string; getAuto: boolean }
export function emptyPreview(user = "alice"): SubscriptionPreview { return { user, token: user === "alice" ? "Wm5tR0aE3uLx9Nb2" : "", auto: "", appliedPrefix: "", getAuto: false }; }
export async function subscriptionPreview(user: User | undefined, read: Read = api): Promise<SubscriptionPreview> {
  if (!user) return emptyPreview();
  const links = await read<{ auto?: string }>(userPath(user.username) + "/links");
  const auto = links.auto || "", index = auto.lastIndexOf("/sub/");
  return { user: user.username, auto, token: index < 0 ? "" : auto.slice(index + 5), appliedPrefix: index < 0 ? "" : auto.slice(0, index), getAuto: true };
}
export function recentPreviewUser(users: User[], pulls: SubPull[], selected?: string): User | undefined {
  const recent = [...pulls].sort((a, b) => +new Date(b.last_pulled_at) - +new Date(a.last_pulled_at)).find(pull => users.some(user => user.username === pull.username));
  return users.find(user => user.username === selected) || users.find(user => user.username === recent?.username) || users[0];
}
export function subscriptionLink(draft: SubscriptionDraft, original: SubscriptionDraft, preview: SubscriptionPreview, fallback: string) {
  const kept = draft.sub_url_prefix === original.sub_url_prefix;
  const base = draft.sub_url_prefix || preview.appliedPrefix || fallback;
  return kept && preview.auto ? preview.auto : (kept && preview.getAuto ? preview.appliedPrefix || fallback : base).replace(/\/+$/, "") + "/sub/" + preview.token;
}
export async function subscriptionTemplates(read: Read = api): Promise<Record<TemplateFormat, string>> {
  const entries = await Promise.all(TEMPLATE_FORMATS.map(async format => [format, await read<string>("/api/settings/templates/" + format, { asText: true, headers: { Accept: "text/plain" } })] as const));
  return Object.fromEntries(entries) as Record<TemplateFormat, string>;
}
export const templateBytes = (text: string) => new TextEncoder().encode(text).length;
export const templateLines = (text: string) => text ? text.split("\n").length - (text.endsWith("\n") ? 1 : 0) : 0;
export const templateSize = (bytes: number) => bytes < 1024 ? `${bytes} B` : `${(bytes / 1024).toFixed(bytes < 10240 ? 1 : 0)} KB`;
export interface TemplateError { message: string; line?: number; server?: boolean }
function jsonErrorAt(text: string): number | null {
  let i = 0;
  const whitespace = () => { while (/[ \t\n\r]/.test(text[i] || "")) i++; };
  const fail = (): never => { throw i; };
  const string = () => { i++; while (i < text.length && text[i] !== '"') { if (text[i] === "\n") fail(); i += text[i] === "\\" ? 2 : 1; } if (text[i] !== '"') fail(); i++; };
  const value = (): void => {
    whitespace(); const character = text[i];
    if (character === "{") { i++; whitespace(); if (text[i] === "}") { i++; return; } for (;;) { whitespace(); if (text[i] !== '"') fail(); string(); whitespace(); if (text[i] !== ":") fail(); i++; value(); whitespace(); if (text[i] === ",") { i++; continue; } if (text[i] === "}") { i++; return; } fail(); } }
    if (character === "[") { i++; whitespace(); if (text[i] === "]") { i++; return; } for (;;) { value(); whitespace(); if (text[i] === ",") { i++; continue; } if (text[i] === "]") { i++; return; } fail(); } }
    if (character === '"') { string(); return; }
    const match = /^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/.exec(text.slice(i, i + 64)); if (match) i += match[0].length; else fail();
  };
  try { value(); whitespace(); return i < text.length ? i : null; } catch (offset) { return typeof offset === "number" ? offset : null; }
}
export function validateTemplate(format: TemplateFormat, text: string): TemplateError | null {
  if (!text.trim()) return { message: "模板不能是空的" };
  if (TEMPLATE_META[format].language === "json") {
    try { JSON.parse(text); return null; } catch (error) {
      const message = error instanceof Error ? error.message : String(error), location = /line (\d+) column (\d+)/.exec(message), position = /position (\d+)/.exec(message);
      const offset = location ? null : position ? +position[1] : jsonErrorAt(text);
      const before = offset === null ? [] : text.slice(0, offset).split("\n"), line = location ? +location[1] : before.length || undefined, column = location ? +location[2] : before.length ? before[before.length - 1].length + 1 : undefined;
      return { line, message: `不是合法的 JSON${line ? `：第 ${line} 行第 ${column} 列附近` : ""}(${message.replace(/^JSON\.parse: /, "")})` };
    }
  }
  const lines = text.split("\n"), tab = lines.findIndex(line => /^\s*\t/.test(line));
  if (tab >= 0) return { line: tab + 1, message: `第 ${tab + 1} 行用 Tab 缩进了，YAML 只能用空格` };
  const first = lines.findIndex(line => line.trim() && !/^\s*#/.test(line) && line.trim() !== "---");
  if (first >= 0 && !/^[^\s#-][^:]*:(\s|$)/.test(lines[first])) return { line: first + 1, message: "顶层必须是映射(key: value)，不能是列表或纯文本" };
  if (!lines.some(line => /^(rule-providers|"rule-providers"|'rule-providers')\s*:/.test(line))) return { message: "缺顶层 rule-providers 段(proxies 的插入点)，服务端会拒收" };
  return null;
}
export function sinceText(iso: string): string { if (!iso) return "—"; const date = new Date(iso), seconds = (Date.now() - +date) / 1000, pad = (value: number) => String(value).padStart(2, "0"); if (seconds < 60) return "刚刚"; if (seconds < 3600) return Math.floor(seconds / 60) + " 分钟前"; if (seconds < 86400) return `${pad(date.getHours())}:${pad(date.getMinutes())}`; return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`; }
