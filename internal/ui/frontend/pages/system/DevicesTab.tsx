import { useEffect, useRef, useState, type FormEvent } from "react";
import { Field } from "../../components/primitives";
import { Button } from "../../components/ui/button";
import { useApp } from "../../runtime/app-context";
import { write } from "../../runtime/api";
import { sinceText } from "../../domain/settings";

export interface Device { id: number; name: string; current: boolean; last_used_at?: string | null; last_ip?: string }
export function DevicesTab({ devices, refresh }: { devices: Device[]; refresh: () => Promise<void> }) {
  const { can, confirmBox, toast } = useApp();
  const revoke = (device: Device) => confirmBox({ title: `吊销设备 ${device.name}？`, text: "它的 token 立即失效，要再用得在那台机器上重新接入。", ok: "吊销", danger: true,
    run: async () => { await write("DELETE", "/api/devices/" + device.id); toast(`已吊销设备 ${device.name}`); await refresh(); } });
  return <><div className="card"><div className="ch">接入的设备<small>· 共 {devices.length} 台</small></div>
    {devices.length ? devices.map(device => <div className="row" key={device.id}><span className="sub mono w18">{device.id}</span><b className="ell grow">{device.name}</b>{device.current && <span className="pill on">这台</span>}<span className="sub">{device.last_used_at ? "上次 " + sinceText(device.last_used_at) : "未用过"}{device.last_ip ? " · " + device.last_ip : ""}</span>{can("DELETE", "/api/devices") && !device.current && <Button size="sm" data-act="dev-revoke" data-id={device.id} data-name={device.name} onClick={() => revoke(device)}>吊销</Button>}</div>) : <div className="row sub">还没有设备接入。在另一台设备的 MPX 里选「接入已有控制面」即可。</div>}
    <div className="row sub">「这台」就是当前正在用的设备，界面里不让吊销它，免得把自己踢下线;要吊销它，请换一台已接入的设备操作。</div>
  </div>{(can("PUT", "/api/device-password") || can("DELETE", "/api/device-password")) && <DevicePasswordForm />}</>;
}
function DevicePasswordForm() {
  const { can, confirmBox, toast } = useApp();
  const [password, setPassword] = useState(""), [again, setAgain] = useState(""), [revoke, setRevoke] = useState(false);
  const [error, setError] = useState(""), [saving, setSaving] = useState(false);
  const lock = useRef(false), mounted = useRef(true), writable = can("PUT", "/api/device-password");
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (lock.current || !writable) return;
    const errors: string[] = []; if ([...password].length < 12) errors.push("设备密码至少 12 个字符"); if (password !== again) errors.push("两次输入不一致");
    setError(errors.join("\n")); if (errors.length) return;
    lock.current = true; setSaving(true);
    try {
      const result = await write<{ revoked?: number } | null>("PUT", "/api/device-password", { password, revoke_all: revoke });
      toast(revoke ? `设备密码已更新，吊销了 ${result?.revoked || 0} 台设备(这台也要重新接入)` : "设备密码已更新;已接入的设备不受影响");
      if (mounted.current) { setPassword(""); setAgain(""); setRevoke(false); }
    } catch (cause) { if (mounted.current) setError(cause instanceof Error ? cause.message : String(cause)); }
    finally { lock.current = false; if (mounted.current) setSaving(false); }
  };
  const disablePassword = () => confirmBox({ title: "关闭设备密码接入？", text: "之后新设备不能用设备密码接入;已接入的设备不受影响。要再开，在这里设一个新密码即可。", ok: "关闭设备密码接入", danger: true,
    run: async () => { await write("DELETE", "/api/device-password"); toast("已关闭设备密码接入;已接入的设备不受影响"); } });
  return <form className="card pad18 mt" id="dpw-form" noValidate onSubmit={submit}><b className="b6">设备密码</b><p className="sub mt6 mb14">在另一台设备的 MPX 里选「接入已有控制面」时输入它。改密码不影响已接入的设备。</p>
    <div className="two"><Field label="新密码" htmlFor="dpw1" hint="至少 12 个字符"><input className="in" id="dpw1" type="password" autoComplete="new-password" value={password} onChange={event => setPassword(event.target.value)} readOnly={!writable || saving} aria-describedby="dpw1-hint dpw-err" /></Field><Field label="再输一次" htmlFor="dpw2"><input className="in" id="dpw2" type="password" autoComplete="new-password" value={again} onChange={event => setAgain(event.target.value)} readOnly={!writable || saving} aria-describedby="dpw-err" /></Field></div>
    <label className="chk"><input type="checkbox" id="dpw-revoke" checked={revoke} onChange={event => setRevoke(event.target.checked)} disabled={!writable || saving} /> 同时吊销所有已接入设备(包括这台，之后要重新接入)</label><div className="err mt8" id="dpw-err" role="alert">{error}</div>
    <div className="flex8 end mt">{can("DELETE", "/api/device-password") && <Button data-act="dpw-off" disabled={saving} onClick={disablePassword}>关闭设备密码接入</Button>}{writable && <Button type="submit" variant="primary" id="dpw-save" loading={saving}>保存设备密码</Button>}</div><p className="sub mt8">关闭后新设备不能再接入，已接入的设备不受影响;要再开，在这里设一个新密码。</p>
  </form>;
}
