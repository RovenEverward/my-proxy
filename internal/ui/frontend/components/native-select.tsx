import { Children, forwardRef, isValidElement, useLayoutEffect, useReducer, useRef, useState, type ReactNode, type SelectHTMLAttributes } from "react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { FlagText } from "./primitives";

interface Option { value: string; label: string; disabled: boolean }
const optionText = (children: ReactNode): string => Children.toArray(children).map(child => typeof child === "string" || typeof child === "number" ? String(child) : isValidElement<{ children?: ReactNode }>(child) ? optionText(child.props.children) : "").join("");
function optionsOf(children: ReactNode, groupDisabled = false): Option[] {
  return Children.toArray(children).flatMap(child => {
    if (!isValidElement<{ value?: string | number; children?: ReactNode; label?: string; disabled?: boolean }>(child)) return [];
    if (child.type === "optgroup") return optionsOf(child.props.children, groupDisabled || !!child.props.disabled);
    if (child.type !== "option") return [];
    const label = child.props.label || optionText(child.props.children);
    return [{ value: child.props.value === undefined ? optionText(child.props.children) : String(child.props.value), label, disabled: groupDisabled || !!child.props.disabled }];
  });
}
export const NativeSelect = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(function NativeSelect({ children, onChange, onPointerDown, onMouseDown, onKeyDown, disabled, ...props }, forwardedRef) {
  const select = useRef<HTMLSelectElement | null>(null);
  const [open, setOpen] = useState(false);
  const [, render] = useReducer(value => value + 1, 0);
  const [options, setOptions] = useState(() => optionsOf(children));
  useLayoutEffect(() => {
    // Read the rendered options so reusable option components work too.
    const next = [...(select.current?.options || [])].map(option => ({ value: option.value, label: option.label, disabled: option.disabled || option.parentElement instanceof HTMLOptGroupElement && option.parentElement.disabled }));
    setOptions(previous => previous.length === next.length && previous.every((option, index) => option.value === next[index].value && option.label === next[index].label && option.disabled === next[index].disabled) ? previous : next);
  }, [children]);
  const selected = props.value === undefined ? select.current?.value ?? String(props.defaultValue ?? options[0]?.value ?? "") : String(props.value);
  const change = (value: string) => {
    const element = select.current;
    if (!element || element.value === value) return;
    element.value = value;
    element.dispatchEvent(new Event("change", { bubbles: true }));
  };
  return <DropdownMenu open={open} onOpenChange={setOpen} modal={false}><DropdownMenuTrigger asChild><select {...props} style={open ? { ...props.style, outline: "2px solid var(--ui-focus-strong-ring)", outlineOffset: 0 } : props.style} disabled={disabled} ref={element => { select.current = element; if (typeof forwardedRef === "function") forwardedRef(element); else if (forwardedRef) forwardedRef.current = element; }} onChange={event => { onChange?.(event); render(); }} onPointerDown={event => {
    onPointerDown?.(event);
    if (event.defaultPrevented || disabled || event.button !== 0 || event.ctrlKey) return;
    event.preventDefault(); event.currentTarget.focus(); setOpen(value => !value);
  }} onMouseDown={event => { onMouseDown?.(event); if (!disabled) event.preventDefault(); }} onKeyDown={event => {
    onKeyDown?.(event); if (event.defaultPrevented || disabled) return;
    if (!open && !event.altKey && ["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const enabled = options.filter(option => !option.disabled), index = enabled.findIndex(option => option.value === event.currentTarget.value);
      const target = event.key === "Home" ? 0 : event.key === "End" ? enabled.length - 1 : Math.min(enabled.length - 1, Math.max(0, index + (event.key === "ArrowDown" ? 1 : -1)));
      if (enabled[target]) change(enabled[target].value);
    }
  }}>{children}</select></DropdownMenuTrigger><DropdownMenuContent className="selmenu ui-menu" align="start" style={{ minWidth: "var(--radix-dropdown-menu-trigger-width)", maxHeight: "min(300px, var(--radix-dropdown-menu-content-available-height))" }} onCloseAutoFocus={event => { event.preventDefault(); select.current?.focus(); }}>
    {options.map((option, index) => <DropdownMenuItem key={`${option.value}:${index}`} asChild disabled={option.disabled} onSelect={() => { change(option.value); setOpen(false); }}><button type="button" className={`selopt ${selected === option.value ? "on" : ""}`} aria-current={selected === option.value ? "true" : undefined}><span><FlagText>{option.label}</FlagText></span></button></DropdownMenuItem>)}
  </DropdownMenuContent></DropdownMenu>;
});
