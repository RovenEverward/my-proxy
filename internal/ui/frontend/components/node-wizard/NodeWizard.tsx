import { useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { DrawerBody, DrawerFooter, DrawerHeader, Field, FlagText, Icon } from "../primitives";
import { NodeEditor } from "../../pages/nodes/NodeEditor";
import { useApp } from "../../runtime/app-context";
import { api, write } from "../../runtime/api";
import type { Plan } from "../../runtime/types";
import { autoNodeName, errorMessage, nodePath, type NodeView } from "../../domain/nodes";
import type { ConnectionInbound } from "../../domain/connections";

type RowState = "wait" | "run" | "done" | "fail";
interface ProgressRow { status: RowState; title: string; duration: string; error?: string; hint?: string }
interface WizardState {
  step: 1 | 2 | 3 | 4; address: string; sshPort: string; sshUser: string; password: string; showPassword: boolean;
  preset: "reality" | "custom"; inboundPort: string; sni: string; destination: string; errors: Record<string, string>;
  name: string; display: string; locateNote: string; nodeId: number | null; inboundId: number | null;
  stage: number; rows: ProgressRow[]; plans: Plan[] | null;
}
const ROW_TITLES = ["登录并装公钥", "登记节点", "建连接方式和对外地址", "装代理程序并应用配置"];
const initial = (): WizardState => ({ step: 1, address: "", sshPort: "22", sshUser: "root", password: "", showPassword: false, preset: "reality", inboundPort: "443", sni: "www.cloudflare.com", destination: "www.cloudflare.com:443", errors: {}, name: "", display: "", locateNote: "", nodeId: null, inboundId: null, stage: 0, rows: ROW_TITLES.map(title => ({ title, status: "wait", duration: "" })), plans: null });
const duration = (ms: number) => ms < 60000 ? `${(Math.max(0, ms) / 1000).toFixed(1)} 秒` : `${Math.floor(ms / 60000)}:${String(Math.round(ms / 1000 % 60)).padStart(2, "0")}`;
function loginHint(message: string): string {
  if (/refused|timed? ?out|timeout|no route|unreachable|拒绝|超时|连不上/i.test(message)) return "连不上这台机器的 SSH：检查地址和 SSH 端口，以及 VPS 的防火墙或安全组是否放行。节点还没有建，不用回退。";
  if (/password|permission denied|auth|密码/i.test(message)) return "密码不对，或这台机器关了密码登录。节点还没有建，不用回退。";
  return "按上面的原话处理后再试。节点还没有建，不用回退。";
}
async function pickInboundPort(nodeId: number, wanted: number, auto: boolean): Promise<{ port: number; owner?: string }> {
  let ports: { tcp?: { port: number; owner: string; core?: boolean }[] };
  try { ports = await api(`${nodePath(nodeId)}/ports`); } catch { return { port: wanted }; }
  const busy = new Map((ports.tcp || []).filter(port => !port.core).map(port => [port.port, port.owner]));
  if (!busy.has(wanted)) return { port: wanted };
  const owner = busy.get(wanted);
  if (!auto) throw new Error(`${wanted} 端口已被 ${owner} 占用，代理程序起不来。点「改端口」换一个，或先停掉占用它的程序再重试。`);
  const alternate = [443, 2087, 2053, 2083, 2096, 8443, 9443].find(port => port !== wanted && !busy.has(port));
  if (!alternate) throw new Error(`${wanted} 端口已被 ${owner} 占用，备选端口也都被占了。点「改端口」换一个。`);
  return { port: alternate, owner };
}
interface Props { onChanged?: () => Promise<void>; onRegister?: () => void }
export function NodeWizard({ onChanged, onRegister }: Props = {}) {
  const app = useApp();
  const changed = onChanged || app.refresh;
  const [state, setState] = useState<WizardState>(initial);
  const mounted = useRef(true), running = useRef(false);
  useEffect(() => { mounted.current = true; const drawer = document.getElementById("drawer"); drawer?.classList.add("wiz"); return () => { mounted.current = false; drawer?.classList.remove("wiz"); }; }, []);
  const update = (patch: Partial<WizardState>) => { if (mounted.current) setState(previous => ({ ...previous, ...patch })); };
  const row = (index: number, patch: Partial<ProgressRow>) => { if (mounted.current) setState(previous => ({ ...previous, rows: previous.rows.map((value, position) => position === index ? { ...value, ...patch } : value) })); };
  const input = (key: "address" | "sshPort" | "sshUser" | "password" | "inboundPort" | "sni" | "destination", value: string) => setState(previous => ({ ...previous, [key]: value, errors: Object.fromEntries(Object.entries(previous.errors).filter(([field]) => field !== key)) }));
  const register = onRegister || (() => app.openDrawer(<NodeEditor onChanged={changed} />, { key: "node:new" }));
  const validateConnection = () => {
    const errors: Record<string, string> = {};
    if (!state.address.trim()) errors.address = "请填写服务器地址";
    if (!/^\d+$/.test(state.sshPort.trim()) || Number(state.sshPort) < 1 || Number(state.sshPort) > 65535) errors.sshPort = "端口要在 1–65535 之间";
    if (!state.password) errors.password = "请输入 SSH 密码";
    update({ errors }); if (!Object.keys(errors).length) update({ step: 2 });
  };
  const runFrom = async (stage: number) => {
    if (running.current) return;
    running.current = true;
    let nodeId = state.nodeId, inboundId = state.inboundId;
    let name = state.name, display = state.display, locateNote = state.locateNote, port = Number(state.inboundPort);
    const address = state.address, sshPort = state.sshPort, sshUser = state.sshUser, preset = state.preset, sni = state.sni, destination = state.destination;
    let password = state.password;
    update({ step: 3, stage, password: "", errors: {} });
    try {
      if (stage <= 0 && !nodeId) {
        const start = Date.now(); row(0, { status: "run", error: undefined, hint: undefined });
        try {
          name ||= await autoNodeName(); update({ name });
          const request = write<NodeView>("POST", "/api/nodes", { id: 0, name, address, ssh_port: Number(sshPort), ssh_user: sshUser === "root" ? "" : sshUser, api_port: 0, config_path: "", service_name: "", draining: false, ssh_password: password });
          password = "";
          const result = await request;
          nodeId = result.id; display = result.display || ""; locateNote = result.locate_note || "";
          update({ nodeId, display, locateNote }); row(0, { status: "done", duration: duration(Date.now() - start) }); row(1, { status: "done", title: `登记节点 ${display || address}`, duration: duration(Date.now() - start) });
        } catch (reason) { const message = errorMessage(reason); row(0, { status: "fail", duration: duration(Date.now() - start), error: message, hint: loginHint(message) }); return; }
      }
      if (!nodeId) return;
      if (stage <= 2 && state.rows[2].status !== "done") {
        update({ stage: 2 }); const start = Date.now(); row(2, { status: "run", error: undefined, hint: undefined });
        try {
          if (!inboundId) {
            const picked = await pickInboundPort(nodeId, port, preset !== "custom");
            if (picked.port !== port) { row(2, { title: `建连接方式：${port} 端口已被 ${picked.owner} 占用，改用 ${picked.port}` }); port = picked.port; update({ inboundPort: String(port) }); }
            const inbound = await write<ConnectionInbound>("POST", "/api/inbounds", { id: 0, node_id: nodeId, tag: "", protocol: "vless", listen: "", port, network: "tcp", security: "reality", sni, reality_dest: destination, wg_mtu: 1420, flow: "xtls-rprx-vision" });
            inboundId = inbound.id; update({ inboundId });
          }
          await write("POST", `/api/inbounds/${inboundId}/hosts`, { id: 0, inbound_id: inboundId, remark: "", address: "", port: 0 });
          row(2, { status: "done", duration: duration(Date.now() - start) });
        } catch (reason) { row(2, { status: "fail", duration: duration(Date.now() - start), error: errorMessage(reason), hint: "节点已经建了，公钥也在上面，重试不再需要密码。" }); return; }
      }
      update({ stage: 3 }); const start = Date.now(); row(3, { status: "run", error: undefined, hint: undefined });
      try {
        await write("POST", `${nodePath(nodeId)}/install`); row(3, { status: "done", duration: duration(Date.now() - start) });
        let plans: Plan[] | null = null;
        try { const [node, result] = await Promise.all([api<NodeView>(nodePath(nodeId)), api<Plan[]>("/api/plans")]); display = node.display || display; locateNote = node.locate_note || locateNote; plans = result; } catch { /* Use the values returned when the node was registered. */ }
        update({ step: 4, plans, display, locateNote }); await changed();
      } catch (reason) { row(3, { status: "fail", duration: duration(Date.now() - start), error: errorMessage(reason), hint: "节点、连接方式、对外地址都保留。常见原因是机器暂时下不了包，过一会儿重试即可。" }); }
    } finally { password = ""; running.current = false; }
  };
  const start = () => {
    const errors: Record<string, string> = {};
    if (!/^\d+$/.test(state.inboundPort.trim()) || Number(state.inboundPort) < 1 || Number(state.inboundPort) > 65535) errors.inboundPort = "端口要在 1–65535 之间";
    if (!/^[^:\s]+:\d{1,5}$/.test(state.destination.trim())) errors.destination = "回落目标写成 域名:端口";
    if (!state.sni.trim()) errors.sni = "请填写 SNI";
    update({ errors }); if (Object.keys(errors).length) return;
    if (state.nodeId && !state.inboundId) { row(2, { status: "wait", title: ROW_TITLES[2], error: undefined, hint: undefined }); void runFrom(2); }
    else void runFrom(0);
  };
  const failed = state.rows.findIndex(value => value.status === "fail");
  let steps = state.step === 1 ? ["cur", "", "", ""] : state.step === 2 ? ["done", "cur", "", ""] : state.step === 4 ? ["done", "done", "done", "cur"] : failed === 0 ? ["fail", "", "", ""] : ["done", "done", failed > 0 ? "fail" : "cur", ""];
  const field = (key: "address" | "sshPort" | "sshUser" | "password" | "inboundPort" | "sni" | "destination", id: string, label: string, placeholder: string, hint?: string, numeric = false) => <Field label={label} htmlFor={id} hint={hint ? <FlagText>{hint}</FlagText> : undefined} error={state.errors[key]}><Input id={id} className={`in mono ${state.errors[key] ? "bad" : ""}`} value={state[key]} placeholder={placeholder} inputMode={numeric ? "numeric" : undefined} autoComplete="off" aria-invalid={!!state.errors[key]} aria-describedby={state.errors[key] ? `${id}-error` : hint ? `${id}-hint` : undefined} onChange={event => input(key, event.target.value)} /></Field>;
  let body: ReactNode, footer: ReactNode;
  if (state.step === 1) {
    body = <><p className="lead">填一台新 VPS 的 SSH 信息。密码只用来装一次控制面的公钥，用完即丢，不保存。<button type="button" className="lnk wlnk" data-act="node-new" onClick={register}>已能免密 SSH，直接登记</button></p>
      {field("address", "w-address", "服务器地址", "203.0.113.10", "显示名(如 🇯🇵 东京 A)按这台机器的 IP 自动识别，之后可在节点页改")}
      <div className="two">{field("sshPort", "w-ssh-port", "SSH 端口", "22", undefined, true)}{field("sshUser", "w-ssh-user", "SSH 用户", "root")}</div>
      <Field label="SSH 密码" htmlFor="w-pw" hint="机房给的初始密码即可" error={state.errors.password}><div className="pw"><Input id="w-pw" type={state.showPassword ? "text" : "password"} className={`in ${state.errors.password ? "bad" : ""}`} value={state.password} autoComplete="off" aria-invalid={!!state.errors.password} aria-describedby={state.errors.password ? "w-pw-error" : "w-pw-hint"} onChange={event => input("password", event.target.value)} /><Button type="button" size="icon" id="w-pw-eye" aria-label={state.showPassword ? "隐藏密码" : "显示密码"} onClick={() => { update({ showPassword: !state.showPassword }); requestAnimationFrame(() => { const input = document.getElementById("w-pw"); if (input instanceof HTMLInputElement) { input.focus(); input.setSelectionRange(input.value.length, input.value.length); } }); }}><Icon name={state.showPassword ? "eyeoff" : "eye"} /></Button></div></Field></>;
    footer = <><Button data-act="close" onClick={app.closeOverlays}>取消</Button><Button variant="primary" id="w-next" onClick={validateConnection}>下一步</Button></>;
  } else if (state.step === 2) {
    body = <><p className="lead">给这台节点加一条连接方式。默认值与首次安装时建的一致。</p><div className="opts" role="radiogroup" aria-label="连接方式预设"><button type="button" className={`opt ${state.preset === "reality" ? "on" : ""}`} id="w-opt-reality" role="radio" aria-checked={state.preset === "reality"} onClick={() => update({ preset: "reality", inboundPort: "443", sni: "www.cloudflare.com", destination: "www.cloudflare.com:443", errors: {} })}><span className="rd" /><div><b>REALITY · 443（推荐）</b><span className="h">VLESS + TCP + REALITY，SNI 和回落目标 www.cloudflare.com</span></div></button><button type="button" className={`opt ${state.preset === "custom" ? "on" : ""}`} id="w-opt-custom" role="radio" aria-checked={state.preset === "custom"} onClick={() => update({ preset: "custom", errors: {} })}><span className="rd" /><div><b>自定义</b><span className="h">在下面的高级里改端口、SNI 和回落目标</span></div></button></div>
      <details className="adv" id="w-adv" open={state.preset === "custom"}><summary>高级</summary><div className="two">{field("inboundPort", "w-inb-port", "端口", "443", undefined, true)}{field("sni", "w-sni", "SNI", "www.cloudflare.com")}</div>{field("destination", "w-dest", "回落目标", "域名:端口")}</details></>;
    footer = <><Button variant="ghost" id="w-back" onClick={() => update({ step: 1, errors: {} })}><Icon name="back" />上一步</Button><span className="sp" /><Button data-act="close" onClick={app.closeOverlays}>取消</Button><Button variant="primary" id="w-start" onClick={start}>开始安装</Button></>;
  } else if (state.step === 3) {
    body = <ol className="plist">{state.rows.map((value, index) => <li key={index} className={value.status}><span className="pi">{value.status === "run" ? <span className="spin" /> : value.status === "done" ? <Icon name="check" /> : value.status === "fail" ? <Icon name="x" /> : <i />}</span><span className="t"><FlagText>{value.title}</FlagText></span><span className="du">{value.duration}</span>{value.error && <div className="sub2"><div className="raw">{value.error}</div><div className="mt2">{value.hint}</div></div>}</li>)}</ol>;
    if (failed === 0) footer = <><span className="sp" /><Button variant="primary" id="w-back1" onClick={() => update({ step: 1, password: "", errors: { password: "请重新输入 SSH 密码" } })}><Icon name="back" />返回修改</Button></>;
    else if (failed === 2) footer = <><Button variant="ghost" id="w-del" onClick={() => app.confirmBox({ title: `删除刚加的节点 ${state.display || state.address}？`, text: "节点、它的连接方式和对外地址一起删掉。公钥会留在这台机器上，要清理得自己登上去。", ok: "删除节点", danger: true, run: async () => { await write("DELETE", nodePath(state.nodeId!)); app.closeOverlays(); await changed(); } })}>删除刚加的节点</Button><span className="sp" /><Button id="w-port" onClick={() => update({ step: 2, preset: "custom", errors: {} })}><Icon name="back" />改端口</Button><Button variant="primary" id="w-retry" onClick={() => void runFrom(state.stage)}><Icon name="refresh" />重试这一步</Button></>;
    else if (failed === 3) footer = <><span className="sp" /><Button id="w-later" onClick={() => { app.closeOverlays(); app.navigate(`nodes/${state.nodeId}`); }}>稍后在节点页处理</Button><Button variant="primary" id="w-retry" onClick={() => void runFrom(3)}><Icon name="refresh" />重试安装</Button></>;
    else footer = <><span className="sub" style={{ flex: 1 }}>每一步都是一次普通请求，关掉抽屉也能在节点页接着装</span><Button variant="primary" disabled>安装中…</Button></>;
  } else {
    const allPlans = state.plans?.filter(plan => plan.all_nodes);
    body = <><div className="done-hd"><span className="ok"><Icon name="check" /></span><div><h3><FlagText>{`${state.display || state.address} 已就绪`}</FlagText></h3><p>{state.address} · xray 已安装，配置已应用</p></div></div><dl className="kvw"><dt>显示名</dt><dd><FlagText>{state.display || "—"}</FlagText> <span className="sub">· {state.locateNote || "按 IP 自动识别，可在节点页改"}</span></dd><dt>连接方式</dt><dd className="mono">REALITY · {state.inboundPort} · 1 个对外地址</dd><dt>进了哪些订阅</dt><dd>{!allPlans ? "到套餐页查看" : !allPlans.length ? "没有包含全部节点的套餐，需要的话到套餐页勾上" : <>{allPlans.map(plan => plan.name).join("、")} · <b>{allPlans.reduce((total, plan) => total + (Number(plan.users) || 0), 0)}</b> 个用户的订阅会在下次刷新时拿到它</>}</dd></dl><div className="callout stone mb"><Icon name="info" /><div><b>包含全部节点的套餐会自动包含它。</b>只给部分节点的套餐不会自动加，需要的话到套餐页勾上。</div></div></>;
    footer = <><Button id="w-again" onClick={() => setState(initial())}><Icon name="plus" />再加一台</Button><span className="sp" /><Button variant="primary" id="w-view" onClick={() => { app.closeOverlays(); app.navigate(`nodes/${state.nodeId}`); }}>查看节点</Button></>;
  }
  return <><DrawerHeader title="添加节点" onClose={app.closeOverlays} /><div className="wsteps">{["连接", "连接方式", "安装", "完成"].map((title, index) => <span key={title} style={{ display: "contents" }}>{index > 0 && <span className={`ln ${steps[index - 1] === "done" ? "done" : ""}`} />}<span className={`s ${steps[index]}`}><i>{steps[index] === "done" ? <Icon name="check" /> : steps[index] === "fail" ? <Icon name="x" /> : index + 1}</i><span>{title}</span></span></span>)}</div><DrawerBody style={{ paddingTop: 4 }}>{body}</DrawerBody><DrawerFooter>{footer}</DrawerFooter></>;
}
