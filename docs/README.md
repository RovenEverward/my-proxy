# mpx 是怎么搭起来的

本机一个入口,管若干台服务器上的 xray,并对外托管订阅。这一份自足:看完它,整体架构就清楚了。其余几份各回答一个问题,见[文末](#其他文档)。安装与部署看[仓库 README](../README.md)。

## 先说要解决什么

一台服务器跑 xray,手工维护还行:改 `config.json`、重启、给人发一条链接。

机器变成几台、人变成几百个,手工就要开始出错:

- 谁该在哪些节点上?他到期了吗?这个月用超了吗?
- 他手上那份订阅,此刻应该长什么样?
- 加一个人的时候,怎么只动他一个,不把别人的连接碰断?

这些答案一旦散在每台机器的配置文件里,就没人能一眼看全。所以要有一个地方存**唯一一份真相**,由它算出「每个节点上该是什么」「每个人该拿到什么」,再送到该去的地方。

mpx 就是这个东西:一个控制面,加一个在本机敲的命令行。

## 为什么不像 Marzban 那样做

Marzban 和 Marzneshin 解决同一类问题,但它们在每台节点上跑一个常驻组件,控制面通过它下命令。这里换了个做法:**节点上什么都不装**,只用系统自带的 `ssh` 和 xray 自带的 API。

代价是拿不到节点的结构化状态,要问就得 `ssh` 上去执行命令;改结构必须重启。换来的是节点零维护——不用升级组件、不用守护它的进程、不用给它签证书。机器少、维护的人只有一个时,这样更省事。

控制面只在两件事上碰节点:改**结构**(下发配置、重启)和收**流量**(读计数)。所以控制面挂了不影响已连上的用户,它的开销也不随流量涨。

## 三层,各管一段

形状图在[仓库 README](../README.md#它管什么),不重画。这里只补一件那边没有的事:**每一层不做什么**。

| | 做什么 | 不做什么 |
| --- | --- | --- |
| **CLI** | 把参数拼成请求,把结果显示成人能看的 | 不碰数据库,不碰节点。它是 `/api` 的纯客户端 —— 每条命令都能用 `curl` 等价做出来 |
| **控制面** | 持有唯一一份真相;渲染节点配置与订阅;决定谁该在哪个节点上 | **不进数据通路**,用户流量直接打到节点上 |
| **节点** | 跑控制面推过去的 xray;按用户计量流量 | 不装任何 mpx 组件,不主动联系控制面,也不需要能连外网 |

## 一次改动,从头到尾

真机上跑一遍(两台服务器,三个入站):

```console
$ mpx node add hk-1 --address 203.0.113.10
已创建节点 1(hk-1)

$ mpx node install 1
已在节点 1 上装好并启动 xray 26.3.27

$ mpx inbound add --node 1 --protocol vless --port 443 --security reality \
    --sni www.microsoft.com --reality-dest www.microsoft.com:443 \
    --short-ids d4834461 --flow xtls-rprx-vision
已创建入站 1(vless-443)

$ mpx host add --inbound 1 --remark "香港 REALITY {PROTOCOL}"
已创建对外入口 1

$ mpx user add alice --limit 100GB --reset-strategy monthly --note "老朋友"
已创建用户 alice
```

`node add`、`inbound add`、`host add`、`user add` 这四条只写控制面的库,**节点上一个字节都没动** —— 真正碰节点的是上面那条 `node install`(装核心、写单元文件、下发、启动),以及之后每次改结构要跑的 `node sync`。

`mpx node config 1` 输出**下发给节点的那份文件**(节选):

```json
{
  "api":  { "tag": "api", "services": ["HandlerService", "StatsService"] },
  "stats": {},
  "policy": { "levels": { "0": { "statsUserUplink": true, "statsUserDownlink": true } } },
  "inbounds": [
    { "tag": "api", "listen": "127.0.0.1", "port": 10085,
      "protocol": "dokodemo-door", "settings": { "address": "127.0.0.1" } },
    { "tag": "vless-443", "listen": "0.0.0.0", "port": 443, "protocol": "vless",
      "settings": {
        "decryption": "none",
        "clients": [{ "id": "f72f49e8-d77e-4632-be66-e2812f428827",
                      "email": "alice", "flow": "xtls-rprx-vision" }] },
      "streamSettings": {
        "network": "tcp", "security": "reality",
        "realitySettings": { "dest": "www.microsoft.com:443",
          "serverNames": ["www.microsoft.com"],
          "privateKey": "ErYtiruvvq_nEkIZ1GUdYKfChwhzOI_ffeD126QYn80",
          "shortIds": ["d4834461"] } },
      "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"],
                    "routeOnly": true } }
  ]
}
```

三处值得留意:

- `api` 入站只监听 `127.0.0.1`。控制面靠它增删用户 —— 这是「不重启」的基础。
- `clients[].email` 就是**用户名**。节点按 email 分账,读回来才对得上人。
- `policy.levels.0` 打开逐用户统计;不打开,`statsquery` 只给总数。

把它送到节点上:

```console
$ mpx node sync 1
```

顺序是:备份现有文件 → 写入新的 → `xray -test -config <路径>` 验一遍 → **验不过就回滚** → 重启服务 → **没起来就再回滚一次**。因为最后一步是重启,`sync` 只用于改结构 —— 加人、摘人走的是另一条路(xray API),不重启。

## 之后每 30 秒发生什么

`mpx serve` 起一个后台任务,按 `--interval`(默认 30s)跑一轮,**三步的顺序是有意的**:

| 顺序 | 做什么 | 为什么在这个位置 |
| --- | --- | --- |
| 1. 采样 | 逐节点读 `xray api statsquery` 的累计计数,减去上次值后加进 `users.used_traffic` | 先收数,这一轮才判得出「刚超限的人」 |
| 2. 重置 | 到点的用户本周期用量归零 | 放采样之后,否则刚过去的用量会被算进新周期 |
| 3. 收敛 | 逐节点比对「节点上现在有谁」与「按当前状态该有谁」,该加的加、该摘的摘 | 用采样并重置后的状态算,一轮就对外生效 |

采样读的是**累计值**,不再 `-reset` 清零:每次读数追加进 `traffic_samples`,下次采样拿该用户在该节点上的最近一行当基准相减。少采一轮只是把增量并到下一次,不会丢;读数变小说明节点上的 xray 重启过,会记一条异常日志(见 [decisions.md](decisions.md) 第 9 条)。

第 3 步只调 `xray api adu` / `rmu`(增删用户),**不重启**。配额恢复和到期续费都会导致加人,要是每次都重启,节点上所有人的连接都会跟着断一次。

**每 10 轮**(约 5 分钟)还顺路做一件不在这三步里的事:巡检。它用**一条** ssh 从每台节点上读回四样东西 —— 服务在不在跑、核心版本、配置的 sha256、每张证书还有几天到期 —— 落进 `node_status` 表,和「最后一次成功下发时记下的哈希」一比就知道配置有没有被人动过。这件事**第 1 轮就跑**(最需要答案的时候是刚起来的时候),整轮有 45 秒预算,用完了剩下的留到下一轮并记一条日志 —— 它是顺路做的,不能反过来压住 30 秒的主线。条件出现时报日志告警,同一个问题 30 分钟内只报一次,好了打一条「已恢复」。

于是这些情况是这样表现的:

| 情况 | 用户拿到什么 | 节点上 |
| --- | --- | --- |
| 超限 / 到期 / 停用 | **占位节点**(名字写明是哪一种,地址 `0.0.0.0:1`) | 下一轮被 `rmu` 摘掉 |
| 续费 / `mpx user reset-traffic` | 恢复成真节点 | 下一轮被 `adu` 加回 |
| 刚加用户 | 立刻能拿到订阅 | 最多 30 秒后能连上 |
| 改端口 / 加入站 / 换证书 | 不影响手上这份订阅 | 要 `mpx node sync`,会重启 |
| 节点 SSH 连不上 | 不影响订阅 | 这一轮跳过,记一条日志 |
| 有人在节点上改了配置 | 不影响订阅 | 巡检发现哈希对不上,报漂移,`node sync` 推回 |

占位节点是「连不通、但各家客户端都肯接受」的一份配置。不可用的用户给真配置,只会让客户端连不上还不报错。它的名字按状态分开写(已停用 / 已过期 / 已超限)—— 客户端里用户只能看到这一句话,而这三件事他该做的动作不一样。

## 订阅是这样产出的

`GET /sub/{token}` 收三个输入:**用户的 token**、**路径里可有可无的格式**、**User-Agent**。认格式按顺序:

1. 路径里带了格式(`/sub/{token}/sing-box`)就用它;
2. 否则拿 User-Agent 逐条匹配订阅规则,取第一条命中的;
3. 都没命中 → 分享链接。

规则就是「一个正则 → 一个结果」,存库,`mpx settings set` 整体替换:

```console
$ mpx settings show
字段               值
订阅地址前缀         https://sub.example.com
订阅名              {USERNAME} 的订阅
支持链接            https://t.me/example
自动更新间隔(小时)   12

订阅规则:
序号  匹配结果         User-Agent 正则
1     clash-meta      (?i)clash
2     sing-box        (?i)sing-box
3     base64-links    .*
```

同一个地址,不同客户端拿到不同格式(实测):

| User-Agent | Content-Type | 正文开头 |
| --- | --- | --- |
| `clash-verge/v2.0` | `text/yaml` | `proxies:` … |
| `sing-box 1.10` | `application/json` | `{ "dns": …` |
| `v2rayN/6.0` | `text/plain` | `dmxlc3M6Ly8…`(base64) |

**一条都没命中时不报错**,退回分享链接;正则编译不过的规则跳过。一条坏规则不该让整个订阅不可用。

响应头带上客户端认的用量信息:

```
Content-Disposition: attachment; filename=alice
Profile-Title: base64:YWxpY2Ug55qE6K6i6ZiF
Profile-Update-Interval: 12
Subscription-Userinfo: upload=0; download=0; total=107374182400; expire=0
```

## 谁拥有什么

| 状态 | 拥有者 |
| --- | --- |
| 节点 / 入站 / 入口 / 用户 / 规则 | 控制面的 SQLite。唯一一份真相,CLI 不缓存,每次都问控制面 |
| 节点上的 `config.json` | 控制面渲染,节点执行。全量生成,不是手工维护的 |
| 用户在节点上的存在 | 控制面收敛出来的,不是手工加的 |
| 已用流量 | 节点计量,控制面落库 |

## 不做什么

| 不做 | 为什么 |
| --- | --- |
| 管理员与多租户 | 只有一个人操作。多管理员会带来权限模型、审计、越权测试一整条链 |
| Web UI | 入口是 CLI,UI 得跟着模型一起演进 |
| 自己维护 xray 构建 | 只用 XTLS 官方发布包原样推送。自己编就等于把上游的安全修复变成我们的事 |
| 证书签发与续期 | 假定证书已在节点上,库里只存路径。REALITY 公私钥由本工具生成 |
| 节点侧常驻 agent | 节点只用 SSH,不装任何 mpx 组件 |

## 其他文档

| 文档 | 回答什么 |
| --- | --- |
| [model.md](model.md) | 数据长什么样、状态怎么判 |
| [contracts.md](contracts.md) | 对外契约的确切形状 |
| [decisions.md](decisions.md) | 为什么这么选,换掉会怎样 |
| [status.md](status.md) | 做到哪、验到什么程度 |
| [roadmap.md](roadmap.md) | 分批演进计划,从用量与计量做起 |
| [open-questions.md](open-questions.md) | 还有哪些缺口、下一步做什么 |
