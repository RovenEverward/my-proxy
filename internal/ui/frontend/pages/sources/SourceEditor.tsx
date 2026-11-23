import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { DrawerBody, ErrorState, Icon, LoadingTable } from "@/components/primitives";
import { useApp } from "@/runtime/app-context";
import { api } from "@/runtime/api";
import type { User } from "@/runtime/types";
import { errorMessage, readSubscriptionUsers, sourcePath, type SubscriptionSource } from "@/domain/routing";
import type { Plan } from "@/domain/plans";
import { CreateSourceEditor } from "./CreateSourceEditor";
import { ExistingSourceEditor } from "./ExistingSourceEditor";

export type SourceDrawerTab = "config" | "nodes";
export interface SourcePacket { source: SubscriptionSource; grants: string[]; planGrants: number[]; users: User[]; plans: Plan[] }
interface DrawerProps { id?: number; initialTab?: SourceDrawerTab; onChanged: () => Promise<void>; onCreated: (id: number) => void }

export function SourceDrawerHeader({ title, onClose }: { title: string; onClose: () => void }) {
  return <div className="dh">
    <h2 id="source-drawer-title" className="ell" tabIndex={-1}>{title}</h2>
    <span className="sp" />
    <Button size="icon" data-act="close" aria-label="关闭" onClick={onClose}><Icon name="x" /></Button>
  </div>;
}

export function SourceDrawer({ id, initialTab = "config", onChanged, onCreated }: DrawerProps) {
  const app = useApp();
  const [packet, setPacket] = useState<SourcePacket | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!id) return;
    const controller = new AbortController(); let active = true;
    setError("");
    void Promise.all([
      api<SubscriptionSource>(sourcePath(id), { signal: controller.signal }),
      api<{ users: string[]; plans?: { id: number; name: string }[] }>(sourcePath(id) + "/grants", { signal: controller.signal }),
      readSubscriptionUsers((path, init) => api(path, { ...init, signal: controller.signal })),
      api<Plan[]>("/api/plans", { signal: controller.signal }).catch((): Plan[] => []),
    ]).then(([source, grants, users, plans]) => { if (active) setPacket({ source, grants: grants.users, planGrants: (grants.plans || []).map(p => p.id), users, plans }); })
      .catch((reason: unknown) => { if (active) setError(errorMessage(reason)); });
    return () => { active = false; controller.abort(); };
  }, [id, attempt]);

  if (!id) return <CreateSourceEditor onChanged={onChanged} onCreated={onCreated} />;
  if (packet) return <ExistingSourceEditor key={id} initial={packet} initialTab={initialTab} onChanged={onChanged} />;
  return <div className="source-drawer-frame">
    <SourceDrawerHeader title="订阅详情" onClose={app.closeOverlays} />
    <DrawerBody>{error ? <ErrorState title="订阅详情" error={error} onRetry={() => setAttempt(previous => previous + 1)} /> : <LoadingTable rows={4} columns={2} />}</DrawerBody>
  </div>;
}
