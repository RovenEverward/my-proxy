import { Fragment, useEffect, useRef, type ComponentProps, type KeyboardEvent, type ReactNode } from "react";
import { Icon } from "@/components/primitives";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useApp } from "@/runtime/app-context";
import type { ProgressState } from "./model";

export type WizardState = "" | "cur" | "done" | "fail";
export function SetupFrame({ steps, children, footer }: { steps?: WizardState[]; children: ReactNode; footer: ReactNode }) {
  const { meta } = useApp();
  const frame = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previous = document.activeElement;
    frame.current?.focus({ preventScroll: true });
    return () => { if (previous instanceof HTMLElement && previous.isConnected) previous.focus({ preventScroll: true }); };
  }, []);
  const containFocus = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "Tab") return;
    const controls = [...event.currentTarget.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [tabindex="0"]')].filter(element => element.getClientRects().length > 0);
    const first = controls[0], last = controls.at(-1);
    if (!first || !last) { event.preventDefault(); return; }
    if (event.shiftKey && (document.activeElement === first || document.activeElement === event.currentTarget)) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  return <div className="setup" ref={frame} role="dialog" aria-modal="true" aria-label="MPX 安装与接入" tabIndex={-1} style={{ outline: "none" }} onKeyDown={containFocus}><div className="tb-drag" /><div className="sbrand">
    <img className="lk lk-l" src="/lockup-light.svg" alt="MPX" /><img className="lk lk-d" src="/lockup-dark.svg" alt="MPX" /> · 首次安装
    {meta.version && <small>{meta.version}</small>}
  </div><div className="scard">{steps && <div className="wsteps" aria-label="安装进度">{["连接", "检查", "设置", "安装", "完成"].map((title, index) => <Fragment key={title}>
    {index > 0 && <span className={"ln " + (steps[index - 1] === "done" ? "done" : "")} />}
    <span className={"s " + (steps[index] || "")} aria-current={steps[index] === "cur" ? "step" : undefined}><i>{steps[index] === "done" ? <Icon name="check" /> : steps[index] === "fail" ? <Icon name="x" /> : index + 1}</i><span>{title}</span></span>
  </Fragment>)}</div>}<div className="db">{children}</div><div className="df">{footer}</div></div></div>;
}
export function PasswordInput({ id, shown, onToggle, className = "", ...props }: Omit<ComponentProps<typeof Input>, "type" | "id"> & { id: string; shown: boolean; onToggle: () => void }) {
  const container = useRef<HTMLDivElement>(null);
  return <div className="pw" ref={container}><Input {...props} id={id} className={"in " + (shown ? "mono " : "") + className} type={shown ? "text" : "password"} autoComplete="off" />
    <Button size="icon" id={id + "-eye"} aria-label={shown ? "隐藏密码" : "显示密码"} title={shown ? "隐藏密码" : "显示密码"} onClick={() => {
      onToggle(); container.current?.querySelector("input")?.focus();
    }}><Icon name={shown ? "eyeoff" : "eye"} /></Button></div>;
}
export function ProgressRow({ state, title, duration, children }: { state: ProgressState; title: ReactNode; duration?: string; children?: ReactNode }) {
  return <li className={state}><span className="pi">{state === "run" ? <span className="spin" /> : state === "done" ? <Icon name="check" /> : state === "fail" ? <Icon name="x" /> : state === "skip" ? <Icon name="dash" /> : <i />}</span><span className="t">{title}</span><span className="du">{duration || ""}</span>{children}</li>;
}
export function Callout({ tone, title, children, id }: { tone: "bad" | "warn" | "stone"; title: ReactNode; children?: ReactNode; id?: string }) {
  return <div className={"callout " + tone + " mb"} id={id} role={tone === "bad" ? "alert" : undefined}><Icon name={tone === "stone" ? "check" : "warn"} className={tone === "bad" ? "ico-bad" : tone === "warn" ? "ico-warn" : ""} /><div><b>{title}</b>{children}</div></div>;
}
export function fieldDescription(id: string, error?: string, hint?: boolean): { "aria-invalid"?: true; "aria-describedby"?: string } {
  return { "aria-invalid": error ? true : undefined, "aria-describedby": error ? id + "-error" : hint ? id + "-hint" : undefined };
}
