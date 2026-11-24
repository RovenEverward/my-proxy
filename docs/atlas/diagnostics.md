---
kind: part
parent: README.md
code: [internal/control/capability.go, internal/node/capability.go, internal/cli/doctor.go, internal/cli/capability.go, internal/control/quality.go, internal/server/quality.go, internal/server/external_probe.go, internal/cli/quality.go, internal/model/external.go, internal/store/external.go, internal/server/external_names.go]
verified: 2026-11-24 @ 16d41cd
---
# 能力检测、质量评级与自检

> **30 秒版**：三种按需执行的只读/评级动作：**自检**（`mpx doctor`）把整套系统查一遍（数据库、后台心跳、每台节点现场 SSH 状态、六种订阅格式、采样表，仅 fail 时退出码为 3）；**能力检测**（`mpx node capability`）经指定入口真实代理出站，验证出口 IP 与流媒体解锁供策略组筛选；**质量评级**（`mpx node grade` / `mpx source grade` / `mpx node regrade`）探测出口 IP 的地区与风险标记并按分数重排同地区显示名字母。

## 边界

负责：手动触发的轻量自检、代理入口能力检测与 IP 质量评级。

不负责：周期巡检（[节点](nodes.md) 的后台循环）；修复问题（只报告）；常驻 CDN 优选探测或重型融合怪测评（UC-30 / UC-32 已退役，按需深度排障与本机 CDN 优选直接由操作者或 AI Agent 执行）。

## 怎么运转

1. **自检（`mpx doctor`）**：调用 `GET /api/doctor`，由控制面依次检查 SQLite、后台循环心跳、每台节点的现场状态（可达性、服务活跃、配置漂移、证书余期）、六种订阅渲染和采样表；有任一项 `fail` 返回退出码 `3`，仅有 `warn` 返回 `0`。
2. **能力检测**：在控制面起临时 SOCKS 客户端，经指定入站或入口发请求，检测流量计入该用户配额；检测器版本是证据指纹的一部分，升级后旧结果显示未知。
3. **质量评级（UC-14）**：`POST /api/nodes/{id}/quality` 经 SSH 在节点上跑一段只读探测脚本（出口 IP → ipwho.is → ipapi.is → 可选 AbuseIPDB），解析打分存入 `node_quality`（每节点一行，重测覆盖）；`POST /api/regrade` 用存档分数按（地区, 城市）分组排字母，预览与 `apply:true` 走同一份计算并在 `metadataMu` 下事务写入。
4. **外部端点降级探测与清洗（UC-17）**：`POST /api/sources/{id}/endpoints/{key}/quality` 在控制面侧直连第三方 IP 查询服务（不走 SSH），结果并入 `POST /api/regrade` 同套排序循环；上游名解析不出地区的端点可通过来源默认地区或 `mpx source meta` 写入清洗层。
5. **已退役项（UC-30 / UC-32）**：触达检查与自动剔除、带宽测量（`internal/reach`、`internal/bwtest`）、内置 CDN 优选与常驻探测点（`internal/cdn`、`internal/probeagent`、`mpx cdn`、`mpx probe`）以及重型内置体检与融合怪引擎（`internal/diagnostics`、`internal/fusion`、`mpx diagnose`、`mpx node diagnose`）已整体移除；对应数据库表保留迁移不再读写，老库无损升级。

## 坑

- 能力检测失败 → 结果保留「未知」，不当成「不可用」。
- 质量探测是顺序的四段脚本，单步失败只让那一段空输出 → 报告 `partial`，分数照样出；出口 IP 都没拿到才 `untested` 不出分。节点上没有 `curl` 时整个判 `untested`。
- `regrade` 的组是归一化后的（地区, 城市）：已存合法国家码与探测冲突时不改、不参与，靠行 `note` 提示人工决定——避免多出口漂移误改手工地区。
