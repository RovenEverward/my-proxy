# mpx

自建 xray 多节点管理:一台控制面 + 若干节点 + 一个在本机操作的 CLI。

参考 [Marzneshin](https://github.com/marzneshin/marzneshin) 0.7.4 的设计自建,不复用其代码。

> 这份 README 讲**怎么用**。想知道**为什么这么设计**、数据长什么样、还差什么,看 [docs/](docs/README.md)。

## 它管什么

```
        mpx CLI (本机)                           客户端(Clash / sing-box / xray)
             │                                              │
             │ /api  管理接口(Bearer token)                │ /sub/<token>  订阅(无鉴权,token 即凭证)
             ▼                                              ▼
        ┌──────────────────────── 控制面 ────────────────────────┐
        │  SQLite:节点 / 入站 / 对外入口 / 用户 / 订阅规则 / 设置  │
        │  渲染订阅(links / clash / clash-meta / sing-box / xray)│
        │  后台每 30s:采样流量 → 周期重置 → 收敛人员名单           │
        └───────────────────────────┬────────────────────────────┘
                                    │ ssh + xray api
                    ┌───────────────┼───────────────┐
                    ▼               ▼               ▼
                 节点 A          节点 B          节点 C     (各跑一个 xray)
```

控制面自己不进数据通路:用户流量直接打到节点上,控制面只在变更与采样时碰节点。

### 三层数据模型

| 概念 | 含义 |
| --- | --- |
| **节点 node** | 一台跑 xray 的机器,控制面经 ssh 下发配置、经 xray 的 API 增删用户 |
| **入站 inbound** | 节点上的一个 xray inbound(协议 / 端口 / 传输 / 安全层) |
| **对外入口 host** | 一个入站对外暴露的方式(地址、端口、SNI、Host、Path、客户端看到的节点名)。**一个入站可以有多个 host**,这是同一台机器给出多个"节点"的方式 |

用户授权到**入站**粒度(`--inbounds` 留空表示全部)。订阅渲染时,每个用户拿到的是
「他被授权的入站 × 这些入站的对外入口」展开出来的端点列表。

节点名(host 的 `remark`)与地址支持 `{SERVER_IP}`、`{NODE_NAME}`、`{PROTOCOL}`、`{TRANSPORT}`
变量;地址里写 `*` 会按用户派生出一个稳定的随机子域(同一个人每次更新订阅看到的是同一个
地址,换一个人就不同)。`{USERNAME}` 只在订阅名里生效,在节点名里被抹成空串 —— 节点名会
被截图转发,不该带账号名,见 [docs/open-questions.md](docs/open-questions.md) 第 3 节。

## 快速开始

### 1. 起控制面

本机试:

```bash
go build -o mpx ./cmd/mpx
./mpx serve --db ~/mpx.db      # 不设 token 就不校验鉴权,所以默认只监听回环
```

真正拿它服务人,就在控制面那台机器上装成 systemd 服务。**装和更新是同一条命令**:

```bash
# 开发机:编一个 linux 版,连同 deploy/ 一起传过去
GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/mpx ./cmd/mpx
scp -r /tmp/mpx deploy root@<控制面机器>:/tmp/

# 控制面机器
sudo /tmp/deploy/install.sh /tmp/mpx
```

它做的是:换二进制(先试跑,跑不起来就不换)→ 建 `mpx` 用户和目录 → 第一次生成管理
token → 生成到节点去的 SSH 密钥 → 写单元文件 → 重启并**确认它真的在跑**。已经存在的
token、数据库、密钥一律不动,所以重复跑就是更新。跑完会印出 token、公钥和接下来该敲的
命令。细节见[装到机器上](#装到机器上)。

### 2. 让 CLI 记住它

```bash
./mpx login --server http://127.0.0.1:8080 --token "$MPX_TOKEN"
./mpx login                 # 不带参数时显示当前生效的值(token 打码)
```

配置落在 `os.UserConfigDir()/mpx/config.json`,权限 0600。`--server` / `--token` /
环境变量 `MPX_SERVER` / `MPX_TOKEN` 的优先级都高于这个文件。

### 3. 加一台节点

节点上只要有 sshd 和 systemd —— 不用预先装 xray,也不用能连 GitHub:

```bash
./mpx node add hk --address 203.0.113.10
./mpx node install 1                       # 装 xray、写单元、下发配置、启动
./mpx node config 1 > /tmp/preview.json    # 只是打印,不碰节点
./mpx node sync 1                          # 只重新下发配置
```

`node install` 一步到能用:发布包由控制面下载一次缓存在自己那边,再把核心和 geodata
推过去;换核心也走这条命令(默认最新版,`--xray-version` 指定)。推之前先和节点对一次
sha256,**已经一样的文件不重推** —— 换一版核心通常只动其中一个,重复跑一遍基本不用等传输。

两条命令都带兜底。`node sync` 先把节点上的原配置备份成 `.bak`,新配置用 `xray -test`
校验,**不过就自动回滚**;`node install` 换核心之前先拿节点上**现有的**配置试跑新核心,
通不过就不换。结构改动(加入站、改端口、换证书)之后要再跑一次 `node sync`,这些字段
xray 只能重启才生效;但**加人、删人不需要** —— 见第 4 步。

### 4. 加入站与对外入口

```bash
# 一个 VLESS + TCP + REALITY 入站
./mpx inbound add --node 1 --protocol vless --port 443 \
  --network tcp --security reality \
  --sni www.microsoft.com --reality-dest www.microsoft.com:443

# 一个 VLESS + WS + TLS 入站
./mpx inbound add --node 1 --protocol vless --port 8443 \
  --network ws --security tls --ws-path /ws \
  --sni node.example.com --cert-file /etc/ssl/node.crt --key-file /etc/ssl/node.key

# 给它加一个对外的名字
./mpx host add --inbound 2 --remark "香港 {TRANSPORT}"

./mpx inbound list
./mpx host list --inbound 2
```

改完结构要按第 3 步再跑一次 `./mpx node sync <id>`。**加人/删人不需要**:那是走
xray API 增量做的,节点上其他人的连接不会断。

### 5. 加用户并拿订阅

```bash
./mpx user add alice --limit 100GB --expire 2026-12-31 --reset-strategy monthly
./mpx user add bob   --inbounds 2,3          # 只给这两个入站的权限
./mpx user enable alice
./mpx user links alice
```

`user links` 给出六个地址:

```
auto          http://127.0.0.1:8080/sub/<token>          按 User-Agent 自动选格式
links         .../sub/<token>/links                       分享链接(vless:// 等)
base64-links  .../sub/<token>/base64-links                base64 过的分享链接
clash         .../sub/<token>/clash
clash-meta    .../sub/<token>/clash-meta
sing-box      .../sub/<token>/sing-box
xray          .../sub/<token>/xray
```

对外的订阅地址由设置里的 `sub_url_prefix` 决定;没设就用请求里的 `Host`。正式对外
必须设它,否则复制出去的链接会指向客户端自己。

```bash
echo '{"sub_url_prefix":"https://sub.example.com","profile_title":"我的机场","update_interval":12}' \
  | ./mpx settings set -f -
```

### 6. 按 User-Agent 分格式

订阅规则是「正则 + 返回格式」的有序列表,第一条命中的生效。客户端不认路径参数的
时候全靠它:

```bash
echo '{
  "sub_url_prefix": "https://sub.example.com",
  "rules": [
    {"pattern": "(?i)clash",    "result": "clash-meta"},
    {"pattern": "(?i)sing-box", "result": "sing-box"},
    {"pattern": "(?i)xray",     "result": "xray"}
  ]
}' | ./mpx settings set -f -
```

`result` 可选 `links` / `base64-links` / `clash` / `clash-meta` / `sing-box` / `xray` / `block`。
`block` 是不给出订阅(返回 404),用来拒绝某些客户端。路径里的格式(`/sub/<token>/clash`)
优先级高于规则;**没命中任何规则时返回分享链接**。

### 7. 看控制面现在怎么样

```bash
./mpx status
```

打印控制面启动多久了、后台周期多长、上一轮是什么时候跑的、走了多久、一共跑了几轮,五类对象
各有多少,以及正在退避的节点(连续失败几次、什么时候再试)。数据来自控制面内存里的心跳,
**不发 ssh**,所以节点卡住时它照样立刻回答;同样的数据在 `GET /api/status`。

一轮的耗时超过周期时,控制面会在日志里打一条 Warn —— 那是「循环开始落后」的早期信号:这台
机器上节点太多或者有机器在慢慢超时,`mpx status` 里的「上一轮耗时」会一直贴着周期。

### 8. 节点状态与自检

`node list` 的后五列来自**最近一次检查的缓存**,不发 ssh,所以它可以随手敲:

```bash
./mpx node list
```

```
ID  名称   地址              …  状态    版本      漂移    最后检查   证书最短
1   香港   203.0.113.46     …  active  26.3.27   否      2 分前     88 天
2   美国   203.0.113.45   …  未知    -         未下发  从未       -
```

- **状态**:`active` / `inactive` 是服务在不在跑,`不可达` 是 ssh 没答,`未知` 是**还没查过**;
- **漂移**:节点上那份配置的哈希和「我最后一次成功下发的」对不对得上。`未下发` 表示没有基准
  (从没推过配置),`未知` 表示问不到 —— 这两个都不是「没问题」;
- **证书最短**:那台节点上所有证书里剩余天数最少的一张;读不到到期时间(文件不是证书、节点上
  没装 `openssl`)时显示「读不到」。

要看**此刻**的情况,让它现场查一次:

```bash
./mpx node status            # 全部节点
./mpx node status 香港        # 一台
```

一条命令体检查完:

```bash
./mpx doctor
```

```
名称        结论  说明
数据库      ok    能读能写
后台任务    ok    60 秒前跑过,用了 1.2 秒
节点 香港   ok    可达,服务在跑(v26.3.27),配置没有漂移
节点 美国   fail  连不上:ssh: connect to host … → 确认地址、ssh 端口和控制面的私钥
订阅 links  ok    渲染出 1843 字节
…           …     六种格式各一行 …(省略 5 行)
流量采样    ok    128 行,最近一条在 2026-10-08T14:52:00+08:00(1 分前)

11 项检查,1 项失败,0 项警告 —— 失败的项要看。
```

提示(hint)紧跟在问题后面显示在同一列里;一台节点只占一行,好几个毛病时按最重的定结论、
其余写进说明。

**有 fail 时退出码是 3**(不是 1),可以直接当健康检查用:`1` 也可能是「连不上控制面」
这种重试一下就好的情况,`3` 才是「查成了,系统有毛病」。只有警告时退出码是 0。

## 配额、到期与流量

用户状态按这个优先级判定:**显式停用 > 已过期 > 超流量 > 可用**。

- 流量统计走节点上 xray 的 stats 接口,控制面每 30 秒读一次**累计值**,和上一次采样算差后累加;
  节点重启过导致计数回退的那一轮不累加(只记一条日志)。所以控制面重启或漏采几次都不会丢账 ——
  下次读到的是从上次基准往后的总量,跳过几轮只是差大一点。一台不通的节点在退避窗口里被整轮跳过,
  也是同一个道理。
- 超限 / 到期的用户会在下一轮收敛里被从节点上摘掉;续费或重置后自动加回去。
- 周期重置策略 `none` / `daily` / `weekly` / `monthly`,按上一次重置时刻滚动推进。
- `mpx user reset-traffic <name>` 手动归零。
- `mpx user revoke-sub <name>` 只换订阅 token,**不动协议凭证** —— 用户在所有协议上的
  配置不变,只是旧订阅地址失效。订阅链接比协议凭证更容易泄露(截图、转发),分开存
  就是为了能单独换。

## 装到机器上

### 控制面

「快速开始」第 1 步里的 `deploy/install.sh` 就是全部,重复跑就是更新。它做的是:

- **先试跑再替换**:二进制先落到 `/usr/local/bin/mpx.new` 跑一次 `--help`,不通过就删掉
  并保留原来那份 —— 更新时换上一个交叉编译错了架构的二进制,这台机器就没有控制面了;
- 建 `mpx` 用户、`/var/lib/mpx`、`/etc/mpx`。家目录必须是 `/var/lib/mpx`,控制面要去
  那里找自己的 `.ssh`;
- `/etc/mpx/env` 不存在才生成 `MPX_TOKEN`,已存在就不动 —— 换了 token,所有登录过的
  CLI 都要重登;
- 生成到节点去的密钥 `/var/lib/mpx/.ssh/id_ed25519`;
- 写 `/etc/systemd/system/mpx.service`(和手上这份不同就先备份成 `.bak`),重启,再
  **确认它真的在跑**;起不来就打印状态和最近 20 行日志,以非零退出。

不想用脚本的话,`deploy/install.sh` 本身就是那几步 shell,照着敲一遍也行。

控制面只要一个能跑二进制的 Linux 机器和一个 SQLite 文件。`ExecStart` 绑 `127.0.0.1`,
对外交给反向代理;`ProtectSystem=strict` + `ReadWritePaths=/var/lib/mpx` 把写权限限在
库目录里 —— 控制面下载的 xray 缓存也放在那儿(`/var/lib/mpx/xray`),因为那是它唯一
能写的地方。

### 反向代理与证书

控制面同时提供两个前缀,反代时都要转发:`/api/`(管理,带 Bearer token)和
`/sub/`(订阅,无鉴权,token 在路径里)。

nginx:

```nginx
server {
    listen 443 ssl http2;
    server_name sub.example.com;

    ssl_certificate     /etc/letsencrypt/live/sub.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/sub.example.com/privkey.pem;

    # 订阅是纯文本/JSON,不用压也小;管理接口的请求体也很小
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

节点自己那侧的 TLS 证书要放在**节点机器上**,路径写进入站的 `--cert-file` /
`--key-file`。多个子域建议用泛域名证书(地址里写了 `*` 就需要)。

### 节点

节点上不需要装任何 mpx 组件 —— 下发走系统自带的 `ssh`,准备工作都在控制面那边:

- `BatchMode=yes`,需要密码时**直接失败**而不是挂在等输入的提示上,所以
  **必须先配好免密**(公钥认证),控制面才能下发;
- 读的是 `mpx` 用户的 `~/.ssh/config`(即 `/var/lib/mpx/.ssh/config`),跳板机、
  ssh-agent、`known_hosts` 那一套同样生效;
- 用的用户是 `--ssh-user`(默认 `root`),要能写 `config.json` 并 `systemctl` 管服务。

mpx 在节点上占的都是自己的一份,和机器上可能已经有的 xray 不打架(同机跑着 Marzban
也没关系):

| | mpx 用的 | 别人常装的 |
| --- | --- | --- |
| 核心 | `/usr/local/lib/mpx/xray` | `/usr/local/bin/xray` |
| geodata | 与核心同目录(xray 只去二进制同目录找) | `/usr/local/share/xray/` |
| 配置 | `/etc/mpx/xray/config.json` | `/usr/local/etc/xray/config.json` |
| 服务名 | `mpx-xray` | `xray` |

默认值都在 `mpx node add` 的 `--help` 里,也可以逐个指定。

### 接上一台已经在跑的机器

```bash
# 控制面机器:把这行公钥加到节点上对应 ssh 用户的 ~/.ssh/authorized_keys
sudo cat /var/lib/mpx/.ssh/id_ed25519.pub

# 操作者本机
./mpx node add hk --address 203.0.113.10                   # 其余留空走默认值
./mpx node add lab --address 10.0.0.9 --ssh-port 5522 --ssh-user ubuntu
./mpx node install 1
```

`node install` 装的是 mpx 自己那份 xray,不碰节点上已有的那一份。`--config-path` /
`--service-name` 只决定配置写到哪、重启哪个服务;核心始终是 `/usr/local/lib/mpx/xray`。

**今天做不到的**:经跳板机连节点。`--address` 一个字段同时在当「ssh 往哪连」和「订阅里
发给客户端的地址」,跳到别人的机器上时这两件事不是同一个值。

## 安全

- `MPX_TOKEN` 拿到就能改所有节点。为空时**完全不校验鉴权**,所以
  `mpx serve` 在 token 为空时拒绝绑非回环地址(除非你显式知道自己在做什么)。
- `/sub/<token>` 是唯一的用户凭证,拿到订阅地址就等于拿到节点。泄露了就
  `user revoke-sub`。
- 渲染出的节点配置默认把 `geoip:private` 送进 `block`:挡住用户借节点打节点自己的
  内网。这是有意的,不要为了让某个本地测试通过而删掉它。
- 数据库里有所有用户的协议凭证(由用户 key 派生),`mpx.db` 按密钥对待。

## 备份与恢复

库一没,所有用户的协议凭证(由 `users.key` 派生)和节点凭据都复现不出来,节点也连不上。`mpx backup` / `mpx restore` 就是为这个准备的:

```bash
# 从控制面下载一份归档,默认 mpx-backup-<时间戳>.tar.gz,写完权限 0600
./mpx backup
./mpx backup -o /tmp/mpx-backup-$(date +%F).tar.gz

# 把一份归档暂存到控制面,校验通过后返回「已就绪,重启 mpx 后生效」
./mpx restore /tmp/mpx-backup-....tar.gz
sudo systemctl restart mpx
```

归档是 gzip 过的 tar,里面有:

- `manifest.json` —— 创建时间、控制面主机名、各表行数、是否含 ssh 私钥、表名清单(不含任何密钥正文);
- `mpx.db` —— 一致快照(用户、节点、入站、流量历史都在,`users.key` 也在这里);
- `id_ed25519` —— 控制面连节点用的系统 ssh 私钥(存在才放)。

**没有管理 token** —— token 在 `/etc/mpx/env`,不在库里。这份归档等于一整套凭据,**当密码文件保管**:不传不加密的通道、不随手拷来拷去。

恢复是「校验 + 暂存」:控制面只把校验过的库写成 `mpx.db.restore`,**下次启动时换入**,旧库改名 `mpx.db.pre-restore-<时间戳>` 留着回退。所以:

- 恢复后**要重新 `mpx login`**:管理 token 不在归档里,若恢复到一台 token 不同的机器,用那台机器 `/etc/mpx/env` 里的 token 重新登录;
- 归档里的 ssh 私钥**不会自动放回去**(恢复只换数据库):新机器上的私钥和节点 `authorized_keys` 对不上时,要把新公钥加到每台节点重新授权,否则控制面连不上节点;
- 没有控制面也能恢复:把 `mpx.db` 改名成 `mpx.db.restore` 再启动,同一套换入逻辑生效。

## 开发

```bash
go build ./...
go vet ./...
go test ./... -count=1 -race
gofmt -l .
```

有一部分测试要**真实的 xray 二进制**才跑,没装就自动跳过:

```bash
# 从 https://github.com/XTLS/Xray-core/releases 取 Xray-<平台>.zip,解出 xray
export MPX_XRAY_BIN=/path/to/xray
go test ./... -count=1
```

它们做的是单测做不到的事:把控制面渲染出的**服务端配置**和**各格式的客户端配置**
交给真实 xray 做 `-test` 校验,并起一个真节点验证 `xray api` 的增删用户与流量统计
契约(实测值:`adu` 打印 `Added 1 user(s)`、`rmu` 打印 `Removed 1 user(s)`、`statsquery
-reset` 读走即清零)。字段名写错、某个键在这个 xray 版本上改了名,只有这些测试能发现。

### 目录

```
cmd/mpx/              入口
internal/model/       领域模型、密钥派生、重置周期(纯计算)
internal/store/       SQLite 读写
internal/render/      渲染:服务端配置 + links/clash/sing-box/xray 客户端格式
internal/xrayrelease/ 取 Xray 发布包(下载一次、缓存在控制面)
internal/node/        对一台节点的动作:ssh、装核心、下发回滚、xray api
internal/control/     「让节点和数据库一致」的那一层 + 后台周期任务
internal/server/      HTTP:管理接口与订阅接口
internal/cli/         CLI 客户端
deploy/               控制面的安装脚本、systemd 单元、环境变量样例
```
