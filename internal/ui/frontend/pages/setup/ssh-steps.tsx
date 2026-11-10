import { useState } from "react";
import { Field, Icon } from "@/components/primitives";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Callout, PasswordInput, ProgressRow, SetupFrame, fieldDescription } from "./components";
import { canInstall, formatDuration, portNotes, type FieldErrors, type MachineProbe, type SetupDraft } from "./model";

export type UpdateDraft = <K extends keyof SetupDraft>(key: K, value: SetupDraft[K]) => void;
export function SSHConnectionStep({ draft, errors, update, onBack, onCheck }: { draft: SetupDraft; errors: FieldErrors; update: UpdateDraft; onBack: () => void; onCheck: () => void }) {
  const [shown, setShown] = useState(false);
  const key = draft.auth === "key";
  return <SetupFrame steps={["cur", "", "", "", ""]} footer={<><Button id="s-back" variant="ghost" onClick={onBack}><Icon name="back" />上一步</Button><span className="sp" /><Button id="s-check" variant="primary" onClick={onCheck}>检查机器</Button></>}>
    <h2>连接 VPS</h2><p className="lead">控制面要装在这台机器上。</p>
    <div className="two"><Field label="服务器地址" htmlFor="s-host" error={errors.host}><Input id="s-host" className={"in mono " + (errors.host ? "bad" : "")} placeholder="203.0.113.10" autoComplete="off" value={draft.host} onChange={event => update("host", event.target.value)} {...fieldDescription("s-host", errors.host)} /></Field>
      <Field label="SSH 端口" htmlFor="s-port" error={errors.sshPort}><Input id="s-port" className={"in mono " + (errors.sshPort ? "bad" : "")} placeholder="22" inputMode="numeric" autoComplete="off" value={draft.sshPort} onChange={event => update("sshPort", event.target.value)} {...fieldDescription("s-port", errors.sshPort)} /></Field></div>
    <div className="field"><label id="s-auth-label">登录方式</label><div className="opts" style={{ margin: 0 }} role="radiogroup" aria-labelledby="s-auth-label">
      <div className={"opt " + (!key ? "on" : "")} id="s-auth-pw" role="radio" aria-checked={!key} tabIndex={key ? -1 : 0} onClick={() => update("auth", "password")} onKeyDown={event => {
        if ([" ", "Enter", "ArrowLeft", "ArrowUp", "ArrowRight", "ArrowDown"].includes(event.key)) { event.preventDefault(); if (["ArrowRight", "ArrowDown"].includes(event.key)) { update("auth", "key"); document.getElementById("s-auth-key")?.focus(); } else update("auth", "password"); }
      }}><span className="rd" /><div><b>SSH 密码</b><span className="h">只用来装一次本机公钥，用完即丢</span></div></div>
      <div className={"opt " + (key ? "on" : "")} id="s-auth-key" role="radio" aria-checked={key} tabIndex={key ? 0 : -1} onClick={() => update("auth", "key")} onKeyDown={event => {
        if ([" ", "Enter", "ArrowLeft", "ArrowUp", "ArrowRight", "ArrowDown"].includes(event.key)) { event.preventDefault(); if (["ArrowLeft", "ArrowUp"].includes(event.key)) { update("auth", "password"); document.getElementById("s-auth-pw")?.focus(); } else update("auth", "key"); }
      }}><span className="rd" /><div><b>这台电脑已能用密钥登录</b><span className="h">例如之前跑过 ssh-copy-id</span></div></div>
    </div></div>
    {key ? <Field label="私钥路径（可选）" htmlFor="s-identity" hint="留空时和 ssh 命令一样自动找"><Input id="s-identity" className="in mono" placeholder="~/.ssh/id_ed25519" autoComplete="off" value={draft.identity} onChange={event => update("identity", event.target.value)} {...fieldDescription("s-identity", undefined, true)} /></Field>
      : <Field label="root 密码" htmlFor="s-pw" error={errors.password}><PasswordInput id="s-pw" shown={shown} onToggle={() => setShown(value => !value)} value={draft.password} onChange={event => update("password", event.target.value)} className={errors.password ? "bad" : ""} {...fieldDescription("s-pw", errors.password)} /></Field>}
    <details className="adv"><summary>高级</summary><Field label="SSH 用户" htmlFor="s-user" hint="固定 root：控制面要求以 root 身份安装"><Input id="s-user" className="in mono" autoComplete="off" value={draft.sshUser} onChange={event => update("sshUser", event.target.value)} {...fieldDescription("s-user", undefined, true)} /></Field></details>
  </SetupFrame>;
}
export interface CheckState { probe: MachineProbe | null; error: string; started: number; finished: number }
export function MachineCheckStep({ draft, check, onBack, onNext }: { draft: SetupDraft; check: CheckState; onBack: () => void; onNext: () => void }) {
  const first = draft.auth === "key" ? "用密钥登录" : "用密码登录并装上本机公钥";
  const probe = check.probe;
  const success = probe && canInstall(probe);
  const waiting = !probe && !check.error;
  const duration = formatDuration(check.finished - check.started);
  const osBad = probe?.os !== "Linux", uidBad = String(probe?.uid) !== "0", downloadBad = !probe?.fetch;
  const reasons: string[] = [];
  if (probe) {
    if (osBad) reasons.push(`系统是 ${probe.os || "未知"}，控制面只能装在 Linux 上。`);
    if (uidBad) reasons.push(`登录用户不是 root（uid=${probe.uid}）。回到上一步，在高级里确认 SSH 用户是 root，或先在 VPS 上开启 root 登录。`);
    if (downloadBad) reasons.push("机器上没有 curl 或 wget，装不了控制面。先在 VPS 上装一个。");
  }
  const notes = probe && success ? portNotes(probe) : [];
  return <SetupFrame steps={["done", check.error ? "fail" : waiting ? "cur" : "done", "", "", ""]} footer={waiting
    ? <><span className="sp" /><Button variant="primary" disabled>检查中…</Button></>
    : success ? <><Button id="s-back" variant="ghost" onClick={onBack}><Icon name="back" />上一步</Button><span className="sp" /><Button id="s-next" variant="primary" onClick={onNext}>下一步</Button></>
      : <Button id="s-back2" variant="primary" onClick={onBack}><Icon name="back" />返回修改</Button>}>
    <h2>检查机器</h2><p className="lead">登录一次、装上本机公钥，再看看这台机器能不能装控制面。</p>
    <ol className="plist" aria-busy={waiting}><ProgressRow state={check.error ? "fail" : waiting ? "run" : "done"} title={first} duration={probe ? duration : ""} /><ProgressRow state={probe ? "done" : "wait"} title="检查系统、架构、权限和下载工具" duration={probe ? duration : ""} /></ol>
    {check.error && <Callout tone="bad" title="登录或检查失败"><div className="mt2">密码不对、这台机器关了密码登录，或连不上。</div><div className="raw">{check.error}</div></Callout>}
    {probe && <><div className="probe">{[
      { label: "系统", value: probe.os || "未知", bad: osBad }, { label: "架构", value: probe.arch || "未知", bad: false },
      { label: "用户", value: uidBad ? `uid=${probe.uid}` : "root", bad: uidBad }, { label: "下载", value: probe.fetch?.split("/").pop() || "无", bad: downloadBad },
    ].map(item => <div key={item.label} className={item.bad ? "no" : ""}><span>{item.label}</span><b>{item.value}</b></div>)}</div>
      {success ? <><Callout tone="stone" title="可以安装。">本机公钥已装上，后面的步骤不再需要密码。</Callout>{notes.length > 0 && <Callout tone="warn" title="端口已换" id="s-ports">{notes.map(note => <div className="mt2" key={note}>{note}</div>)}</Callout>}</>
        : <Callout tone="bad" title={osBad ? "只能装在 Linux 上" : uidBad ? "要用 root 登录" : "缺少下载工具"}><div className="mt2">{reasons.join(" ")}</div></Callout>}
    </>}
  </SetupFrame>;
}
