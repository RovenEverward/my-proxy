# 数据模型与语义

## 关系

```
nodes ──1:N──► inbounds ──1:N──► hosts
  │               │
  │               │  users.inbound_ids(JSON 数组,空 = 全部)
  │               ▼
  │             users          settings(单行)
  │               │             traffic_samples(只追加:node_id 外键级联,username 是文本)
  │               └──1:N──► user_notices(只追加:记「这条提醒发过了」)
  └──1:1──► node_status
```

级联删除:`ON DELETE CASCADE`。删节点连带删它的入站、入口、流量采样与状态行,删入站连带删它的入口。删入站**不**动用户,用户只是不再映射到任何节点。

## 八张表

| 表 | 关键列 | 备注 |
| --- | --- | --- |
| `nodes` | `name`(唯一)、`address`、`ssh_port`、`ssh_user`、`api_port`、`config_path`、`service_name`、`deployed_hash`、`deployed_at`、`draining` | 空值有默认:22 / `root` / 10085 / `/etc/mpx/xray/config.json` / `mpx-xray`。**不存 xray 版本**:节点上装的是哪个版本随时能读出来,存进库就是第二份真相。`deployed_hash` / `deployed_at` 是**下发成功之后**才记的基准;从没下发过时是空串 / NULL,那表示「没有基准」,不是「一致」。`draining` 见下面「排水」一节 |
| `node_status` | `node_id`(主键)、`checked_at`、`reachable`、`active`、`version`、`observed_hash`、`certificates`、`error` | 每次检查**覆盖**一行,只写这张表、不碰 `nodes`。`certificates` 是 `[{inbound_id, inbound_name, path, not_after, days_left, error}]` 的 JSON 文本 |
| `inbounds` | `node_id`、`tag`、`protocol`、`listen`、`port`、`network`、`security`、`ws_path`、`grpc_service`、`sni`、`alpn`、`fingerprint`、`cert_file`、`key_file`、`reality_dest`、`reality_priv`、`reality_pub`、`short_ids`、`flow`、`xhttp_path`、`xhttp_host`、`xhttp_mode`、`wg_secret`、`wg_address`、`wg_mtu`、`mux`、`fragment`、`max_users` | 切片存 JSON 文本;`max_users` 是 0 或正整数,0 表示不限 |
| `hosts` | `inbound_id`、`remark`、`address`、`port`、`sni`、`host`、`path`、`alpn` | 全部可空,空 = 回落入站自身 |
| `users` | `username`(唯一)、`key`、`sub_token`、`enabled`、`data_limit`、`used_traffic`、`lifetime_used_traffic`、`expire_at`、`reset_strategy`、`traffic_reset_at`、`inbound_ids`、`note`、`created_at` | `expire_at` / `traffic_reset_at` 可空,存 Unix 秒 |
| `settings` | `id`(恒为 1)、`sub_url_prefix`、`profile_title`、`support_link`、`update_interval`、`rules`、`templates`、`webhook_url`、`notify_days_left`、`notify_usage_percent` | `rules` 是 `[{pattern, result}]` 的 JSON 文本,`templates` 是 `{格式: 正文}`。`notify_days_left` / `notify_usage_percent` 为 0 表示关掉这一类提醒 |
| `traffic_samples` | `username`、`node_id`、`sampled_at`、`uplink`、`downlink` | 只追加的流量历史:`uplink` / `downlink` 是 xray 报的**累计值**;索引 `(node_id, username, sampled_at)` |
| `user_notices` | `user_id`、`kind`、`sent_at`(主键 `(user_id, kind)`) | 只是「这条用户提醒已经发过了」的流水账,不承载业务数据:整张表删掉用户、流量、订阅照常工作,只是下一轮可能重发。所以备份校验的必需表清单里**刻意没有它** |

时间一律存 Unix 秒,不用 SQLite 的日期文本 —— 免得时区与格式来回转换。

