// 探测点页(UC-21/30):节点页第三个页签,只有探测点名册。
// 探测点是境内常驻主机,跑起来后常驻执行 CDN IP 优选任务。
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, DrawerBody, DrawerFooter, DrawerHeader, EmptyState, ErrorState, Field, Icon, LoadingTable, PageHeader, Tabs } from "@/components/primitives";
import { api, write } from "@/runtime/api";
import { useApp, usePageTitle } from "@/runtime/app-context";
import { usePageData } from "@/runtime/query";
import type { Read, Status } from "@/runtime/types";
import { NODE_TABS, dateTime, errorMessage, sinceText } from "@/domain/nodes";
import type { ProbePoint, ProbeCreated } from "@/domain/probes";

interface ProbesData { probes: ProbePoint[]; fingerprint: string }

async function loadProbes(read: Read): Promise<ProbesData> {
  const [probes, status] = await Promise.all([
    read<{ probes?: ProbePoint[] }>("/api/probes").then(d => d.probes ?? []),
    read<Status>("/api/status").catch(() => null),
  ]);
  return { probes, fingerprint: status?.tls_fingerprint || "" };
}

export function ProbesPage() {
  const app = useApp();
  usePageTitle([{ label: "节点" }, { label: "探测点" }]);
  const query = usePageData<ProbesData>("nodes:probes", loadProbes);
  const writable = app.can("POST", "/api/probes");

  const addProbe = () => app.openDrawer(<AddProbeDrawer fingerprint={query.data?.fingerprint || ""} onCreated={() => void query.refresh(false)} />, { key: "probe:add" });

  const rotate = (p: ProbePoint) => app.confirmBox({
    title: `重发 ${p.name} 的接入命令？`, text: "旧凭据立即失效，探测点要拿新命令重新接入。", ok: "重发命令", danger: true,
    run: async () => {
      const res = await write<{ join_command: string }>("POST", `/api/probes/${p.id}/rotate`);
      app.openDrawer(<JoinCommandDrawer command={res.join_command} fingerprint={query.data?.fingerprint || ""} name={p.name} />, { key: `probe:rotate:${p.id}` });
    },
  });
  const remove = (p: ProbePoint) => app.confirmBox({
    title: `删除探测点 ${p.name}？`, text: "它的凭据立即失效，CDN 优选不再有这台来源。", ok: "删除", danger: true,
    run: async () => { await write("DELETE", `/api/probes/${p.id}`); app.toast(`已删除探测点 ${p.name}`); void query.refresh(false); },
  });

  return <>
    <PageHeader title="节点" description="探测点是境内常驻主机，常驻执行 CDN IP 优选" actions={writable && <Button variant="primary" data-act="probe-add" onClick={addProbe}><Icon name="plus" />添加探测点</Button>} />
    <Tabs items={NODE_TABS} value="probes" />
    {!query.data ? query.error ? <ErrorState title="探测点" error={query.error} onRetry={() => void query.refresh()} /> : <LoadingTable rows={4} columns={4} /> : <>
      {query.error && <ErrorState title="探测点" error={query.error} onRetry={() => void query.refresh()} />}
      <Card title="探测点">
        {!query.data.probes.length ? <EmptyState icon="nodes" title="还没有探测点"
          description="装一台境内常驻探测点后，它常驻替控制面做 CDN IP 优选。"
          actions={writable && <Button size="sm" variant="primary" data-act="probe-add" onClick={addProbe}><Icon name="plus" />添加探测点</Button>} />
          : <div className="tw"><table><thead><tr><th>探测点</th><th>状态</th><th className="hide-m">最近上报</th><th className="hide-m">接入于</th><th /></tr></thead><tbody>
            {query.data.probes.map(p => <tr key={p.id}>
              <td><b className="bold">{p.name}</b>{p.last_ip && <div className="sub mono">{p.last_ip}</div>}</td>
              <td><span className="st"><span className={`dot ${p.online ? "ok" : "bad"}`} />{p.online ? "在线" : p.last_seen_at ? "离线" : "未接入"}</span></td>
              <td className="hide-m sub">{p.last_seen_at ? sinceText(p.last_seen_at) : "—"}</td>
              <td className="hide-m sub">{dateTime(p.created_at)}</td>
              <td className="num"><span className="row-btns">
                {writable && <Button size="icon" title={`重发 ${p.name} 的接入命令`} aria-label={`重发 ${p.name} 的接入命令`} data-act="probe-rotate" data-id={p.id} onClick={() => rotate(p)}><Icon name="key" /></Button>}
                {writable && <Button size="icon" title={`删除 ${p.name}`} aria-label={`删除 ${p.name}`} data-act="probe-del" data-id={p.id} onClick={() => remove(p)}><Icon name="trash" /></Button>}
              </span></td>
            </tr>)}
          </tbody></table></div>}
        <div className="probe-note sub">探测点只拿「领任务、回结果」两个权限，凭据可单独吊销。</div>
      </Card>
    </>}
  </>;
}

