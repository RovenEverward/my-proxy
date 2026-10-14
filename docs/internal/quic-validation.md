# QUIC 运行验证记录

本记录对应 issue #3 的 `feat/3-quic-protocols` 工作树。真实运行使用未经修改的官方 sing-box v1.14.3，提交 `7054cac5124465ffdb145870d7c01885566711b1`，构建标签 `with_quic,with_v2ray_api`。控制面工具链为 Go 1.27.1 darwin/arm64。

## 官方构建与多用户验证

官方发布包的 `version` 标签有 `with_quic`，没有 `with_v2ray_api`；启用该 API 的检查错误为 `v2ray api is not included in this build, rebuild with -tags with_v2ray_api`。因此实现使用固定官方源码构建，不以普通发布包冒充统计可用的服务端。

```sh
MPX_SINGBOX_BUILD_TEST_CACHE="$PWD/.spike/singbox-build-cache" \
  go test -v ./internal/singboxbuild -count=1 -timeout=15m
```

真实交叉构建与二次缓存校验均通过，包耗时 14.323 秒。Linux amd64 产物 39,788,668 字节，Linux arm64 产物 36,962,428 字节。测试校验版本、官方提交、平台、CGO、标签、SHA256；构建产物没有更改官方业务源码。

先行本地 server/client spike 分别为 Hysteria2 与 TUIC 配置 alice、bob 的独立凭据。每协议 alice 发送 10,000 字节，bob 发送 20,000 字节，统计均按用户名分别归属上下行；错误密码不能转发。非 reset 查询保留计数，reset 查询返回旧值后归零；服务重启计数重新开始。移除用户并重启后，原活跃连接中止，已移除凭据不能重新转发。

真实 API 路径为 `/v2ray.core.app.stats.command.StatsService/QueryStats`。上游生成客户端的 `experimental.v2rayapi` 路径会报 `rpc error: code = Unimplemented desc = unknown service experimental.v2rayapi.StatsService`；实现以官方服务器注册名称为准，并用公共 protobuf wire 调用。生产查询不发送 reset 标记。

## Linux、SSH 与 systemd

真实节点是隔离 Ubuntu 22.04 arm64 容器，PID 1 为 systemd，SSH 监听 `127.0.0.1:19322`，QUIC 映射 UDP 19301、19302。Docker 主机为 Linux aarch64、cgroup v2。该测试直接调用生产 `node.SSH`、安装、配置下发与 `ssh -W` 统计转发，节点无常驻 mpx 代理。

```sh
MPX_SINGBOX_LIVE_NODE=127.0.0.1:19322 \
MPX_SINGBOX_LIVE_KEY="$PWD/.spike/core-live-key" \
MPX_SINGBOX_LIVE_BINARY="$PWD/.spike/singbox-build-cache/sing-box/v1.14.3/linux-arm64/sing-box" \
MPX_SINGBOX_LIVE_CLIENT="$PWD/.spike/sing-box-1.14.3-darwin-arm64/sing-box" \
  go test -v ./internal/node -run TestLiveSingBoxSSHSystemd -count=1 -timeout=5m
```

结果 PASS，测试 8.03 秒，包 8.046 秒。Hysteria2 alice 的 TCP 16,384 字节加 UDP 1,024 字节，实际累计为上行 17,408、下行 17,408；TUIC bob 的 TCP 32,768 加 UDP 2,048，实际累计为上行 34,816、下行 34,816，无跨用户归属。撤销下发验证停止前最终采样、停止后的边界、启动新进程、原连接断开、重连无法 echo，以及新统计 API 返回空名单。

测试使用自签证书并仅在测试客户端允许 insecure；为访问回环 echo，测试配置临时移除私网目标限制。生产配置保留私网限制和证书验证。测试容器已按名称、ID、镜像核对后停止、删除，原有容器保持运行。amd64 已验证真实构建；systemd 运行验证仅覆盖 arm64。

## 客户端与控制面验证

实际渲染产物可用 `QUIC_RENDER_DIR="$PWD/.spike" go test ./internal/render -run TestQUICExportRuntimeFixtures -count=1` 导出。官方 sing-box 与 mihomo v1.19.32 均成功检查、加载实际生成的两协议配置。分别通过两个客户端的 SOCKS 获取 102,400 字节固定响应，四次传输全部逐字节与 SHA256 一致，统计只增加 alice，bob 不增加。两协议错误密码均被拒绝。分享链接验证 URI 的密码、UUID、TLS/SNI 与 IPv6 编码，未将链接导入 GUI 作为额外已完成证据。

控制器测试使用 fake SSH 与进程内 gRPC，属于单元集成证据：两核心计数隔离、进程世代变更、配额摘除、最终采样跨限后的重新暂存、删除用户残留计数、稳定配置不重启、采样/启动失败不写成功哈希。SQLite 测试覆盖原子记账、重复并发采样与旧库迁移；备份测试覆盖恢复后核心元数据、流量与进程世代。

