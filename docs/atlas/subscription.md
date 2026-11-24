---
kind: part
parent: README.md
code: [internal/server/subscribe.go, internal/server/subcache.go, internal/server/routing_api.go, internal/server/ruleset.go, internal/server/settings.go, internal/render/clash.go, internal/render/singbox.go, internal/render/links.go, internal/render/routing.go, internal/model/routing.go, internal/model/settings.go, internal/ruleset/ruleset.go, internal/store/ruleset.go]
verified: 2026-11-24 @ HEAD + sub_node_prefix
---
# 订阅与分流

> **30 秒版**：每个用户有一条订阅地址 `/sub/{token}`。同一条地址，Clash Meta、sing-box、v2rayN 各自拿到认识的格式，浏览器打开是用量页。Clash Meta 和 sing-box 的分流部分由一张从上往下匹配的规则表生成：每条规则指向直连、拦截或一个策略组，策略组在渲染时按条件收入口。

## 边界

负责：认格式、渲染六种订阅格式和用量页、响应头里的用量信息、规则表与策略组、订阅缓存、排水和容量上限。

不负责：节点上的配置（[节点](nodes.md)，代码也不共用）；用户能不能用、能用哪些入站（读 [用户、套餐与计量](users.md) 的结果）。

## 怎么运转

1. 认格式按顺序：路径里点名的格式 → User-Agent 逐条匹配订阅规则，取第一条命中 → 都没命中退回分享链接。正则编译不过的规则跳过，不让整个订阅报错。
2. 渲染先把用户能用的入站和入口整理成中间表示，再按格式输出（[internal/render](../../internal/render/)）。不可用的用户输出占位节点，名字写明已停用、已过期还是已超限。
3. 分流：全局规则表加按用户追加的规则。
4. 订阅缓存整批失效。缓存键包含失效代次，旧渲染不能重新填回已失效结果；排水和容量上限只在订阅层生效，不踢已建立的连接。
5. 外部订阅由 [来源服务](../../internal/source/) 单独拉取，不占用节点采样循环。默认 6 小时刷新，URL 与凭证仅留在库和授权输出中。拉取、解析或空结果失败时保留上次有效快照。配置版本检查与快照替换共用事务，旧地址响应不能覆盖新配置。
6. [订阅组装器](../../internal/server/sources.go) 先判断用户状态，再合并自建入口与授权的来源快照。来源可见性 = `subscription_sources.grant_all`（全员授权，含之后新建用户）∪ `user_source_grants` 按人授权 ∪ `plan_source_grants` 经用户 `user_plans` 归属的按套餐授权（与「套餐 ∪ 额外授权」的入口模型同构；不挂套餐的用户不吃套餐授权，只能靠全员或按人）。`grant_all` 只经 POST/PATCH `/api/sources` 的 `grant_all` 键写（CLI `--everyone`、抽屉「授权给所有用户」开关），关掉后回到按人/按套餐授权，两份记录不被清除也不决定可见性。外部节点按目标格式筛选后参与策略组。来源条件独立保存，与地区、协议和能力条件取交集。删除来源保留组内的失效来源选择，避免扩大范围。`GET /api/sources` 响应另带脱敏 `nodes` 名单（显示名+协议，无地址与凭证）、已授权用户名列表 `grants` 与已授权套餐 `plan_grants`（`{id,name}`），节点页的「外部订阅」页签用它展示接收用户并展开只读成员；成员不进 SSH/下发/体检链路。
7. 外部端点通过来源 ID 和连接指纹标识，不伪造托管节点 ID。sing-box 的 tag 映射使用统一端点键。共享凭证不能由 MPX 撤销，外部流量不进入 MPX 配额。浏览器订阅页只提供原格式链接，并注明仅统计自建流量。
8. 订阅装配唯一边界是 `subscriptionEndpointsFor`（internal/server/pool.go），它读「清洗池」视图：`poolEndpointsFor` / `poolFor` 把托管端点原始流（`resolveManagedEndpoints`，过排水标记但不删行）与外部端点（快照 + `resolveExternalRemark`）归一化成 `PoolEndpoint`（`Endpoint` + `Origin` + `Excluded` + `ExcludeReason` + `Index`）。UC-30 后 `Excluded` 只有「排水」一个来源（不再按境内活性剔除）：`Excluded` 行留在池里带原因，预览与 `GET /api/pool` 直接读它。协议后缀 `AddProtocolSuffix` 落在原始流一层，池视图和订阅视图看到的是同一个名字。`mpx pool` 是 CLI 的可视化入口。
9. 规则集私有化分发（UC-05，`settings.ruleset_mode=privatized`）：[镜像服务](../../internal/ruleset/) 把引用到的上游规则集拉进 `ruleset_snapshots`（key 为 `clash/<名>` 或 `singbox/<tag>`），渲染层经 `RoutingPlan.RulesetURL` 闭包把就绪文件的 url 改写成 `/sub/{token}/ruleset/{fmt}/{name}`；未就绪的原样写上游，订阅不会出 404。镜像首次就绪（becameReady）才失效。
订阅缓存，错误重试不动缓存。镜像刷新无论开关状态都跑（侧栏状态展示用），拉取器与来源服务同构：单飞、24h 周期、失败留上次快照。管理面 `GET /api/rulesets` 按内置规则组聚合，`POST /api/rulesets/refresh` 手动刷新；CLI 是 `mpx ruleset` 与 `mpx settings ruleset-mode`。

