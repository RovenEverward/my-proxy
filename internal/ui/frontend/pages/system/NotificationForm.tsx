import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "../../components/ui/button";
import { Field } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import type { Settings } from "../../runtime/types";
import { modifySettings } from "../../domain/settings";

type NotificationValues = { webhook_url: string; notify_days_left: string; notify_usage_percent: string };
const valuesOf = (settings: Settings): NotificationValues => ({ webhook_url: settings.webhook_url || "", notify_days_left: String(settings.notify_days_left || 0), notify_usage_percent: String(settings.notify_usage_percent || 0) });
export function NotificationForm({ settings, refresh }: { settings: Settings; refresh: (fresh?: boolean) => Promise<void> }) {
  const { can, toast } = useApp();
  const [original, setOriginal] = useState(settings), [values, setValues] = useState(() => valuesOf(settings));
  const [error, setError] = useState(""), [saving, setSaving] = useState(false), [testing, setTesting] = useState(false);
  const lock = useRef(false), mounted = useRef(true);
  const writable = can("PUT", "/api/settings");
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => { if (original !== settings && JSON.stringify(values) === JSON.stringify(valuesOf(original)) && !lock.current) { setOriginal(settings); setValues(valuesOf(settings)); } }, [settings, original, values]);
  const edit = (key: keyof NotificationValues, value: string) => { setValues(previous => ({ ...previous, [key]: value })); setError(""); };
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (lock.current || !writable) return;
    const errors: string[] = [];
    for (const [key, label] of [["notify_days_left", "到期前提醒(天)"], ["notify_usage_percent", "用量提醒(%)"]] as const) if (values[key].trim() && !/^\d+$/.test(values[key].trim())) errors.push(label + "要是非负整数");
    setError(errors.join("\n")); if (errors.length) return;
    const parsed = { webhook_url: values.webhook_url.trim(), notify_days_left: Number(values.notify_days_left.trim()) || 0, notify_usage_percent: Number(values.notify_usage_percent.trim()) || 0 };
    const webhookChanged = parsed.webhook_url !== (original.webhook_url || ""), daysChanged = parsed.notify_days_left !== (original.notify_days_left || 0), usageChanged = parsed.notify_usage_percent !== (original.notify_usage_percent || 0);
    if (!webhookChanged && !daysChanged && !usageChanged) { toast("没有改动"); return; }
    lock.current = true; setSaving(true);
    try {
      await modifySettings(latest => { if (webhookChanged) latest.webhook_url = parsed.webhook_url; if (daysChanged) latest.notify_days_left = parsed.notify_days_left; if (usageChanged) latest.notify_usage_percent = parsed.notify_usage_percent; });
      if (mounted.current) { const saved = { ...original, ...parsed }; setOriginal(saved); setValues(valuesOf(saved)); }
      toast("已保存通知设置"); await refresh();
    } catch (cause) { if (mounted.current) setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { lock.current = false; if (mounted.current) setSaving(false); }
  };
  return <div className="grid g2"><form className="card pad18" id="set-form" noValidate onSubmit={submit}>
    <Field label="Webhook" htmlFor="s-webhook_url" hint="到期、超限等通知发到这里;留空不发"><div className="flex8"><input className="in mono" id="s-webhook_url" name="webhook_url" value={values.webhook_url} onChange={event => edit("webhook_url", event.target.value)} readOnly={!writable || saving} />{can("POST", "/api/settings/test-webhook") && <Button data-act="set-test" loading={testing} onClick={async () => { if (testing) return; setTesting(true); try { await write("POST", "/api/settings/test-webhook"); toast("测试通知已送到"); } catch (cause) { toast(cause instanceof Error ? cause.message : String(cause), true); } finally { if (mounted.current) setTesting(false); } }}>发测试</Button>}</div></Field>
    <div className="two"><Field label="到期前提醒(天)" htmlFor="s-notify_days_left" hint="0 表示关闭"><input className="in mono" id="s-notify_days_left" name="notify_days_left" value={values.notify_days_left} inputMode="numeric" onChange={event => edit("notify_days_left", event.target.value)} readOnly={!writable || saving} /></Field>
      <Field label="用量提醒(%)" htmlFor="s-notify_usage_percent" hint="0 表示关闭"><input className="in mono" id="s-notify_usage_percent" name="notify_usage_percent" value={values.notify_usage_percent} inputMode="numeric" onChange={event => edit("notify_usage_percent", event.target.value)} readOnly={!writable || saving} /></Field></div>
    <div className="err" id="s-err" role="alert">{error}</div>{writable && <div className="flex8 end"><Button data-act="set-revert" disabled={saving} onClick={() => { setOriginal(settings); setValues(valuesOf(settings)); setError(""); void refresh(true); }}>还原</Button><Button type="submit" variant="primary" id="s-save" loading={saving}>保存</Button></div>}
  </form><div className="card pad16 self-start"><div className="sub mb8">会通知什么</div><p className="sub">用户快到期、用量快用完时，往 Webhook 发一条消息。两个提醒填 0 就不发。</p></div></div>;
}
