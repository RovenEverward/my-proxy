# mpx 是怎么搭起来的

本机一个入口,管若干台服务器上的 xray,并对外托管订阅。

[设计](../design.md)回答「它为什么要有、基本形态是什么、哪些事不做」;这一份回答「它实际怎么跑」——一条命令走下去会看到什么、每 30 秒后台在做什么、订阅是怎么发出去的。其余几份各回答一个问题,见[文末](#其他文档)。安装与日常操作看[用户文档站](https://RovenEverward.github.io/my-proxy/)。

## 一条命令装完

```console
$ mpx init --host 203.0.113.10 --ssh-password
==> 看清这台机器
==> 在这台机器上装控制面(监听 0.0.0.0:8080)
...
==> 建节点
    节点 default 建好了(id=1)
...
搞定。把下面这条填进客户端(clash / sing-box / v2rayN 都认它):

  http://203.0.113.10:8080/sub/xxxxxxxx
```

它替用户跑完了下面这一整节的手工步骤。重复跑是安全的:已有的对象会被复用,不会出现第二份。

## 一次改动,从头到尾

真机上跑一遍(两台服务器,三个入站):

```console
$ mpx node add hk-1 --address 203.0.113.10
已创建节点 1(hk-1)

$ mpx node install 1
已在节点 1 上装好并启动 xray 26.3.27

$ mpx inbound add --node 1 --protocol vless --port 443 --security reality \
    --sni www.cloudflare.com --reality-dest www.cloudflare.com:443 \
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
        "realitySettings": { "dest": "www.cloudflare.com:443",
          "serverNames": ["www.cloudflare.com"],
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

## 其他文档

「不做什么」、三层各管一段、以及每一条取舍的理由,在[设计](../design.md)与[关键决策与理由](decisions.md)里,不在这里重复。

管理界面 `mpx ui` 的方案与安全设计见 [管理 UI 方案](ui-rfc.md)。自动升级与发布件校验见 [自动升级方案](update-rfc.md)。分流规则(替代用途)见 [分流规则方案](routing-rules-rfc.md)。套餐(权限组)见 [套餐方案](plans-rfc.md)。添加节点与首次安装两个向导见 [向导方案](wizard-rfc.md)。

### 用户帮助与运维指南
- [用户文档站](https://RovenEverward.github.io/my-proxy/)：教程、指南、参考与原理。

### 内部工程与决策文档
| 文档 | 回答什么 |
| --- | --- |
| [model.md](model.md) | 数据长什么样、状态怎么判 |
| [contracts.md](contracts.md) | 对外契约的确切形状 |
| [decisions.md](decisions.md) | 为什么这么选,换掉会怎样 |
| [status.md](status.md) | 做到哪、验到什么程度 |
| [roadmap.md](roadmap.md) | 分批演进计划,从用量与计量做起 |
| [open-questions.md](open-questions.md) | 还有哪些缺口、下一步做什么 |
| [QUIC 运行验证](quic-validation.md) | 官方构建、真实 SSH 与分用户流量证据及限制 |
