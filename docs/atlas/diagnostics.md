---
kind: part
parent: README.md
code: [internal/diagnostics/, internal/fusion/, internal/control/capability.go, internal/control/checkup.go, internal/node/capability.go, internal/cli/doctor.go, internal/cli/capability.go, internal/probeagent/, internal/cli/probe.go, internal/control/quality.go, internal/server/quality.go, internal/server/external_probe.go, internal/cli/quality.go, internal/ui/frontend/domain/quality.ts, internal/ui/frontend/pages/nodes/GradeModal.tsx, internal/model/external.go, internal/store/external.go, internal/server/external_names.go]
verified: 2026-11-23 @ 67ceb5e
---
# 体检与诊断

> **30 秒版**：五种「看一看」：**能力检测**经某个入口真的连出去，看出口 IP 在哪、能不能解锁特定网站，结果给策略组做筛选条件；**一键体检**（`mpx node diagnose`）在节点上跑一套有时间和体积上限的网络测量；**自检**（`mpx doctor`）把整套系统查一遍：控制面的库和后台任务、每台节点的现场状态、六种订阅格式各渲染一次、采样表；**质量评级**（`mpx node grade` / `mpx node regrade`、节点列表「IP 质量评级」）在节点上探测出口 IP 的地区与风险标记打出 0–100 分，再按分重排同地区节点的显示名字母。另有 `mpx diagnose` 在操作机本地跑同样有上限的网络测量。

## 边界

负责：手动触发的、有上限的测量和报告。

不负责：周期巡检（[节点](nodes.md) 的后台循环）；修复问题（只报告）。

## 怎么运转

1. 能力检测在控制面起临时 SOCKS 客户端，经指定入站或入口发请求，检测流量计入那个用户的配额；检测器版本是证据指纹的一部分，升级后旧结果显示未知。
2. 一键体检默认引擎是固定版本的官方 Go ECS 融合怪，经 SSH 在节点上跑，报告受传输预算约束。
3. 自检只有 fail 才让退出码非 0。管理台的「体检」页走 `GET/POST /api/doctor`，由控制面自己执行。
4. 控制面给有漂移的节点检查项带 `node_id` 与 `ui_action: "apply"`（只增，v0.9.10 起）。界面据此在体检页放行内「应用到<节点>」：点击只记一个 10 秒有效的标记并跳到节点详情，由详情页弹确认框，用户确认才真的下发；节点连不上时详情头部的「应用到节点」是 `aria-disabled`（仍可聚焦、读屏读得到原因，点击被界面统一拦下），提示先点「刷新状态」。
5. 触达检查、自动剔除与境内指标已在 UC-30 整体退役:`internal/reach`、`internal/bwtest` 包与 `/api/reach/*`、`/api/metrics`、`/api/bandwidth/*` 接口全部删除,`mpx node check`、`mpx node bandwidth`、`mpx reach` 命令移除;`reach_*`/`bandwidth_*` 表保留只迁移不再读写(老库升级无损)。探测点(`probe_points` + `/api/probes*` + `mpx probe run`)保留,职责收窄为 CDN IP 优选 —— `probeagent.Tick` 只做 `cdnOptimize` 一轮(领 `GET /api/cdn-optimize/targets`、逐目标跑 `cdn.TrustedPath` 金丝雀、`POST /api/cdn-optimize/results` 回传),凭据权限收敛到这两个接口加 `hello`;在线判定仍看 `last_seen_at` 三小时内。探测点健康面仍在 `/api/status`(`probe_total`/`probe_offline`),只是不再关联剔除。

UC-31 起优选从「延迟+边缘」两段补成「丢包+延迟+吞吐」三段(方法对齐 CloudflareSpeedTest,但独立实现 —— 它 GPL-3.0 与本仓 MIT 不兼容):`cdn.PingN` 对每 IP 拨 `ping_count` 次出丢包率,边缘通过的按延迟升序取前 `speed_count` 个对 `settings.cdn_speed_url` 做 `speed_seconds` 秒限时下载测速(`cdn.MeasureSpeed`,IP 直拨 + 真实 Host/SNI + 浏览器 UA,从首字节起计时折算);`cdn_pool` 加 `loss`/`speed_mbps` 两列,`applyCDNHosts` 选优按 speed_mbps 降序(无测速数据按延迟兜底),丢包超 `loss_max` 不参选;测速地址整体不可达(前两个边缘都失败)时整轮测速跳过,结果照常回存不误汰换。`mpx cdn pool` 列出丢包/速度列。
6. 质量评级分两段（UC-14）：`POST /api/nodes/{id}/quality` 经 SSH 在节点上跑一段只读探测脚本（出口 IP → ipwho.is → ipapi.is → 可选 AbuseIPDB），把分段输出回传控制面解析打分，存 `node_quality`（每节点一行，重测覆盖）；`POST /api/regrade` 不探测，只用存档分数按（地区, 城市）分组排字母，预览与 `apply:true` 调同一份服务端计算并在 `metadataMu` 下事务写入。字母是订阅里用户看到的节点名的一部分，所以重排只出手动动作，没有周期任务；未测出分的节点保留原字母、占住原位。打分权重与「能力检测不出分数」的立场无关——分数在 `internal/model/quality.go`，用途只是排字母和界面分档，不进体检判定。
7. 外部端点降级探测（UC-17）：`POST /api/sources/{id}/endpoints/{key}/quality`（`{key}` 为端点指纹；收到 `s:<id>:` 前缀的路由身份会返回 400 而非静默 404，`bareExternalKey` 显式拒绝并提示剥前缀）在控制面侧做同一套 IP 查询但不走 SSH——域名由控制面解析（`EgressIP` 是端点声明地址的解析结果，对域名/CDN 端点不等于机场真实出口），存 `external_quality`；`GET` 同名接口与 `GET /api/sources/{id}/quality` 只读存档。外部端点在 regrade 表里以 `kind:"external"`、`source_id`/`source_key` 标识，字母在（来源, 地区, 城市）组内重排——不同来源各自有 A。CLI 对应 `mpx source grade 来源名`，界面由「IP 质量评级」弹窗一并探测；上游名解析不出地区的端点（JMS 主机名备注）靠来源「默认地区」或 `mpx source meta 来源 端点名 --region XX`（`PATCH /api/sources/{id}/endpoints/{key}`，graded）进清洗层。

## 坑

- 体检报告超出传输预算 → 被截断会丢掉最有用的部分 → 已有几次修复（#13、#14、#16），改报告格式时先看预算。
- 官方测速在默认预算内跑不完 → 别把部分报告说成全模块成功。
- 能力检测失败 → 结果保留「未知」，不当成「不可用」。
- 质量探测是顺序的四段脚本，单步失败只让那一段空输出 → 报告 `partial`，分数照样出；出口 IP 都没拿到才 `untested` 不出分。节点上没有 `curl` 时整个判 `untested`。
- regrade 的组是归一化后的（地区, 城市）：已存的非法地区（如「美西」）会被探测结果覆盖并提示，但已存的合法国家码与探测冲突时不改、不参与，靠行 `note` 提示人工决定——探测可能因多出口漂移，不能因为一次探测就把人写的地区改了。