用户实际看到的格式和规则怎么配，写在用户文档站的 [客户端格式](../guides/client-format.md) 和 [订阅参考](../reference/subscription.md)。

## 坑

- 策略组用「标签」条件 → v0.7.0 删掉用途后没有标签来源，匹配不到任何节点 → 改用地区、协议或能力检测筛选。
- 订阅链接带 `?purpose=` → v0.7.0 起被忽略，返回完整订阅 → 不报错是有意的，老客户端不会坏。
- UA 规则表为空 → 表示「没配过」，用内置默认，不是「关掉分流」。
- 「国内媒体」「苹果」等直连组必须排在大表之前 → globalmedia/category-media 等大集合会吞掉 snssdk.com 等国内域名 → 排后面匹配不到，抖音评论区会走代理加载不出。规整后的层序：lan/ads → 自定义 suffix → 直连组(apple/cnmedia) → 主题代理组(media/ai/google/ms/social/game/dev) → global 大表 → cn/geoip 兜底。例外:global 有意收 apple.com/mzstatic.com（海外 CDN 更稳），不纠正。
- 字节基建域必须直连且在「广告拦截」前面 → reject 集误杀 mssdk.bytedance.com（签名）、tnc*.isnssdk.com（配置）、vcs.zijieapi.com（互动接口），globalmedia 又吞走 isnssdk.com → 抖音看不了评论、发不了评论 → 已加 `isnssdk.com / zijieapi.com / bytedance.com` 三条 suffix 直连排在 ads 前（DB 全局规则 2–4 位；模板同步）。
- `/sub/{token}/ruleset/*` 与订阅本体不共用限流桶（`subRateLimit` 按路径挑 `rulesetLimiter`，30/s·burst 400）→ 客户端冷启动并行拉几十个规则集文件是正常形态，共桶时 privatized 一开就批量 429（ISS-18）。桶键仍是 `RemoteAddr`，CDN 边缘聚合会把多用户折算进一个桶，量级撑不住时先考虑在边缘放行 `/sub/*/ruleset/` 再动这里。大件按 `Accept-Encoding` 协商 gzip（≥64KB、压不出收益回退原文）：clash `direct` 2.27MB→512KB；`.srs` 已压缩、gzip 无效。已在 CF 配边缘缓存：zone ruleset `MPX CDN bypass cache` 里 `sub.shiziyi.top` 且 path 含 `/ruleset/` 的请求 `cache:true`+`edge_ttl 7200`，`MISS→HIT`。注意 Cache Rules 是“可叠加、最后命中 wins”，该条必须排在 `mpx_sub_cache_bypass` 之后，否则被同主机 bypass 冲掉。
- 节点名里写 `{USERNAME}` → 会被抹成空串，只在订阅名里生效 → 见 [缺口与待定项] 第 3 条。
- 订阅走明文 HTTP → token 可被链路窃听 → 已接受的代价，换地址用 `/sub/{token}/rotate`。
- Marzban 迁库兜底(`settings.marzban_sub_secret`,GET 只回 `marzban_sub_secret_set`):`/sub/{token}` 精确查不到时,再按 Marzban 铸法(数据 base64url(用户名,unix秒) + sha256(数据+secret) 前 10 字符签名)验签,过了按用户名找人 → 用户客户端里任何一枚旧 token 都认得(它本来就不落库、每次请求重铸),旧订阅链接不用换。精确匹配仍优先,同名用户没建过去不会串号。`revoke-sub` / 旋转 token 不受影响 —— 那两条路径只认 MPX 自己的 token。
- `settings.sub_node_prefix`(空串=不加)给 MPX 托管端点的订阅名统一加前缀:所有 ResolveNamed 产出的端点都带,不管 remark 是不是手写的 —— 用途是跟外采/转发订阅并排时认出自建;外采源(resolveExternalRemark 那一路)不带。
