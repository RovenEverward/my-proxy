---
kind: part
parent: README.md
code: [internal/control/, internal/node/, internal/render/xray.go, internal/render/serverconfig.go, internal/render/singbox_server.go]
verified: 2026-11-24 @ 5de6b0c
---
# 节点：下发、收敛与巡检

> **30 秒版**：节点是真正承载流量的服务器，上面运行预装好的 `xray`（承载 VLESS / XHTTP / REALITY / Trojan 等）与 `sing-box`（承载 Hysteria2 / TUIC），不装任何 MPX 组件。控制面经系统自带的 `ssh` 把算好的配置校验后原子推过去，每 30 秒一轮按人增删、按人读流量，每 5 分钟巡检一次有没有被人改过。

## 边界

负责：渲染并原子下发节点配置（`xray` / `sing-box`）、周期收敛用户名单、采样流量并清理 7 天前历史采样、巡检与告警、不通节点的退避。

不负责：决定谁该在哪（读 [用户与计量](users.md) 的结果）；在节点上安装或升级 `xray`/`sing-box` 二进制（由操作者或 AI Agent 直接通过 SSH 管理）；ACME 证书签发与轮换（QUIC 入站缺失证书时控制面自签兜底，生产证书可通过 `mpx inbound cert set` 托管下发或由节点外部 `acme.sh` 管理）。

## 怎么运转

两条路按改动性质分开走：

| 改动 | 走哪条路 | 会不会断别人的连接 |
|---|---|---|
| 结构：端口、入站、证书、出口策略、订阅镜像 | `mpx node sync`：全量渲染 → 备份 → 写入 → `xray -test` / `sing-box check` → 不过就回滚 → 重启 → 起不来再回滚 | 会，重启 |
| 名单：加人、摘人、超限、续期 | 后台循环第 3 步，调 xray API 的 `adu` / `rmu`（sing-box QUIC 入站为重启更新） | Xray 不会；sing-box 会 |

后台每一轮的顺序是有意的：先**采样**（读累计值，减去 `traffic_samples` 里的上次值），再**重置**到期周期，最后**收敛**名单（[internal/control/loop.go](../../internal/control/loop.go)）。每 10 轮顺路**巡检**：一条 ssh 读回服务状态、核心版本、配置哈希、证书剩余天数，和「最后一次下发记下的哈希」比对发现漂移；整轮 45 秒预算，第一轮就跑；每小时清理 7 天前的 `traffic_samples` 历史行（保留每个 `(node_id, core, username)` 最新基线）。

SSH、配置部署与核心统计在 [internal/node](../../internal/node/)。

## 为什么这样设计

节点零维护：不用在节点上部署额外守护进程或 Agent，控制面只依赖 SSH 和核心自带的统计/校验命令。

### 订阅镜像（UC-10）

名单内一台节点可同时是订阅入口：`sub_mirrors` 登记「节点 + 端口 + 上游」，渲染时在该节点的 xray 配置里追加一个 `dokodemo-door` 入站，把公开端口的 TCP 转给控制面。镜像只在主核心 xray 上渲染——sing-box 是第二核心，同机同端口只能绑一边；纯 QUIC 节点有镜像时强制下发 xray。镜像不进数据通路、不持真相、不参与计量；内容与鉴权以控制面为唯一来源。加/删/改上游都是结构性变更，`mpx mirror add|rm|set` 默认走 `DeployNode` 下发，`--sync=false` 只登记。上游指到控制面的 `--sub-addr` 专用口时，`/api` 不会出现在镜像端口。

### TLS 证书：托管下发与自签兜底（UC-09 / UC-27）

QUIC 入站（hysteria2 / tuic）走 sing-box，`sing-box check` 会打开 `certificate_path` / `key_path`。下发前 `node.EnsureTLSCertificates` 一次报回各对 cert/key 状态：

- **托管证书（UC-27）**：入站带了 `cert_pem`/`key_pem`（经 `PUT /api/inbounds/{id}/cert` 写入）时，按 sha256 比对覆盖；材料变化时 `deploySingBox` 强制重载服务。
- **自签兜底（UC-09）**：没带 PEM 且节点缺文件时，控制面按 SNI 自签 RSA 2048 证书（10 年）；两文件都在则原样不动。

写文件经 `PutFileMode` 原子落盘（先 `.tmp` 再 `mv`）。`inbound add/edit` 里 QUIC + TLS 的 `--cert-file`/`--key-file` 可省略，服务端按 tag 分配默认路径；非 QUIC 的 TLS 仍必填。续期流水线留在 mpx 之外：`acme.sh` 续期钩子经 `mpx inbound cert set` + `mpx node sync` 推送。

## 坑

- sing-box 下发的"hash 相同就跳过"只信「最后一次下发成功」的基准：`node sync`（`verifyRemote`）会额外校验节点磁盘上的 `sha256sum`，对不上就重发；周期收敛仍是零 ssh 快路径。
- 订阅镜像只在主核心 xray 渲染：同机同端口只能绑一边，两边都写会让 sing-box 起不来。
- 名单变化 → sing-box 承载的 Hysteria2 / TUIC 会重启该节点全部 QUIC 连接；xray 摘人后已建立的 TCP 连接可能继续传。
- 读数比上次小 → 说明节点核心重启过 → 记异常日志，不当成负用量。
- ssh 连接复用的 socket 路径太长 → 超过 unix socket 长度上限直接失败 → 路径必须短。
- 直连出口默认 IPv4 优先（xray 写 `UseIPv4v6`，sing-box 经 `route` 的 `action: resolve` 写 `prefer_ipv4`，避免双栈节点 IPv6 失联时直连卡死），节点可用 `outbound_ip` 覆盖（如 `v4` / `v6` / `asis` + `node sync`）。
- 节点不通 → 按退避跳过（短期 30s 起翻倍封顶 5 分钟，持续不通 1 小时后每小时一探）；修好后 `mpx node retry <节点>` 或 `GET /api/nodes/{id}?fresh=1` 立即清退避。
