import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { DrawerBody, DrawerFooter, Field } from "@/components/primitives";
import { useApp } from "@/runtime/app-context";
import { write } from "@/runtime/api";
import { errorMessage, type SubscriptionSource } from "@/domain/routing";
import { SourceDrawerHeader } from "./SourceEditor";

export function CreateSourceEditor({ onChanged, onCreated }: { onChanged: () => Promise<void>; onCreated: (id: number) => void }) {
  const app = useApp();
  const [name, setName] = useState("");
  const [url, setURL] = useState("");
  const [interval, setIntervalValue] = useState("6");
  const [region, setRegion] = useState("");
  const [error, setError] = useState("");
  const [invalid, setInvalid] = useState<"name" | "url" | "interval" | null>(null);
  const [saving, setSaving] = useState(false);
  const inputs = { name: useRef<HTMLInputElement>(null), url: useRef<HTMLInputElement>(null), interval: useRef<HTMLInputElement>(null) };
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);

  async function save(event: FormEvent) {
    event.preventDefault();
    if (saving) return;
    const seconds = Number(interval) * 3600;
    let field: typeof invalid = null;
    if (!name.trim()) field = "name";
    else if (!url.trim()) field = "url";
    else if (interval === "" || !Number.isSafeInteger(seconds) || seconds < 0) field = "interval";
    if (field) { setError("请填写来源名称、有效订阅地址和非负刷新间隔。"); setInvalid(field); inputs[field].current?.focus(); return; }
    setError(""); setInvalid(null); setSaving(true);
    const body = { name: name.trim(), url: url.trim(), interval_seconds: seconds, region: region.trim() };
    try {
      const saved = await write<SubscriptionSource>("POST", "/api/sources", body);
      if (!mounted.current) return;
      app.toast("来源已添加，正在拉取节点");
      onCreated(saved.id);
      await onChanged();
    } catch (reason) {
      if (mounted.current) { setError(errorMessage(reason)); setInvalid("url"); }
    } finally {
      if (mounted.current) setSaving(false);
    }
  }

  return <div className="source-drawer-frame">
    <SourceDrawerHeader title="添加外部订阅" onClose={app.closeOverlays} />
    <form id="source-form" noValidate onSubmit={event => void save(event)}>
      <DrawerBody>
        <Field label="来源名称" htmlFor="source-name">
          <Input id="source-name" name="name" autoFocus maxLength={64} value={name} required ref={inputs.name} aria-invalid={invalid === "name" || undefined} aria-describedby="source-err" onChange={event => setName(event.target.value)} disabled={saving} />
        </Field>
        <Field label="订阅地址" htmlFor="source-url">
          <Input className="mono" id="source-url" name="url" type="url" autoComplete="off" spellCheck={false} aria-describedby="source-url-hint source-err" aria-invalid={invalid === "url" || undefined} placeholder="https://" value={url} ref={inputs.url} disabled={saving} onChange={event => setURL(event.target.value)} />
          <span className="sub" id="source-url-hint">支持 Clash、sing-box 与分享链接订阅。添加后默认不分配给用户。</span>
        </Field>
        <div className="two">
          <Field label="刷新间隔（小时）" htmlFor="source-interval">
            <Input id="source-interval" name="interval" type="number" min="0" step="any" value={interval} ref={inputs.interval} aria-describedby="source-interval-hint source-err" aria-invalid={invalid === "interval" || undefined} disabled={saving} onChange={event => setIntervalValue(event.target.value)} />
            <span className="sub" id="source-interval-hint">0 表示仅手动刷新。</span>
          </Field>
          <Field label="默认地区（选填）" htmlFor="source-region">
            <Input id="source-region" name="region" maxLength={2} value={region} placeholder="如 JP" disabled={saving} onChange={event => setRegion(event.target.value)} />
          </Field>
        </div>
        <div className="err" id="source-err" role="alert">{error}</div>
      </DrawerBody>
      <DrawerFooter>
        <Button variant="ghost" data-act="close" disabled={saving} onClick={app.closeOverlays}>取消</Button>
        <Button type="submit" variant="primary" id="source-save" loading={saving}>添加并拉取</Button>
      </DrawerFooter>
    </form>
  </div>;
}
