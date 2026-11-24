---
kind: part
parent: README.md
code: [internal/server/subscribe.go, internal/server/subcache.go, internal/server/routing_api.go, internal/server/ruleset.go, internal/server/settings.go, internal/render/clash.go, internal/render/singbox.go, internal/render/links.go, internal/render/routing.go, internal/model/routing.go, internal/model/settings.go, internal/ruleset/ruleset.go, internal/store/ruleset.go]
verified: 2026-11-24 @ 5de6b0c
---
# 订阅与分流

> **30 秒版**：每个用户有一条订阅地址 `/sub/{token}`。同一条地址，Clash Meta、sing-box、v2rayN 各自拿到认识的格式，浏览器打开是用量页。Clash Meta 和 sing-box 的分流部分由一张从上往下匹配的规则表生成：每条规则指向直连、拦截或一个策略组，策略组在渲染时按条件收入口；规则集统一采用 `MetaCubeX/meta-rules-dat` 二进制格式（`.mrs` / `.srs`）。

## 边界

负责：认格式、渲染六种订阅格式和用量页、响应头里的用量信息、规则表与策略组、规则集私有化镜像、订阅缓存、排水和容量上限。

不负责：节点上的配置（[节点](nodes.md)，代码也不共用）；用户能不能用、能用哪些入站（读 [用户与计量](users.md) 的结果）。

## 怎么运转

1. 认格式按顺序：路径里点名的格式 → User-Agent 逐条匹配订阅规则，取第一条命中 → 都没命中退回分享链接。正则编译不过的规则跳过，不让整个订阅报错。
2. 渲染先把用户能用的入站和入口整理成中间表示，再按格式输出（[internal/render](../../internal/render/)）。不可用的用户输出占位节点，名字写明已停用、已过期还是已超限。
3. 分流：全局规则表加按用户追加的规则。默认规则集统一指向 `MetaCubeX/meta-rules-dat`（Mihomo 使用 `format: mrs` 预编译二进制，sing-box 使用 `.srs` 预编译二进制），5 个高频策略组（AI 服务、流媒体、Telegram、微软、苹果）加国内直连兜底；默认 DNS 模式保留 `redir-host` 以避免 fake-ip 干扰开发工具。
4. 订阅缓存整批失效。缓存键包含失效代次，旧渲染不能重新填回已失效结果；排水和容量上限只在订阅层生效，不踢已建立的连接。
5. 外部订阅由 [来源服务](../../internal/source/) 单独拉取，不占用节点采样循环。默认 6 小时刷新，URL 与凭证仅留在库和授权输出中。拉取、解析或空结果失败时保留上次有效快照。配置版本检查与快照替换共用事务，旧地址响应不能覆盖新配置。
6. [订阅组装器](../../internal/server/sources.go) 先判断用户状态，再合并自建入口与授权的来源快照。来源可见性 = `subscription_sources.grant_all`（全员授权，含之后新建用户）∪ `user_source_grants` 按人授权。`grant_all` 只经 POST/PATCH `/api/sources` 写（CLI `--everyone`），关掉后回到按人授权。外部节点按目标格式筛选后参与策略组。来源条件独立保存，与地区、协议条件取交集。删除来源保留组内的失效来源选择，避免扩大范围。`GET /api/sources` 响应带脱敏 `nodes` 名单与已授权用户名列表 `grants`；外部成员不进 SSH/下发链路。
7. 外部端点通过来源 ID 和连接指纹标识，不伪造托管节点 ID。sing-box 的 tag 映射使用统一端点键。共享凭证不能由 MPX 撤销，外部流量不进入 MPX 配额。浏览器订阅页只提供原格式链接，并注明仅统计自建流量。
8. 订阅装配唯一边界是 `subscriptionEndpointsFor`（internal/server/pool.go），它读「清洗池」视图：`poolEndpointsFor` / `poolFor` 把托管端点原始流（`resolveManagedEndpoints`，过排水标记但不删行）与外部端点（快照 + `resolveExternalRemark`）归一化成 `PoolEndpoint`。`Excluded` 只来源于排水；协议后缀 `AddProtocolSuffix` 落在原始流一层，`mpx pool` 是 CLI 的可视化入口。
9. 规则集私有化分发（UC-05，`settings.ruleset_mode=privatized`）：[镜像服务](../../internal/ruleset/) 把引用到的上游规则集拉进 `ruleset_snapshots`（key 为 `clash/<名>` 或 `singbox/<tag>`），渲染层经 `RoutingPlan.RulesetURL` 闭包把就绪文件的 url 改写成 `/sub/{token}/ruleset/{fmt}/{name}?v=<fetched_at>`；`?v=<fetched_at>` 版本查询参数确保上游规则集格式或内容刷新后自动穿透 CDN 边缘缓存；未就绪的原样写上游，订阅不会出 404。

用户实际看到的格式和规则怎么配，写在用户文档站的 [客户端格式](../guides/client-format.md) 和 [订阅参考](../reference/subscription.md)。

## 坑

- 策略组用「标签」条件 → v0.7.0 删掉用途后没有标签来源，匹配不到任何节点 → 改用地区、协议筛选。
- 订阅链接带 `?purpose=` → v0.7.0 起被忽略，返回完整订阅 → 不报错是有意的，老客户端不会坏。
- UA 规则表为空 → 表示「没配过」，用内置默认，不是「关掉分流」。
- `/sub/{token}/ruleset/*` 与订阅本体不共用限流桶（`subRateLimit` 按路径挑 `rulesetLimiter`，30/s·burst 400）→ 客户端冷启动并行拉几十个规则集文件是正常形态。私有化 URL 带 `?v=<fetched_at>`，配合边缘缓存（`Cache-Control: public, max-age=86400`）兼顾命中率与切换格式时的零脏缓存。
- 节点名里写 `{USERNAME}` → 会被抹成空串，只在订阅名里生效。
- 订阅走明文 HTTP（或经反代 HTTPS）→ token 可被链路窃听 → 换地址用 `/sub/{token}/rotate`。
- Marzban 迁库兜底（`settings.marzban_sub_secret`）：`/sub/{token}` 精确查不到时按 Marzban 铸法验签并按用户名定位，旧订阅链接无需更换。
- `settings.sub_node_prefix`（空串=不加）给 MPX 托管端点的订阅名统一加前缀，外采源不带。
