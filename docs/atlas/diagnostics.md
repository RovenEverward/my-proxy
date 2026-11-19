---
kind: part
parent: README.md
code: [internal/diagnostics/, internal/fusion/, internal/control/capability.go, internal/control/checkup.go, internal/node/capability.go, internal/cli/doctor.go, internal/cli/capability.go, internal/reach/, internal/cli/reach.go, internal/ui/reach.go, internal/probeagent/, internal/cli/probe.go, internal/control/quality.go, internal/server/quality.go, internal/server/external_probe.go, internal/cli/quality.go, internal/ui/frontend/domain/quality.ts, internal/ui/frontend/pages/nodes/GradeModal.tsx, internal/model/external.go, internal/store/external.go, internal/server/external_names.go]
verified: 2026-11-19 @ 509de43 + working tree
---
# 体检与诊断

> **30 秒版**：五种「看一看」：**能力检测**经某个入口真的连出去，看出口 IP 在哪、能不能解锁特定网站，结果给策略组做筛选条件；**一键体检**（`mpx node diagnose`）在节点上跑一套有时间和体积上限的网络测量；**自检**（`mpx doctor`）把整套系统查一遍：控制面的库和后台任务、每台节点的现场状态、六种订阅格式各渲染一次、采样表；**触达检查**（`mpx node check`、节点列表「检查触达」）从本机与控制面各探一轮入站 TCP/UDP，给出「双通 / 境内不通 / 都不通 / 未知」判定。**质量评级**（`mpx node grade` / `mpx node regrade`、节点列表「IP 质量评级」）在节点上探测出口 IP 的地区与风险标记打出 0–100 分，再按分重排同地区节点的显示名字母。另有 `mpx diagnose` 在操作机本地跑同样有上限的网络测量。**自动剔除**（UC-12）在触达判定之上做周期轮转：境内常驻**探测点**（`mpx probe`）与操作机各按周期摊批探一遍，判定为「境内不通 / 都不通」且数据新鲜的节点与外部订阅端点不再进订阅，`reach keep` 可逐对象豁免；开关关掉时订阅输出与旧版逐字节一致。

## 边界

负责：手动触发的、有上限的测量和报告。

不负责：周期巡检（[节点](nodes.md) 的后台循环）；修复问题（只报告）。

## 怎么运转

