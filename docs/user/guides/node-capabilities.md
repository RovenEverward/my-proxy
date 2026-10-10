# 检测节点出口与流媒体能力

用 `mpx node probe` 检查指定入站和入口的出口 IP、地区、YouTube 连通性及 Netflix 片库访问结果。检测结果属于这条代理入口；同一节点上的其他入站、直连入口或 CDN 入口需要分别检测。

以下示例使用节点 `1`、入站 `2` 和入口 `3`。请先登录控制面，再把 ID 换成自己的值。

## 选择入口并手动检测

查看入站和入口 ID：

```bash
mpx inbound list --node 1
mpx host list --inbound 2
```

已有显式入口时，必须用 `--host` 指定其中一个：

```bash
mpx node probe 1 --inbound 2 --host 3 --targets egress,youtube,netflix
```

如果入站没有显式入口，可省略 `--host`，使用节点地址和入站端口的默认入口。入口必须属于指定入站，入站必须属于指定节点。

`--targets` 可选 `egress`、`youtube`、`netflix`，用逗号分隔，省略时检测全部三项。手动检测会保存结果。同一入口的两次检测至少间隔 30 秒。

包含 `*` 或 `{USERNAME}` 的用户专属动态地址、HTTP Host 或 TLS SNI 无法共享检测证据，请先配置固定入口；此类入口不会进入自动能力候选。

检测需要节点正常运行，并且至少有一个当前有效、获授权使用该入站的用户。节点处于 drain 状态或没有这样的用户时，结果为 `unknown`，原因会说明无法检测的条件。

## 查看结果

```bash
mpx node capabilities 1
mpx node capabilities 1 --output json
```

表格显示检测配置以及各目标的稳定状态、原始观测、地区、出口 IP、检查时间、到期时间、检测位置、检测器和原因。`mpx node list` 与 `mpx node show 1` 也会显示入口能力摘要。

| 状态 | 含义 |
| --- | --- |
| `available` | 连续两次一致的有效观测确认可用 |
| `unavailable` | 连续两次一致的有效观测确认不可用 |
| `unknown` | 证据不足、检测失败、结果过期或入口条件已变化 |

稳定状态需要连续两次一致的观测。第一次看到 `available` 原始观测时，稳定状态仍可能是 `unknown`；等待至少 30 秒再检测一次，或等待下一轮定时检测。地区变化也需要重新确认。

各目标的判断范围不同：

- `egress`：报告这条入口实际使用的出口 IP 与 IP 地理信息服务返回的地区。
- `youtube`：确认 YouTube 连通性。结果不保证 Premium 订阅、视频播放或特定地区内容可用。
- `netflix`：确认指定授权片目与原创片目的页面证据及地区。仅能访问原创内容会显示在原因中；结果不保证账号、播放或整个片库可用。

结果过期，节点、入站或入口配置改变，或者入站不再有有效授权用户时，读取结果会显示 `unknown`。原始观测保留供排查；重新检测可更新当前证据。

## 开启或停止定时检测

为同一入口保存每小时一次、有效期三小时的检测配置：

```bash
mpx node capability-set 1 --inbound 2 --host 3 \
  --targets egress,youtube,netflix --enabled --interval 1h --ttl 3h
```

`capability-set` 保存配置，后续由定时任务检测；手动 `probe` 不会自动启用定时检测。修改配置会清除这条入口的旧结果，重新积累观测。

停止该入口的定时检测：

```bash
mpx node capability-set 1 --inbound 2 --host 3 --enabled=false
```

`capability-set` 每次保存整份配置；省略的选项使用下表默认值。如果需要保留自定义目标或时长，请在修改时一并传入。

| 选项 | 默认值 | 范围 |
| --- | --- | --- |
| `--enabled` | `false` | 加上 `--enabled` 开启；`--enabled=false` 停止 |
| `--targets` | `egress,youtube,netflix` | 三个支持目标的非重复组合 |
| `--interval` | `1h` | `5m` 到 `24h` |
| `--ttl` | `3h` | 不小于检测间隔的两倍，最多 `168h` |

时长可使用 `5m`、`1h` 等 Go duration 格式，必须为整秒。三个命令都支持 `--output json`，详细参数见 `mpx node probe --help`、`mpx node capabilities --help` 和 `mpx node capability-set --help`。

## 用检测结果筛选用途订阅

先为各候选入站和入口开启检测，再将检测要求加入已有用途：

在完整用途 JSON 中合并以下字段（其他标签、策略和域名字段保留）：

```json
{"required_capabilities":["netflix","egress"],"capability_region":"US"}
```

保存完整配置后应用并预览：

```bash
mpx purpose set media -f media.json
mpx purpose preview media alice clash-meta
```

请把用途名、用户和地区换成实际配置。只有已获该用户授权、未 drain、符合手动标签且每项检测均为有效 `available` 的入口会进入候选；未知、不可用和过期结果会退出，恢复稳定可用后重新进入。地区条件来自 Netflix 片库地区或出口 IP 地理信息，单独要求 YouTube 连通性时不能判断地区。候选为空会返回明确错误，并保留普通订阅作为回退入口。

用途订阅读取当前证据，不会启动检测，也不会沿用已经过期的缓存候选。客户端已下载的配置仍需更新订阅才能得到候选变化。

## 运行要求与结果展示

节点需要已安装的 Xray 和 `curl`。控制面经 SSH 启动临时客户端，使用当前获授权用户的独立凭证连接指定入口，再通过本地 SOCKS5 发起所有检测请求；目标 DNS 也交给代理解析。客户端配置只写入权限为 `600` 的临时文件，结束、超时和断连时清理。检测失败不会改用机器的直连出口。检测流量使用该用户凭证，会计入该用户配额；每次响应有大小上限，建议保持低频检测。

检测任务有独立调度，与流量采样和用户收敛分开；全控制面最多一个检测运行，每个远程任务最多约 65 秒，只保存每个入口、每个目标的最新结果。读取结果不发网络请求。公网目标为 `api.ip.sb/geoip`、YouTube `generate_204` 与 Netflix 两个固定片目页面；目标服务变更、限流、网络失败或解析失败都会显示未知并保留原因。

订阅页面展示当前用户有权限使用的入口结果、出口、地区、检测时间与位置。已配置 webhook 时，稳定状态改变发送 `capability_changed` 通知。启用或禁用定时检测时，正在执行的旧任务不会重新写回已清除的证据。
