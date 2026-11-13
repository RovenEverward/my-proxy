import { useEffect, useRef, useState } from "react";
import { ErrorState, Icon, LoadingTable, PageHeader, Tabs } from "../../components/primitives";
import { Button } from "../../components/ui/button";
import { useApp, usePageTitle } from "../../runtime/app-context";
import { sleep } from "../../runtime/api";
import { usePageData } from "../../runtime/query";
import type { PageProps, Settings } from "../../runtime/types";
import { NotificationForm } from "./NotificationForm";
import { DevicesTab, type Device } from "./DevicesTab";

const SYSTEM_TABS = [{ key: "", label: "通知", href: "#system" }, { key: "devices", label: "设备与接入", href: "#system/devices" }, { key: "backup", label: "备份与恢复", href: "#system/backup" }, { key: "update", label: "更新与版本", href: "#system/update" }];
interface SystemData { settings?: Settings; devices?: Device[] }
export function SystemPage({ route }: PageProps) {
  const { meta, setBusy, openUpdate } = useApp();
  const tab = SYSTEM_TABS.find(item => item.key === (route[1] || "")) || SYSTEM_TABS[0];
  usePageTitle([{ label: "系统", href: "#system" }, { label: tab.label }]);
  const query = usePageData<SystemData>("system/" + tab.key, async read => {
    if (tab.key === "update") return {};
    const settings = await read<Settings>("/api/settings");
    return tab.key === "devices" ? { settings, devices: await read<Device[]>("/api/devices") } : { settings };
  });
  useEffect(() => { setBusy(query.loading); return () => setBusy(false); }, [query.loading, setBusy]);
  return <><PageHeader title="系统" description="通知、设备、备份与版本" /><Tabs items={SYSTEM_TABS} value={tab.key} />
    {query.data && query.error && <ErrorState title="系统" error={query.error} onRetry={() => void query.refresh()} />}
    {tab.key === "update" ? <div className="card pad18"><b className="b6">当前版本</b><p className="sub mt6 mb14 mono">{meta.cli_version || "—"}</p><Button variant="primary" data-act="update-open" onClick={openUpdate}><Icon name="refresh" />检查更新</Button></div>
      : !query.data ? query.error ? <ErrorState title="系统" error={query.error} onRetry={() => void query.refresh()} /> : <LoadingTable rows={6} columns={2} />
      : tab.key === "devices" ? <DevicesTab devices={query.data.devices || []} refresh={() => query.refresh(false)} />
      : tab.key === "backup" ? <BackupTab />
      : query.data.settings ? <NotificationForm settings={query.data.settings} refresh={fresh => query.refresh(fresh ?? false)} /> : null}
  </>;
}
function BackupTab() {
  const { toast } = useApp();
  const [busy, setBusy] = useState(false), lock = useRef(false), mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const download = async () => {
    if (lock.current) return; lock.current = true; setBusy(true);
    try {
      let response: Response;
      for (let attempt = 0; ; attempt++) { response = await fetch("/api/backup", { credentials: "same-origin" }); if (response.status !== 429 || attempt >= 3) break; await sleep(2000); }
      if (!response.ok) {
        let message = response.statusText;
        try { const body: unknown = await response.json(); if (body && typeof body === "object" && "error" in body && typeof body.error === "string") message = body.error; } catch { /* Use the HTTP status when there is no JSON error. */ }
        throw new Error(message);
      }
      const filename = /filename="([^"]+)"/.exec(response.headers.get("Content-Disposition") || "")?.[1] || "mpx-backup.tar.gz";
      const url = URL.createObjectURL(await response.blob()), anchor = document.createElement("a");
      anchor.href = url; anchor.download = filename; document.body.append(anchor); anchor.click(); anchor.remove(); setTimeout(() => URL.revokeObjectURL(url), 10000);
      toast("已开始下载 " + filename);
    } catch (cause) { toast(cause instanceof Error ? cause.message : String(cause), true); }
    finally { lock.current = false; if (mounted.current) setBusy(false); }
  };
  return <div className="grid g2"><div className="card pad18"><b className="b6">下载备份</b><p className="sub mt6 mb14">一份数据库快照加控制面密钥，打包成一个归档。里面有用户订阅 token、外部订阅地址及节点凭证，妥善保管。</p><Button variant="primary" data-act="backup" loading={busy} onClick={() => void download()}><Icon name="dl" />下载备份</Button></div><div className="card pad18"><b className="b6">从备份恢复</b><p className="sub mt6 mb14">从备份恢复暂不支持在窗口里操作，见文档「备份控制面」。恢复会整库替换并重启控制面，务必先确认备份文件。</p></div></div>;
}
