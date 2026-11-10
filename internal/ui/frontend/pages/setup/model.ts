export interface SetupDraft {
  host: string;
  sshPort: string;
  sshUser: string;
  auth: "password" | "key";
  password: string;
  identity: string;
  username: string;
  devicePassword: string;
  devicePasswordAgain: string;
  addr: string;
  inboundPort: number;
  subPrefix: string;
  sni: string;
}
export const initialDraft: SetupDraft = {
  host: "", sshPort: "22", sshUser: "root", auth: "password", password: "", identity: "",
  username: "user1", devicePassword: "", devicePasswordAgain: "", addr: "0.0.0.0:8080",
  inboundPort: 0, subPrefix: "", sni: "www.cloudflare.com",
};
export type FieldErrors = Partial<Record<keyof SetupDraft, string>>;
export interface MachineProbe {
  os?: string; arch?: string; uid?: string; fetch?: string;
  api_port?: string; api_busy?: string; api_suggest?: string;
  inbound_port?: string; inbound_busy?: string; inbound_suggest?: string;
}
export interface SetupParams {
  host: string; ssh_port: number; ssh_user: string; identity: string; name: string;
  username: string; device_password: string; addr: string; sub_prefix: string; sni: string;
  inbound_port: number;
}
export interface SetupResult {
  host_port?: string; fingerprint?: string; connected?: boolean; username?: string;
}
export type ProgressState = "run" | "done" | "fail" | "skip" | "wait";
export interface InstallStep { title: string; state: ProgressState; secs: number }
export interface SetupDelta {
  id: string; seq?: number; state: "running" | "done" | "failed";
  steps?: InstallStep[]; lines?: string[]; more?: boolean; error?: string;
  result?: SetupResult; elapsed?: number;
}
export interface SetupStatus { mode: "normal" | "setup"; job?: SetupDelta | null }
export function validateConnection(draft: SetupDraft): FieldErrors {
  const errors: FieldErrors = {};
  if (!draft.host.trim()) errors.host = "请填写服务器地址";
  if (!/^\d+$/.test(draft.sshPort.trim()) || +draft.sshPort < 1 || +draft.sshPort > 65535) errors.sshPort = "端口要在 1–65535 之间";
  if (draft.auth === "password" && !draft.password) errors.password = "请输入 root 密码";
  return errors;
}
export function validateSettings(draft: SetupDraft): FieldErrors {
  const errors: FieldErrors = {};
  if (!/^[A-Za-z0-9_-]+$/.test(draft.username.trim())) errors.username = "只能用字母、数字、- 和 _";
  if (draft.devicePassword.length < 12) errors.devicePassword = "设备密码至少 12 个字符";
  if (draft.devicePasswordAgain !== draft.devicePassword) errors.devicePasswordAgain = "两次输入不一致";
  if (!/^[^:\s]+:\d{1,5}$/.test(draft.addr.trim())) errors.addr = "监听地址写成 地址:端口";
  return errors;
}
export function canInstall(probe: MachineProbe): boolean {
  return probe.os === "Linux" && String(probe.uid) === "0" && !!probe.fetch;
}
export function portNotes(probe: MachineProbe): string[] {
  const notes: string[] = [];
  if (probe.api_busy) notes.push(probe.api_suggest
    ? `控制面默认用的 ${probe.api_port} 端口已被 ${probe.api_busy} 占用，已改用 ${probe.api_suggest}。可在下面「高级」里的「监听地址」改。`
    : `控制面默认用的 ${probe.api_port} 端口已被 ${probe.api_busy} 占用，请在下面「高级」里的「监听地址」换一个端口。`);
  if (probe.inbound_busy && probe.inbound_suggest) notes.push(`连接方式默认用的 ${probe.inbound_port} 端口已被 ${probe.inbound_busy} 占用，已改用 ${probe.inbound_suggest}。`);
  return notes;
}
export function applyPortSuggestions(draft: SetupDraft, probe: MachineProbe): SetupDraft {
  if (!canInstall(probe)) return draft;
  return {
    ...draft,
    addr: probe.api_busy && probe.api_suggest ? draft.addr.replace(/:\d+$/, ":" + probe.api_suggest) : draft.addr,
    inboundPort: probe.inbound_busy && probe.inbound_suggest ? +probe.inbound_suggest : draft.inboundPort,
  };
}
export function installationParams(draft: SetupDraft): SetupParams {
  return {
    host: draft.host, ssh_port: +draft.sshPort || 22, ssh_user: draft.sshUser || "root", identity: draft.identity,
    name: "", username: draft.username, device_password: draft.devicePassword, addr: draft.addr,
    sub_prefix: draft.subPrefix, sni: draft.sni, inbound_port: draft.inboundPort || 0,
  };
}
export function passwordStrength(password: string): number {
  return Number(password.length >= 12) + Number(password.length >= 16)
    + Number(/\d/.test(password) && /[A-Za-z]/.test(password)) + Number(/[^A-Za-z0-9]/.test(password));
}
export function formatDuration(ms: number): string {
  const seconds = Math.max(0, ms) / 1000;
  return seconds < 60 ? seconds.toFixed(1) + " 秒" : Math.floor(seconds / 60) + ":" + String(Math.round(seconds % 60)).padStart(2, "0");
}
export function formatClock(seconds: number): string {
  const value = Math.max(0, Math.floor(seconds || 0));
  return Math.floor(value / 60) + ":" + String(value % 60).padStart(2, "0");
}
export function errorMessage(error: unknown): string { return error instanceof Error ? error.message : String(error); }
