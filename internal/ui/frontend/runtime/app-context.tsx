import { createContext, useContext, useEffect, type ReactNode } from "react";
import type { Meta, Confirmation, DrawerOptions, ToastAction } from "./types";

export interface Crumb { label: string; href?: string }
export type LeaveGuard = (next: string | (() => void)) => boolean | Promise<boolean>;
export interface AppContextValue {
  meta: Meta;
  route: string[];
  navigationState: Record<string, string>;
  can: (method: string, path: string) => boolean;
  navigate: (to: string, state?: Record<string, string>) => void;
  runGuarded: (action: () => void) => void;
  refresh: () => Promise<void>;
  setCrumbs: (crumbs: Crumb[]) => void;
  setBusy: (busy: boolean) => void;
  setLeaveGuard: (guard: LeaveGuard | null) => void;
  toast: (text: ReactNode, bad?: boolean, action?: ToastAction) => void;
  openDrawer: (content: ReactNode, options?: DrawerOptions) => void;
  closeOverlays: () => void;
  closeModal: () => void;
  openModal: (content: ReactNode) => void;
  setModalLocked: (locked: boolean) => void;
  confirmBox: (confirmation: Confirmation) => void;
  openUpdate: () => void;
  openProfiles: () => void;
  openConnect: () => void;
}
export const AppContext = createContext<AppContextValue | null>(null);
export function useApp(): AppContextValue {
  const value = useContext(AppContext);
  if (!value) throw new Error("AppProvider is required");
  return value;
}
export function usePageTitle(crumbs: Crumb[]) {
  const { setCrumbs } = useApp();
  const key = JSON.stringify(crumbs);
  useEffect(() => { setCrumbs(JSON.parse(key) as Crumb[]); }, [key, setCrumbs]);
}
export function useLeaveGuard(guard: LeaveGuard | null) {
  const { setLeaveGuard } = useApp();
  useEffect(() => { setLeaveGuard(guard); return () => setLeaveGuard(null); }, [guard, setLeaveGuard]);
}
