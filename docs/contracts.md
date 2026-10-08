# 对外契约

三组契约:**CLI ↔ 人**、**控制面 ↔ 客户端**、**控制面 ↔ 节点**。

## 1. CLI(`mpx`)

全局 flag:`--server`(覆盖 `MPX_SERVER` 与配置文件)、`--token`(覆盖 `MPX_TOKEN`)、`-o table|json`。

CLI **是 `/api` 的一个纯客户端**,自己不碰数据库,也不碰节点。所以下面每条命令都能用 `curl` 等价做出来;CLI 只是把参数拼成请求再把结果显示成人能看的样子。

```
login                                    存控制面地址与 token
serve                                    起控制面:--addr --db --interval(默认 30s)
status                                   控制面心跳、对象数量、退避中的节点(读缓存,不碰节点)
backup   [-o 文件]                        下载一份备份归档(默认 mpx-backup-<时间戳>.tar.gz)
restore  <归档> [--force]                 把一份备份暂存到控制面,重启后生效

node     list | show | add | rm | config | sync | install
inbound  list | show | add | rm
host     list | add | rm
user     list | show | add | rm | enable | disable
         | links | reset-traffic | revoke-sub
settings show | set
```

各命令的参数用 `mpx <命令> --help` 看(那是代码的职责,不在文档里抄第二份)。

```console
$ mpx user add alice --limit 100GB --reset-strategy monthly --note "老朋友"
已创建用户 alice

$ mpx user list
ID  用户名    状态        已用  上限     到期          重置策略     入站  备注
1   alice  active    0B  100GB  不过期         monthly  全部  老朋友
2   bob    active    0B  不限     2030-01-01  none     全部
3   carol  disabled  0B  不限     不过期         none     全部

$ mpx user revoke-sub alice      # 只换订阅 token,不动协议凭证
```

两个输入约定:

- `--limit` 走 **1024 进制**(`1GB` = 2³⁰ 字节)。直接往 `/api` 塞十进制数会得到另一个数。
- `--expire` 收 `YYYY-MM-DD`,解释成**当地时区当天 23:59:59**。`--expire 2030-01-01` 存成 `2030-01-01T15:59:59Z`(Asia/Shanghai)。

`node config` 与 `node sync` 是两回事:**前者只打印,后者真下发并重启**。改结构前先 `config` 看一眼。

`node install` 是第三条路:它装核心、写单元文件、下发配置、启动,一条命令从裸机器到能用。
它的请求默认上限 15 分钟(要下载 21MB 发布包、往节点推 65MB),其它命令是 30 秒 ——
这条例外写在 `internal/cli/client.go` 的 `installTimeout`,不是把全局上限调大。

## 2. HTTP 管理接口

全部挂 `/api/`,过一个 Bearer token。

```
GET|POST         /api/nodes          GET|PUT|DELETE  /api/nodes/{id}
GET              /api/nodes/{id}/config              预览,不下发
POST             /api/nodes/{id}/sync                下发 + 重启
POST             /api/nodes/{id}/install             装核心 + 下发 + 启动(?version= 指定版本)

GET|POST         /api/inbounds       GET|PUT|DELETE  /api/inbounds/{id}
GET|POST         /api/inbounds/{id}/hosts             PUT|DELETE  /api/hosts/{id}

GET|POST         /api/users          GET|PUT|DELETE  /api/users/{username}
POST             /api/users/{username}/reset-traffic
POST             /api/users/{username}/revoke-sub
GET              /api/users/{username}/links

GET|PUT          /api/settings

GET              /api/backup                 流式返回 tar.gz 备份归档
POST             /api/restore                校验并暂存一份备份(?force=1 覆盖已暂存的)

GET              /api/status                 控制面心跳 + 对象数量 + 节点退避状态(只读内存,不发 ssh)
```

`backup` 的正文是 gzip 过的 tar,不是 JSON:CLI 直接落盘,`curl` 也能重定向到文件。`restore` 的**正文就是归档本身**(非 JSON 包装),只做校验 + 暂存,不换库:

```console
$ curl -H 'Authorization: Bearer demo-token' -o mpx-backup.tar.gz localhost:8080/api/backup

$ curl -H 'Authorization: Bearer demo-token' \
    --data-binary @mpx-backup.tar.gz localhost:8080/api/restore
{ "message": "已就绪,重启 mpx 后生效" }
```

归档里有 `manifest.json` + 一致快照 `mpx.db` + 可选的系统 ssh 私钥 `id_ed25519`,**没有管理 token**(见 [decisions.md](decisions.md) 第 16 条)。换入发生在控制面下次启动、打开数据库之前:旧库改名 `<db>.pre-restore-<UTC 时间戳>` 留着回退,`.restore` 变成新库。没有控制面也能恢复 —— 手动把 `mpx.db` 改名成 `<db>.restore` 再启动即可。