1. 能力检测在控制面起临时 SOCKS 客户端，经指定入站或入口发请求，检测流量计入那个用户的配额；检测器版本是证据指纹的一部分，升级后旧结果显示未知。
2. 一键体检默认引擎是固定版本的官方 Go ECS 融合怪，经 SSH 在节点上跑，报告受传输预算约束。
3. 自检只有 fail 才让退出码非 0。`mpx ui` 的「自检」页走本地接口，由操作机上的 `mpx` 执行。
4. 控制面给有漂移的节点检查项带 `node_id` 与 `ui_action: "apply"`（只增，v0.9.10 起）。界面据此在体检页放行内「应用到<节点>」：点击只记一个 10 秒有效的标记并跳到节点详情，由详情页弹确认框，用户确认才真的下发；节点连不上时详情头部的「应用到节点」是 `aria-disabled`（仍可聚焦、读屏读得到原因，点击被界面统一拦下），提示先点「刷新状态」。
5. 触达检查的探针只有一份（`internal/reach/`）：操作机（`mpx node check`、`mpx ui` 的 `/local/reach/check`）与控制面（`POST /api/reach/probe`）跑同一份 `Probe`，各探各的，操作机合并明细与判定再 `POST /api/reach/results` 回存。判定按入站自己的协议选格：TCP 系看 TCP 格，QUIC/UDP 系（wireguard、hysteria2、tuic、network=udp）看 UDP 格；另一协议的探测只进明细不参与判定——在 UDP 端口上探 TCP 必失败，混进判定会把「该协议本来就不该通」误读成「节点不通」。UDP 超时只记 unknown（服务端可以合法沉默），节点全 UDP 入站且全超时判「未知」不判「不通」。页面数据走 `GET /api/reach/last`（含逐入站明细）与 `GET /api/reach`（摘要），列表双列与筛选只在查过一轮后出现。UC-12 起探测对象从「节点」泛化成**对象**：自建节点对象身份是 `node:<id>`，外部订阅端点是 `s:<srcID>:<SourceKey>`；结果按（对象 × 来源 × 方位）存明细行（`reach_source_checks`），`recomputeSubject` 折回对象级存档（`reach_subject_checks`），旧形状 `results:` 回写路径继续走、服务端同时补两份来源行。`Target.Key()` 是字符串身份（`inbound:<id>` 或端点 key），`Probe` 收结果要经目标表回译成入站号，不能直接对键 ParseInt。UDP 判定格只归 QUIC/UDP 系协议（hysteria2、tuic、wireguard、network=udp）——shadowsocks 是 TCP 为主、UDP 可选，吃 UDP 格会让双不通的死端点永远 unknown、剔除不了。QUIC 系（TUIC/Hysteria2）的 UDP 探活不发随机负载，发带不支持版本号、补齐 1200 字节的 QUIC Initial：监听器必须回 Version Negotiation，一个回包即可判 reachable，无需凭据、无 QUIC 依赖；局限是只证明监听在回话，不等于握手与凭据有效，双静默仍落 unknown 不判 down。
6. 探测点（UC-12）是控制面名册里的第一类实体，不是节点：`POST /api/probes` 发一次性接入命令（`mpx probe run --server <base> --join <token>`，HTTPS 另要 `--fp`），接入后按周期（默认 60 分钟，`reach_interval_minutes`）轮转——到期对象摊进整个周期分批探，不整批突扫（`probeagent.Tick` + `reach.DueSubjects/RotationBatch`）。`mpx ui` 开着时操作机也算一个境内来源。探测端只拿「读目标、回结果」两个权限（`GET /api/reach/targets`、`POST /api/reach/results`、`POST /api/reach/probe`、hello），凭据可轮换、可吊销；在线判定看 `last_seen_at` 是否在三小时内。
7. 自动剔除只在订阅组装时读时算（`exclusion()`）：开关 `reach_exclude` 开、判定 blocked/down、且境内侧数据新鲜（≤24h）的对象进剔除集，`reach_keeps` 逐对象豁免。剔除不改存档，判定恢复或过期后对象自动回来；**外部订阅端点按同一条链路剔除**（对象身份 `s:<srcID>:<SourceKey>`，UC-18 明确：境内侧 `reach.Probe` TCP 探活 + 连续不通 → `down`/`blocked` → `excl.exts` → `subscriptionEndpointsFor` 外部分支过滤，恢复后单次探测回归）。`ExtCheck` 存档的 `source_id`/`source_name` 由 `recomputeSubject` 从对象身份拆出回填。探测点健康面在 `/api/status`（`probe_total`/`probe_offline`/`probe_last_seen_at`），`mpx status` 显示一行：自动剔除依赖探测点一直在，全离线时数据会停在过期结论上。节点列表挂「已剔除 / 保留中」标，外部订阅成员行同。
8. 质量评级分两段（UC-14）：`POST /api/nodes/{id}/quality` 经 SSH 在节点上跑一段只读探测脚本（出口 IP → ipwho.is → ipapi.is → 可选 AbuseIPDB），把分段输出回传控制面解析打分，存 `node_quality`（每节点一行，重测覆盖）；`POST /api/regrade` 不探测，只用存档分数按（地区, 城市）分组排字母，预览与 `apply:true` 调同一份服务端计算并在 `metadataMu` 下事务写入。字母是订阅里用户看到的节点名的一部分，所以重排只出手动动作，没有周期任务；未测出分的节点保留原字母、占住原位。打分权重与「能力检测不出分数」的立场无关——分数在 `internal/model/quality.go`，用途只是排字母和界面分档，不进体检判定。
9. 外部端点降级探测（UC-17）：`POST /api/sources/{id}/endpoints/{key}/quality`（`{key}` 为端点指纹；收到 `s:<id>:` 前缀的路由身份会返回 400 而非静默 404，`bareExternalKey` 显式拒绝并提示剥前缀）在控制面侧做同一套 IP 查询但不走 SSH——域名由控制面解析（`EgressIP` 是端点声明地址的解析结果，对域名/CDN 端点不等于机场真实出口），存 `external_quality`；`GET` 同名接口与 `GET /api/sources/{id}/quality` 只读存档。外部端点在 regrade 表里以 `kind:"external"`、`source_id`/`source_key` 标识，字母在（来源, 地区, 城市）组内重排——不同来源各自有 A。CLI 对应 `mpx source grade 来源名`，界面由「IP 质量评级」弹窗一并探测；上游名解析不出地区的端点（JMS 主机名备注）靠来源「默认地区」或 `mpx source meta 来源 端点名 --region XX`（`PATCH /api/sources/{id}/endpoints/{key}`，graded）进清洗层。

## 坑

- 体检报告超出传输预算 → 被截断会丢掉最有用的部分 → 已有几次修复（#13、#14、#16），改报告格式时先看预算。
- 官方测速在默认预算内跑不完 → 别把部分报告说成全模块成功。
- 能力检测失败 → 结果保留「未知」，不当成「不可用」。
- 触达检查里 UDP 的语义弱于 TCP：回包才可达，沉默不算不通；判定列的「不通」来自 TCP 超时/拒绝或 UDP 端口不可达，UDP 超时全部落「未知」。
- 质量探测是顺序的四段脚本，单步失败只让那一段空输出 → 报告 `partial`，分数照样出；出口 IP 都没拿到才 `untested` 不出分。节点上没有 `curl` 时整个判 `untested`。
- regrade 的组是归一化后的（地区, 城市）：已存的非法地区（如「美西」）会被探测结果覆盖并提示，但已存的合法国家码与探测冲突时不改、不参与，靠行 `note` 提示人工决定——探测可能因多出口漂移，不能因为一次探测就把人写的地区改了。
