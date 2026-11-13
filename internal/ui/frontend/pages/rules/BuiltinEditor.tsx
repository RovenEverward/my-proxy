import { NativeSelect } from "@/components/native-select";
import { useState, type FormEvent } from "react";
import { Button } from "../../components/ui/button";
import { DrawerBody, DrawerFooter, DrawerHeader } from "../../components/primitives";
import { useApp } from "../../runtime/app-context";
import { errorMessage, type RouteRule, type RulesPageData } from "../../domain/routing";
import { TargetOptions } from "./components";

export function BuiltinEditor({ data, onSave }: { data: RulesPageData; onSave: (rules: RouteRule[], version: number, message: string) => Promise<void> }) {
  const app = useApp();
  const have = new Set(data.rules.global.filter(rule => rule.type === "set").map(rule => rule.value));
  const [selected, setSelected] = useState<string[]>([]);
  const [targets, setTargets] = useState<Record<string, string>>(() => Object.fromEntries(data.catalog.map(entry => [entry.key, entry.default === "DIRECT" || entry.default === "REJECT" ? entry.default : data.rules.final || (data.groups[0] ? "group:" + data.groups[0].id : "DIRECT")])));
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  async function save(event: FormEvent) {
    event.preventDefault();
    if (!selected.length) { setError("先勾选要加的规则组"); return; }
    const add: RouteRule[] = data.catalog.filter(entry => selected.includes(entry.key) && !have.has(entry.key)).map(entry => ({ type: "set", value: entry.key, target: targets[entry.key], enabled: true }));
    const list = data.rules.global.slice(), at = list.findIndex(rule => rule.type === "set" && rule.value === "cn" || rule.type === "geoip");
    list.splice(at < 0 ? list.length : at, 0, ...add);
    setSaving(true); setError("");
    try { await onSave(list, data.rules.global_version, `已添加 ${add.length} 组规则`); app.closeOverlays(); }
    catch (reason) { setError(errorMessage(reason)); }
    finally { setSaving(false); }
  }
  return <form id="bi-form" onSubmit={event => void save(event)}><DrawerHeader title="内置规则组" onClose={app.closeOverlays} /><DrawerBody>
    <p className="sub mt0">常用网站已经整理好，勾上就加进规则表，去向可以改。规则内容由客户端定期从公开规则集下载更新。</p>
    {data.catalog.map(entry => <div className="row rp-bi" key={entry.key}><label className="chk grow"><input type="checkbox" value={entry.key} checked={have.has(entry.key) || selected.includes(entry.key)} disabled={have.has(entry.key) || saving} onChange={event => setSelected(previous => event.target.checked ? [...previous, entry.key] : previous.filter(value => value !== entry.key))} /><div><b className="bold">{entry.name}</b> {have.has(entry.key) && <span className="pill on">已添加</span>}<div className="sub">{entry.desc}</div></div></label><NativeSelect className="in w180" data-to={entry.key} aria-label={entry.name + " 的去向"} value={targets[entry.key]} onChange={event => setTargets(previous => ({ ...previous, [entry.key]: event.target.value }))}><TargetOptions data={data} /></NativeSelect></div>)}
    <div className="err" id="bi-err" role="alert">{error}</div>
  </DrawerBody><DrawerFooter><Button data-act="close" onClick={app.closeOverlays}>取消</Button><Button type="submit" variant="primary" loading={saving}>添加所选</Button></DrawerFooter></form>;
}
