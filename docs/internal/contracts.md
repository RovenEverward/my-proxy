# 对外契约

三组契约:**CLI ↔ 人**、**控制面 ↔ 客户端**、**控制面 ↔ 节点**。

## 1. CLI(`mpx`)

全局 flag:`--server`(覆盖 `MPX_SERVER` 与配置文件)、`--token`(覆盖 `MPX_TOKEN`)、`--profile`(覆盖 `MPX_PROFILE` 与文件里的 `current`)、`-o table|json`。

`--server` / `--token` 是**这一次**用这个地址,不碰配置文件 —— 临时对另一台控制面敲一条命令就靠它。
要长期记住另一台,用 `login --profile <档名>`,不填档名就是 `default`。

CLI **是 `/api` 的一个纯客户端**,自己不碰数据库,也不碰节点。所以下面每条命令都能用 `curl` 等价做出来;CLI 只是把参数拼成请求再把结果显示成人能看的样子。

`init` 会 SSH 到一台机器上(它是在控制面存在之前运行的命令),但建对象的活交给 VPS 上的 `bootstrap`,后者仍是 `/api` 客户端。理由见 [decisions.md](decisions.md) 第 32 条。`diagnose` 是本地诊断入口，不连接管理 API；`node diagnose` 和 `node diagnostics` 则是纯 API 客户端。

```
login   [--profile 档名] [--server 地址] [--token token]
                                         存控制面地址与 token;不带参数时显示当前生效的值
init    --host <地址> [--ssh-password]   一条命令在单台 VPS 上装完控制面 + 节点 + 订阅(见下)
serve                                    起控制面:--addr --db --interval(默认 30s)
status                                   控制面心跳、对象数量、退避中的节点(读缓存,不碰节点)
doctor                                   自检:控制面、每台节点、六种订阅格式、采样表(有 fail 时退出码 3)
backup   [-o 文件]                        下载一份备份归档(默认 mpx-backup-<时间戳>.tar.gz)
restore  <归档> [--force]                 把一份备份暂存到控制面,重启后生效

node     list | show | status | add | edit | rm | config | sync | install
         | diagnose (ID|--all) [--engine fusion|light] [--hardware] [--speed=false]
         |          [--fusion-timeout 15m] [--network-only] [--from node|control]
         | diagnostics ID [--from node|control]
diagnose [--socks5 socks5h://host:port] [--speed]  当前操作机本地诊断,不读取管理登录
inbound  list | show | add | edit | rm
host     list | add | edit | rm
user     list | show | add | edit | rm | enable | disable | renew | export | import
         | links | reset-traffic | revoke-sub
settings show | set | test | template get|set|del
rule     list | add | move | rm | toggle | final TARGET | test DOMAIN
         | catalog | migrate [--dry-run|--confirm|--replace|--report] | mode
group    list | add | edit | rm | preview
```

`bootstrap` 是 **Hidden 子命令**(不在 `--help` 里),它是 `init` 在 VPS 上的另一半,由 `init` 调用而不是给人直接敲:

```
bootstrap --address <节点对外地址> [--name default] [--username user1] [--inbound-port 443]
          [--api-port 8080] [--sni www.cloudflare.com] [--reality-dest www.cloudflare.com:443]
          [--sub-prefix <订阅前缀>] [--remark <备注>] [--env-file /etc/mpx/env]
          [--key-file /var/lib/mpx/.ssh/id_ed25519.pub]
```

它假定控制面跑在本机(`127.0.0.1:8080`),token 从 `--env-file` 里读(不经过命令行,所以不进 `ps` 与 shell 历史),公钥从 `--key-file` 读并追加进 `~/.ssh/authorized_keys`。每一步都是**幂等**的:节点按名字复用、入站按「同协议+同安全层+同端口」复用、入口已有就不再加、订阅前缀非空就不动、用户存在就跳过,重复跑不会建出第二份。

### 元数据 API

