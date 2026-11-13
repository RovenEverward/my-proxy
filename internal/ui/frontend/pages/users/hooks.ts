import { useCallback, useEffect, useRef, useState } from "react";
import { api, readEpoch, requestEvents } from "../../runtime/api";
import type { Read, Settings } from "../../runtime/types";
import type { PlanData } from "../../domain/plans";
import { PAGE_SIZE, STATUS_TABS, asModelUser, userPath, type Activity, type ModelUser, type Pull, type User, type UserLinks, type UserList } from "../../domain/users";
import { write } from "../../runtime/api";
export interface UserFilters { status: string; search: string; plan: string; offset: number }
export interface UsersData extends Omit<PlanData, "users"> { page: UserList; counts: Record<string, number>; pulls: Pull[]; pullsKnown: boolean }
export async function readUsers(read: Read, filters: UserFilters): Promise<UsersData> {
  const query = new URLSearchParams({ limit: String(PAGE_SIZE), offset: String(filters.offset) });
  if (filters.status) query.set("status", filters.status);
  if (filters.search) query.set("search", filters.search);
  if (filters.plan) query.set("plan", filters.plan);
  const [page, all, plans, inbounds, nodes, pulls] = await Promise.all([
    read<UserList>("/api/users?" + query), read<UserList>("/api/users?limit=1000"),
    read<PlanData["plans"]>("/api/plans"), read<PlanData["inbounds"]>("/api/inbounds"),
    read<PlanData["nodes"]>("/api/nodes").catch(() => []), read<Pull[]>("/api/sub-pulls").catch(() => null),
  ]);
  const counts: Record<string, number> = { "": all.total };
  if (all.total <= all.users.length) for (const [status] of STATUS_TABS.slice(1)) counts[status] = all.users.filter(u => u.status === status).length;
  return { page, counts, plans: plans || [], inbounds: inbounds || [], nodes: nodes || [], pulls: pulls || [], pullsKnown: pulls !== null };
}
export async function readModifyUser(name: string, change: (user: ModelUser) => void) {
  const model = asModelUser(await api<User>(userPath(name)));
  change(model);
  return write<User>("PUT", userPath(name), model);
}
export type DetailPart = "user" | "links" | "activity" | "settings";
export interface UserDetailData {
  user: User | null; links: UserLinks | null; activity: Activity | null; settings: Settings | null;
  pending: Partial<Record<DetailPart, boolean>>; errors: Partial<Record<DetailPart, string>>;
}
const detailCache = new Map<string, Pick<UserDetailData, "user" | "links" | "activity" | "settings">>();
requestEvents.addEventListener("invalidate", event => {
  const path = new URL((event as CustomEvent<string>).detail, location.origin).pathname;
  const userMatch = /^\/api\/users\/([^/]+)/.exec(path);
  for (const [name, cached] of detailCache) {
    if (path === "/api/users" || path.startsWith("/api/users/") && (!userMatch || decodeURIComponent(userMatch[1]) === name)) {
      detailCache.set(name, { ...cached, user: null, links: null, activity: null });
    } else if (path.startsWith("/api/settings") && !path.startsWith("/api/settings/templates/")) {
      detailCache.set(name, { ...cached, links: null, settings: null });
    } else if (/^\/api\/(plans|inbounds|sources)(\/|$)/.test(path)) {
      detailCache.set(name, { ...cached, user: null, links: null });
    }
  }
});
export function useUserDetail(name: string, base: UsersData | null) {
  const found = base?.page.users.find(u => u.username === name);
  const initial = () => {
    const cached = detailCache.get(name);
    if (found) detailCache.set(name, { user: found, links: cached?.links || null, activity: cached?.activity || null, settings: cached?.settings || null });
    return { user: found || cached?.user || null, links: cached?.links || null, activity: cached?.activity || null, settings: cached?.settings || null, pending: { links: true, activity: true, settings: true, ...(!found ? { user: true } : {}) }, errors: {} } satisfies UserDetailData;
  };
  const [data, setData] = useState<UserDetailData>(initial);
  const alive = useRef(true);
  const generations = useRef<Record<DetailPart, number>>({ user: 0, links: 0, activity: 0, settings: 0 });
  const readPart = useCallback(async (part: DetailPart) => {
    const epoch = readEpoch, generation = ++generations.current[part];
    setData(current => ({ ...current, pending: { ...current.pending, [part]: true }, errors: { ...current.errors, [part]: undefined } }));
    const path = part === "settings" ? "/api/settings" : userPath(name) + (part === "user" ? "" : "/" + part);
    try {
      const value = await api<User | UserLinks | Activity | Settings>(path);
      if (!alive.current || generation !== generations.current[part]) return;
      if (epoch !== readEpoch) throw new Error("资料在读取时有变动，请重试");
      setData(current => {
        const next = { ...current, [part]: value, pending: { ...current.pending, [part]: false } };
        const cached = detailCache.get(name) || { user: null, links: null, activity: null, settings: null };
        detailCache.delete(name);
        detailCache.set(name, { ...cached, [part]: value });
        if (detailCache.size > 16) detailCache.delete(detailCache.keys().next().value!);
        return next;
      });
    } catch (error) {
      if (alive.current && generation === generations.current[part]) setData(current => ({ ...current, pending: { ...current.pending, [part]: false }, errors: { ...current.errors, [part]: epoch !== readEpoch ? "资料在读取时有变动，请重试" : error instanceof Error ? error.message : String(error) } }));
    }
  }, [name]);
  useEffect(() => {
    alive.current = true;
    for (const part of ["links", "activity", "settings", ...(!found ? ["user"] : [])] as DetailPart[]) void readPart(part);
    return () => { alive.current = false; };
  }, [readPart]);
  const refresh = useCallback(async () => { await Promise.all((["user", "links", "activity"] as DetailPart[]).map(readPart)); }, [readPart]);
  const user = data.user && { ...data.user, presence: data.activity?.presence || data.user.presence || found?.presence };
  return { data, user, retry: readPart, refresh };
}
