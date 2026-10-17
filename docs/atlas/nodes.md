---
kind: part
parent: README.md
code: [internal/control/, internal/node/, internal/render/xray.go, internal/render/serverconfig.go, internal/render/singbox_server.go, internal/xrayrelease/, internal/singboxbuild/]
verified: 2026-10-17 @ 626beea
---
# 节点：下发、收敛与巡检

> **30 秒版**：节点是真正承载流量的服务器，上面只有 xray（可选再加一个 sing-box 承载 Hysteria2 / TUIC），不装任何 MPX 组件。控制面经系统自带的 `ssh` 把算好的配置推过去，每 30 秒一轮按人增删、按人读流量，每 5 分钟巡检一次有没有被人改过。

## 边界

负责：装核心与升级核心、渲染并下发节点配置、周期收敛用户名单、采样流量、巡检与告警、不通节点的退避。升级 xray 时由节点自己从 GitHub 拉官方包，控制面只递 URL 和取自官方 `.dgst` 的摘要，新核心先过 `xray run -test` 再顶替、失败回滚（v0.8.0 起，编排见 [安装与升级](install-update.md)）。

不负责：决定谁该在哪（读 [用户、套餐与计量](users.md) 的结果）；证书签发（假定证书已在节点上）；自己编译 xray（只推官方发布包；sing-box 用官方源码带统计标签在控制面编）。

## 怎么运转

两条路按改动性质分开走：

| 改动 | 走哪条路 | 会不会断别人的连接 |
|---|---|---|
| 结构：端口、入站、证书、出口策略 | `mpx node sync`：全量渲染 → 备份 → 写入 → `xray -test` → 不过就回滚 → 重启 → 起不来再回滚 | 会，重启 |
| 名单：加人、摘人、超限、续期 | 后台循环第 3 步，调 xray API 的 `adu` / `rmu` | 不会 |

后台每一轮的顺序是有意的：先**采样**（读累计值，减去 `traffic_samples` 里的上次值），再**重置**到期周期，最后**收敛**名单（[internal/control/loop.go](../../internal/control/loop.go)）。每 10 轮顺路**巡检**：一条 ssh 读回服务状态、核心版本、配置哈希、证书剩余天数，和「最后一次下发记下的哈希」比对发现漂移；整轮 45 秒预算，第一轮就跑。完整走查和真实输出见 [mpx 是怎么搭起来的](../internal/index.md)。

SSH 和核心安装在 [internal/node](../../internal/node/)；节点配置渲染在 [internal/render/xray.go](../../internal/render/xray.go) 等文件，和订阅渲染不共用代码（decisions 第 12 条）。

## 为什么这样设计

节点零维护：不用升级组件、不用守护进程、不用给它签证书（decisions 第 2、13、14 条）。代价是拿不到结构化状态，要问就得 ssh。分两条路是因为配额恢复和续费都会触发加人，每次都重启会把整台节点的人断一次（第 3 条）。

## 坑

- 名单变化 → sing-box 承载的 Hysteria2 / TUIC 会重启该节点全部 QUIC 连接；xray 摘人后已建立的 TCP 连接可能继续传 → 这是已接受的限制，见 [QUIC 验证](../internal/quic-validation.md)。
- 读数比上次小 → 说明节点上的 xray 重启过 → 记异常日志，不当成负用量（decisions 第 9 条）。
- ssh 连接复用的 socket 路径太长 → 超过 unix socket 长度上限直接失败 → 路径必须短（decisions 第 34 条）。
- 直连出口没设 `domainStrategy` → 双栈网站会优先走 IPv6，VPS 的 v6 段常被限流 → v0.7.3 起默认 `UseIPv4v6`，节点可用 `outbound_ip` 覆盖。
- 节点不通 → 每轮重试会拖慢整轮 → 按退避跳过（decisions 第 18 条）。