管理接口：`GET/PUT /api/nodes/{id}/metadata` 和 `/api/hosts/{id}/metadata`。全部使用同一 Bearer 管理鉴权。PUT 是完整替换；元数据 `{}` 清空覆盖，回落 node。节点元数据只认 `region`、`city`、`letter`（命名 RFC N3）：省略这两个键时保留原值（老客户端整份 PUT 不会冲掉）；有 `region` 而 `letter` 为空时由控制面按（地区, 城市）分配最小空闲字母，显式字母冲突 409；host 元数据带 `city`/`letter` 400。老客户端带来的 `line`/`purposes` 字段不再认识，写入时被忽略，读出时也不会出现（v0.7.0 删除）。`GET /api/nodes`、`GET /api/nodes/{id}` 的节点视图多一个只读 `display`（旗帜 + 城市或国家中文名 + 字母，无地区时省略）。新建或改名为纯数字的节点名 400。`POST /api/nodes/{id}/locate`（N4）只返回建议不写入：`{region, city, city_en, ip, source:"ipwho.is", via:"control"|"node", display}`；地址是 IP 时控制面查 ipwho.is，是域名时 SSH 到节点 `curl https://ipwho.is/` 看出口；城市不在内置对照表里 `city` 为空；查不到 502。`POST /api/nodes` 建好节点后，控制面默认按 IP 自动 locate 并写入显示名（只在节点还没有地区时写；地址是域名时不自动探测），回答在节点字段之外多 `display` 与 `locate_note`（失败提示，不影响 201）；`mpx serve --auto-locate=false` 或 `MPX_AUTO_LOCATE=0` 关闭。

### 分流规则 API 与订阅

`GET /api/routing`（只读，`mode` 恒为 `rules`）、`POST /api/routing/migrate`、`GET/PUT /api/rules`、`PATCH /api/rules/{id}`、`PUT /api/rules/final`、`POST /api/rules/test`、`GET /api/rule-catalog`，以及 `GET/POST /api/proxy-groups`、`GET/PUT/DELETE /api/proxy-groups/{id}`、`POST /api/proxy-groups/preview`。`PUT /api/routing`（旧的分流模式开关）与 `GET/POST /api/purposes*` 已在 v0.7.0 删除，旧版 CLI 或脚本调用得到 404。

`/sub/{token}[/{format}]` 保持 token 授权。旧的 `?purpose={slug}` 查询参数被忽略，一律返回普通订阅（按规则表分流）。`clash-meta` 与 `sing-box` 的分流部分由规则表生成，原版 `clash`、`links`、`xray` 不变。

### 综合体检 API

`POST /api/nodes/{id}/diagnostics` 接收 `diagnostics.Config` 加可选 `network_only`。空配置默认在 node 位置执行完整 Fusion 体检，使用官方 Go ECS v0.2.12；完整体检保留 mpx 健康、配置漂移和全部实际获授权入口检查。`engine:light` 显式选择原有完整流程。control 位置未指定引擎时默认 light；Fusion 只允许 node。`network_only:true` 仅执行原有有界网络测量，默认 light。执行位置不允许 local、SOCKS URL、API 密钥或自选密钥环境变量。

完整体检的 `speed` 默认 true，显式 false 关闭所选引擎测速。仅网络模式默认 false。Fusion 的 `hardware` 默认 false，显式 true 才开启 CPU、内存和磁盘测试；`fusion_timeout_seconds` 默认 900，允许 60..1800 秒。light 或仅网络模式不能配置 Fusion 专属参数；Fusion 不能配置原有的流量、样本、请求时限、目标或供应商参数，不静默忽略调用方输入。

`GET /api/nodes/{id}/diagnostics?from=node|control` 读取该位置最新报告。两个 API 沿用管理鉴权；不存在的节点或报告 404，配置错误 400，全局正在运行 409，同节点同位置一分钟内再运行 429，部分执行取消等错误 502。成功正文为 `{node_id, measured_address, source, checked_at, report}`，失败时若已获得合法部分报告则为 `{error, record}`。CLI 输出部分证据后返回错误，429 不自动重试。`measured_address` 保留开始测量时的节点地址，后续编辑节点不改写旧证据。

