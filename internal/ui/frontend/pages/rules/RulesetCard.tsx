import { useEffect, useState } from "react";
import { Button } from "../../components/ui/button";
import { Card, DrawerBody, DrawerFooter, DrawerHeader, Icon } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { usePageData } from "../../runtime/query";
import { write } from "../../runtime/api";
import { modifySettings } from "../../domain/settings";
import { sinceText } from "../../domain/nodes";
const rulesetSince = (ms?: number) => ms ? sinceText(new Date(ms).toISOString()) : "—";
import { errorMessage, rulesetCounts, rulesetStatus, type RulesetGroup, type RulesPageData } from "../../domain/routing";

// 规则集私有化分发:分流页侧栏卡 + 管理抽屉。开关改全局设置,镜像清单按内置规则组聚合。
export function RulesetCard({ data }: { data: RulesPageData }) {
  const app = useApp();
  const [busy, setBusy] = useState(false);
  // pending:乐观翻开关。PUT 回来、页数据刷新到新模式前,界面先显示点下去的状态;
  // 失败时清回 null,开关落回服务器上的真实模式。
  const [pending, setPending] = useState<boolean | null>(null);
  useEffect(() => { if (pending !== null && (data.rulesetMode === "privatized") === pending) setPending(null); }, [data.rulesetMode, pending]);
  const on = pending ?? data.rulesetMode === "privatized";
  const failing = data.rulesets.filter(group => rulesetStatus(group) === "failed");
  async function setMode(privatized: boolean) {
    if (busy) return;
    setBusy(true); setPending(privatized);
    try {
      await modifySettings(settings => { settings.ruleset_mode = privatized ? "privatized" : "upstream"; });
      app.toast(privatized ? "已开启经控制面分发，镜像陆续就绪" : "已恢复客户端直连上游");
    } catch (reason) { setPending(null); app.toast(errorMessage(reason), true); }
    finally { setBusy(false); }
  }
  return <Card title="规则集分发" actions={<>{failing.length > 0 && <span className="pill warn">{failing.length} 个失败</span>}</>}>
    <div className="pad16 rs-pad">
      <div className="rs-t"><span className="rs-tl">经控制面分发</span>
        {app.can("PUT", "/api/settings") && <label className="sw" title={on ? "改回客户端直连上游" : "客户端改从控制面下载规则集"}><input type="checkbox" data-act="rs-toggle" checked={on} disabled={busy} onChange={event => void setMode(event.target.checked)} /><i /></label>}</div>
      {on ? <div className="rs-l">{data.rulesets.length ? data.rulesets.map(group => <MirrorRow key={group.key} group={group} compact />) : <p className="sub">规则表还没有引用规则组。</p>}</div>
        : <p className="sub">客户端直连上游规则集;连不上时对应规则在客户端分流失效。</p>}
      <div className="rs-bt"><Button size="sm" className="ton" data-act="rs-manage" onClick={() => app.openDrawer(<RulesetDrawer mode={data.rulesetMode} />, { key: "rulesets" })}>管理镜像</Button><span className="sub">失败镜像用上次结果</span></div>
    </div>
  </Card>;
}

function MirrorRow({ group, compact }: { group: RulesetGroup; compact?: boolean }) {
  const status = rulesetStatus(group);
  const { ready: readyCount, total: files } = rulesetCounts(group);
  return <div className="rs-mr" data-k={group.key}>
    <span className={"dot" + (status === "ready" ? " ok" : status === "failed" ? " warn" : status === "pending" ? " warn" : "")} />
    <div className="grow"><b className="rs-mn">{group.name}</b>{!compact && group.last_error && <div className="c-warn rs-err">{group.last_error}</div>}</div>
    <span className="sub rs-mm">{files} 文件 · {status === "refreshing" ? "刷新中" : status === "ready" ? `就绪 · ${rulesetSince(group.last_attempt_at)}` : status === "failed" ? `失败 · ${rulesetSince(group.last_attempt_at)}` : status === "partial" ? `${readyCount}/${files} 就绪` : "等待拉取"}</span>
  </div>;
}

