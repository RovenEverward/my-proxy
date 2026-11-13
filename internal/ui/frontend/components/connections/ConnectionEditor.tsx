import { useState } from "react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { DrawerBody, DrawerHeader, Field, FlagText, Icon } from "../primitives";
import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import { usePageData } from "../../runtime/query";
import { addrOfHost, asModelHost, asModelInbound, customAddr, customName, inbKind, inbNeedsApply, inbPath, inbTitle, nameOfHost, type ConnectionHost, type ConnectionInbound } from "../../domain/connections";
import { errorMessage, nodeLabel, type NodeView } from "../../domain/nodes";
import { ApplyFooter, DrawerReadState, ErrorMessage, useConnectionFinish } from "./EditorFrame";
import { collectInbound, InboundFields, inboundDraft } from "./InboundFields";
import { HostEditor } from "./HostEditor";
import { useConnectionActions } from "./use-connection-actions";

interface Props { inboundId?: number; presetNode?: number; title?: string; full?: boolean; onChanged: () => Promise<void> }
interface Data { inbound: ConnectionInbound; hosts: ConnectionHost[]; nodes: NodeView[] }
export function ConnectionEditor(props: Props) {
  const query = usePageData(`drawer:connection:${props.inboundId || `new:${props.presetNode}`}:${props.full ? "full" : "simple"}`, async read => {
    const [inbound, hosts, nodes] = await Promise.all([
      props.inboundId ? read<ConnectionInbound>(inbPath(props.inboundId)) : null,
      props.inboundId ? read<ConnectionHost[]>(`${inbPath(props.inboundId)}/hosts`) : Promise.resolve([]), read<NodeView[]>("/api/nodes"),
    ]);
    if (!nodes.length) throw new Error("先添加一台节点");
    return { inbound: inbound || { id: 0, node_id: props.presetNode || nodes[0].id, protocol: "vless", port: 0, network: "tcp", security: "none", wg_mtu: 1420 }, hosts, nodes };
  }, { poll: 0 });
  if (!query.data) return <DrawerReadState title={props.title || (props.inboundId ? "编辑连接方式" : "添加连接方式")} loading={query.loading} error={query.error} retry={() => void query.refresh()} />;
  return <ConnectionForm {...props} data={query.data} />;
}
function ConnectionForm({ inboundId, full, onChanged, data }: Props & { data: Data }) {
  const app = useApp();
  const finish = useConnectionFinish(onChanged);
  const actions = useConnectionActions(onChanged);
  const isNew = !inboundId;
  const base = data.inbound, host = data.hosts[0];
  const [draft, setDraft] = useState(() => ({ inbound: inboundDraft(base), name: host?.remark || "", address: host?.address || "" }));
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const node = data.nodes.find(value => value.id === Number(draft.inbound.node_id)) || data.nodes[0];
  const initial = collectInbound(inboundDraft(base), base).value;
  const collected = collectInbound(draft.inbound, base);
  const need = isNew || inbNeedsApply({ ...base, ...initial }, { ...base, ...collected.value });
  const openHost = (hostId?: number) => app.openDrawer(<HostEditor inbound={base} node={node} hostId={hostId} onChanged={onChanged} />, { key: `host:${base.id}:${hostId || "new"}` });
  const save = async (apply: boolean) => {
    if (busy) return;
    setError(collected.errors.join("\n"));
    if (collected.errors.length) return;
    setBusy(true);
    try {
      const messages: string[] = [];
      if (isNew) {
        const created = await write<ConnectionInbound>("POST", "/api/inbounds", asModelInbound(collected.value));
        messages.push(`已创建连接方式 ${inbTitle(created, node)}`);
      } else {
        const next = asModelInbound({ ...base, ...collected.value });
        if (full || JSON.stringify(next) !== JSON.stringify(asModelInbound(base))) {
          const fresh = await api<ConnectionInbound>(inbPath(base.id));
          const updated = await write<ConnectionInbound>("PUT", inbPath(base.id), asModelInbound({ ...asModelInbound(fresh), ...collected.value, node_id: fresh.node_id }));
          messages.push(`已更新连接方式 ${inbTitle(updated, node)}`);
        }
        const name = draft.name.trim(), address = draft.address.trim();
        if (!full && (host ? name !== (host.remark || "") || address !== (host.address || "") : !!(name || address))) {
          const freshHost = host ? (await api<ConnectionHost[]>(`${inbPath(base.id)}/hosts`)).find(value => value.id === host.id) : null;
          if (host && !freshHost) throw new Error("对外地址已经不存在了");
          await write(host ? "PUT" : "POST", host ? `/api/hosts/${host.id}` : `${inbPath(base.id)}/hosts`, asModelHost({ ...freshHost, id: host?.id || 0, inbound_id: base.id, remark: name, address }));
          messages.push(host ? `已更新对外地址「${name || base.sub_name || nodeLabel(node)}」` : name ? `已创建对外地址「${name}」` : "已创建对外地址，名字跟随节点名");
        }
      }
      await finish(node, messages.join("；") || "已保存", need, apply);
    } catch (reason) { setError(errorMessage(reason)); setBusy(false); }
  };
  const fields = <InboundFields draft={draft.inbound} onChange={inbound => setDraft({ ...draft, inbound })} nodes={data.nodes} isNew={isNew} />;
  const remove = app.can("DELETE", "/api/inbounds/x") && !isNew && <div className="dz"><div><b>删除连接</b><span>它的 {data.hosts.length} 个入口一起删掉</span></div><span className="sp" /><Button type="button" size="sm" data-act="conn-rm" data-id={base.id} data-tag={base.tag} data-title={inbTitle(base, node)} onClick={() => actions.removeInbound(base, node)}>删除</Button></div>;
  return <form id={full || isNew ? "inb-form" : "conn-form"} noValidate onSubmit={event => { event.preventDefault(); void save(true); }}>
    <DrawerHeader title={<FlagText>{isNew ? "添加连接方式" : full ? `编辑连接方式 ${inbTitle(base, node)}` : nameOfHost(base, host, node)}</FlagText>} description={full || isNew ? undefined : <FlagText>{`${inbKind(base)} · 端口 ${base.port} · 在 ${nodeLabel(node)} 上`}</FlagText>} onClose={app.closeOverlays} />
    <DrawerBody>{full || isNew ? <>{fields}<p className="sub">{isNew ? "REALITY 的密钥对由控制面生成。" : "只改用户数上限不用应用到节点。"}</p>{remove}</> : <>
      <Field label="名称" htmlFor="d-name" hint={customName(host) ? <>已自定义 · <button type="button" className="lnk" data-act="conn-fix" data-inb={base.id} data-id={host.id} onClick={() => { void actions.restoreName(base, host, node).catch(reason => setError(errorMessage(reason))); }}>改回跟随节点名</button></> : "留空就跟着节点名走"}><Input className="in" id="d-name" name="d_name" value={draft.name} placeholder={base.sub_name || nodeLabel(node)} autoComplete="off" onChange={event => setDraft({ ...draft, name: event.target.value })} /></Field>
      <Field label="地址" htmlFor="d-addr" hint={customAddr(host) ? "已自定义 · 留空改回节点地址" : "留空用节点地址"}><Input className="in mono" id="d-addr" name="d_addr" value={draft.address} placeholder={node.address} autoComplete="off" onChange={event => setDraft({ ...draft, address: event.target.value })} /></Field>
      <details className="adv2"><summary><Icon name="arrow" />高级<span>协议、端口、{data.hosts.length > 1 ? `${data.hosts.length} 个入口` : "入口"}</span></summary><div className="adv-b">{fields}
        <div className="ae-h">入口<span>{data.hosts.length ? `${data.hosts.length} 个 · 订阅里每个入口一行` : "还没有入口 · 订阅会用入站自己的地址"}</span><span className="sp" />{app.can("PUT", "/api/hosts/x") && <Button type="button" size="sm" variant="ghost" data-act="conn-host-new" data-inb={base.id} onClick={() => openHost()}><Icon name="plus" />入口</Button>}</div>
        {!!data.hosts.length && <div className="ae">{data.hosts.map(value => <div className="ae-r" key={value.id}><div><b className={customName(value) ? "" : "dim"}><FlagText>{nameOfHost(base, value, node)}</FlagText></b><span className="mono">{addrOfHost(base, value, node)}</span></div><span className="ptag lite">{value.address ? "自定义地址" : "节点地址"}</span><Button type="button" size="icon" title="编辑这个入口" aria-label="编辑这个入口" data-act="conn-host-edit" data-inb={base.id} data-id={value.id} onClick={() => openHost(value.id)}><Icon name="pen" /></Button></div>)}</div>}{remove}
      </div></details>
    </>}<ErrorMessage id="i-err">{error}</ErrorMessage></DrawerBody><ApplyFooter node={node} need={need} isNew={isNew} busy={busy} save={apply => void save(apply)} />
  </form>;
}
