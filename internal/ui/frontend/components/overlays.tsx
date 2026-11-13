import { useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Dialog, DialogContent, DialogTitle } from "./ui/dialog";
import { Button } from "./ui/button";
import { FlagText, Icon } from "./primitives";
import type { Confirmation, DrawerOptions, ToastAction } from "@/runtime/types";

type Overlay = { id: number; content: ReactNode; focus: HTMLElement | null };
type DrawerState = Overlay & { options: DrawerOptions };
type ModalState = Overlay & { confirmation?: Confirmation };
export function useOverlayManager() {
  const [drawer, setDrawer] = useState<DrawerState | null>(null);
  const [modal, setModal] = useState<ModalState | null>(null);
  const [toastState, setToast] = useState<{ id: number; text: ReactNode; bad?: boolean; action?: ToastAction } | null>(null);
  const sequence = useRef(0);
  const locked = useRef(false);
  const [modalLocked, setModalLocked] = useState(false);
  const onLock = useCallback((value: boolean) => { locked.current = value; setModalLocked(value); }, []);
  const drawerRef = useRef(drawer); drawerRef.current = drawer;
  const closeModal = useCallback(() => { if (!locked.current) setModal(null); }, []);
  const closeOverlays = useCallback(() => {
    if (locked.current) return;
    setModal(null);
    const previous = drawerRef.current;
    setDrawer(null);
    previous?.options.onClose?.();
  }, []);
  const dismissOverlays = useCallback(() => { if (!locked.current) { setModal(null); setDrawer(null); } }, []);
  const openDrawer = useCallback((content: ReactNode, options: DrawerOptions = {}) => {
    if (locked.current) return;
    setDrawer({ id: ++sequence.current, content, options, focus: document.activeElement as HTMLElement | null });
  }, []);
  const openModal = useCallback((content: ReactNode) => {
    if (locked.current) return;
    setModal({ id: ++sequence.current, content, focus: document.activeElement as HTMLElement | null });
  }, []);
  const confirmBox = useCallback((confirmation: Confirmation) => {
    if (locked.current) return;
    setModal({ id: ++sequence.current, content: null, confirmation, focus: document.activeElement as HTMLElement | null });
  }, []);
  const toast = useCallback((text: ReactNode, bad?: boolean, action?: ToastAction) => { setToast({ id: ++sequence.current, text, bad, action }); }, []);
  useEffect(() => {
    if (!toastState) return;
    const timer = setTimeout(() => setToast(current => current?.id === toastState.id ? null : current), toastState.bad || toastState.action ? 6000 : 2600);
    return () => clearTimeout(timer);
  }, [toastState]);
  const restoreFocus = (event: Event, element: HTMLElement | null) => { event.preventDefault(); if (element?.isConnected) element.focus({ preventScroll: true }); };
  const layers = <>
    <Dialog open={!!drawer} onOpenChange={open => { if (!open) closeOverlays(); }}>
      {drawer && <DialogContent key={drawer.options.key || drawer.id} id="drawer" aria-modal="true" className={"drawer open" + (drawer.options.wide ? " wide" : "")} showCloseButton={false} overlayProps={{ id: modal ? "drawer-scrim" : "scrim" }} aria-describedby={undefined}
        onCloseAutoFocus={event => restoreFocus(event, drawer.focus)} onEscapeKeyDown={event => { if (locked.current) event.preventDefault(); }} onPointerDownOutside={event => { if (locked.current) event.preventDefault(); }}>
        <OverlayAccessibleTitle container="drawer" fallback="详情" />{drawer.content}
      </DialogContent>}
    </Dialog>
    <Dialog open={!!modal} onOpenChange={open => { if (!open) closeModal(); }}>
      {modal && <DialogContent key={modal.id} id="modal" role={modal.confirmation ? "alertdialog" : "dialog"} aria-modal="true" className="modal open" showCloseButton={false} overlayProps={{ id: "scrim" }} aria-describedby={undefined} aria-busy={modalLocked || undefined} data-locked={modalLocked ? "true" : undefined}
        onCloseAutoFocus={event => restoreFocus(event, modal.focus)} onEscapeKeyDown={event => { event.preventDefault(); if (!locked.current) closeOverlays(); }} onPointerDownOutside={event => { if (locked.current) event.preventDefault(); }}>
        <OverlayAccessibleTitle container="modal" fallback="操作" />
        {modal.confirmation ? <ConfirmationBody spec={modal.confirmation} onClose={closeModal} onLock={onLock} onSuccess={() => setModal(current => current?.id === modal.id ? null : current)} /> : modal.content}
      </DialogContent>}
    </Dialog>
    {toastState && <div className={"toast" + (toastState.bad ? " bad" : "")} role="status"><Icon name={toastState.bad ? "warn" : "check"} />{typeof toastState.text === "string" ? <FlagText>{toastState.text}</FlagText> : toastState.text}{toastState.action && <button type="button" className="toast-act" onClick={() => { setToast(null); toastState.action?.run(); }}>{toastState.action.label}</button>}</div>}
  </>;
  return { openDrawer, openModal, closeOverlays, dismissOverlays, closeModal, confirmBox, toast, layers, setModalLocked: onLock, isLocked: () => locked.current, hasOverlay: !!drawer || !!modal };
}
function OverlayAccessibleTitle({ container, fallback }: { container: string; fallback: string }) {
  const [title, setTitle] = useState(fallback);
  useLayoutEffect(() => {
    const frame = document.getElementById(container);
    if (!frame) return;
    const sync = () => setTitle(frame.querySelector("h2")?.textContent?.trim() || fallback);
    sync();
    const observer = new MutationObserver(sync);
    observer.observe(frame, { subtree: true, childList: true, characterData: true });
    return () => observer.disconnect();
  }, [container, fallback]);
  return <DialogTitle asChild><span className="vh">{title}</span></DialogTitle>;
}
function ConfirmationBody({ spec, onClose, onSuccess, onLock }: { spec: Confirmation; onClose: () => void; onSuccess: () => void; onLock: (locked: boolean) => void }) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState<"main" | "alt" | null>(null);
  const [error, setError] = useState("");
  const disabled = !!busy || !!spec.typed && typed !== spec.typed;
  useEffect(() => () => { if (spec.lock) onLock(false); }, [onLock, spec.lock]);
  const execute = async (kind: "main" | "alt") => {
    if (disabled) return;
    setBusy(kind); setError("");
    if (spec.lock) onLock(true);
    try { await (kind === "main" ? spec.run() : spec.alt?.run()); onSuccess(); }
    catch (failure) { setError(failure instanceof Error ? failure.message : String(failure)); }
    finally { setBusy(null); if (spec.lock) onLock(false); }
  };
  return <><div className="mbody"><h2>{spec.danger && <span className="warnico"><Icon name="warn" /></span>}<span className="mt-t">{typeof spec.title === "string" ? <FlagText>{spec.title}</FlagText> : spec.title}</span></h2><div className="confirm-text">{typeof spec.text === "string" ? <p><FlagText>{spec.text}</FlagText></p> : spec.text}</div>
    {spec.typed && <div className="field"><label htmlFor="typed" className="sub">输入 <b className="c-ink">{spec.typed}</b> 确认</label><input id="typed" className={"in" + (/[\u3400-\u9fff]/.test(spec.typed) ? "" : " mono")} value={typed} onChange={event => setTyped(event.target.value)} autoFocus autoComplete="off" spellCheck={false} disabled={!!busy && spec.lock} /></div>}
    <fieldset disabled={!!busy && spec.lock} className="confirm-extra">{spec.extra}</fieldset><div className="err" id="modal-err" role="alert">{error}</div></div>
    <div className="mf"><Button data-act="modal-cancel" disabled={!!busy && spec.lock} onClick={onClose}>取消</Button>{spec.alt && <Button id="altb" disabled={disabled} loading={busy === "alt"} onClick={() => void execute("alt")}>{spec.alt.label}</Button>}<Button id="okb" variant={spec.danger ? "danger" : "primary"} disabled={disabled} loading={busy === "main"} autoFocus={!spec.typed} onClick={() => void execute("main")}>{spec.ok}</Button></div></>;
}