function RulesetDrawer({ mode }: { mode: string }) {
  const app = useApp();
  const page = usePageData("rulesets", read => read<RulesetGroup[]>("/api/rulesets").catch(() => []));
  const [busy, setBusy] = useState<Set<string>>(new Set());
  const [modeBusy, setModeBusy] = useState(false);
  // 抽屉不跟着页数据走,开关是抽屉自己的乐观状态;失败翻回。
  const [on, setOn] = useState(mode === "privatized");
  useEffect(() => () => setBusy(new Set()), []);
  async function setMode(privatized: boolean) {
    if (modeBusy) return;
    setModeBusy(true); setOn(privatized);
    try {
      await modifySettings(settings => { settings.ruleset_mode = privatized ? "privatized" : "upstream"; });
      app.toast(privatized ? "已开启经控制面分发，镜像陆续就绪" : "已恢复客户端直连上游");
    } catch (reason) { setOn(!privatized); app.toast(errorMessage(reason), true); }
    finally { setModeBusy(false); }
  }
  async function refreshGroup(key?: string) {
    if (!app.can("POST", "/api/rulesets")) return;
    setBusy(previous => new Set(previous).add(key || "*"));
    try {
      await write("POST", "/api/rulesets/refresh", key ? { keys: [key] } : {});
      void page.refresh();
    } catch (reason) { app.toast(errorMessage(reason), true); }
    finally { setBusy(previous => { const next = new Set(previous); next.delete(key || "*"); return next; }); }
  }
  const groups = page.data || [];
  const failing = groups.filter(group => rulesetStatus(group) === "failed");
  return <>
    <DrawerHeader title="镜像管理" description="控制面把规则集取到本地，客户端从控制面地址下载。" onClose={app.closeOverlays} />
    <DrawerBody>
      <div className="rs-t rs-td"><span className="rs-tl">经控制面分发<span className="sub rs-th">开启后开始拉取镜像；未就绪的规则集仍输出上游地址，订阅不出错。</span></span>
        {app.can("PUT", "/api/settings") && <label className="sw"><input type="checkbox" data-act="rs-toggle" checked={on} disabled={modeBusy} onChange={event => void setMode(event.target.checked)} /><i /></label>}</div>
      <div className="rs-hd"><span className="sub">镜像集(按规则组)</span><span className="sub">状态</span></div>
      {groups.map(group => <div className="rs-gr" key={group.key}>
        <div className="rs-mr">
          <span className={"dot" + ({ ready: " ok", failed: " warn", pending: " warn" } as Record<string, string>)[rulesetStatus(group)] || ""} />
          <div className="grow"><b className="rs-mn">{group.name}</b><div className="sub">{group.clash.total} 个 clash + {group.sing_box.total} 个 srs</div>
            {group.last_error && <div className="c-warn rs-err">{group.last_error}</div>}</div>
          <span className="sub rs-mm">{({ ready: `就绪 · ${rulesetSince(group.last_attempt_at)}`, refreshing: "刷新中……", failed: `刷新失败 · 用上次结果`, partial: `${rulesetCounts(group).ready}/${rulesetCounts(group).total} 就绪`, pending: "等待拉取" } as Record<string, string>)[rulesetStatus(group)]}</span>
          {app.can("POST", "/api/rulesets") && <Button size="icon-sm" className="rs-rr" title={"刷新 " + group.name} data-act="rs-refresh" data-k={group.key} loading={busy.has(group.key) || group.refreshing} onClick={() => void refreshGroup(group.key)}><Icon name="refresh" /></Button>}
        </div>
      </div>)}
      {!groups.length && !page.loading && <p className="sub pad16">规则表还没有引用规则组，没有要镜像的内容。</p>}
      {failing.length > 0 && <div className="callout mt"><Icon name="warn" className="ico-warn" /><div>{failing.map(group => group.name).join("、")}刷新失败，正在使用上次结果。客户端仍从镜像地址下载，内容不丢。</div></div>}
    </DrawerBody>
    <DrawerFooter><Button size="sm" className="ton" data-act="rs-refresh-all" disabled={!app.can("POST", "/api/rulesets")} loading={busy.has("*")} onClick={() => void refreshGroup()}><Icon name="refresh" />全部刷新</Button><span className="sp" /><span className="sub">改动立即生效，订阅下次渲染时采用</span></DrawerFooter>
  </>;
}