`status` 给的是「控制面现在怎么样」:启动时间与运行时长、后台周期、上一轮 Tick 的时间与耗时、Tick 次数、节点/入站/对外入口/用户的数量,以及正在退避的节点(连续失败几次、什么时候再试)。它只读控制面内存里的心跳和库里的计数,**不查节点、不发 ssh**,所以节点卡住时它照样立刻回答;`mpx status` 读的就是这个接口,字段见 [model.go](../internal/model/model.go) 的 `ControlStatus`。

token 为空时**完全不校验**,所以那种模式只允许绑回环地址(非回环 + 空 token 会启动失败):

```console
$ curl -H 'Authorization: Bearer demo-token' localhost:8080/api/users
[
  { "id": 1, "username": "alice", "sub_token": "DOeGM8286gAsgFfm",
    "enabled": true, "status": "active",
    "data_limit": 107374182400, "used_traffic": 0, "lifetime_used_traffic": 0,
    "reset_strategy": "monthly", "note": "老朋友",
    "created_at": "2026-10-07T10:24:38Z" }
]

$ curl -H 'Authorization: Bearer wrong' localhost:8080/api/nodes
401 { "error": "管理 token 不正确" }
```

**`enabled` 可以不写,但两个方向含义不同。** 请求体里不带 `enabled` 时:

| 请求 | 结果 |
| --- | --- |
| `POST /api/users` | 启用 —— 新建的用户默认能用,和 `mpx user add` 一致 |
| `PUT /api/users/{name}` | 保持原样 —— 只想改流量上限的一次 PUT 不会把停用的用户悄悄启用 |

显式写 `"enabled": false` / `true` 时以请求体为准。服务端为此在请求体上把 `enabled` 读成 `*bool`(`internal/server/validate.go` 的 `userRequest`),为的是分得清「没说」和「说了 false」;领域模型 `model.User.Enabled` 仍是裸 bool,零值只在真正要表达「停用」的地方出现。

**对外入口的返回里有两组值。** 存下来的字段原样返回(空就是空),`effective` 里才是合并节点与入站之后真正发出去的值:

```console
$ curl ... /api/inbounds/2/hosts
[ { "id": 2, "inbound_id": 2, "remark": "美国 WS", "address": "", "port": 0,
    "effective": { "address": "203.0.113.45", "port": 2087, "path": "/ws" } } ]
```

`mpx host list` 显示的是 `effective` —— 只填了备注时,光看那几个空字段看不出客户端最终会连到哪里。回落规则只有 `render.ResolveExposure` 一份,见 [model.md](model.md) 的「host 的回落」。

## 3. HTTP 订阅接口

```
GET /sub/{token}              按 User-Agent 决定格式
GET /sub/{token}/{format}     强制格式
```

`format` ∈ `links | base64-links | clash | clash-meta | sing-box | xray`。

**不鉴权** —— 订阅 token 即凭证。这是行业惯例,但也意味着订阅链接泄露等同于凭证泄露。所以订阅 token(`sub_token`)与协议凭证种子(`key`)是分开存的:`revoke-sub` 只换前者。

### 响应头

```
Content-Disposition: attachment; filename=alice
Content-Type: text/yaml; charset=utf-8
Profile-Title: base64:YWxpY2Ug55qE6K6i6ZiF
Profile-Update-Interval: 12
Subscription-Userinfo: upload=0; download=0; total=107374182400; expire=0
Support-Url: https://t.me/example
```

`Profile-Title` 用 `base64:` 前缀是因为订阅名里可能是中文。`Support-Url` 与 `Profile-Update-Interval` 只在设了值时才发。

| 格式 | Content-Type |
| --- | --- |
| `clash` / `clash-meta` | `text/yaml; charset=utf-8` |
| `sing-box` / `xray` | `application/json; charset=utf-8` |
| 其余 | `text/plain; charset=utf-8` |

### User-Agent 匹配

按 `settings.rules` 顺序逐条 `regexp` 匹配:

- 第一条命中的决定格式(或 `block` → 404);
- 一条都没命中 → 分享链接;
- **正则写坏的那条跳过而不报错**;
- 路径里的格式优先级高于规则。

正则里的 `(?i)` 用来忽略大小写 —— `(?i)clash` 能同时接住 `clash-verge` 和 `ClashforWindows`。

## 4. 节点侧契约

节点上不装任何 mpx 组件,全部经系统自带的 `ssh`。

### 连接参数

```
ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new \
    [-p <非 22 端口>] user@address <脚本>
```

`BatchMode=yes` 是这条设计里最要紧的一项:控制面是常驻进程,`ssh` 一旦停在等密码的提示上,整轮收敛就跟着停住。**必须先配好免密。**

`ConnectTimeout=10` 只管到握手为止,**远端命令本身另有一条 deadline**:调用点按操作给预算(采样 20 秒、收敛 60 秒、`node sync` 2 分钟、`node install` 15 分钟),`internal/node` 再兜一个 15 分钟的下限,调用方自己带了更短的 deadline 就以调用方为准。超时和连不上是两种错,报错文本分开写:超时是「命令超时(节点卡住了)」,连不上还是 `ssh` 原话。理由见 [decisions.md](decisions.md) 第 17、18 条。

