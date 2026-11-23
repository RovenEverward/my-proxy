// 探测点(UC-21):境内常驻主机,跑 mpx probe run 常驻执行 CDN IP 优选任务。
// 它是独立名册对象,不是代理节点:没有 SSH、没有核心、不进订阅。

export interface ProbePoint {
  id: number; name: string; created_at: string;
  last_seen_at?: string; last_ip?: string; online: boolean;
}

export interface ProbeCreated extends ProbePoint { join_token: string; join_command: string }
