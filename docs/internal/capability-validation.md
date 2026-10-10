# 节点能力检测验证记录

当前检测器 `mpx-proxy-capability/2` 通过指定入站/入口启动临时 SOCKS 客户端，Xray 与 Hysteria2 / TUIC 分别选择对应核心。所有目标请求及目标 DNS 都经过该代理；临时配置包含当前有效授权用户的独立凭证，检测流量计入该用户配额。检测器版本属于证据指纹，升级后旧版本结果显示未知，需重新检测。以下 Xray 公网结果记录于 v1，保留当时的运行边界。

## 真实运行证据

使用官方 Xray `26.3.27`（提交 `d2758a0`，darwin/arm64）及真实 VLESS 服务端验证：

- 本地 HTTP 测试目标域名只能通过所选 VLESS 出口解析和访问；错误 UUID 时目标请求计数不增加，即使环境存在 `NO_PROXY=*`。
- 正常结束、错误凭证和 watchdog 超时均清理客户端、curl、守护进程及临时凭证文件。
- 通过生产 `node.ProbeCapabilities` 入口检测固定公网目标：出口信息 `available`，IP 地区 `US`；YouTube `available`，原因 `connectivity_only`；Netflix `unknown`，原因 `request_network_error_56`。报告不保存本次公网 IP。

复现公网观察需要网络可达，结果会随实际出口和服务变化：

```bash
MPX_CAPABILITY_PUBLIC_TEST=1 MPX_XRAY_BIN=/path/to/xray \
  go test ./internal/node -run TestLiveCapabilityPublicTargets -count=1 -v
```

普通真实代理与清理检查：

```bash
MPX_XRAY_BIN=/path/to/xray go test ./internal/node -count=1
```

## Hysteria2 / TUIC 入口验证

v2 在两个协议的真实 sing-box 服务端上调用生产 `node.ProbeCapabilities`。固定目标 `https://api.ip.sb/geoip` 不变，测试服务端把出口转到本地可信 HTTPS fixture；TLS 保留证书签名与主机名校验，`NO_PROXY=*` 不导致直连。测试仅向客户端注入 fixture CA，未启用 insecure，未修改生产配置。

```bash
MPX_SING_BOX_BIN=/path/to/sing-box \
  go test ./internal/node -run TestLiveCapabilityQUICActualProxy -count=1 -v
```

官方 sing-box 1.14.2（提交 `af6e64c`，`with_quic`）运行通过，16.476 秒。每协议正确认证使源站恰好收到一次请求，得到 `available/US`；错误密码不增加源站计数，返回未知。watchdog 和调用方取消分别验证客户端、curl、守护进程、sleep 子进程及临时凭证目录清理。该 fixture 证明两种真实协议、身份和代理路径，不声称两种公网出口的服务解锁结果。

## 判断依据与覆盖范围

参考成熟 RegionRestrictionCheck 的 Netflix 探测方案，再采用严格页面证据解析。固定目标为 `api.ip.sb/geoip`、YouTube `generate_204`、Netflix 授权片目 `81280792` 与原创片目 `80018499`。Netflix 正向结果需要精确片目 URL 的 Movie/TVSeries JSON-LD 与可识别地区；普通 HTTP 200、登录页、错误片目、损坏 JSON 和冲突地区均为未知。明确 HTTP 403/404 拒绝与“仅原创”另行区分。

解析器测试覆盖正向页面、明确拒绝、仅原创、未知地区、网络/超时、响应过大及格式变化。可用与拒绝的 Netflix 页面证据为确定性 fixture；本次公网实测没有取得 Netflix 可用或拒绝证据，不声称真实播放验收。YouTube 检查仅证明连通性，不证明播放、Premium 或服务地区。

控制层及订阅 HTTP 测试覆盖两次观测稳定判定、过期、地区变化、配置代际、停用时在途任务丢弃、动态用户域名拒绝，以及候选缺失/可用/未知/过期/地区不符/恢复/授权撤销。配置与结果按入站/入口/目标保存，配置变化不复用旧路径证据。

## 资源与局限

全控制面最多一个探测，远程约 65 秒上限、单请求连接 4 秒/整体 12 秒、响应 1 MiB 上限，结果只保留最近一份。调度独立于流量计量与用户收敛。三方目标或页面结构变化可能导致未知；修改固定目标或判断规则后应升级检测器版本并重新验证。

节点侧结果不描述客户端到节点的延迟、吞吐、游戏 RTT 或丢包。不同入口分别检测；用户专属动态域名不共享证据。配置已下载到客户端后，候选变化需客户端更新订阅才生效。
