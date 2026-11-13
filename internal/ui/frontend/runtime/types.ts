import type { ReactNode } from "react";

export interface Meta {
  server: string;
  profile: string;
  allow: string[];
  cli_version?: string;
  version?: string;
  fingerprint?: string;
  profiles?: boolean;
  connect?: boolean;
  relabel?: boolean;
  mode?: "normal" | "setup";
}

export type Read = <T>(path: string, init?: RequestInit & { asText?: boolean }) => Promise<T>;
export interface PageProps { route: string[] }
export interface PageData<T> {
  data: T | null;
  error: Error | null;
  loading: boolean;
  refreshing: boolean;
  at: Date | null;
  refresh: (fresh?: boolean) => Promise<void>;
}
export interface ToastAction { label: string; run: () => void }
export interface Confirmation {
  title: ReactNode;
  text: ReactNode;
  ok: ReactNode;
  danger?: boolean;
  typed?: string;
  extra?: ReactNode;
  lock?: boolean;
  run: () => Promise<unknown> | unknown;
  alt?: { label: string; run: () => Promise<unknown> | unknown };
}
export interface DrawerOptions { wide?: boolean; onClose?: () => void; key?: string }

export interface User {
  id: number; username: string; enabled: boolean; status: string;
  key?: string; sub_token?: string; note?: string; plan_id?: number;
  data_limit: number; used_traffic: number; lifetime_used_traffic?: number;
  expire_at?: string; reset_strategy?: string; traffic_reset_at?: string;
  inbound_ids?: number[]; created_at?: string;
  presence?: Presence;
}
export interface Presence {
  online?: boolean; active_24h?: boolean | number; last_active_at?: string;
  last_node_id?: number; node_ids?: number[]; ip_count?: number;
  updated_at?: string; stale_nodes?: { node_id: number; since: string }[];
}
export interface UserList { users: User[]; total: number; offset: number; limit?: number; counts?: Record<string, number>; presence?: Presence }
export interface Node {
  id: number; name: string; display?: string; address: string; draining: boolean;
  ssh_port?: number; ssh_user?: string; api_port?: number; config_path?: string;
  service_name?: string; outbound_ip?: string; deployed_hash?: string; deployed_at?: string;
  status?: NodeStatus;
}
export interface NodeStatus {
  reachable: boolean; active: boolean; version?: string; checked_at?: string;
  error?: string; summary?: string[]; cores?: { core: string; active: boolean; version?: string }[];
  certificates?: { not_after?: string; days_left: number; error?: string; name?: string }[];
  [key: string]: unknown;
}
export interface NodeMetadata { region?: string; city?: string; letter?: string }
export interface Inbound {
  id: number; node_id: number; tag?: string; protocol: string; port: number;
  listen?: string; network?: string; security?: string; sub_name?: string;
  max_users?: number; [key: string]: unknown;
}
export interface Host {
  id: number; inbound_id: number; remark?: string; address?: string; port?: number;
  sub_name?: string; auto_name?: boolean; effective?: { address?: string; port?: number };
  [key: string]: unknown;
}
export interface Plan {
  id: number; name: string; note?: string; is_default: boolean; all_nodes: boolean;
  node_ids?: number[]; inbound_ids?: number[]; users: number; active_users: number;
  extra_auth?: number;
}
export interface Settings {
  sub_url_prefix: string; profile_title: string; support_link: string; update_interval: number;
  webhook_url: string; notify_days_left: number; notify_usage_percent: number;
  ruleset_mode?: string;
  rules: ClientRule[] | null; templates: Record<string, boolean> | null;
}
export interface ClientRule { pattern: string; result: string; label?: string }
export interface SubPull { username: string; client: string; first_pulled_at: string; last_pulled_at: string }
export interface Status {
  version: string; uptime_seconds: number; counts: Record<string, number>;
  last_tick_at?: string; last_tick_seconds?: number;
  update?: { behind?: number; latest?: string };
  backoffs: { node_id: number; node_name: string; failures: number; skip_until: string }[];
}
