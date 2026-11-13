import { useLayoutEffect, useRef, type HTMLAttributes, type KeyboardEvent } from "react";

export function Menu({ open, children, ...props }: HTMLAttributes<HTMLDivElement> & { open: boolean }) {
  const element = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const menu = element.current;
    if (!open || !menu) return;
    menu.style.top = ""; menu.style.bottom = "";
    const bounds = menu.getBoundingClientRect();
    if (bounds.bottom > window.innerHeight - 8 && bounds.top > bounds.height) { menu.style.top = "auto"; menu.style.bottom = "calc(100% + 4px)"; }
    menu.querySelector<HTMLButtonElement>("button:not([disabled])")?.focus();
  }, [open]);
  const keyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const items = [...event.currentTarget.querySelectorAll<HTMLButtonElement>("button:not([disabled])")];
    const index = items.findIndex(item => item === document.activeElement);
    const target = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
    items[target]?.focus();
  };
  return <div {...props} ref={element} className={`menu ${props.className || ""}`} role="menu" hidden={!open} onKeyDown={keyDown}>{children}</div>;
}
