import { Fragment, useId, useState, type HTMLAttributes, type ReactNode } from "react";
import { ICONS, ICON_NAMES } from "./icon-map";
import { Button } from "./ui/button";
import { Label } from "./ui/label";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./ui/dropdown-menu";
import { cn } from "@/lib/utils";

export function Icon({ name, className = "" }: { name: string; className?: string }) {
  if (!Object.hasOwn(ICONS, name)) throw new Error("Unknown UI icon: " + name);
  const key = name as keyof typeof ICONS;
  const Component = ICONS[key];
  return <Component className={cn("icon", className)} data-lucide={ICON_NAMES[key]} aria-hidden="true" />;
}
function Flag({ value }: { value: string }) {
  const [failed, setFailed] = useState(false);
  const code = [...value].map(letter => String.fromCharCode(letter.codePointAt(0)! - 0x1f1e6 + 97)).join("");
  return failed ? <span className="flag-code" title={"地区 " + code.toUpperCase()}>{code.toUpperCase()}</span>
    : <span className="flag-w" data-flag="1"><img className="flag" src={"/flags/" + code + ".svg"} alt={value} draggable={false} onError={() => setFailed(true)} /></span>;
}
export function FlagText({ children }: { children: string }) {
  return <>{children.split(/([\u{1F1E6}-\u{1F1FF}]{2})/gu).map((part, index) => /[\u{1F1E6}-\u{1F1FF}]{2}/u.test(part) ? <Flag key={`${index}:${part}`} value={part} /> : <Fragment key={index}>{part}</Fragment>)}</>;
}
export function Avatar({ name }: { name: string }) { return <span className="av">{[...name][0]?.toUpperCase() || "?"}</span>; }
export function StatusBadge({ tone = "", children }: { tone?: string; children: ReactNode }) { return <span className="st"><span className={cn("dot", tone)} />{children}</span>; }
export function PageHeader({ title, description, actions, className }: { title: ReactNode; description?: ReactNode; actions?: ReactNode; className?: string }) {
  return <div className={cn("ph", className)}><div><h1>{title}</h1>{description && <p>{description}</p>}</div>{actions && <><span className="sp" />{actions}</>}</div>;
}
export function Card({ title, actions, children, className, ...props }: HTMLAttributes<HTMLDivElement> & { title?: string; actions?: ReactNode }) {
  return <div className={cn("card", className)} {...props}>{(title || actions) && <div className="ch">{title}<span className="sp" />{actions}</div>}{children}</div>;
}
export function LoadingTable({ rows = 6, columns = 3 }: { rows?: number; columns?: number }) {
  return <div className="card"><table aria-label="正在读取列表"><tbody>{Array.from({ length: rows }, (_, row) => <tr key={row}>{Array.from({ length: columns }, (_, column) => <td key={column}><span className="sk" style={{ width: [70, 50, 90, 40, 60][column % 5] + "%" }} /></td>)}</tr>)}</tbody></table></div>;
}
export function EmptyState({ icon, title, description, actions }: { icon: string; title: string; description?: ReactNode; actions?: ReactNode }) {
  return <div className="empty"><div className="ill"><Icon name={icon} /></div><h4>{title}</h4><p>{description}</p>{actions && <div className="mt">{actions}</div>}</div>;
}
export function ErrorState({ title, error, onRetry }: { title: string; error: Error | string; onRetry?: () => void }) {
  const message = typeof error === "string" ? error : error.message;
  const auth = typeof error === "object" && "status" in error && error.status === 401;
  return <div className="empty" role="alert"><div className="ill bad"><Icon name="warn" /></div><h4>读不到{title}</h4><p>{message}</p>{auth && <p className="sub mt">登录状态已失效，请重新打开 MPX 并登录。</p>}{onRetry && <div className="mt"><Button size="sm" onClick={onRetry} data-act="reload"><Icon name="refresh" />重试</Button></div>}</div>;
}
export function Field({ label, htmlFor, hint, error, children, className }: { label: ReactNode; htmlFor?: string; hint?: ReactNode; error?: ReactNode; children: ReactNode; className?: string }) {
  const generatedId = useId();
  return <div className={cn("field", className)}><Label htmlFor={htmlFor}>{label}</Label>{children}{error ? <span className="fe" id={(htmlFor || generatedId) + "-error"} role="alert">{error}</span> : hint ? <span className="h" id={(htmlFor || generatedId) + "-hint"}>{hint}</span> : null}</div>;
}
export function Tabs({ items, value }: { items: { key: string; label: ReactNode; href: string }[]; value: string }) {
  return <nav className="tabs" aria-label="页面分类">{items.map(item => <a key={item.key} className={item.key === value ? "on" : ""} href={item.href} aria-current={item.key === value ? "page" : undefined}>{item.label}</a>)}</nav>;
}
export interface MenuAction { label: ReactNode; icon?: string; danger?: boolean; disabled?: boolean; reason?: string; onSelect: () => void }
export function ActionMenu({ name, items }: { name: string; items: MenuAction[] }) {
  return <span className="mw"><DropdownMenu modal={false}><DropdownMenuTrigger asChild><Button size="icon-sm" title="更多" aria-label={name + " 的更多操作"} data-act="row-menu"><Icon name="more" /></Button></DropdownMenuTrigger><DropdownMenuContent className="menu ui-menu" align="end">{items.map((item, index) => <DropdownMenuItem key={index} asChild disabled={item.disabled} onSelect={() => { if (!item.disabled) item.onSelect(); }}><button type="button" className={item.danger ? "dg" : ""} title={item.reason}>{item.icon && <Icon name={item.icon} />}{item.label}</button></DropdownMenuItem>)}</DropdownMenuContent></DropdownMenu></span>;
}
export function DrawerHeader({ title, description, onClose }: { title: ReactNode; description?: ReactNode; onClose?: () => void }) {
  return <div className="dh"><div className="dt"><h2>{title}</h2>{description && <span className="sub">{description}</span>}</div><span className="sp" />{onClose && <Button size="icon" onClick={onClose} aria-label="关闭" data-act="close"><Icon name="x" /></Button>}</div>;
}
export function DrawerBody({ className, ...props }: HTMLAttributes<HTMLDivElement>) { return <div className={cn("db", className)} {...props} />; }
export function DrawerFooter({ className, ...props }: HTMLAttributes<HTMLDivElement>) { return <div className={cn("df", className)} {...props} />; }