混合核心回归进一步覆盖 Xray 和 sing-box 故障的两个方向：三轮执行超限摘除、禁用和恢复授权，整节点退避窗口不阻断健康核心，每轮失败计数仅增加一次。实际 gRPC 处理器阻塞到请求上下文取消，在 30 毫秒测试预算耗尽后，Xray 仍得到未取消的独立上下文。删除最后一个 QUIC 入站会最终采样并下发空入站配置，不查询不存在的 Xray。`go test -race ./internal/control -run '^TestSingBox' -count=1 -v` 全十组通过，耗时 4.181 秒。

最终检查命令：

```sh
go test ./... -count=1 -race
go vet ./...
go build ./...
git diff --check
npm ci --ignore-scripts
npm run docs:build
```

全量 Go 检查与文档构建通过。首次文档构建因未安装 VitePress 返回 `sh: vitepress: command not found`，安装锁定依赖后通过；npm 报告现有锁定依赖的 2 个 moderate、1 个 high 漏洞，未更改依赖锁。

## 混合核心真实生命周期

另外在隔离 Ubuntu 22.04 Linux arm64 systemd 容器同机运行 Xray 与 sing-box，SSH 端口 `19422`，入站端口 `19401`、`19402`、`19403`。Xray 为官方 v26.3.27、提交 `d2758a0`，Linux 二进制 SHA256 为 `c2d20a7045250497083afea0d79db0672f6c89a25aaaf37c92de034d6b764b04`；sing-box 为同一固定 v1.14.3 源码构建，SHA256 为 `a37a25914f63e7a8e3be2e29678ead8108fac487e647abd545f16f41db6e786d`，两服务均实际 active。

运行器直接调用生产 `Control`、SSH、安装与备份接口，使用官方 macOS 客户端。排水与订阅经过真实 HTTP 处理器；用户重置、授权、到期和续费通过生产 Store 方法加 Control 收敛完成，没有覆盖这些用户变更的 HTTP 路由。证书为自签，测试客户端允许 insecure。echo 的 `8.8.8.8:19490` 仅分配在专用容器回环，不发送公网流量，生产私网目标限制保持启用。

```sh
PATH="$PWD/.spike/mixed-live:$PATH" \
  go run ./.spike/mixed-live >.spike/mixed-live/run.log 2>&1
```

运行结果为 `PASS mixed SSH+Control lifecycle runtime`，包含下列实际观察：

- Alice 的 sing-box 上下行各 `50,176` 字节，Xray 上下行各 `50,176` 字节，合并 used/lifetime 为 `200,704/200,704`；Bob 为 `24,576`。重复采样没有再次累加。
- 配额设为 `180,000` 后，Alice 的已有 Hysteria2 连接断开，Hysteria2、TUIC、VLESS 新认证全部被拒绝。已有 Xray TCP 连接仍可 echo；未单独持有 TUIC 活跃连接作该项验证。
- 排水使真实 HTTP 订阅候选由 `3` 变为 `0`；已有 Hysteria2 和 Xray 连接仍传输，sing-box `InvocationID` 不变。
- 用量重置保留累计用量 `201,728`；再次传输后 used/lifetime 为 `4,160/205,888`，三协议均可重新认证。
- 将授权收窄到 Hysteria2 和 VLESS 后，TUIC 拒绝认证，另外两协议正常；到期后两核心拒绝新连接，续费并恢复授权后全部三协议恢复。
- `Backup.Create`、`Validate` 和恢复后的 Store 比较通过：核心部署元数据、两个核心计数基准、节点部署哈希、三个入站均一致，used/lifetime 为 `9,280/211,008`。

运行器、日志和包含测试凭据的备份留在本地 spike 中，不作为源码提交。容器由验证代理按其名称、ID 核对后清理；其他节点不受影响。

## 与用途订阅、能力检测的集成

混合 HTTP 测试验证 Hysteria2、TUIC 和 Xray 的独立 Endpoint 身份与原始凭证、授权撤回、排水、用户到期和停用。能力证据按入站及 Host 隔离：无结果、未知、过期、地区不符和配置指纹变化退出候选，恢复后重新加入；日常用途依赖也检查自身 TTL。不兼容用途格式返回明确 422 与普通订阅 URL。默认、自定义模板分别通过 mihomo 和 sing-box 官方解析，共四份混合 QUIC 配置。

```sh
MPX_TEST_MIHOMO=/path/to/mihomo MPX_TEST_SINGBOX=/path/to/sing-box \
  go test ./internal/render ./internal/server -run '^TestQUICPurpose' -count=1 -race -v
```

QUIC 入口检测的真实认证、可信 TLS、无直连回退和进程清理见 [能力检测验证](capability-validation.md)。`doctor` 对原版 Clash、Xray 的 QUIC 格式限制报告兼容性警告，兼容格式的坏模板仍报告失败；删除最后 QUIC 入站后保留正确的历史核心状态归属。

## 已知运行边界

sing-box 没有可用的动态用户增删 API；名单变化会断开该节点所有 QUIC 连接。计划重启先采样再停止；采样与停止之间的字节以及未经采样的崩溃尾部流量无法追回。进程 `InvocationID` 可防止重启后的累计值误当旧差值，不能恢复已经丢失的旧进程统计。配额在采样周期内可能超额。在线 IP 列表不支持 QUIC。具体操作见 Hysteria2 与 TUIC 节点（用户指南待迁移）。
