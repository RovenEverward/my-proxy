import type { ReactNode } from "react";
import { Icon } from "../../components/primitives";
import type { RulesPageData, SavedProxyGroup } from "../../domain/routing";
import { groupID, targetName } from "../../domain/routing";

export function TargetPill({ target, groups }: { target: string; groups: SavedProxyGroup[] }) {
  const group = groups.find(item => item.id === groupID(target));
  const gone = !!target && target !== "DIRECT" && target !== "REJECT" && !group;
  const kind = gone ? "err" : target === "DIRECT" ? "dir" : target === "REJECT" ? "rej" : "grp";
  return <span className={"rp-to " + kind} title={gone ? "这个策略组已不存在" : undefined}>
    {kind === "dir" ? <Icon name="direct" /> : kind === "rej" ? <Icon name="ban" /> : !gone && group?.icon ? group.icon + " " : null}
    {targetName(target, groups)}{gone && <span className="vh">（已不存在）</span>}
  </span>;
}
export function TargetOptions({ data, follow }: { data: RulesPageData; follow?: string }) {
  return <>{follow && <option value="">{follow}</option>}<optgroup label="基本"><option value="DIRECT">直连(不走代理)</option><option value="REJECT">拦截</option></optgroup>
    <optgroup label="策略组">{data.groups.map(group => <option key={group.id} value={"group:" + group.id}>{group.icon ? group.icon + " " : ""}{group.name}</option>)}</optgroup></>;
}
export function RuleWarning({ children }: { children: ReactNode }) {
  return <div className="callout rp-warn"><Icon name="warn" className="ico-warn" /><div>{children}</div></div>;
}
