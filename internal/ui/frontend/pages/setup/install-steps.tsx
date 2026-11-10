import { useEffect, useRef, useState } from "react";
import { Icon } from "@/components/primitives";
import { Button } from "@/components/ui/button";
import { api } from "@/runtime/api";
import { useApp } from "@/runtime/app-context";
import { Callout, ProgressRow, SetupFrame } from "./components";
import { formatClock, formatDuration, type SetupResult } from "./model";
import type { InstallationState } from "./use-installation";

function InstallLog({ lines }: { lines: string[] }) {
  const log = useRef<HTMLDivElement>(null);
  useEffect(() => { if (log.current) log.current.scrollTop = log.current.scrollHeight; }, [lines]);
  return <div className="logt" ref={log}>{lines.join("\n")}</div>;
}
export function InstallationStep({ state, starting, onRetry }: { state: InstallationState; starting: boolean; onRetry: () => void }) {
  const failed = state.phase === "failed";
  const steps = state.steps.length ? state.steps : [{ title: "准备安装", state: "run" as const, secs: 0 }];
  return <SetupFrame steps={["done", "done", "done", failed ? "fail" : "cur", ""]} footer={failed
    ? <><span className="sp" /><Button id="s-rerun" variant="primary" loading={starting} onClick={onRetry}><Icon name="refresh" />重新运行</Button></>
    : <><span className="sub" style={{ flex: 1 }}>已用 {formatClock(state.elapsed)} · 最长约 25 分钟</span><Button variant="primary" disabled>安装中…</Button></>}>
    <h2>安装</h2>{!failed && <div className="banner"><Icon name="term" /><span>安装期间不要关闭 MPX。可以刷新这个页面，进度会接着显示。</span></div>}
    <ol className="plist" aria-busy={!failed}>{steps.map((step, index) => <ProgressRow key={index} state={step.state} title={step.title} duration={step.secs > 0 ? formatDuration(step.secs * 1000) : ""}>
      {!failed && step.state === "run" && <><div className="sub2"><span className="tog"><Icon name="arrow" />最近 50 行输出</span></div><InstallLog lines={state.lines} /></>}
      {failed && step.state === "fail" && <InstallLog lines={state.lines} />}
    </ProgressRow>)}</ol>
    {failed && <Callout tone="bad" title="安装失败"><div className="raw">{state.error || "安装没有完成"}</div><div className="mt2">每一步都可以安全重跑，已经装好的部分会复用。处理完点「重新运行」即可。</div></Callout>}
  </SetupFrame>;
}
function CopyBox({ value }: { value: string }) {
  const { toast } = useApp();
  const copy = async () => {
    try { await navigator.clipboard.writeText(value); toast("已复制"); }
    catch { toast("复制失败，请手动复制", true); }
  };
  return <div className="cmdc"><div className="cmd">{value}</div><Button size="icon" data-act="copy" data-text={value} aria-label="复制" title="复制" onClick={() => void copy()}><Icon name="copy" /></Button></div>;
}
export function CompletionStep({ result, host, username }: { result: SetupResult | null; host: string; username: string }) {
  const address = result?.host_port || host || "";
  const name = result?.username || username || "";
  const connected = !!result?.connected;
  const [url, setUrl] = useState("");
  useEffect(() => {
    let active = true;
    setUrl("");
    if (connected) void api<{ auto?: string; links?: string }>(`/api/users/${encodeURIComponent(name)}/links`).then(response => {
      if (active) setUrl(response?.auto || response?.links || "");
    }).catch(() => {});
    return () => { active = false; };
  }, [connected, name]);
  return <SetupFrame steps={["done", "done", "done", "done", "cur"]} footer={<><span className="sp" /><Button id="s-enter" variant="primary" onClick={() => location.reload()}>进入管理界面<Icon name="arrow" /></Button></>}>
    <div className="done-hd"><span className="ok"><Icon name="check" /></span><div><h3>装好了</h3><p>控制面已经在 <b>{address}</b> 上运行{connected ? "，这台电脑已经接入。" : "。"}</p></div></div>
    {connected ? url ? <div className="qrw"><img className="qr" src={"/local/qr.svg?d=" + encodeURIComponent(url)} alt="订阅二维码" /><div className="field"><label>{name} 的订阅链接</label><CopyBox value={url} /><span className="h">在手机客户端里扫码，或复制链接导入</span></div></div>
      : <div className="field"><label>订阅链接</label><span className="h">到用户页查看订阅链接</span></div>
      : <div className="callout stone mb"><Icon name="info" /><div><b>本机没有自动接入</b><div className="mt2">在这台电脑的 MPX 里选「接入已有控制面」，输入下面的地址和设备密码。</div></div></div>}
    <div className="field"><label>在另一台设备上接入</label>{address ? <CopyBox value={address} /> : <span className="h">控制面地址未知</span>}<span className="h">在 MPX 里选「接入已有控制面」，输入这个地址，核对证书指纹后输入设备密码。</span></div>
    <div className="field"><label>证书指纹</label><div className="cmd">{result?.fingerprint || "（未知）"}</div></div>
    <p className="sub">本机用了 <code>~/.ssh</code> 里的一把密钥；VPS 上追加了这把公钥和控制面自己的公钥。</p>
  </SetupFrame>;
}
