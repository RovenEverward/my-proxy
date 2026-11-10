import { NativeSelect } from "@/components/native-select";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "../../components/ui/button";
import { Card, EmptyState, ErrorState, FlagText, Icon, LoadingTable, PageHeader } from "../../components/primitives";
import { useApp, usePageTitle } from "../../runtime/app-context";
import { batchWrites, write } from "../../runtime/api";
import { usePageData } from "../../runtime/query";
import type { PageProps } from "../../runtime/types";
import { errorMessage, readRulesPage, RULE_CAPS, RULE_REGIONS, rulesPath, stripRule, type MigrationReport, type RouteRule, type RoutingReadSupport, type RulesPageData, type SavedProxyGroup } from "../../domain/routing";
import { BuiltinEditor } from "./BuiltinEditor";
import { GroupEditor } from "./GroupEditor";
import { RuleWarning } from "./components";
import { RuleList, type RuleDraft } from "./RuleList";
import { TestCard } from "./TestCard";

let rememberedScope = "";
export function RulesPage({ route }: PageProps) {
  const app = useApp();
  usePageTitle([{ label: "分流规则" }]);
  const tab = route[1] === "groups" ? "groups" : "rules";
  const [scope, setScope] = useState(rememberedScope);
  const [draft, setDraft] = useState<RuleDraft | null>(null);
  const previousScope = useRef<{ value: string; started: boolean } | null>(null);
  const readSupport = useMemo<RoutingReadSupport>(() => ({ sources: "unknown" }), [app.meta.server, app.meta.profile]);
  const page = usePageData(`rules:${tab}:${scope}`, read => readRulesPage(read, scope, tab === "groups", readSupport));
  const { data } = page;
  const previousUsers = useRef<string[]>([]);
  if (data) previousUsers.current = data.users;
  useEffect(() => { app.setBusy(page.loading); return () => app.setBusy(false); }, [app.setBusy, page.loading]);
  useEffect(() => { setDraft(null); }, [tab]);
  useEffect(() => {
    const previous = previousScope.current;
    if (!previous) return;
    if (page.refreshing) { previous.started = true; return; }
    if (!previous.started) return;
    if (page.error) {
      setScope(previous.value); rememberedScope = previous.value; previousScope.current = null;
      app.toast(page.error.message, true);
    } else if (data) previousScope.current = null;
  }, [page.error, page.refreshing, data, app.toast]);
  const putRules = async (list: RouteRule[], version: number, message: string) => {
    await write("PUT", rulesPath(scope), { rules: list.map(stripRule), version }); app.toast(message);
  };
  function openGroup(group?: SavedProxyGroup) { if (data) app.openDrawer(<GroupEditor data={data} original={group} />, { key: "group:" + (group?.id || "new") }); }
  function migrate(confirm = false) {
    app.confirmBox({ title: confirm ? "确认迁移到规则表？" : "把现在的分流转换成规则表？", text: "按现在的模板生成策略组与规则，订阅改由规则表生成。原来的模板保留。", ok: "转换",
      run: async () => {
        const result = await write<MigrationReport>("POST", "/api/routing/migrate", confirm ? { confirm: true } : {});
        if (result.status !== "migrated") throw new Error(result.status === "needs_confirm" ? "存过自定义 sing-box 模板，需要确认后迁移" : (result.unconvertible || result.notes || []).join(";") || result.status);
        app.toast(`已转换：${result.groups?.length || 0} 个策略组、${result.rules || 0} 条规则`);
      } });
  }
  const empty = data && !data.rules.global.length && !data.groups.length && !scope;
  return <>
    <PageHeader title="分流" description="决定每个网站怎么走：直连、走代理还是拦截。改完，客户端更新一次订阅就生效。" />
    {data && <ModeBanner data={data} onMigrate={migrate} />}
    {!empty && <div className="rp-bar"><div className="seg">{(["rules", "groups"] as const).map(value => <Button key={value} className={tab === value ? "on" : ""} data-act="rp-tab" data-k={value} aria-pressed={tab === value} onClick={() => { setDraft(null); app.navigate("rules/" + value); }}>{value === "rules" ? "规则" : "策略组"}<em>{data ? value === "rules" ? data.rules.global.length + 1 : data.groups.length : ""}</em></Button>)}</div><span className="sp" />
      {tab === "rules" && <><label className="sub" htmlFor="rp-scope">作用于</label><NativeSelect className={"in w200" + (page.loading ? " busy" : "")} id="rp-scope" disabled={!data} value={scope} aria-busy={page.loading || undefined} onChange={event => { previousScope.current = { value: scope, started: false }; rememberedScope = event.target.value; setScope(event.target.value); setDraft(null); }}><option value="">全部用户(全局)</option>{(data?.users || previousUsers.current).map(name => <option key={name} value={name}>只改 {name}</option>)}</NativeSelect></>}
    </div>}
    {page.error && !data ? <ErrorState title="分流规则" error={page.error} onRetry={() => void page.refresh()} /> : !data ? <LoadingTable rows={6} columns={4} /> : <>
      {page.error && <p className="c-bad" role="alert">读取分流规则失败：{page.error.message}<Button size="sm" data-act="reload" onClick={() => void page.refresh()}>重试</Button></p>}
      {empty ? <RulesStart onMigrate={() => migrate()} /> : <div className="rp-main"><div>{tab === "rules" ? <RuleList key={scope} data={data} scope={scope} draft={draft} onDraft={setDraft} onSave={putRules} onBuiltin={() => app.openDrawer(<BuiltinEditor data={data} onSave={putRules} />, { key: "rule-builtins" })} /> : <GroupsCard data={data} onOpen={openGroup} />}</div><div className="rp-side"><TestCard data={data} scope={scope} /></div></div>}
    </>}
  </>;
}
function ModeBanner({ data, onMigrate }: { data: RulesPageData; onMigrate: (confirm?: boolean) => void }) {
  const app = useApp(), report = data.routing.migration;
  return <>{report && (report.status === "needs_confirm" || report.status === "blocked") && <RuleWarning><b>{report.status === "needs_confirm" ? "需要确认后才能改用规则表" : "旧模板里有转换不了的规则"}</b>
    {report.status === "needs_confirm" ? <div>存过自定义 sing-box 模板：迁移后 sing-box 的分流改为跟随规则表(模板里的其他部分照用)。</div> : <div>{report.unconvertible?.slice(0, 5).map((value, index) => <span key={index}><code>{value}</code> </span>)}{(report.unconvertible?.length || 0) > 5 ? `等 ${report.unconvertible?.length} 条` : ""}</div>}
    {report.status === "needs_confirm" && app.can("POST", "/api/routing/migrate") && <div className="mt6"><Button size="sm" data-act="rp-migrate" data-confirm="1" onClick={() => onMigrate(true)}>确认并迁移</Button></div>}
  </RuleWarning>}{!!data.routing.warnings?.length && <RuleWarning>{data.routing.warnings.map((warning, index) => <div key={index}>{warning}</div>)}</RuleWarning>}</>;
}
function GroupsCard({ data, onOpen }: { data: RulesPageData; onOpen: (group?: SavedProxyGroup) => void }) {
  const app = useApp();
  const used = (id: number) => data.rules.global.filter(rule => rule.target === "group:" + id).length + (data.rules.final === "group:" + id ? 1 : 0);
  const conditions = (group: SavedProxyGroup) => [...(data.origins[group.id] || []).map(origin => origin === "managed" ? "自建节点" : data.sources.find(source => "source:" + source.id === origin)?.name || "来源已删除"), ...(group.regions || []).map(value => RULE_REGIONS[value] || value), ...(group.protocols || []), ...(group.capabilities || []).map(value => RULE_CAPS[value] || value), ...(group.tags?.length ? ["旧标签条件(已不生效)"] : [])].join(" + ") || "全部节点";
  return <Card><div className="ch">策略组：按条件自动收节点，新加的节点符合条件会自己进来<span className="sp" />{app.can("POST", "/api/proxy-groups") && <Button variant="primary" size="sm" data-act="gp-edit" data-id="" onClick={() => onOpen()}><Icon name="plus" />新建策略组</Button>}</div>
    {data.groups.length ? data.groups.map(group => {
      const members = data.previews[group.id]?.filter(entry => entry.admitted).map(entry => entry.name);
      return <div className="row rp-g" data-act="gp-edit" data-id={group.id} tabIndex={0} role="button" key={group.id} onClick={() => onOpen(group)} onKeyDown={event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onOpen(group); } }}><span className="rp-gi">{group.icon || "◎"}</span><div className="grow"><b className="bold">{group.name}</b> <span className="pill">{group.mode === "auto" ? "自动选最快" : "手动选"}</span><div className="sub"><FlagText>{conditions(group)}</FlagText> · 被 {used(group.id)} 条规则使用</div><div className="chips mt6">{members?.map((name, index) => <span className="tag" key={index}><FlagText>{name}</FlagText></span>)}{members && !members.length && <span className="c-warn">没有符合条件的节点，订阅里这个组只有「直连」</span>}</div></div><Icon name="arrow" /></div>;
    }) : <EmptyState icon="tpl" title="还没有策略组" description="策略组是规则的去向：按地区、协议、能力检测挑节点。点右上角「新建策略组」开始。" />}
  </Card>;
}
function RulesStart({ onMigrate }: { onMigrate: () => void }) {
  const app = useApp(); const [busy, setBusy] = useState("");
  async function start(kind: "all" | "blank") {
    if (busy) return;
    setBusy(kind);
    try { await batchWrites(async () => {
      const group = await write<SavedProxyGroup>("POST", "/api/proxy-groups", { name: "自动选择", icon: "⚡", mode: "auto" });
      if (kind === "all") await write("PUT", "/api/rules", { rules: [{ type: "set", value: "lan", target: "DIRECT", enabled: true }], version: 0 });
      await write("PUT", "/api/rules/final", { target: "group:" + group.id });
    }); } catch (reason) { app.toast(errorMessage(reason), true); } finally { setBusy(""); }
  }
  return <Card className="rp-empty"><div className="rp-flow"><div className="rp-fb"><b>你打开一个网站</b><span className="mono">netflix.com</span></div><span className="rp-arr"><Icon name="arrow" /></span><div className="rp-fb wide"><b>按规则从上往下匹配</b><span>广告<Icon name="arrow" className="rp-fa" />拦截</span><span>国内网站<Icon name="arrow" className="rp-fa" />直连</span><span className="on">流媒体<Icon name="arrow" className="rp-fa" />📺 流媒体</span><span>其他全部<Icon name="arrow" className="rp-fa" />⚡ 自动选择</span></div><span className="rp-arr"><Icon name="arrow" /></span><div className="rp-fb"><b>走对应的出口</b><span>策略组挑出的节点</span></div></div>
    <h4>还没有分流规则</h4><p className="sub">先选一套起步方案，之后随时增删、拖动排序、改去向。</p>
    {app.can("POST", "/api/routing/migrate") && <div className="rp-starts"><button type="button" className="rp-start rec" data-act="rp-migrate" onClick={onMigrate}><span className="pill on">推荐</span><b>沿用现在的分流</b><span>把现在的模板转换成规则表：国内直连、拦广告、各类网站走对应分组，其他自动选最快。</span><em>转换 <Icon name="arrow" /></em></button><button type="button" className={"rp-start" + (busy === "all" ? " busy" : "")} data-act="rp-blank" data-k="all" disabled={!!busy} onClick={() => void start("all")}><b>全部走代理</b><span>只建一个「自动选择」组，除了局域网，所有网站都走它。</span><em>用这套 <Icon name="arrow" /></em></button><button type="button" className={"rp-start" + (busy === "blank" ? " busy" : "")} data-act="rp-blank" data-k="blank" disabled={!!busy} onClick={() => void start("blank")}><b>从空白开始</b><span>只有一条「其他全部」走「自动选择」，你自己一条条加。</span><em>开始 <Icon name="arrow" /></em></button></div>}
  </Card>;
}
