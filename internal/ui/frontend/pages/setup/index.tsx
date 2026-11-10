import { useEffect, useRef, useState } from "react";
import { Icon } from "@/components/primitives";
import { Button } from "@/components/ui/button";
import { api, write } from "@/runtime/api";
import { useApp } from "@/runtime/app-context";
import { SetupFrame } from "./components";
import { ConnectForm } from "./ConnectForm";
import { CompletionStep, InstallationStep } from "./install-steps";
import { applyPortSuggestions, errorMessage, initialDraft, installationParams, validateConnection, validateSettings, type FieldErrors, type MachineProbe, type SetupDraft, type SetupStatus } from "./model";
import { MachineCheckStep, SSHConnectionStep, type CheckState, type UpdateDraft } from "./ssh-steps";
import { SettingsStep } from "./settings-step";
import { useInstallation } from "./use-installation";

export { ConnectForm } from "./ConnectForm";
export type { ConnectFormProps } from "./ConnectForm";
type Screen = "loading" | "welcome" | "existing" | "ssh" | "check" | "settings" | "installation";
function WelcomeStep({ onStart, onConnect }: { onStart: () => void; onConnect: () => void }) {
  const { meta, openProfiles } = useApp();
  return <SetupFrame footer={<><span className="sub" style={{ flex: 1 }}>{meta.profiles && <a href="#" className="u-link" id="s-profiles" onClick={event => { event.preventDefault(); openProfiles(); }}>切换到已有的控制面</a>}</span><Button id="s-connect" onClick={onConnect}><Icon name="lock" />接入已有控制面</Button><Button id="s-start" variant="primary" onClick={onStart}>首次安装（新服务器）</Button></>}>
    <div className="welcome"><div className="ill"><img src="/mark-dot-globe.svg" alt="" /></div><h2>在你的 VPS 上装好 MPX</h2>
      <p className="lead">一台机器同时当控制面和第一个节点，大约 5–10 分钟。</p>
      <ul><li><Icon name="srv" /><span>装控制面，并在同一台机器上建第一个节点、连接方式和对外地址</span></li><li><Icon name="users" /><span>建第一个用户，给你一条可以直接导入的订阅链接</span></li>
        <li><Icon name="lock" /><span>设一个设备密码，以后别的电脑用它接入</span></li><li><Icon name="term" /><span>这台电脑自动接入，装完直接进管理界面</span></li></ul>
      <p className="sub">需要：一台 Linux VPS 的地址和 root 密码（或这台电脑已能用密钥登录）。已经有控制面？选右边的「接入已有控制面」。</p></div>
  </SetupFrame>;
}
export function SetupPage() {
  const { toast } = useApp();
  const [screen, setScreen] = useState<Screen>("loading");
  const [draft, setDraft] = useState<SetupDraft>(() => ({ ...initialDraft }));
  const [errors, setErrors] = useState<FieldErrors>({});
  const [check, setCheck] = useState<CheckState>({ probe: null, error: "", started: 0, finished: 0 });
  const checking = useRef(false);
  const checkGeneration = useRef(0);
  const installation = useInstallation();
  const { resume } = installation;
  useEffect(() => {
    let active = true;
    void api<SetupStatus>("/local/setup").then(status => {
      if (!active) return;
      if (status?.job?.state === "running") { setScreen("installation"); resume(status.job.id); }
      else if (status?.mode === "normal") location.reload();
      else setScreen("welcome");
    }).catch(() => { if (active) setScreen("welcome"); });
    return () => { active = false; checkGeneration.current++; };
  }, [resume]);
  useEffect(() => {
    if (installation.state.phase === "done") setDraft(previous => ({ ...previous, devicePassword: "", devicePasswordAgain: "" }));
  }, [installation.state.phase]);
  const update: UpdateDraft = <K extends keyof SetupDraft,>(key: K, value: SetupDraft[K]) => {
    setDraft(previous => ({ ...previous, [key]: value }));
    setErrors(previous => { if (key === "auth") return {}; const next = { ...previous }; delete next[key]; return next; });
  };
  const checkMachine = async () => {
    if (checking.current) return;
    const nextErrors = validateConnection(draft);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length) return;
    checking.current = true;
    const generation = ++checkGeneration.current;
    const started = Date.now();
    const body: { host: string; ssh_port: number; ssh_user: string; password?: string; identity?: string } = {
      host: draft.host, ssh_port: +draft.sshPort || 22, ssh_user: draft.sshUser || "root",
    };
    if (draft.auth === "password") body.password = draft.password;
    else if (draft.identity) body.identity = draft.identity;
    setDraft(previous => ({ ...previous, password: "" }));
    setCheck({ probe: null, error: "", started, finished: 0 }); setScreen("check");
    try {
      const response = await write<{ probe?: MachineProbe }>("POST", "/local/setup/check", body);
      if (checkGeneration.current !== generation) return;
      const probe = response?.probe || {};
      setDraft(previous => applyPortSuggestions(previous, probe));
      setCheck({ probe, error: "", started, finished: Date.now() });
    } catch (failure) {
      if (checkGeneration.current === generation) setCheck({ probe: null, error: errorMessage(failure), started, finished: Date.now() });
    } finally { body.password = ""; checking.current = false; }
  };
  const run = async () => {
    const nextErrors = validateSettings(draft);
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length) return;
    try { await installation.start(installationParams(draft)); setScreen("installation"); }
    catch (failure) { toast(errorMessage(failure) || "安装启动失败", true); }
  };
  const retry = async () => {
    if (!installation.canRetry) { setScreen("settings"); return; }
    try { await installation.start(); }
    catch (failure) { toast(errorMessage(failure) || "安装启动失败", true); }
  };
  if (installation.state.phase === "done") return <CompletionStep result={installation.state.result} host={draft.host} username={draft.username} />;
  if (installation.state.phase === "running" || installation.state.phase === "failed" && screen === "installation") return <InstallationStep state={installation.state} starting={installation.starting} onRetry={() => void retry()} />;
  if (screen === "loading") return <SetupFrame footer={null}><h2>首次安装</h2><p role="status">正在读取安装进度…</p></SetupFrame>;
  if (screen === "existing") return <ConnectForm mode="setup" onBack={() => setScreen("welcome")} />;
  if (screen === "ssh") return <SSHConnectionStep draft={draft} errors={errors} update={update} onBack={() => { setErrors({}); setScreen("welcome"); }} onCheck={() => void checkMachine()} />;
  if (screen === "check") return <MachineCheckStep draft={draft} check={check} onBack={() => {
    setDraft(previous => ({ ...previous, password: "" }));
    setErrors(draft.auth === "password" ? { password: "请重新输入 root 密码" } : {}); setScreen("ssh");
  }} onNext={() => setScreen("settings")} />;
  if (screen === "settings") return <SettingsStep draft={draft} errors={errors} update={update} starting={installation.starting} onBack={() => { setErrors({}); setScreen("ssh"); }} onRun={() => void run()} />;
  return <WelcomeStep onStart={() => setScreen("ssh")} onConnect={() => setScreen("existing")} />;
}
