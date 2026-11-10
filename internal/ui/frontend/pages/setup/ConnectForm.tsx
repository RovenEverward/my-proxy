import { useEffect, useRef, useState } from "react";
import { Field, Icon } from "@/components/primitives";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { write } from "@/runtime/api";
import { Callout, PasswordInput, ProgressRow, SetupFrame, fieldDescription } from "./components";
import { errorMessage } from "./model";

export interface ConnectFormProps { mode: "setup" | "add"; onBack: () => void }
interface Fingerprint { server: string; fingerprint: string }
export function ConnectForm({ mode, onBack }: ConnectFormProps) {
  const add = mode === "add";
  const [addr, setAddr] = useState("");
  const [name, setName] = useState("");
  const [profile, setProfile] = useState("");
  const [certificate, setCertificate] = useState<Fingerprint | null>(null);
  const [fingerprintError, setFingerprintError] = useState("");
  const [error, setError] = useState("");
  const [errors, setErrors] = useState<{ addr?: string; profile?: string }>({});
  const [probing, setProbing] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [password, setPassword] = useState("");
  const [shown, setShown] = useState(false);
  const request = useRef({ generation: 0, probing: false, connecting: false });
  useEffect(() => () => { request.current.generation++; }, []);
  const endpoint = mode === "setup" ? "/local/setup" : "/local/profiles";
  const probe = async () => {
    if (request.current.probing || request.current.connecting) return;
    if (!addr.trim()) { setErrors({ addr: "请填写控制面地址" }); return; }
    const generation = ++request.current.generation;
    request.current.probing = true;
    setErrors({}); setError(""); setFingerprintError(""); setCertificate(null); setConfirmed(false); setProbing(true);
    try {
      const response = await write<Partial<Fingerprint>>("POST", endpoint + "/connect/probe", { addr: addr.trim() });
      if (request.current.generation !== generation) return;
      if (response?.server && response.fingerprint) setCertificate({ server: response.server, fingerprint: response.fingerprint });
      else setFingerprintError("控制面没有报证书指纹");
    } catch (failure) {
      if (request.current.generation === generation) setFingerprintError(errorMessage(failure));
    } finally {
      if (request.current.generation === generation) { request.current.probing = false; setProbing(false); }
    }
  };
  const connect = async () => {
    if (request.current.connecting || !certificate || !confirmed || !password) return;
    if (add && !/^[A-Za-z0-9_-]{1,32}$/.test(profile.trim())) {
      setErrors({ profile: "只能用字母、数字、- 和 _，1–32 个字符" }); return;
    }
    const body = { server: certificate.server, fingerprint: certificate.fingerprint, password, name: name.trim(), ...(add ? { profile: profile.trim() } : {}) };
    const generation = ++request.current.generation;
    request.current.connecting = true;
    setPassword(""); setError(""); setErrors({}); setConnecting(true);
    try {
      const response = await write<{ ok?: boolean }>("POST", endpoint + "/connect", body);
      if (request.current.generation !== generation) return;
      if (response?.ok) { location.reload(); return; }
      setError("控制面没有确认接入");
    } catch (failure) {
      if (request.current.generation === generation) setError(errorMessage(failure));
    } finally {
      body.password = "";
      if (request.current.generation === generation) { request.current.connecting = false; setConnecting(false); }
    }
  };
  return <SetupFrame footer={<><Button id="s-cp-back" variant="ghost" onClick={onBack} disabled={connecting}><Icon name="back" />{add ? "取消" : "上一步"}</Button><span className="sp" />
    <Button id="s-cp-probe" variant={certificate ? "default" : "primary"} loading={probing} disabled={connecting} onClick={() => void probe()}>{certificate && <Icon name="refresh" />}{certificate ? "重新取指纹" : probing ? "取指纹中…" : "取指纹"}</Button>
    {certificate && <Button id="s-cp-go" variant="primary" loading={connecting} disabled={!confirmed || !password} onClick={() => void connect()}>接入</Button>}
  </>}>
    <h2>{add ? "添加控制面" : "接入已有控制面"}</h2><p className="lead">{add ? "把另一套控制面接进来，记下来并切过去；先取回证书指纹核对，再输入设备密码。" : "把这台电脑接进已经在跑的控制面：先取回证书指纹核对，再输入设备密码。"}</p>
    <div className="two"><Field label="控制面地址" htmlFor="s-cp-addr" hint="IP 或域名，端口默认 8080" error={errors.addr}><Input id="s-cp-addr" className={"in mono " + (errors.addr ? "bad" : "")} placeholder="203.0.113.10:8080" autoComplete="off" value={addr} disabled={connecting} onChange={event => {
      setAddr(event.target.value); setErrors({}); setCertificate(null); setFingerprintError(""); setConfirmed(false);
      request.current.generation++; request.current.probing = false; setProbing(false);
    }} {...fieldDescription("s-cp-addr", errors.addr, true)} /></Field>
      <Field label="设备名" htmlFor="s-cp-name" hint="这台电脑在设备列表里叫什么；留空用主机名"><Input id="s-cp-name" className="in mono" autoComplete="off" value={name} onChange={event => setName(event.target.value)} disabled={connecting} {...fieldDescription("s-cp-name", undefined, true)} /></Field></div>
    {add && <Field label="名称" htmlFor="s-cp-profile" hint="只能用字母、数字、- 和 _，1–32 个字符；名字已存在会提示换一个" error={errors.profile}><Input id="s-cp-profile" className={"in mono " + (errors.profile ? "bad" : "")} placeholder="work" autoComplete="off" value={profile} disabled={connecting} onChange={event => { setProfile(event.target.value); setErrors({}); }} {...fieldDescription("s-cp-profile", errors.profile, true)} /></Field>}
    {error && <Callout tone="bad" title="接入失败"><div className="raw">{error}</div></Callout>}
    {fingerprintError && <Callout tone="bad" title="取指纹失败"><div className="raw">{fingerprintError}</div></Callout>}
    {probing && <ol className="plist" aria-busy="true"><ProgressRow state="run" title="连上控制面，取回证书指纹" /></ol>}
    {certificate && <><div className="field"><label>证书指纹</label><div className="cmd">{certificate.fingerprint}</div><span className="h">和首次安装完成时显示的那一行对一下，或向控制面的管理员确认</span></div>
      <label className="chk mb"><input type="checkbox" id="s-cp-ok" checked={confirmed} disabled={connecting} onChange={event => setConfirmed(event.target.checked)} /> 指纹核对无误，是我的控制面</label>
      <Field label="设备密码" htmlFor="s-cp-pw" hint="首次安装时设的；忘了可以在已接入设备的「系统」页的「设备与接入」里重设"><PasswordInput id="s-cp-pw" shown={shown} onToggle={() => setShown(value => !value)} value={password} onChange={event => setPassword(event.target.value)} disabled={connecting} {...fieldDescription("s-cp-pw", undefined, true)} /></Field></>}
    <p className="sub">接入时会核对证书指纹，设备密码只用于这一次接入。</p>
  </SetupFrame>;
}
