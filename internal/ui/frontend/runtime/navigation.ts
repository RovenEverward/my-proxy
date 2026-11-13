const oldRoutes: Record<string, string> = {
  inbounds: "nodes", settings: "sub", "settings/general": "sub", "settings/rules": "sub/clients",
  "settings/templates": "sub/templates", "settings/devices": "system/devices", "settings/backup": "system/backup", plans: "users/plans",
};
const known = new Set(["overview", "nodes", "users", "sub", "sources", "rules", "doctor", "system"]);
export function canonicalKey(value: string) {
  const key = value.replace(/^#/, "") || "overview";
  const moved = oldRoutes[key] || (key.startsWith("inbounds/") ? "nodes" : key.startsWith("plans/") ? "users/" + key : key);
  return known.has(moved.split("/")[0]) ? moved : "overview";
}
export function routeOf(key: string) {
  let route: string[];
  try { route = key.split("/").map(decodeURIComponent); } catch { return ["overview"]; }
  return route[0] === "users" && route[1] === "plans" ? route.slice(1) : route;
}
export const navigation = [
  { key: "overview", label: "总览", icon: "home" }, { key: "nodes", label: "节点", icon: "nodes" },
  { key: "users", label: "用户", icon: "users" }, { key: "sub", label: "订阅", icon: "sub" },
  { key: "rules", label: "分流", icon: "split" }, { key: "doctor", label: "体检", icon: "pulse" },
  { key: "system", label: "系统", icon: "set" },
] as const;
export function navKey(route: string[]) { return route[0] === "plans" ? "users" : route[0] === "sources" ? "sub" : route[0]; }
export function shortVersion(value?: string) {
  const text = String(value || ""), release = /^v?\d+\.\d+\.\d+/.exec(text);
  if (release) return release[0].startsWith("v") ? release[0] : "v" + release[0];
  const development = /^dev-([0-9a-f]{7,40})(?:-(clean|dirty))?$/.exec(text);
  return text.length > 16 && development ? "dev-" + development[1].slice(0, 7) + (development[2] === "dirty" ? "*" : "") : text;
}
