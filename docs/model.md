# 数据模型与语义

## 关系

```
nodes ──1:N──► inbounds ──1:N──► hosts
                  │
                  │  users.inbound_ids(JSON 数组,空 = 全部)
                  ▼
                users          settings(单行)
```

级联删除:`ON DELETE CASCADE`。删节点连带删它的入站与入口,删入站连带删它的入口。删入站**不**动用户,用户只是不再映射到任何节点。

## 五张表

| 表 | 关键列 | 备注 |
| --- | --- | --- |
| `nodes` | `name`(唯一)、`address`、`ssh_port`、`ssh_user`、`api_port`、`config_path`、`service_name` | 空值有默认:22 / `root` / 10085 / `/usr/local/etc/xray/config.json` / `xray` |
| `inbounds` | `node_id`、`tag`、`protocol`、`listen`、`port`、`network`、`security`、`ws_path`、`grpc_service`、`sni`、`alpn`、`fingerprint`、`cert_file`、`key_file`、`reality_dest`、`reality_priv`、`reality_pub`、`short_ids`、`flow` | 切片存 JSON 文本 |
| `hosts` | `inbound_id`、`remark`、`address`、`port`、`sni`、`host`、`path`、`alpn` | 全部可空,空 = 回落入站自身 |
| `users` | `username`(唯一)、`key`、`sub_token`、`enabled`、`data_limit`、`used_traffic`、`lifetime_used_traffic`、`expire_at`、`reset_strategy`、`traffic_reset_at`、`inbound_ids`、`note`、`created_at` | `expire_at` / `traffic_reset_at` 可空,存 Unix 秒 |
| `settings` | `id`(恒为 1)、`sub_url_prefix`、`profile_title`、`support_link`、`update_interval`、`rules` | `rules` 是 `[{pattern, result}]` 的 JSON 文本 |

时间一律存 Unix 秒,不用 SQLite 的日期文本 —— 免得时区与格式来回转换。

## 语义

### 用户状态

按优先级判定,先命中先返回:

1. `enabled = false` → disabled
2. `expire_at` 已过 → expired
3. `data_limit > 0` 且 `used_traffic >= data_limit` → limited
4. 其余 → active

只有 active 的用户会拿到真实节点;其余拿到一个**占位节点**(地址 `0.0.0.0:1`),这样客户端能正常拉到订阅不报错,但连不上。

过期与超限只是不给真实节点,数据都还在;续费或 `reset-traffic` 之后自动恢复。

### 授权

`inbound_ids` 为空 = 全部入站。这个方向是有意的:**新建用户默认什么都能用,要限权才填**。

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

`{USERNAME}` 在这里**不生效**(展开成空串),只在订阅名 `settings.profile_title` 里生效 —— 两处行为不一致,见 [open-questions.md](open-questions.md) 第 4 节。

### key 与 sub_token

分开存。`key` 是整个用户凭证的种子(各协议的 UUID / 密码由它派生),`sub_token` 只用于订阅地址。

理由:订阅链接容易泄露(截图、转发),分开后换 token 不用连带换掉用户在所有协议上的凭证。

### 重置策略

`reset_strategy` ∈ `none | daily | weekly | monthly`,按 `traffic_reset_at` 滚动推进。**认不出的取值不参与轮转** —— 既不会推进,也不会卡住。这条有来历,见 [status.md](status.md) 的教训二。

### 到期日的解释

`--expire YYYY-MM-DD` 解释成**当地时区当天 23:59:59**。`--expire 2030-01-01` 存成 `2030-01-01T15:59:59Z`(Asia/Shanghai)。
