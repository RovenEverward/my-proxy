# Hysteria2 与 TUIC 节点

Hysteria2 与 TUIC 入站由独立 sing-box 服务承载。同一节点可以同时保留 Xray 入站。两核心分别使用配置、systemd 单元、API 端口和统计基准，用户用量在控制面合并。

## 安装与配置

节点需为 Linux amd64 或 arm64，SSH 用户需要安装 systemd 服务、写入 `/etc/mpx` 和 `/usr/local/lib/mpx` 的权限。UDP 入站端口需在防火墙开放；统计接口只监听节点回环，通过现有 SSH 密钥与 `ssh -W` 转发。

```sh
mpx node add quic-01 --address 203.0.113.10
mpx inbound add --node 1 --protocol hysteria2 --port 8443 \
  --network udp --security tls --sni proxy.example.com \
  --cert-file /etc/letsencrypt/live/proxy.example.com/fullchain.pem \
  --key-file /etc/letsencrypt/live/proxy.example.com/privkey.pem
mpx inbound add --node 1 --protocol tuic --port 9443 \
  --network udp --security tls --sni proxy.example.com \
  --cert-file /etc/letsencrypt/live/proxy.example.com/fullchain.pem \
  --key-file /etc/letsencrypt/live/proxy.example.com/privkey.pem \
  --congestion-control bbr
mpx node config 1 --core sing-box
mpx node install 1 --core sing-box
mpx node status 1
mpx doctor
```

证书与私钥由操作者准备在节点上，安装器不申请证书。客户端默认验证 TLS 证书。TUIC 支持 `bbr`（默认）、`cubic`、`new_reno`。网络与安全层缺省时，QUIC 入站默认 `udp` / `tls`。

默认 sing-box 路径为 `/usr/local/lib/mpx/sing-box`、配置 `/etc/mpx/sing-box/config.json`、服务 `mpx-sing-box`、统计端口 `10086`；Xray 默认端口为 `10085`。首次安装前可以设置第二核心的元数据：

```sh
mpx node core show 1 sing-box
mpx node core edit 1 sing-box --api-port 11086
```

已部署服务的路径、服务名和统计端口不能直接改名。迁移需要明确停止旧服务并处理计量基准，当前接口会拒绝这类修改。

`node install` 默认仍安装 Xray；选 `--core sing-box` 才安装第二核心。`node sync` 同步该节点需要的两核心；结构变更前可分别查看 `node config --core xray` 与 `--core sing-box`。

## 为什么从官方源码构建

默认固定官方 sing-box **v1.14.3**、提交 `7054cac5124465ffdb145870d7c01885566711b1`，从未经修改的官方源码使用 `CGO_ENABLED=0`、`with_quic,with_v2ray_api` 构建。普通官方发布包缺少 `with_v2ray_api`，不能提供这里所需的单用户统计。因此安装器不自动退回发布包。

首次安装要求控制面机器有 Git、适合上游的 Go 工具链与到官方源码、Go 模块源的网络访问；构建在控制面进行，节点不需要 Go。构建缓存按版本、平台、架构分开，并校验源码提交、构建标签、二进制 SHA256 与平台。安装前会验证标签、已有配置；候选配置检查失败时保留现有服务。不同官方版本可以用 `--version vX.Y.Z` 指定，需自行验证该版本行为。

## 用户、流量与停用

每名用户使用自身种子派生的密码；TUIC 还使用派生 UUID。服务端用户名与统计用户名一致，未授权、停用、过期或达到配额的用户不会出现在该入站名单中。两个协议共用该用户自身凭据，同一用户在两个 QUIC 入站的用量由 sing-box 合并；这不是共享账号。

计量读取 V2Ray 兼容 API 的 `user>>>用户名>>>traffic>>>uplink/downlink`。控制面按 `(节点,核心,用户名)` 保存独立累计值，事务内更新历史与用户用量，避免 Xray 和 sing-box 基准串用。配额使用上下行之和，统计为核心的代理流量字节，不等于网卡 QUIC/TLS 开销。

sing-box 没有这里可用的动态用户修改 API。名单变化会使控制面下发新配置并停止、启动 sing-box，**该节点全部 QUIC 连接都会断开**。这包括超限、到期、停用与删除用户；其他用户可用自身凭据重连，Xray 服务继续使用原有动态操作。名单与配置哈希未变化时不会每轮重启。

计划重启前采样，服务停止后写入持久的零基准，再启动；统计不可用时拒绝计划重启，避免丢掉最后一份可计量数据。systemd `InvocationID` 记录进程世代，人工重启后新进程计数按新的累计值计入，即使其已超过旧计数也不会误减。控制面无法追回未经最终采样的崩溃或人工重启前尾部流量，也无法保证采样与服务停止之间完全没有新字节。配额由后台采样周期执行，因此会有周期内超额。使用授权用户凭据进行的能力探测也会产生代理流量，计入该用户配额；探测应限制次数和响应大小。

配置检查失败时原服务继续运行；停用部署启动失败时服务保持停止，避免自动恢复旧的用户授权，旧配置留在 `.bak` 供操作者处理。`doctor` 会检查各核心存活、配置哈希与第二核心单用户统计 API。

sing-box 不提供单用户在线 IP 接口。含 QUIC 的节点执行在线名单查询会返回明确错误。节点排水仅改变订阅，不会立即断开已建立的连接。

## 订阅与备份

支持 `hysteria2://`、`tuic://` 分享链接、Base64 链接、mihomo（`clash-meta`）与 sing-box 订阅。原版 Clash 与 Xray 客户端格式不能表示这两个协议，会返回不支持错误。客户端需要近期的 mihomo 或 sing-box；服务端与客户端配置均已使用官方二进制检查和真实流量验证。

备份中的 SQLite 快照包括 QUIC 入站、独立核心部署信息、统计世代与用户用量。旧备份在打开时迁移，原有采样归入 Xray。备份不包含远端证书、核心二进制或远端配置；恢复后应准备证书与 SSH 连通性，再安装核心并同步配置。恢复旧快照不能恢复远端活跃连接。

## 官方参考

- [sing-box Hysteria2 入站](https://sing-box.sagernet.org/configuration/inbound/hysteria2/)
- [sing-box TUIC 入站](https://sing-box.sagernet.org/configuration/inbound/tuic/)
- [sing-box V2Ray API](https://sing-box.sagernet.org/configuration/experimental/v2ray-api/)
- [sing-box 构建标签](https://sing-box.sagernet.org/installation/build-from-source/)
- [v1.14.3 官方源代码](https://github.com/SagerNet/sing-box/tree/v1.14.3)
- [v1.14.3 官方统计实现](https://github.com/SagerNet/sing-box/blob/v1.14.3/experimental/v2rayapi/stats.go)
