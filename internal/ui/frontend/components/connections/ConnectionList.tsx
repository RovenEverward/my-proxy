import { useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "../ui/button";
import { FlagText, Icon, LoadingTable } from "../primitives";
import { useApp } from "../../runtime/app-context";
import { addrOfHost, customAddr, customName, inbKind, inbShortKind, inbTitle, nameMismatch, nameOfHost, type ConnectionData, type ConnectionHost, type ConnectionInbound } from "../../domain/connections";
import { errorMessage, type NodeView } from "../../domain/nodes";
import { AddConnection } from "./AddConnection";
import { ConnectionEditor } from "./ConnectionEditor";
import { HostEditor } from "./HostEditor";
import { useConnectionActions } from "./use-connection-actions";
import { Menu } from "./Menu";

export function ConnectionList({ data, node, onChanged }: { data?: ConnectionData; node: NodeView; onChanged: () => Promise<void> }) {
  const app = useApp();
  const actions = useConnectionActions(onChanged);
  const [expanded, setExpanded] = useState<Set<number>>(() => new Set());
  const [menu, setMenu] = useState<number | null>(null);
  const root = useRef<HTMLElement>(null);
  useEffect(() => {
    if (menu === null) return;
    const close = (event: MouseEvent) => { if (!(event.target instanceof Element) || !event.target.closest(".menu,[data-act=conn-menu]")) setMenu(null); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") { setMenu(null); root.current?.querySelector<HTMLButtonElement>(`[data-act="conn-menu"][data-id="${menu}"]`)?.focus(); } };
    document.addEventListener("click", close); document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("click", close); document.removeEventListener("keydown", escape); };
  }, [menu]);
  if (!data) return <div data-conn-groups aria-busy="true"><LoadingTable rows={6} columns={4} /></div>;
  const currentNode = data.nodes.find(value => value.id === node.id) || node;
  const inbounds = data.inbounds.filter(inbound => inbound.node_id === node.id);
  const openEditor = (inbound: ConnectionInbound) => app.openDrawer(<ConnectionEditor inboundId={inbound.id} title={inbTitle(inbound, currentNode)} onChanged={onChanged} />, { key: `connection:${inbound.id}` });
  const openHost = (inbound: ConnectionInbound, host?: ConnectionHost) => app.openDrawer(<HostEditor inbound={inbound} node={currentNode} hostId={host?.id} onChanged={onChanged} />, { key: `host:${inbound.id}:${host?.id || "new"}` });
  const custom = (children: ReactNode, on: boolean, mono = false) => <span className={`cx-v ${on ? "on" : ""} ${mono ? "mono" : ""}`} title={on ? "已自定义" : undefined}><span className="t">{children}</span>{on && <Icon name="pen" className="cx-pen" />}</span>;
  return <section ref={root} className="ng conn-node" data-conn-groups data-node={node.id}><div className="cx-list">{inbounds.map(inbound => {
    const hosts = data.hosts[inbound.id] || [], host = hosts[0], many = hosts.length > 1, open = expanded.has(inbound.id);
    const mismatch = customName(host) ? nameMismatch(host.remark || host.sub_name || "", currentNode) : null;
    return <div className={`cx ${open ? "open" : ""}`} key={inbound.id}><div className="cx-row" tabIndex={0} role="button" data-act="conn-edit" data-id={inbound.id} onClick={event => { if (!(event.target instanceof Element) || !event.target.closest("button,.menu")) openEditor(inbound); }} onKeyDown={event => { if (event.target === event.currentTarget && (event.key === "Enter" || event.key === " ")) { event.preventDefault(); openEditor(inbound); } }}>
      <span className="cx-p"><span className="ptag" title={inbKind(inbound)}>{inbShortKind(inbound)}</span></span><span className="cx-port mono">:{inbound.port}</span>
      <span className="cx-name">{custom(<FlagText>{nameOfHost(inbound, host, currentNode)}</FlagText>, customName(host))}{many && <button type="button" className="cx-more" data-act="conn-exp" data-id={inbound.id} aria-expanded={open} onClick={() => setExpanded(previous => { const next = new Set(previous); if (next.has(inbound.id)) next.delete(inbound.id); else next.add(inbound.id); return next; })}>{hosts.length} 个对外地址<Icon name="down" /></button>}</span>
      <span className="cx-addr">{custom(addrOfHost(inbound, host, currentNode), customAddr(host), true)}</span><span className="cx-st" /><span className="cx-acts">
        {(app.can("PUT", "/api/inbounds/x") || app.can("PUT", "/api/hosts/x")) && <Button type="button" size="icon" title="编辑" aria-label="编辑" data-act="conn-edit" data-id={inbound.id} onClick={() => openEditor(inbound)}><Icon name="pen" /></Button>}
        <Button type="button" size="icon" title="更多" aria-label="更多" aria-haspopup="menu" aria-expanded={menu === inbound.id} data-act="conn-menu" data-id={inbound.id} onClick={() => setMenu(menu === inbound.id ? null : inbound.id)}><Icon name="more" /></Button>
        {menu === inbound.id && <Menu open>{app.can("PUT", "/api/hosts/x") && <button type="button" role="menuitem" data-act="conn-host-new" data-inb={inbound.id} onClick={() => { setMenu(null); openHost(inbound); }}><Icon name="plus" />再加一个对外地址</button>}{app.can("DELETE", "/api/inbounds/x") && <><div className="msep" /><button type="button" className="dg" role="menuitem" data-act="conn-rm" data-id={inbound.id} data-tag={inbound.tag} data-title={inbTitle(inbound, currentNode)} onClick={() => { setMenu(null); actions.removeInbound(inbound, currentNode); }}><Icon name="trash" />删除</button></>}</Menu>}
      </span></div>
      {mismatch && <div className="cx-warn"><Icon name="warn" className="wi" /><span>{mismatch}</span><Button type="button" size="sm" data-act="conn-fix" data-inb={inbound.id} data-id={host.id} onClick={() => { void actions.restoreName(inbound, host, currentNode).catch(reason => app.toast(errorMessage(reason), true)); }}><Icon name="undo" />改回跟随节点名</Button></div>}
      {many && open && <div className="cx-subs">{hosts.map(value => <div className="cx-sub" role="button" tabIndex={0} key={value.id} data-act="conn-host-edit" data-inb={inbound.id} data-id={value.id} onClick={() => openHost(inbound, value)} onKeyDown={event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); openHost(inbound, value); } }}><span className="cx-name">{custom(<FlagText>{nameOfHost(inbound, value, currentNode)}</FlagText>, customName(value))}</span><span className="cx-addr">{custom(addrOfHost(inbound, value, currentNode), customAddr(value), true)}</span><span className="cx-tg"><span className="ptag lite">{value.address ? "自定义地址" : "节点地址"}</span></span></div>)}</div>}
    </div>;
  })}</div>{!inbounds.length && <div className="ng-empty"><div className="ill"><Icon name="inb" /></div><div><b>这台节点上还没有连接</b><span>加一条，用户刷新订阅后就能用这台节点</span></div><span className="sp" />{(app.can("POST", "/api/inbounds") || app.can("PUT", "/api/hosts/x")) && <Button size="sm" variant="primary" data-act="conn-new" data-node={node.id} onClick={() => app.openDrawer(<AddConnection data={data} presetNode={node.id} onChanged={onChanged} />, { key: `connection:new:${node.id}` })}><Icon name="plus" />添加连接</Button>}</div>}</section>;
}