跳板机、`ssh-agent`、`~/.ssh/config`、`known_hosts` 全部复用系统 `ssh` 已有的那套 —— 这也是当初不引 `golang.org/x/crypto/ssh` 的理由(见 [decisions.md](decisions.md) 第 2 条)。

### 下发流程

```
备份 p → p.bak
  → 新配置经 stdin 落到 p.tmp
  → mv p.tmp p
  → xray -test -config p
      ├─ 退出码非 0 → cp p.bak p 回滚,报错,不重启
      └─ 退出码 0   → systemctl restart <service>
```

内容走 stdin 而不是命令行参数,是为了让配置内容不落进节点上的进程表。写入用 `umask 077`。

`xray -test -config` 的退出码:**好配置 0 且打出 `Configuration OK.`,坏配置 23**。回滚就靠这个。

### 节点上的 `config.json`

| 段 | 内容 |
| --- | --- |
| `api` | 入站 `dokodemo-door`,只监听 `127.0.0.1:<api_port>`,services 是 `HandlerService` + `StatsService` |
| `stats` | `{}` |
| `policy` | 打开按用户、按入站的统计 |
| `inbounds` | API 入站 + 每个业务入站各一条 |
| `outbounds` | `direct`(freedom) + `block`(blackhole) |
| `routing` | `inboundTag: ["api"] → api`;`geoip:private → block` |
| `log` | `loglevel: warning` |

`geoip:private → block` 挡住用户借节点打节点自己的内网。**这是有意的,不要为了让某个本地测试通过而删掉它**(本地测试要放行回环,就在 `routing.rules` 里插一条 `ip: ["127.0.0.1/32"] → direct`,且必须插在 `api → api` 那条**之后**,否则 API 自己会被送进 direct 而失联)。

### 运行时命令

四条都在节点上执行,连的是那个只监听回环的 API 入站:

| 操作 | 命令 |
| --- | --- |
| 读某入站的用户名单 | `xray api inbounduser --server=127.0.0.1:<port> <tag>` |
| 加用户 | `xray api adu --server=... <config>`(配置片段走 stdin) |
| 删用户 | `xray api rmu --server=... <tag> <email>` |
| 读流量累计值 | `xray api statsquery --server=...` |

`email` 就是用户名 —— 按用户聚合流量靠的是这个。读数**不带 `-reset`**:节点上的计数不再被抹掉,控制面自己存上次累计值算差(见 [decisions.md](decisions.md) 第 9 条)。

`inbounduser` 返回 protojson。`xray api adu` 收的是一份**完整配置**,它从里面解出入站再逐个加用户,所以这里复用 `ServerConfig` 的入站渲染,而不是另写一份 account 拼装逻辑(两份必然漂移)。

### 结构变更 vs 人员名单

| 变更 | 通道 | 是否重启 xray |
| --- | --- | --- |
| 加入站、改端口、换证书 | 人显式触发 `node sync` | 是 |
| 加人、摘人、到期、超限 | 周期收敛(每 `--interval`) | **否** |
| 换 xray 核心 | 人显式触发 `node install` | 是 |

理由见 [decisions.md](decisions.md) 第 3 条。

### 装核心

控制面把发布包下载一次缓存在 `<库目录>/xray/<版本>_<os>_<arch>/`,再用和其它下发一样的 `PutFile` 把三个文件推过去:

| 文件 | 落点 |
| --- | --- |
| `xray` | `/usr/local/lib/mpx/xray` |
| `geoip.dat` / `geosite.dat` | 与核心同目录(xray 只去二进制所在目录找 `.dat`) |
| systemd 单元 | `/etc/systemd/system/<服务名>.service` |

节点上只需要 `uname`、`mkdir`、`mv`、`chmod`、`systemctl` —— 不需要 curl / unzip,也不需要能连 GitHub。

**推之前先比一次 sha256**,一样的文件不重推。一次 ssh 问完三个目标路径(节点上有 `sha256sum` 或 `shasum` 就用,一个都没有、文件不在、或者这次查询失败,就当三个都不一样全推一遍 —— 少推一个文件只是多等一会儿,推错一个装不起来)。换一版核心通常只动 65MB 里的一个,这一步把重跑从「等一次传输」变成「等一次握手」。

替换顺序是**先校验后顶替**:新核心先写成 `xray.new`,拿节点上**现有的**配置跑一次 `-test`,通过了才 `mv -f` 顶上去。全新机器没有配置可校验,这一步跳过。核心没变时这一步整个不跑 —— 它当初就是这么装上去的。`InstallCore` 自己**不启动服务** —— 装完紧接着走一次 `Deploy`,由它负责启动并确认真的起来了,失败的兜底也因此在同一处。落地后回读 `xray version` 的第二段作为显示的版本号,拿不到就退回整行。