`traffic_samples` 存的是**累计值快照,不是增量**:控制面每轮读一次 xray 的计数,与该用户在该节点上的最近一行相减,差值才累加进 `users.used_traffic` 与 `lifetime_used_traffic`(见 [decisions.md](decisions.md) 第 9 条)。

## 多核心扩展

`core_deployments` 按 `(node_id,core)` 保存 sing-box 的 API 端口、配置路径、systemd 服务与成功下发哈希。Xray 继续使用 `nodes` 原字段；两核心不可共用 API 端口、服务名或配置路径。`inbounds.congestion_control` 为 TUIC 设置拥塞控制，`hysteria2` / `tuic` 映射到 sing-box。

`traffic_samples` 新增 `core`（旧行及新旧备份迁移默认 `xray`）与 `generation`。最新基准按 `(node_id,core,username)` 取，历史与用户用量在一个事务写入。sing-box 世代来自 systemd InvocationID，每次启动不同；世代变化或计数下降时计入新进程当前累计值，计划停止后持久化零基准。Xray 保留计数下降时零增量的旧策略。两核心用量合并进用户本期与 lifetime 账本。

`node_status.cores` 保存每个第二核心的 active、version、observed_hash、stats_available 与 error；统计可用必须通过真实单用户 API 查询（空统计响应也有效）。QUIC-only 节点的旧摘要字段回落到 sing-box，混合节点分别观察两核心。

上述扩展包含在 SQLite 完整快照中，无需改变备份归档格式。详细用户停用语义与尾部计量边界见 [Hysteria2/TUIC](../user/guides/quic-protocols.md)。

## 语义

### 用户状态

按优先级判定,先命中先返回:

1. `enabled = false` → disabled
2. `expire_at` 已过 → expired
3. `data_limit > 0` 且 `used_traffic >= data_limit` → limited
4. 其余 → active

只有 active 的用户会拿到真实节点;其余拿到一个**占位节点**(地址 `0.0.0.0:1`),这样客户端能正常拉到订阅不报错,但连不上。

过期与超限只是不给真实节点,数据都还在;续费或 `reset-traffic` 之后自动恢复。

### 节点状态与漂移

检查一台节点时读四样东西:服务在不在跑(`is-active`)、核心版本(`xray version`)、配置文件的 sha256、每个入站证书的到期时间。一次 ssh 一条脚本读全,结果写进 `node_status`。

**漂移 = 观察到的哈希 ≠ 基准**,三种情况都**不算**漂移:

| 情况 | 含义 |
| --- | --- |
| `deployed_hash` 为空 | 从没下发过配置,没有基准可比 —— 是「不知道」 |
| 没有状态行 / 连不上 | 没问过、或问了没答 —— 也是「不知道」 |
| 两者相等 | 节点上就是我推的那一份 |

证书那部分:到期时间读不到(文件不是证书、节点上没有 `openssl`)时 `not_after` 为空、`error` 有值;能读到就只留 `not_after`。这两件事不会同时成立。

`node list` 显示的是这张缓存,不发 ssh;`?fresh=1` / `mpx node status` 才现场查。为什么基准和观察分两张表,见 [decisions.md](decisions.md) 第 20 条。

### 授权

`inbound_ids` 为空 = 全部入站。这个方向是有意的:**新建用户默认什么都能用,要限权才填**。

### 排水与容量

订阅是过滤器的最后一道。三件事都只在订阅层生效、不去动 xray 上的现状 ——
在线用户会随客户端自然刷新漂走,不需要当场掐断(那是 `node rm` 或收敛的事):

| 信号 | 在哪设 | 订阅里的表现 |
| --- | --- | --- |
| 节点 `draining` | `mpx node drain` / `undrain` | 不再发这台节点的入口 |
| 入站 `max_users` | `mpx inbound add --max-users` | 占用满 ≥ 上限后,新订阅不再发它 |
| 用户状态 ≠ active | 用户字段 | 只发占位节点 |