// AddProbeDrawer 出一次性接入命令;主机跑完后出现在名册里。
function AddProbeDrawer({ fingerprint, onCreated }: { fingerprint: string; onCreated: () => void }) {
  const app = useApp();
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<ProbeCreated | null>(null);
  const submit = async () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    setBusy(true);
    try {
      const res = await write<ProbeCreated>("POST", "/api/probes", { name: trimmed });
      setCreated(res); onCreated();
    } catch (error) { app.toast(errorMessage(error), true); }
    finally { setBusy(false); }
  };
  if (created) return <JoinCommandDrawer command={created.join_command} fingerprint={fingerprint} name={created.name} />;
  return <>
    <DrawerHeader title="添加探测点" description="探测点是你自己的境内常驻主机，负责 CDN IP 优选" onClose={app.closeOverlays} />
    <DrawerBody>
      <Field label="名字" htmlFor="probe-name" hint="出现在名册里的名字，比如「家 · Mac mini」">
        <Input id="probe-name" placeholder="家 · Mac mini" value={name} autoFocus
          onChange={e => setName(e.target.value)} onKeyDown={e => { if (e.key === "Enter") void submit(); }} />
      </Field>
    </DrawerBody>
    <DrawerFooter><Button onClick={app.closeOverlays}>取消</Button>
      <Button variant="primary" data-act="probe-create" disabled={!name.trim()} loading={busy} onClick={() => void submit()}>生成接入命令</Button></DrawerFooter>
  </>;
}

// JoinCommandDrawer 给可复制的接入命令;https 地址要带上证书指纹。
function JoinCommandDrawer({ command, fingerprint, name }: { command: string; fingerprint: string; name: string }) {
  const app = useApp();
  const needsFp = command.startsWith("mpx probe run --server https");
  const full = needsFp && fingerprint ? `${command} --fp ${fingerprint}` : command;
  return <>
    <DrawerHeader title={`接入 ${name}`} description="命令只能用一次，凭据不落日志" onClose={app.closeOverlays} />
    <DrawerBody>
      <Field label="在这台主机上执行" hint={needsFp && !fingerprint ? "https 地址需要 --fp 证书指纹;在控制面上跑 mpx status 能看到" : undefined}>
        <div className="cmd probe-cmd">{full}</div>
      </Field>
      <div className="mt6"><Button size="sm" data-act="probe-copy" onClick={() => { void navigator.clipboard?.writeText(full); app.toast("接入命令已复制"); }}><Icon name="copy" />复制命令</Button></div>
      <ol className="steps">
        <li>把命令拷到境内常驻主机上跑(比如家里的 Mac mini)。</li>
        <li>主机用这一次性命令换取正式凭据，随后常驻执行 CDN 优选。</li>
        <li>接入后出现在列表里；凭据可随时轮换或删除。</li>
      </ol>
    </DrawerBody>
    <DrawerFooter><Button variant="primary" onClick={app.closeOverlays}>完成</Button></DrawerFooter>
  </>;
}
