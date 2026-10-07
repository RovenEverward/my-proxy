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
地址,换一个人就不同)。`{USERNAME}` 只在订阅名里生效,在节点名里不生效 —— 见
[docs/open-questions.md](docs/open-questions.md) 第 4 节。

## 快速开始

### 1. 起控制面

```bash
go build -o mpx ./cmd/mpx

# 本机开发:不设 token 就不校验鉴权,所以默认只监听回环
./mpx serve --db ~/mpx.db

# 对外提供服务:必须给 token,否则任何人都能加节点
export MPX_TOKEN="$(openssl rand -base64 32)"
./mpx serve --addr 127.0.0.1:8080 --db /var/lib/mpx/mpx.db
```

### 2. 让 CLI 记住它

```bash
./mpx login --server http://127.0.0.1:8080 --token "$MPX_TOKEN"
./mpx login                 # 不带参数时显示当前生效的值(token 打码)
```

配置落在 `os.UserConfigDir()/mpx/config.json`,权限 0600。`--server` / `--token` /
环境变量 `MPX_SERVER` / `MPX_TOKEN` 的优先级都高于这个文件。

### 3. 加一台节点

节点上需要:xray 已装好(systemd 服务名默认 `xray`)、能 ssh 上去、`config.json`
默认在 `/usr/local/etc/xray/config.json`。

```bash
./mpx node add hk --address 203.0.113.10
./mpx node show 1
./mpx node config 1 > /tmp/preview.json   # 只是打印,不碰节点
./mpx node sync 1                          # 真下发:备份 → 校验 → 回滚兜底 → 重启
```

`node sync` 会先在节点上把原配置备份成 `.bak`,把新配置推过去用 `xray -test` 校验,
**校验不过就自动回滚**,不会把一个起不来的 xray 留在节点上。

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

结构性改动(新增入站、改端口、换证书)之后要再跑一次 `./mpx node sync <id>` ——
这些字段 xray 只能重启才生效。**加人/删人不需要**:那是走 xray API 增量做的,节点上
其他人的连接不会断。

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

## 配额、到期与流量

用户状态按这个优先级判定:**显式停用 > 已过期 > 超流量 > 可用**。

- 流量统计走节点上 xray 的 stats 接口,控制面每 30 秒采一次增量累加,**读走即清零**,
  所以控制面重启或漏采一次不会让计数失真。
- 超限 / 到期的用户会在下一轮收敛里被从节点上摘掉;续费或重置后自动加回去。
- 周期重置策略 `none` / `daily` / `weekly` / `monthly`,按上一次重置时刻滚动推进。
- `mpx user reset-traffic <name>` 手动归零。
- `mpx user revoke-sub <name>` 只换订阅 token,**不动协议凭证** —— 用户在所有协议上的
  配置不变,只是旧订阅地址失效。订阅链接比协议凭证更容易泄露(截图、转发),分开存
  就是为了能单独换。

## 部署

控制面只需要一个能跑二进制的 Linux 机器和一个 SQLite 文件。

```bash
install -m 0755 mpx /usr/local/bin/mpx
useradd --system --home /var/lib/mpx --shell /usr/sbin/nologin mpx
install -d -o mpx -g mpx -m 0750 /var/lib/mpx /etc/mpx

install -m 0644 deploy/mpx.service      /etc/systemd/system/mpx.service
install -m 0600 -o mpx -g mpx deploy/mpx.env.example /etc/mpx/env
$EDITOR /etc/mpx/env        # 填一个 openssl rand -base64 32 出来的 token

systemctl daemon-reload
systemctl enable --now mpx
systemctl status mpx
```

`mpx.service` 里 `ProtectSystem=strict` + `ReadWritePaths=/var/lib/mpx`,把控制面的写权限
限在库目录里。`ExecStart` 绑 `127.0.0.1`,对外交给反向代理。

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

### 节点侧

不需要装任何 mpx 组件。控制面用的是系统自带的 `ssh`:

- 连接参数走 `~/.ssh/config`、跳板机、ssh-agent —— 你机器上已经配好的那套直接生效;
- `BatchMode=yes`,需要密码时**直接失败**而不是挂在等输入的提示上,所以
  **必须先配好免密**(公钥认证),控制面才能下发;
- 用的用户是 `--ssh-user`(默认 `root`),要能写 `config.json` 并 `systemctl` 管 xray。

## 安全

- `MPX_TOKEN` 拿到就能改所有节点。为空时**完全不校验鉴权**,所以
  `mpx serve` 在 token 为空时拒绝绑非回环地址(除非你显式知道自己在做什么)。
- `/sub/<token>` 是唯一的用户凭证,拿到订阅地址就等于拿到节点。泄露了就
  `user revoke-sub`。
- 渲染出的节点配置默认把 `geoip:private` 送进 `block`:挡住用户借节点打节点自己的
  内网。这是有意的,不要为了让某个本地测试通过而删掉它。
- 数据库里有所有用户的协议凭证(由用户 key 派生),`mpx.db` 按密钥对待。

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
cmd/mpx/            入口
internal/model/     领域模型、密钥派生、重置周期(纯计算)
internal/store/     SQLite 读写
internal/render/    渲染:服务端配置 + links/clash/sing-box/xray 客户端格式
internal/node/      对一台节点的动作:ssh、下发回滚、xray api
internal/control/   「让节点和数据库一致」的那一层 + 后台周期任务
internal/server/    HTTP:管理接口与订阅接口
internal/cli/       CLI 客户端
```