完整 `report` 保留 `{summary,health,drift,network,entrances}`，Fusion 路径增加 `fusion` 执行封装，`network` 不伪造为轻量测量结果。light 保留原有 `network` 报告。统一保存一次，每节点 node/control 各一份最新报告，单条最多 256 KiB。CLI `--all` 顺序执行，失败保留报告后继续，取消则停止；全程共用一个诊断槽。

Fusion 总流程预算为上游执行时限加 3 分钟，API 再加 30 秒，CLI 再加 45 秒；默认分别为 18 分钟、18 分 30 秒、18 分 45 秒。light 完整流程仍为 6 分钟，API 为 6 分 30 秒，CLI 为 6 分 45 秒。仅网络模式默认 120 秒、最大 170 秒，API 为 3 分钟，CLI 为 3 分 15 秒。原有 32 MiB HTTP 应用数据预算只属于 light/仅网络执行器；Fusion 上游测速不提供总流量硬上限，以执行时限约束。入口探测仍有独立的正文与时间上限。

#### Fusion 上游边界

控制面缓存固定版本的官方 ZIP，并校验内置 SHA-256；仅接受匹配架构的单个 `goecs` ELF。节点目标为 `/usr/local/lib/mpx/goecs/v0.2.12/goecs`，校验后原子替换。独立可执行程序按需运行，无常驻 Agent，不重启 Xray，也不执行上游安装脚本或系统软件包更新。官方程序采用 GPL-3.0，源码与发布来源见 [oneclickvirt/ecs v0.2.12](https://github.com/oneclickvirt/ecs/releases/tag/v0.2.12)；mpx 本身仍按仓库许可证发布。

调用固定包含 `-menu=false -l=zh -upload=false -privacy=true -json=-`。`speed:false` 禁用上游 nt3/speed。`-upload=false` 关闭报告上传；`-privacy=true` 另外关闭统计信标。隐私模式不阻止外部 IP 服务或其他测评查询，也不保证终端前缀隐藏实际 IP。

`fusion` 封装保存固定版本与来源、执行状态、起止时间、耗时、配置、清理后的原始文本 `output`、真实上游 JSON `data`、截断标志、警告和错误。文本最多 64 KiB，上游 JSON 最多 128 KiB；完整保存记录仍受 256 KiB 限制。上游 `-json=-` 输出是终端文本前缀加 `goecs.report/v1` JSON，不能把整个 stdout 当作 JSON。隐私模式下上游 `text` 可为空，前缀仍须作为可能含 IP 的证据处理。

解析要求 `schema_version:goecs.report/v1` 与 `ecs_version:v0.2.12`，保留上游 `sections` 和类型化 `components`，不凭文本拼造模块数据。状态区分 done、partial、failed、timeout 和 canceled。缺失、截断、版本不符或模块未完成的 JSON 保留部分文本与具体警告；上游取消且返回 0 也不能判为完成。完整运行与清理的证据范围见 [体检验证记录](diagnostic-validation.md)。

light 可选的固定密钥环境变量仍为执行机器的 `MPX_ABUSEIPDB_KEY` 与 `MPX_IPAPI_IS_KEY`，不会经远程 API 接收或进入报告。

### 一键路径(`init` 与 `bootstrap`)

`init` 在**操作机上**跑,只做 SSH 编排,顺序是:探测对面是什么(一次 ssh 问完 `os`/`arch`/`uid`/有没有 `curl`/有没有 `mpx`)→ 校验是 Linux 且 uid 是 0 → 上传 `deploy/install.sh`(优先用本机仓库里的那份,找不到才让 VPS 自己去 GitHub 取)→ 可选上传 `--binary` → 跑 `install.sh --addr 0.0.0.0:8080` → 确认装上的 `mpx` 认 `bootstrap` 子命令 → 远端跑 `bootstrap` → 从 `/etc/mpx/env` 读回 token → 打印订阅链接与 `mpx login` 命令。

带 `--ssh-password` 时,密码只用于**第一次**把操作机的公钥装进 VPS 的 `authorized_keys`(密码认证下 askpass 占着 stdin,没法同时给远端喂标准输入),之后全程走密钥。不带则直接用 `--identity` 指定的私钥。

| 参数 | 默认 | 说明 |
| :--- | :--- | :--- |
| `--host` | *(必填)* | VPS 地址 |
| `--ssh-user` / `--ssh-port` | `root` / `22` | 必须是 root(装 systemd 服务、写 `/etc/mpx` 都要) |
| `--ssh-password` | — | 带它才提示输密码 |
| `--identity` | — | 私钥路径,密码路径下装公钥后也用它 |
| `--binary` | — | 本机编好的 Linux `mpx`,传上去用(控制面连不上 GitHub 时) |
| `--install-script` | 自动查找 | 指定 `install.sh` 的路径 |
| `--addr` | `0.0.0.0:8080` | 控制面监听地址 |
| `--api-port` | 从 `--addr` 取 | 覆盖从 `--addr` 里解析出的端口;也是订阅前缀里的端口 |
| `--name` / `--username` | `default` / `user1` | 节点名与默认用户名 |
| `--sni` / `--reality-dest` | `www.cloudflare.com` / `www.cloudflare.com:443` | REALITY 借用的目标站点 |
| `--sub-prefix` | `http://<host>:<api-port>` | 订阅地址前缀,换了域名要改 |
| `--remark` | 用节点名 | 入站入口的备注 |

`--addr` 默认 `0.0.0.0:8080`,意味着管理接口与订阅都在公网明文 HTTP 上,token 是唯一凭据。长期使用要把 TLS 反向代理放在前面,并把 `--sub-prefix` 换成 `https://` 的域名。

### 连哪一台控制面(`login` 与配置档)

配置文件里能同时放多套控制面,各有一个档名:

```console
$ mpx login --server https://a.example:8080 --token <token>     # 存进 default,并切过去
已写入 /Users/me/Library/Application Support/mpx/config.json(当前配置档 default)

$ mpx login --profile scratch --server http://127.0.0.1:18080 --token docstoken
已写入 /Users/me/Library/Application Support/mpx/config.json(当前配置档 scratch)

$ mpx login                                                     # 不带参数 = 看,不写文件
server: http://127.0.0.1:18080
token:  docsto…
配置档:  default  *scratch

$ mpx login --profile default                                   # 只给档名 = 切换
已切到配置档 default(server: https://a.example:8080)
```

覆盖已有档时会先回显变化,方便出事时一眼看见:

```
配置档 default 的地址:https://a.example:8080 → http://127.0.0.1:18080
配置档 default 的 token:abc123… → docsto…
已写入 /Users/me/Library/Application Support/mpx/config.json(当前配置档 default)
```

- **只想临时换一台**用 `--server` / `--token`(或 `MPX_SERVER` / `MPX_TOKEN`):只影响本次,不写文件。
- **切档只给 `--profile` 就够**,不用把 token 再敲一遍 —— token 只存在服务器上,切回来是一次本地操作。
  但档名必须已经存在:拼错了会报错并列出有哪些档,而不是静静地建一个空档。
- 选档优先级:`--profile` > `MPX_PROFILE` > 文件里的 `current`。
- 旧格式(顶层直接写 `server`/`token`)读得懂,会自动折成 `default` 档;保存时不再写回旧字段。
  读取不会顺手改文件。
- 写配置走「同目录临时文件 → 写 → fsync → rename」,不会留下半个 JSON。

`user list` 默认只给一页(50 个),`--page` / `--page-size` 翻页,`--status` / `--search` / `--inbound` 筛选;
末尾会写共多少个、还有多少个没显示,以及照着敲就能看下一页的命令(带上刚才的筛选条件)。

`user rm` / `enable` / `disable` / `renew` / `links` 都收**一个或多个**用户名,逐个执行:中途某个失败不影响其余,
末尾报「完成:N 个成功,M 个失败」,有失败时退出码非 0。只给一个名字时输出和以前一样(不加汇总行),
这是为了让已有的脚本不被改坏。

各命令的参数用 `mpx <命令> --help` 看(那是代码的职责,不在文档里抄第二份)。

### 改一个已有的对象(四个 `edit`)

`node` / `inbound` / `host` / `user` 各有一个 `edit`,只改**命令行里给出来的**字段,没给的保持原样:

```console
$ mpx node edit 2 --name jp-1
已更新节点 2(jp-1)

$ mpx user edit alice --limit 200GB
已更新用户 alice

$ mpx inbound edit 7 --port 9443
注意:改的是节点配置,节点上还是旧的;跑 mpx node sync 7 才生效(会重启 xray)
已更新入站 7(东京 WS)

$ mpx host edit 13 --inbound 2 --remark "东京 WS 备用"
已更新对外入口 13
```

改节点地址时还会多一行提醒(地址没变就不打):

```
注意:节点地址由 203.0.113.45 变成 203.0.113.9,订阅里没显式写地址的入口会跟着变。
      想确认哪些入口用了节点地址:mpx host list --inbound <id>
```

这条规则不是装饰:控制面的 `PUT` 是**整体替换**,所以 CLI 每次都先 `GET` 回来、只覆盖动过的字段、再整个写回去。
不这么做,「只想加个备注」会把流量上限和到期日一起抹掉。

「没给」和「给了空值」是两回事。要把字段改回**空状态**得显式给空值:

| 命令 | 写法 | 含义 |
| --- | --- | --- |
| `user edit` | `--limit 0` | 不限流量 |
| `user edit` | `--expire ""` | 永不过期 |
| `user edit` | `--inbounds ""` | 可用全部入站 |
| `host edit` | `--address ""` | 回落成节点地址 |
| `host edit` | `--port 0` | 回落成入站端口 |
| `inbound edit` | `--alpn ""` / `--short-ids ""` | 清空该列表 |

因此实现的判断依据是「这个 flag 这次到底出现过没有」,不是「值是不是零」—— 用零值判断就永远改不回上面的状态。

`host edit` 需要 `--inbound`:控制面没有「按 id 读一个入口」的接口,只能把那个入站的入口列出来再找。

改 `node` 的地址、或改 `host` 的地址/端口,会改变订阅里发出去的入口,用户下次刷新订阅就跟着变;
改 `inbound` 的端口/传输/安全层动的是节点上的配置,要跑 `mpx node sync <id>` 才生效(会重启 xray)。
`--max-users` 只在订阅层算,不用下发。

```console
$ mpx user add alice --limit 100GB --reset-strategy monthly --note "老朋友"
已创建用户 alice

$ mpx user list
ID  用户名   可用  状态      已用  上限   到期        重置策略  入站  备注
1   alice   启用  active    0B    100GB  不过期      monthly   全部  老朋友
2   bob     启用  active    0B    不限   2030-01-01  none      全部
3   carol   停用  active    0B    不限   不过期      none      全部
共 3 个用户

$ mpx user list --status expired --search li --inbound 2
(空)
共 0 个用户

$ mpx user renew alice bob --days 30     # 各自从自己的到期日起顺延 30 天
alice: 已把用户 alice 的到期日设为 2026-11-07
bob: 已把用户 bob 的到期日设为 2030-01-31
完成:2 个成功,0 个失败

$ mpx user export > users.csv          # 列名见下;导出的是定义,不含用量与凭证
$ mpx user import -f users.csv         # 按用户名 upsert,逐行独立提交
第 2 行:已创建用户 alice
第 3 行:已更新用户 bob
导入完成:1 个新增,1 个更新,0 个失败

$ mpx user revoke-sub alice      # 只换订阅 token,不动协议凭证
```

`renew` 两种给法:定死日期用 `--expire YYYY-MM-DD`,顺延用 `--days N`(只能给一个)。
`--days` 的基准是**各人自己的到期日**(没到期不抹掉剩余时间),已经过期或不过期的从今天算,
结果时分秒固定当天 23:59:59。批量的 `--days` 因此要逐个读回用户再写,不能按同一天算。

CSV 的七列是 `用户名,流量上限,到期时间,可用性,重置策略,入站范围,备注`(顺序不拘,按列名认;
不认识的列忽略,以后加列旧文件还能读)。空单元格表示「不限 / 不过期 / 全部 / 默认策略」,
`可用性` 只认 `启用`/`停用`(以及 `true`/`false`/`是`/`否`/`1`/`0`)——**认不出的写法会报错而不是当启用**,
理由见 [decisions.md](decisions.md) 第 25 条。`到期时间` 收 `YYYY-MM-DD`(当地时区当天 23:59:59),
`流量上限` 走和 `--limit` 一样的 1024 进制。导入按用户名 upsert:已存在的用户只改这七个定义字段,
`used_traffic` 与协议凭证原样保留。

两个输入约定:

- `--limit` 走 **1024 进制**(`1GB` = 2³⁰ 字节)。直接往 `/api` 塞十进制数会得到另一个数。
- `--expire` 收 `YYYY-MM-DD`,解释成**当地时区当天 23:59:59**。`--expire 2030-01-01` 存成 `2030-01-01T15:59:59Z`(Asia/Shanghai)。

第二核心命令：`node install <id> --core sing-box [--version vX.Y.Z]`、`node config <id> --core sing-box`，默认仍为 Xray。`node core show/edit <id> <xray|sing-box>` 对应 `GET/PUT /api/nodes/{id}/cores/{core}`，只能修改 API 端口、配置路径与服务名，下发哈希/时间由控制面维护。已部署 sing-box 的位置元数据变更返回 409，合法 no-op 允许；碰撞、非绝对配置路径和不安全服务名返回 400。`POST /api/nodes/{id}/install?core=sing-box&version=vX.Y.Z` 使用官方源码 stats 构建。QUIC 参数、客户端兼容与重启边界见 Hysteria2/TUIC（用户指南待迁移）。

`node config` 与 `node sync` 是两回事:**前者只打印,后者真下发并重启**。改结构前先 `config` 看一眼。

`node install` 是第三条路:它装核心、写单元文件、下发配置、启动,一条命令从裸机器到能用。
它的请求默认上限 15 分钟(要下载 21MB 发布包、往节点推 65MB),其它命令是 30 秒 ——
这条例外写在 `internal/cli/client.go` 的 `installTimeout`,不是把全局上限调大。

## 2. HTTP 管理接口

全部挂 `/api/`,过一个 Bearer token。

```
GET|POST         /api/nodes          GET|PUT|DELETE  /api/nodes/{id}
                                                     节点 + 最近一次状态(?fresh=1 现场查一次)
GET              /api/doctor                         自检:控制面、每台节点、六种订阅格式、采样表
GET              /api/nodes/{id}/config              预览,不下发
POST             /api/nodes/{id}/sync                下发 + 重启
POST             /api/nodes/{id}/install             装核心 + 下发 + 启动(?version= 指定版本)

GET|POST         /api/inbounds       GET|PUT|DELETE  /api/inbounds/{id}
GET|POST         /api/inbounds/{id}/hosts             PUT|DELETE  /api/hosts/{id}

GET|POST         /api/users          GET|PUT|DELETE  /api/users/{username}
                                                     列表支持 ?limit= ?offset= ?status= ?search= ?inbound_id=
POST             /api/users/{username}/reset-traffic
POST             /api/users/{username}/revoke-sub
GET              /api/users/{username}/links

GET|PUT          /api/settings
POST             /api/settings/test-webhook   往当前 webhook_url 发一条测试事件

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

`status` 给的是「控制面现在怎么样」:启动时间与运行时长、后台周期、上一轮 Tick 的时间与耗时、Tick 次数、节点/入站/对外入口/用户的数量,以及正在退避的节点(连续失败几次、什么时候再试)。它只读控制面内存里的心跳和库里的计数,**不查节点、不发 ssh**,所以节点卡住时它照样立刻回答;`mpx status` 读的就是这个接口,字段见 `internal/model/model.go` 里的 `ControlStatus`。

`status` 里的 `version` 是**这份控制面自己的版本**(编译期注入,拿不到时为空串),`update` 是后台每 6 小时问一次 GitHub 的结果:`current` / `latest` / `behind`(落后几次发布)/ `url`(发布页)/ `notes`(发布说明摘要,按 rune 截到 2000 字)/ `published_at` / `checked_at` / `error`。**它只报告,不下载不替换** —— 升级是显式动作,走 `POST /api/update/control` 与 `mpx update apply`,见 [安装与升级](../atlas/install-update.md) 和 [decisions.md](decisions.md) 第 39 条。检查失败时 `error` 有值而上次的结论仍然保留(`checked_at` 不变),因为断网不等于没有新版本;`checked_at` 为 `null` 表示还没查过。判断"有没有更新"只需看 `behind > 0`,`behind` 为 0 涵盖了「已是最新」「比不出来(伪版本)」「没装版本号」三种情形。

token 为空时**完全不校验**,所以那种模式只允许绑回环地址(非回环 + 空 token 会启动失败):

```console
$ curl -H 'Authorization: Bearer demo-token' localhost:8080/api/users
{ "users":
  [ { "id": 1, "username": "alice", "sub_token": "DOeGM8286gAsgFfm",
      "enabled": true, "status": "active",
      "data_limit": 107374182400, "used_traffic": 0, "lifetime_used_traffic": 0,
      "reset_strategy": "monthly", "note": "老朋友",
      "created_at": "2026-10-07T10:24:38Z" } ],
  "total": 1, "limit": 50, "offset": 0 }

$ curl -H 'Authorization: Bearer wrong' localhost:8080/api/nodes
401 { "error": "管理 token 不正确" }
```

**用户列表给的是「一页 + 总数」。** `GET /api/users` 的五个查询参数:

| 参数 | 含义 | 不合法时 |
| --- | --- | --- |
| `limit` | 每页条数,1..1000,默认 50(不是「全部」) | 400 `limit 要在 1 到 1000 之间` |
| `offset` | 跳过几条,≥0 | 400 `offset 不能是负数` |
| `status` | `active` / `expired` / `limited` / `disabled`,按 `User.Status` 的判定 | 400,并把可选值列出来 |
| `search` | 用户名子串,不分大小写 | —— |
| `inbound_id` | 只留能用这个入站的用户(没限定入站的用户算在内) | —— |

响应里的 `total` 是**筛选之后**的总数,不是本页条数:客户端靠它才能说「还有 7 个」。
`offset` 超出总数时 `users` 是 `[]`(不是 `null`)、`total` 照旧。筛选与分页在哪一层做、为什么不在 SQL 里写 Where,
见 [decisions.md](decisions.md) 第 24 条。

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

**节点返回的是「节点 + 它最近一次的状态」。** `GET /api/nodes` 与 `GET /api/nodes/{id}` 都回 `NodeView`,在节点字段之外多一个可空的 `status`:

```console
$ curl ... /api/nodes
[ { "id": 3, "name": "hk", "address": "203.0.113.46", "deployed_hash": "9f2c…",
    "status": { "node_id": 3, "checked_at": "2026-10-08T06:31:02Z", "reachable": true,
                "active": true, "version": "26.3.27", "observed_hash": "9f2c…",
                "certificates": [ { "inbound_id": 4, "inbound_name": "reality-8443",
                                    "path": "/etc/mpx/certs/hk.pem", "not_after": "2027-01-04T…",
                                    "days_left": 88 } ] } } ]
```

- **列表读的是缓存**,一次 ssh 都不发 —— 否则一条 `node list` 在十几台节点上要等半分钟。
- **`?fresh=1` 现场查一次**:ssh 过去读服务状态、核心版本、配置哈希和每张证书的到期时间,落库再回答。查过之后紧接着一次不带参数的读看到的就是它,不会出现「刚查过、列表里还是旧的」。
- **`status` 为空是「还没查过」**,不是「查过没问题」;`reachable: false` 才是问了没答,那时 `error` 里是 ssh 的原话。
- 漂移不在响应里算,由读的人拿 `observed_hash` 和 `deployed_hash` 比(`!deployed_hash` 或问不到就是「不知道」,不是「一致」)。为什么这样分,见 [decisions.md](decisions.md) 第 20 条。

**`GET /api/doctor` 是同一批检查的一次汇总**,回 `{ "checks": [ { "name", "status", "detail", "hint" } ] }`:`status` 取 `ok` / `warn` / `fail`,`hint` 是「接下来该做什么」,没有可做的就省略。v0.9.2 起每条还可带 `ui_hint`:同一句建议的界面版(只说界面操作,不出现命令),管理界面优先显示它,没有时退回 `hint`;`hint` 文字不变。检查控制面库与心跳、每台节点(现场查,走的就是上面那条 `?fresh=1` 的路)、六种订阅格式各渲染一次、采样表行数。一台节点只出一行结论,按最重的那条定。

**错误回答里的 `ui_hint`(v0.9.2 起,只增)**:错误一律是 `{ "error": "..." }`。少数错误的原话里带命令行提示(设备被吊销要重新接入、还没设设备密码、设备密码走了明文、识别地区失败),这些回答另带 `ui_hint`,是同一件事的界面说法;`error` 原话不变,CLI 照旧显示它,管理界面和桌面窗口优先显示 `ui_hint`。`POST /api/nodes` 的回答在已有的 `locate_note` 之外同样多一个 `locate_ui`。

`mpx doctor` 有 fail 时退出码是 **3**(不是 1),只有 warn 时是 0 —— 理由见 [decisions.md](decisions.md) 第 23 条。

## 3. HTTP 订阅接口

```
GET /sub/{token}              按 User-Agent 决定格式;浏览器打开是订阅页
GET /sub/{token}/{format}     强制格式
POST /sub/{token}/rotate      换掉订阅 token(页面上「换个订阅地址」的落点)
```

`format` ∈ `links | base64-links | clash | clash-meta | sing-box | xray`。

**不鉴权** —— 订阅 token 即凭证。这是行业惯例,但也意味着订阅链接泄露等同于凭证泄露。所以订阅 token(`sub_token`)与协议凭证种子(`key`)是分开存的:`revoke-sub` 只换前者。

**订阅页**:`GET /sub/{token}` 只有走到「没点名格式、UA 规则没命中、本来要回默认 links」这一步、且请求的 `Accept` 里点名要 `text/html` 时,才回一屏 HTML(状态、流量、到期、各格式分享链接、换地址按钮)而不是配置 —— 见 [decisions.md](decisions.md) 第 26 条。客户端(不带 Accept 或 `*/*`)与任何点名格式的请求都拿不到页面。`POST /sub/{token}/rotate` 用路径里的 token 鉴权,换完旧地址立即 404,页面把新地址亮出来。

### 响应头

```
Content-Disposition: attachment; filename=alice
Content-Type: text/yaml; charset=utf-8
Profile-Title: base64:YWxpY2Ug55qE6K6i6ZiF
Profile-Update-Interval: 12
Profile-Web-Page-Url: https://sub.example.com/sub/2Wi7rfR2XQoiNiBv
Subscription-Userinfo: upload=0; download=0; total=107374182400; expire=0
Support-Url: https://t.me/example
```

`Profile-Title` 用 `base64:` 前缀是因为订阅名里可能是中文。`Support-Url` 与 `Profile-Update-Interval` 只在设了值时才发;`Profile-Web-Page-Url` 是浏览器打开订阅地址看到的那一屏,各家客户端会显示成「打开网页」。

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

`BatchMode=yes` 是这条设计里最要紧的一项:控制面是常驻进程,`ssh` 一旦停在等密码的提示上,整轮收敛就跟着停住。**日常维护必须依赖免密公钥认证。**
新节点首次录入时，支持通过 `mpx node add --ssh-password` 由控制面执行一次密码引导（通过 `SSH_ASKPASS` 注入，不进历史、不进参数），将控制面自身公钥追加至远端 `authorized_keys`，引导完毕后密码即刻销毁，后续全部收敛继续保持 `BatchMode=yes` 免密。

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
