import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { NativeSelect } from "../../components/native-select";
import { EmptyState, ErrorState, Field, FlagText, Icon, LoadingTable, PageHeader, StatusBadge } from "../../components/primitives";
import { AddConnection } from "../../components/connections/AddConnection";
import { ConnectionList } from "../../components/connections/ConnectionList";
import { Menu } from "../../components/connections/Menu";
import { NodeWizard } from "../../components/node-wizard/NodeWizard";
import { APIError, api, write } from "../../runtime/api";
import { useApp, usePageTitle } from "../../runtime/app-context";
import { cachedPage, usePageData } from "../../runtime/query";
import type { PageProps } from "../../runtime/types";
import { loadConnections, type ConnectionData } from "../../domain/connections";
import { coreText, dateTime, driftState, errorMessage, hourMinute, nodeLabel, nodePath, nodeStateLabel, nodeStateTone, sinceText, sshText, type NodeView } from "../../domain/nodes";
import { NodeEditor } from "./NodeEditor";
import { NodeInfoDrawer } from "./NodeInfoDrawer";
import { RelabelModal } from "./RelabelModal";
import { plainSummary } from "./report-summary";
import { useNodeJobs, type NodeJob } from "./use-node-jobs";

interface NodeListData { nodes: NodeView[] }
interface Report { checked_at: string; measured_address?: string; report: string | { summary?: string[] } }
interface DetailData { node: NodeView; conn?: ConnectionData; report?: Report | { error: string } | null }
export function NodesPage({ route }: PageProps) { return route[1] ? <NodeDetailPage key={route[1]} id={Number(route[1])} /> : <NodeListPage />; }
export default NodesPage;
function NodeState({ node }: { node: NodeView }) { return <StatusBadge tone={nodeStateTone(node)}>{nodeStateLabel(node)}</StatusBadge>; }
function Drift({ node }: { node: NodeView }) { const state = driftState(node); return state.tone ? <StatusBadge tone={state.tone}>{state.text}</StatusBadge> : <span className="sub">{state.text}</span>; }
function NodeListPage() {
  const app = useApp();
  const query = usePageData<NodeListData>("nodes:list", async read => ({ nodes: await read<NodeView[]>("/api/nodes") }));
  const changed = useCallback(() => query.refresh(false), [query.refresh]);
  usePageTitle([{ label: "节点" }]);
  useEffect(() => { app.setBusy(query.refreshing); return () => app.setBusy(false); }, [query.refreshing, app.setBusy]);
  const register = () => app.openDrawer(<NodeEditor onChanged={changed} />, { key: "node:new" });
  const wizard = () => app.openDrawer(<NodeWizard onChanged={changed} onRegister={register} />, { key: "node:wizard" });
  const writable = app.can("POST", "/api/nodes");
  return <><PageHeader title="节点" description="状态来自控制面的定时检查" actions={<>{app.meta.relabel && query.data?.nodes.some(node => !node.display) && <Button data-act="relabel-open" onClick={() => app.openModal(<RelabelModal onChanged={changed} />)}><Icon name="globe" />批量识别显示名</Button>}{writable && <Button variant="primary" data-act="node-wizard" onClick={wizard}><Icon name="plus" />添加节点</Button>}</>} />
    {query.error && <ErrorState title="节点" error={query.error} onRetry={() => void query.refresh()} />}
    {!query.data ? !query.error && <LoadingTable rows={5} columns={6} /> : <div className="card">{!query.data.nodes.length ? <EmptyState icon="nodes" title="还没有节点" description="填 VPS 的地址和 SSH 密码，自动装好并加进订阅。点下面的「添加节点」开始。" actions={writable && <div style={{ display: "grid", gap: 10, justifyItems: "center" }}><Button size="sm" variant="primary" data-act="node-wizard" onClick={wizard}><Icon name="plus" />添加节点</Button><button type="button" className="lnk wlnk" data-act="node-new" onClick={register}>已能免密 SSH，直接登记</button></div>} /> : <div className="tw"><table><thead><tr><th>节点</th><th>状态</th><th className="hide-m">代理程序</th><th className="hide-m">配置</th><th className="hide-m">SSH</th><th className="num hide-m">检查于</th></tr></thead><tbody>{query.data.nodes.map(node => <tr key={node.id} data-href={`#nodes/${node.id}`} tabIndex={0} onClick={() => app.navigate(`nodes/${node.id}`)} onKeyDown={event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); app.navigate(`nodes/${node.id}`); } }}><td><b className="bold"><FlagText>{nodeLabel(node)}</FlagText></b><div className="sub mono">{node.address}</div></td><td><NodeState node={node} /></td><td className="hide-m">{node.status?.cores?.length ? node.status.cores.map(core => <span className="tag" key={core.core}>{core.core}</span>) : node.status?.version ? <><span className="tag">xray</span> <span className="sub">{node.status.version}</span></> : null}</td><td className="hide-m"><Drift node={node} /></td><td className="hide-m mono sub">{sshText(node)}</td><td className="num sub hide-m">{sinceText(node.status?.checked_at)}</td></tr>)}</tbody></table></div>}</div>}
  </>;
}
function NodeDetailPage({ id }: { id: number }) {
  const app = useApp();
  const seedNode = cachedPage<NodeListData>("nodes:list")?.nodes.find(node => node.id === id);
  const query = usePageData<DetailData>(`nodes:detail:${id}`, async read => {
    const diagnostics = read<Report>(`${nodePath(id)}/diagnostics?from=node`).catch((error: unknown) => error instanceof APIError && error.status === 404 ? null : { error: errorMessage(error) });
    const [node, conn, report] = await Promise.all([read<NodeView>(nodePath(id)), loadConnections([id], read), diagnostics]);
    return { node, conn, report };
  }, { seed: seedNode ? { node: seedNode } : null });
  const changed = useCallback(() => query.refresh(false), [query.refresh]);
  const jobs = useNodeJobs(query.data?.node || null, changed);
  const [menu, setMenu] = useState(false);
  const [checking, setChecking] = useState(false);
  const menuRoot = useRef<HTMLSpanElement>(null);
  const checkButton = useRef<HTMLButtonElement>(null);
  const applyRequest = useRef({ state: app.navigationState, at: Date.now(), consumed: false });
  if (applyRequest.current.state !== app.navigationState) applyRequest.current = { state: app.navigationState, at: Date.now(), consumed: false };
  const node = query.data?.node;
  const label = nodeLabel(node || seedNode) || "节点详情";
  usePageTitle([{ label: "节点", href: "#nodes" }, { label }]);
  useEffect(() => { app.setBusy(query.refreshing); return () => app.setBusy(false); }, [query.refreshing, app.setBusy]);
  useEffect(() => {
    if (!menu) return;
    const outside = (event: MouseEvent) => { if (event.target instanceof window.Node && !menuRoot.current?.contains(event.target)) setMenu(false); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") { setMenu(false); menuRoot.current?.querySelector<HTMLButtonElement>("[data-act=node-menu]")?.focus(); } };
    document.addEventListener("click", outside); document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("click", outside); document.removeEventListener("keydown", escape); };
  }, [menu]);
  const sync = (fromDoctor = false) => {
    if (!node || (node.status && !node.status.reachable)) return;
    app.confirmBox({ title: `把配置应用到 ${nodeLabel(node)}？`, text: "把当前配置推到节点并重启代理程序：这台节点上所有人的连接会断一次。加人、删人、到期、超限不用手动应用，控制面每 30 秒自动跟上。" + (fromDoctor ? "这次是从体检过来的：节点上的配置和控制面不一致，可能是你自己改的，也可能是别人改的。" : ""), ok: "应用到节点并重启", run: async () => { await write("POST", `${nodePath(id)}/sync`); app.toast(`已应用到 ${nodeLabel(node)}，代理程序已重启`); await api(`${nodePath(id)}?fresh=1`).catch(() => null); await changed(); } });
  };
  useEffect(() => {
    const request = applyRequest.current;
    if (app.navigationState.apply !== "1" || request.consumed || !query.data?.conn || !node) return;
    request.consumed = true;
    if (Date.now() - request.at > 10000) return;
    if (!app.can("POST", "/api/nodes/x") || (node.status && !node.status.reachable)) { app.toast("节点连不上，先点「刷新状态」，连上后再应用"); checkButton.current?.focus(); }
    else sync(true);
  }, [query.data, app.navigationState]);
  if (!node) return <><PageHeader title={label} description={<span className="sk" style={{ width: 220 }} />} />{query.error ? <ErrorState title="节点详情" error={query.error} onRetry={() => void query.refresh()} /> : <div className="grid g3">{[0, 1, 2].map(value => <div className="card stat" key={value}><span className="sk w80" /></div>)}</div>}</>;
  const status = node.status, writable = app.can("POST", "/api/nodes/x"), down = !!status && !status.reachable;
  const pending = !!query.data?.conn?.pending.includes(id);
  const sbOnly = !!status?.cores?.length && !status.cores.some(core => core.core === "xray");
  const hashDrift = !!status?.observed_hash && !!node.deployed_hash && status.observed_hash !== node.deployed_hash;
  const neverApplied = !node.deployed_hash && !sbOnly;
  const needApply = pending || hashDrift || neverApplied || !!node.template_drift;
  const applyWhy = !pending && !hashDrift && !neverApplied && node.template_drift ? "新版本改了节点配置的写法，应用一次才生效" : "";
  const run = jobs.active.find(job => job.status === "running");
  const runWhy = run ? run.kind === "diagnose" ? "这台节点正在体检，等它做完再点" : "这台节点正在安装，等它做完再点" : "";
  const check = async () => { if (checking) return; setChecking(true); try { await api(`${nodePath(id)}?fresh=1`); app.toast("状态已刷新"); await changed(); } catch (error) { app.toast(errorMessage(error), true); } finally { setChecking(false); } };
  const drain = () => {
    const perform = async () => { await write("POST", `${nodePath(id)}/${node.draining ? "undrain" : "drain"}`); app.toast(`${label} 已${node.draining ? "恢复" : "暂停"}分配`); await changed(); };
    if (node.draining) void perform().catch(error => app.toast(errorMessage(error), true));
    else app.confirmBox({ title: `暂停给 ${label} 分配？`, text: "新订阅不再包含这台节点;已连上的用户不会立刻断线，会在下次刷新订阅时换到别的节点。随时可以恢复分配。", ok: "暂停分配", run: perform });
  };
  const started = async (kind: NodeJob["kind"], result: { job?: NodeJob; reused?: boolean }, core = "") => {
    if (!result.job) throw new Error(`控制面没有说明这次${kind === "diagnose" ? "体检" : "安装"}有没有开始`);
    jobs.accept({ ...result.job, core: result.job.core || core }); app.closeModal();
    app.toast(result.reused ? `这台节点已经在${kind === "diagnose" ? "体检" : "安装"}了，接着等它做完就行` : `已开始在 ${label} 上${kind === "diagnose" ? "体检" : `安装 ${core}`}`);
    await changed();
  };
  const diagnose = () => app.confirmBox({ title: `体检 ${label}？`, text: "在节点上做一次完整体检：运行状况、能访问哪些网站、网速（不测硬件）。最长要十几分钟。可以关掉这个页面，做完会在节点上显示结果。", ok: "开始体检", run: async () => started("diagnose", await write("POST", `${nodePath(id)}/diagnostics/jobs`, { engine: "fusion", position: "node", node_id: String(id), speed: true })) });
  const install = () => {
    const options = { core: "xray", version: "" };
    app.confirmBox({ title: `在 ${label} 上安装代理程序？`, text: "控制面下载发布包（下过的会复用），送到节点上，应用当前配置并启动。慢链路上要几分钟。可以关掉这个页面，做完会在节点上显示结果。", ok: "安装", extra: <div className="two"><Field label="代理程序" htmlFor="i-core"><NativeSelect className="in" id="i-core" defaultValue="xray" onChange={event => { options.core = event.target.value; }}><option value="xray">xray</option><option value="sing-box">sing-box（hysteria2 / tuic）</option></NativeSelect></Field><Field label="版本" htmlFor="i-ver"><Input className="in mono" id="i-ver" placeholder="留空用最新版" autoComplete="off" onChange={event => { options.version = event.target.value; }} /></Field></div>, run: async () => { let path = `${nodePath(id)}/install/jobs?core=${encodeURIComponent(options.core)}`; if (options.version.trim()) path += `&version=${encodeURIComponent(options.version.trim())}`; await started("install", await write("POST", path), options.core); } });
  };
  const syncButton = (primary = false, inMenu = false) => <><Button type="button" variant={primary ? "primary" : "default"} role={inMenu ? "menuitem" : undefined} data-act="node-sync" data-id={id} data-name={node.name} data-label={label} aria-disabled={down || undefined} aria-describedby={down ? inMenu ? "sync-why-m" : "sync-why" : applyWhy ? "sync-drift-why" : undefined} data-tip={down ? "节点连不上，连上后再应用" : undefined} onClick={() => { if (!down) { setMenu(false); sync(); } }}>{inMenu && <Icon name="upload" />}应用到节点</Button>{down ? <span id={inMenu ? "sync-why-m" : "sync-why"} className="vh">节点连不上，连上后再应用</span> : applyWhy ? <span id="sync-drift-why" className="vh">{applyWhy}</span> : null}</>;
  const report = query.data?.report && !("error" in query.data.report) ? query.data.report : null;
  let summary: string[] = [];
  try { const body = report ? typeof report.report === "string" ? JSON.parse(report.report) as { summary?: string[] } : report.report : null; summary = body?.summary || []; } catch { /* Keep a malformed report out of the summary. */ }
  const jobDisabled = (kind: NodeJob["kind"]) => jobs.loading || !query.data?.conn || run?.kind === kind;
  const jobReason = (kind: NodeJob["kind"]) => jobs.loading || !query.data?.conn ? "正在读取任务状态" : run?.kind === kind ? runWhy : undefined;
  return <><PageHeader title={<><FlagText>{label}</FlagText> <NodeState node={node} /></>} description={<span className="mono">{node.address} · {sshText(node)}{node.display && <> · <span title="命令行与数据里用的名字">内部名 {node.name}</span></>}</span>} actions={<>{writable && down && needApply && syncButton()}{down ? <Button ref={checkButton} variant="primary" data-act="node-check" data-id={id} loading={checking} onClick={() => void check()}>刷新状态</Button> : writable && needApply && syncButton(true)}<span className="mw" ref={menuRoot}><Button size="icon" title="更多" aria-label="更多" aria-haspopup="menu" aria-expanded={menu} data-act="node-menu" onClick={() => setMenu(!menu)}><Icon name="more" /></Button><Menu open={menu} id="node-menu">{!down && <button type="button" role="menuitem" data-act="node-check" data-id={id} onClick={() => { setMenu(false); void check(); }}><Icon name="refresh" />刷新状态</button>}{writable && !needApply && syncButton(false, true)}{writable && <button type="button" role="menuitem" data-act="node-drain" data-id={id} data-on={node.draining ? "1" : ""} onClick={() => { setMenu(false); drain(); }}><Icon name="pause" />{node.draining ? "恢复分配" : "暂停分配"}</button>}{app.can("PUT", "/api/nodes/x") && <button type="button" role="menuitem" data-act="node-edit" data-id={id} onClick={() => { setMenu(false); app.openDrawer(<NodeEditor nodeId={id} title={label} onChanged={changed} />, { key: `node:edit:${id}` }); }}><Icon name="pen" />编辑</button>}</Menu></span></>} />
    {query.error && <ErrorState title="节点详情" error={query.error} onRetry={() => void query.refresh()} />}
    <div id="job-callout" data-on={!!run}>{run && <div className="callout stone mb" role="status"><Icon name={run.kind === "diagnose" ? "pulse" : "dl"} /><div><b>{run.kind === "diagnose" ? `正在体检（开始于 ${hourMinute(run.started_at)}）。可以离开这个页面，做完会自动显示。` : `正在安装 ${run.core || run.result?.core || ""}（开始于 ${hourMinute(run.started_at)}）…`}</b></div></div>}</div>
    <div id="job-notice" data-on={!run && !!jobs.last}>{!run && jobs.last && <div className="callout stone mb mt"><Icon name="clock" /><div>{jobs.last.kind === "diagnose" ? "上次体检没做完：控制面重启了。再点一次「体检」就行。" : "上次安装没做完：控制面重启了。再点一次「安装」就行。"}</div></div>}</div>
    {status?.error && <div className="callout bad mb" role="alert"><Icon name="warn" className="ico-bad" /><div><b>上次检查{status.reachable ? "有问题" : "连不上"}</b><div className="mono mt2">{status.error}</div></div></div>}{node.draining && <div className="callout mb"><Icon name="warn" className="ico-warn" /><div><b>已暂停分配</b> · 新订阅不再发这台节点;已连上的用户会在下次刷新订阅时换到别的节点。</div></div>}
    <div className="grid g3"><div className="card stat"><div className="l">代理程序</div><div className="v f18">{status?.version}</div><div className="s">{coreText(status)}</div></div><div className="card stat"><div className="l">节点配置</div><div className="v f18" title={status?.observed_hash}><Drift node={node} /></div><div className="s">{node.deployed_at ? `应用于 ${dateTime(node.deployed_at)}` : ""}</div></div><div className="card stat"><div className="l">证书</div><div className="v f18">{status?.certificates?.map(cert => cert.error ? "读不到" : cert.days_left < 0 ? "已过期" : `剩 ${cert.days_left} 天`).join(" · ")}</div><div className="s">{status?.certificates?.map(cert => cert.inbound_name).join(" · ") || "没有用到 TLS 的连接方式"}</div></div></div>
    <div className="card mt conn-card"><div className="ch">连接方式<small>· {query.data?.conn?.inbounds.length ?? "正在读取…"}</small><span className="sp" />{pending && <span className="tag pend" data-pending={id}>有改动未应用</span>}{app.can("POST", "/api/inbounds") && <Button size="sm" data-act="conn-new" data-node={id} aria-disabled={!query.data?.conn || undefined} aria-describedby={!query.data?.conn ? "conn-loading-why" : undefined} data-tip={!query.data?.conn ? "正在读取连接方式" : undefined} onClick={() => { if (query.data?.conn) app.openDrawer(<AddConnection data={query.data.conn} presetNode={id} onChanged={changed} />, { key: `connection:new:${id}` }); }}><Icon name="plus" />添加连接</Button>}{!query.data?.conn && <span className="vh" id="conn-loading-why">正在读取连接方式</span>}</div><ConnectionList data={query.data?.conn} node={node} onChanged={changed} /></div>
    <div className="card mt"><div className="ch">维护</div><div className="row"><div><b className="bold">体检</b><div className="sub">{report ? <>上次 {dateTime(report.checked_at)}{report.measured_address && ` · 测量地址 ${report.measured_address}`}</> : query.data?.report && "error" in query.data.report ? <span className="c-bad">{query.data.report.error}</span> : query.data && "report" in query.data ? "还没有体检报告" : "正在读取体检报告…"}</div>{!!summary.length && <><ul className="summary">{plainSummary(summary, node, query.data?.conn?.inbounds || []).map(line => <li key={line}><FlagText>{line}</FlagText></li>)}</ul><details className="sum-raw"><summary>详情<Icon name="down" /></summary><ul className="summary mono">{summary.filter(Boolean).map((line, index) => <li key={index}><FlagText>{line}</FlagText></li>)}</ul></details></>}</div><span className="sp" />{writable && <><Button size="sm" data-act="node-diagnose" data-id={id} aria-disabled={jobDisabled("diagnose") || undefined} aria-describedby={run?.kind === "diagnose" ? "diag-why" : undefined} data-tip={jobReason("diagnose")} onClick={() => { if (!jobDisabled("diagnose")) diagnose(); }}>开始体检</Button>{run?.kind === "diagnose" && <span id="diag-why" className="vh">{runWhy}</span>}</>}</div>
      {writable && <div className="row"><div><b className="bold">安装代理程序</b><div className="sub">把代理程序装到这台节点上并启动，会用当前配置</div></div><span className="sp" /><Button size="sm" data-act="node-install" data-id={id} aria-disabled={jobDisabled("install") || undefined} aria-describedby={run?.kind === "install" ? "inst-why" : undefined} data-tip={jobReason("install")} onClick={() => { if (!jobDisabled("install")) install(); }}>安装</Button>{run?.kind === "install" && <span id="inst-why" className="vh">{runWhy}</span>}</div>}
      {([["online", "在线用户", "实时查看每个用户连着几个 IP"], ["config", "配置预览", "「应用到节点」会推的 config.json，不碰节点"]] as const).map(([kind, title, description]) => <div className="row" key={kind}><div><b className="bold">{title}</b><div className="sub">{description}</div></div><span className="sp" /><Button size="sm" data-act={`node-${kind}`} data-id={id} onClick={() => app.openDrawer(<NodeInfoDrawer node={node} kind={kind} onApply={() => sync()} />, { key: `node:${kind}:${id}` })}>查看</Button></div>)}
    </div>{app.can("DELETE", "/api/nodes/x") && <div className="card mt node-danger"><div className="ch">危险操作</div><div className="row"><div><b className="bold">删除节点</b><div className="sub">它上面的连接方式会一起删掉，订阅里不再有它。节点上已装的代理程序和配置文件会留着。</div></div><span className="sp" /><Button size="sm" data-act="node-rm" data-id={id} onClick={() => app.confirmBox({ title: `删除节点 ${label}？`, text: <p>不可恢复：它上面的连接方式会一起删掉，订阅里不再有它。节点上的代理程序和配置文件会留着；只想让新订阅<span className="nw">别再发它</span>，用<span className="nw">「暂停分配」</span>。</p>, ok: "删除节点", danger: true, typed: node.name, run: async () => { await write("DELETE", nodePath(id)); app.toast(`已删除节点 ${label}`); app.navigate("nodes"); } })}>删除节点</Button></div></div>}
  </>;
}