占用数按「当前 active 且被授权给这个入站」的用户数算;`inbound_ids` 为空的用户对
每个入站都记一笔。

「在线名单」(`mpx node online`)不落库:它下一秒就过期,存下来只有误导性。

### host 的回落

| host 字段 | 留空时 |
| --- | --- |
| `address` | 节点地址 |
| `port` | 入站端口 |
| `sni` | 入站 SNI |
| `path` | ws 路径或 gRPC serviceName |
| `alpn` | 入站 ALPN |
| `host`(ws/grpc 的 Host 头) | 没有可回落的 —— 入站上不存在这个概念,空就是不发 |

`mpx host list` 显示的是这张表合出来的结果(API 里叫 `effective`),和订阅里发出去的是同一份:合并规则只有 `render.ResolveExposure` 一处,所以「列表上显示的」和「客户端实际连的」不会漂。变量和 `*` 除外 —— 那些跟具体用户有关,列表里原样显示,得看某个用户的订阅才知道展开成什么。

所以「一个入站零个 host」是合法状态,表示沿用入站自身。

一个入站有多个 host 时,一个 host 产出一个客户端节点 —— 这是「同一入站可以对外暴露成多个入口」的机制,例如同时给 IPv4 直连和域名 + TLS。节点名(host 的 `remark`)重复时会自动加后缀消歧。

### 变量与随机子域

`hosts.remark` 与 `hosts.address` 里可写 `{SERVER_IP}`、`{NODE_NAME}`、`{PROTOCOL}`、`{TRANSPORT}`;未知变量原样保留。地址里写 `*` 会换成按用户名派生的稳定子域 ——

```
remark: 香港 REALITY {PROTOCOL}         →  香港 REALITY vless
address: *.example.com                  →  kbayy1jo.example.com   (alice)
```

**子域的随机部分按用户名派生,不是随机数。** 否则客户端每次更新订阅都会看到新地址,节点列表里堆一串连不上的旧条目。

`{USERNAME}` 在这里**被抹成空串**,只在订阅名 `settings.profile_title` 里展开成用户名。节点名会出现在客户端的节点列表里,截图和转发都会带出去,不该写账号名 —— 这是有意的,见 [open-questions.md](open-questions.md) 第 3 节。

### key 与 sub_token

分开存。`key` 是整个用户凭证的种子(各协议的 UUID / 密码由它派生),`sub_token` 只用于订阅地址。

理由:订阅链接容易泄露(截图、转发),分开后换 token 不用连带换掉用户在所有协议上的凭证。

### 重置策略

`reset_strategy` ∈ `none | daily | weekly | monthly`,按 `traffic_reset_at` 滚动推进。**认不出的取值不参与轮转** —— 既不会推进,也不会卡住。这条有来历,见 [status.md](status.md) 的教训二。

### 到期日的解释

`--expire YYYY-MM-DD` 解释成**当地时区当天 23:59:59**。`--expire 2030-01-01` 存成 `2030-01-01T15:59:59Z`(Asia/Shanghai)。

### 提醒

两类用户提醒,各由设置里的一个阈值控制:`notify_days_left`(剩余天数 ≤ 它)与
`notify_usage_percent`(本周期用量 ≥ 它)。两者都是 0 表示关掉这一类。

同一个人同一类提醒只发一次,靠 `user_notices` 记「已发过」;**发出去了才记**,发失败或压根没配
`webhook_url` 都不记,下一轮还会再试。条件不成立时(续费、重置用量、停用)把那一行删掉,所以
续费一年之后再次临近到期会正常提醒第二次。停用的用户不发。

节点故障走的是另一条路:复用告警那条线(冷却 30 分钟、条件消失打「已恢复」),不记 `user_notices`。
两边为什么这么分,见 [decisions.md](decisions.md) 第 27 条。
