import { useState } from "react";
import { Field, FlagText, Icon } from "@/components/primitives";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { PasswordInput, SetupFrame, fieldDescription } from "./components";
import { passwordStrength, type FieldErrors, type SetupDraft } from "./model";
import type { UpdateDraft } from "./ssh-steps";

export function SettingsStep({ draft, errors, update, onBack, onRun, starting }: { draft: SetupDraft; errors: FieldErrors; update: UpdateDraft; onBack: () => void; onRun: () => void; starting: boolean }) {
  const [shown, setShown] = useState(false);
  const strength = passwordStrength(draft.devicePassword);
  return <SetupFrame steps={["done", "done", "cur", "", ""]} footer={<><Button id="s-back" variant="ghost" onClick={onBack} disabled={starting}><Icon name="back" />上一步</Button><span className="sp" /><Button id="s-run" variant="primary" loading={starting} onClick={onRun}>开始安装</Button></>}>
    <h2>设置</h2><p className="lead">之后都能在管理界面里改。</p>
    <div className="two"><Field label="第一个用户" htmlFor="s-username" hint="只能用字母、数字、- 和 _" error={errors.username}><Input id="s-username" className={"in mono " + (errors.username ? "bad" : "")} autoComplete="off" value={draft.username} onChange={event => update("username", event.target.value)} {...fieldDescription("s-username", errors.username, true)} /></Field>
      <div className="field"><label>节点显示名</label><div className="sub" id="s-autoname" style={{ paddingTop: 8 }}><FlagText>{"按这台 VPS 的 IP 自动识别（如 🇯🇵 东京 A），装好后可在节点页改。"}</FlagText></div></div></div>
    <div className="two"><div className="field"><label htmlFor="s-dpw">设备密码</label><PasswordInput id="s-dpw" shown={shown} onToggle={() => setShown(value => !value)} value={draft.devicePassword} onChange={event => update("devicePassword", event.target.value)} className={errors.devicePassword ? "bad" : ""} aria-invalid={errors.devicePassword ? true : undefined} aria-describedby={"s-str" + (errors.devicePassword ? " s-dpw-error" : "")} />
      <div className="meter" id="s-meter" aria-hidden="true">{[1, 2, 3, 4].map(level => <i key={level} className={strength >= level ? "on" : ""} />)}</div><span className="h" id="s-str">强度：{["太短", "弱", "中", "强", "强"][strength]}</span>{errors.devicePassword && <span className="fe" id="s-dpw-error" role="alert">{errors.devicePassword}</span>}</div>
      <Field label="再输一次" htmlFor="s-dpw2" error={errors.devicePasswordAgain}><PasswordInput id="s-dpw2" shown={shown} onToggle={() => setShown(value => !value)} value={draft.devicePasswordAgain} onChange={event => update("devicePasswordAgain", event.target.value)} className={errors.devicePasswordAgain ? "bad" : ""} {...fieldDescription("s-dpw2", errors.devicePasswordAgain)} /></Field></div>
    <p className="sub" style={{ marginTop: -4 }}>在另一台设备的 MPX 里选「接入已有控制面」时要用它。不会保存在这台电脑之外。</p>
    <Field label="链接开头" htmlFor="s-sub" hint="发给用户的链接以它开头。不填就会把服务器 IP 发给用户;之后在「订阅」里也能改。"><Input id="s-sub" className="in mono" autoComplete="off" placeholder="例如 https://sub.example.com" value={draft.subPrefix} onChange={event => update("subPrefix", event.target.value)} {...fieldDescription("s-sub", undefined, true)} /></Field>
    <details className="adv"><summary>高级</summary><Field label="监听地址" htmlFor="s-addr" error={errors.addr}><Input id="s-addr" className={"in mono " + (errors.addr ? "bad" : "")} autoComplete="off" value={draft.addr} onChange={event => update("addr", event.target.value)} {...fieldDescription("s-addr", errors.addr)} /></Field>
      <Field label="SNI" htmlFor="s-sni" hint="第一个连接方式用的 REALITY SNI"><Input id="s-sni" className="in mono" autoComplete="off" value={draft.sni} onChange={event => update("sni", event.target.value)} {...fieldDescription("s-sni", undefined, true)} /></Field></details>
  </SetupFrame>;
}
