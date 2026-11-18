// 探测点(UC-12):境内常驻主机,跑 mpx probe run 主动拉目标、回结果。
// 它是独立名册对象,不是代理节点:没有 SSH、没有核心、不进订阅。

export interface ProbePoint {
  id: number; name: string; created_at: string;
  last_seen_at?: string; last_ip?: string; online: boolean;
}

export interface ProbeCreated extends ProbePoint { join_token: string; join_command: string }

// nodeSubject 是「保留」接口用的对象身份:自建节点是 node:<id>;
// 外部端点直接用 endpoint key(s:<源ID>:<指纹>)。
export const nodeSubject = (id: number) => `node:${id}`;
