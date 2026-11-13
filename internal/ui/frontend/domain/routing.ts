import { APIError } from "../runtime/api";
import type { Read, User, UserList } from "../runtime/types";

export const RULE_TYPES: Record<RuleType, string> = { suffix: "域名后缀", keyword: "域名关键词", set: "规则组", geoip: "IP 所在国家", cidr: "IP 段" };
export const RULE_CAPS: Record<string, string> = { egress: "出口可用", youtube: "能看 YouTube", netflix: "能看 Netflix" };
export const RULE_REGIONS: Record<string, string> = { CN: "🇨🇳 中国", US: "🇺🇸 美国", JP: "🇯🇵 日本", HK: "🇭🇰 香港", SG: "🇸🇬 新加坡", TW: "🇹🇼 台湾", KR: "🇰🇷 韩国", GB: "🇬🇧 英国", DE: "🇩🇪 德国" };
export type RuleType = "suffix" | "keyword" | "set" | "geoip" | "cidr";
export interface RouteRule { id?: number; user_id?: number; position?: number; type: RuleType; value: string; target: string; enabled: boolean; note?: string }
export interface RulesSnapshot { global: RouteRule[]; global_version: number; final: string; user_rules?: RouteRule[]; user_version?: number; user_final?: string }
export interface ProxyGroup { id?: number; name: string; icon?: string; mode: "auto" | "manual"; regions?: string[]; protocols?: string[]; capabilities?: string[]; tags?: string[]; test_url?: string; interval?: number }
export interface SavedProxyGroup extends ProxyGroup { id: number }
export interface GroupPreview { node_id: number; source_id?: number; inbound_id: number; host_id: number; name: string; protocol: string; region?: string; tags?: string[]; capabilities?: string[]; admitted: boolean; reasons?: string[] }
export interface RuleCatalogEntry { key: string; name: string; desc: string; default: string }
export interface RulesetFile { name: string; url: string; ready: boolean; last_error?: string }
export interface RulesetGroupFiles { files: RulesetFile[]; ready: number; total: number }
export interface RulesetGroup {
  key: string; name: string;
  clash: RulesetGroupFiles; sing_box: RulesetGroupFiles;
  refreshing?: boolean;
  last_error?: string; last_attempt_at?: number; // Unix 毫秒
}
export const rulesetCounts = (group: RulesetGroup) => ({ ready: group.clash.ready + group.sing_box.ready, total: group.clash.total + group.sing_box.total });
export function rulesetStatus(group: RulesetGroup): "ready" | "refreshing" | "failed" | "partial" | "pending" {
  const { ready, total } = rulesetCounts(group);
  return group.refreshing ? "refreshing" : group.last_error ? "failed" : ready === total ? "ready" : ready > 0 ? "partial" : "pending";
}
export interface MigrationReport { status: string; unconvertible?: string[]; notes?: string[]; groups?: unknown[]; rules?: number }
export interface RoutingState { mode: "template" | "rules"; migration?: MigrationReport; warnings?: string[]; group_members?: Record<string, number> }
export interface RuleMatch { index: number; scope?: string; rule: RouteRule; reason?: string }
export interface RuleTestResult { domain: string; user?: string; target: string; mode?: string; members?: string[]; matched?: RuleMatch; uncertain?: RuleMatch[]; disabled_hits?: RuleMatch[] }
export interface SourceIssue { index?: number; name: string; reason: string }
export const SOURCE_FORMATS = [["clash-meta", "Clash Meta"], ["sing-box", "sing-box"], ["links", "分享链接"], ["base64-links", "Base64"], ["clash", "Clash"], ["xray", "Xray"]] as const;
export type SourceFormat = typeof SOURCE_FORMATS[number][0];
export interface SubscriptionSource {
  id: number; name: string; enabled: boolean; revision: number; interval_seconds: number;
  region?: string; url_masked?: string; refreshing: boolean; node_count: number;
  last_good_at?: string; last_error?: string; snapshot_stale?: boolean; input_format?: string;
  formats: Partial<Record<SourceFormat, { count: number; issues?: SourceIssue[] }>>; issues?: SourceIssue[];
}
export interface SubscriptionPreview { status: string; format: string; nodes: { name: string; protocol: string; source_id: number; source_name: string; groups: string[]; reasons?: string[] }[]; issues?: SourceIssue[] }
export interface RulesPageData {
  routing: RoutingState; rules: RulesSnapshot; groups: SavedProxyGroup[]; catalog: RuleCatalogEntry[];
  previews: Record<number, GroupPreview[] | null>; sources: SubscriptionSource[];
  origins: Record<number, string[]>; users: string[];
  rulesets: RulesetGroup[]; rulesetMode: string;
}
export const rulesPath = (user = "") => "/api/rules" + (user ? "?user=" + encodeURIComponent(user) : "");
export const finalPath = (user = "") => "/api/rules/final" + (user ? "?user=" + encodeURIComponent(user) : "");
export const groupPath = (id: number) => "/api/proxy-groups/" + id;
export const sourcePath = (id: number) => "/api/sources/" + id;
export const groupID = (target: string) => /^group:(\d+)$/.test(target) ? Number(target.slice(6)) : 0;
export const errorMessage = (error: unknown) => error instanceof Error ? error.message : String(error);
export function targetName(target: string, groups: SavedProxyGroup[]): string {
  if (target === "DIRECT") return "直连";
  if (target === "REJECT") return "拦截";
  return groups.find(group => group.id === groupID(target))?.name || target || "—";
}
export const ruleValueText = (rule: RouteRule, catalog: RuleCatalogEntry[]) => rule.type === "set" ? catalog.find(entry => entry.key === rule.value)?.name || rule.value : rule.type === "geoip" ? RULE_REGIONS[rule.value] || rule.value : rule.value;
export const stripRule = (rule: RouteRule): RouteRule => ({ type: rule.type, value: rule.value, target: rule.target, enabled: !!rule.enabled, ...(rule.note ? { note: rule.note } : {}) });
export const previewInit = (group: ProxyGroup): RequestInit => ({ method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ...group, name: group.name || "preview" }) });
export interface RoutingReadSupport { sources: "unknown" | "supported" | "unsupported" }
export async function readRulesPage(read: Read, scope: string, groupsTab: boolean, support: RoutingReadSupport): Promise<RulesPageData> {
  const [routing, rules, groups, catalog, users, rulesets, settings] = await Promise.all([
    read<RoutingState>("/api/routing"), read<RulesSnapshot>(rulesPath(scope)), read<SavedProxyGroup[]>("/api/proxy-groups"),
    read<RuleCatalogEntry[]>("/api/rule-catalog"), read<UserList>("/api/users?limit=1000"),
    read<RulesetGroup[]>("/api/rulesets").catch(() => []),
    read<{ ruleset_mode?: string }>("/api/settings").catch((): { ruleset_mode?: string } => ({})),
  ]);
  const previews: RulesPageData["previews"] = {}, origins: RulesPageData["origins"] = {};
  // Endpoint support belongs to this control-plane visit, separate from read data.
  const sourcesRequest = support.sources === "unsupported" ? Promise.resolve<SubscriptionSource[]>([]) : read<SubscriptionSource[]>("/api/sources").then(sources => {
    support.sources = "supported";
    return sources;
  }).catch((error: unknown) => {
    if (error instanceof APIError && error.status === 404) support.sources = "unsupported";
    return [];
  });
  if (groupsTab) await Promise.all(groups.map(async group => {
    const [preview, origin] = await Promise.all([
      read<GroupPreview[]>("/api/proxy-groups/preview", previewInit(group)).catch(() => null),
      sourcesRequest.then(() => support.sources === "unsupported" ? { sources: [] } : read<{ sources: string[] }>(groupPath(group.id) + "/sources").catch(() => ({ sources: [] }))),
    ]);
    previews[group.id] = preview; origins[group.id] = origin.sources;
  }));
  return { routing, rules, groups, catalog, previews, origins, sources: await sourcesRequest, users: users.users.map(user => user.username), rulesets, rulesetMode: settings.ruleset_mode || "" };
}
export async function readSubscriptionUsers(read: Read): Promise<User[]> {
  const users: User[] = [];
  for (;;) {
    const page = await read<UserList>(`/api/users?limit=1000&offset=${users.length}`);
    users.push(...page.users);
    if (!page.users.length || users.length >= page.total) return users;
  }
}
