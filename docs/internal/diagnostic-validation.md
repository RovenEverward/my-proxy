# 一键体检验证记录

Issue #11 将 `mpx node diagnose ID` / `--all` 的 node 默认引擎改为官方 Go ECS 融合怪 v0.2.12，保留健康、漂移与全部获授权入口检查。本页区分已完成的官方程序样本与 issue #7 的轻量引擎证据。当前证据不证明默认 Fusion 全模块、SSH 全流程、远程清理或生产验收已全部通过。

## 官方 Fusion 已有证据

官方 Linux arm64 ZIP 在 Docker 中完成两组基础模块测量，均返回退出码 0：

| 模式 | 实际产物 | 结果与边界 |
| --- | --- | --- |
| 文件模式，仅基础信息 | JSON 3957 字节、文本 1973 字节，JSON `duration_ms:8086` | `goecs.report/v1`、`ecs_version:v0.2.12`、`status:ok`；sections 只有 basics 启用。JSON 是旧文本包装，没有类型化 components |
| `-json=- -privacy=true`，基础信息与 NAT | stdout 9191 字节，JSON `duration_ms:15820` | stdout 是终端文本前缀加 JSON。`components` 含 basics 的 `goecs.system/v1` 与 gostun.nat 的 `goecs.stun/v1`，两者均 ok；JSON `text` 为空 |

结构化模式仍会执行旧基础信息采集，再执行类型化基础信息与 NAT，因而该样本约 15.82 秒。上游结构化配置会关闭旧硬件与网络执行，不能由此推断硬件测试重复执行。上述样本没有运行默认全部流媒体、安全、邮件、线路和测速模块。

向官方程序发送 TERM 的样本返回退出码 0，但结构化 JSON 缺失或截断。因此 mpx 必须严格解析版本化 JSON，并以自身上下文区分取消与超时；不能只按进程退出码或部分文本判断完成。该上游取消样本不证明 mpx 的 SSH 清理已通过。

上游源码在 `PrivacyMode` 为 true 时跳过统计信标，关闭上传是另一项独立设置。结构化隐私样本隐藏 JSON 主机标识并将 `text` 清空，终端前缀仍含真实 IP。外部 IP 服务查询仍会发生；没有将隐私模式写成完全离线或全部输出脱敏。

### 完整模块组合，关闭测速

官方 Linux arm64 程序在 Docker 中执行与节点适配器一致的模块组合，显式 `-speed=false -nt3=false`，硬件测试关闭，上游时限 120 秒。进程退出码 0，外层耗时 35.117 秒；stdout 为 285681 字节，其中 JSON 为 283673 字节，不含文件末尾换行。上游 `duration_ms:33189`、状态 partial，共七个真实类型化 components。

- 流媒体共 360 条结果，IPv4、IPv6 各 180 条；部分失败与限流保留为 partial，不能推广成入口解锁结论。
- 安全数据包含供应商不可用或降级结果，不用于证明 IP 信誉数据准确。
- 邮件模块覆盖 205 项能力，153 项可用，其余保持失败或缺失状态。
- `goecs.backtrace/v2` 返回真实结构，但线路分类置信度为 inconclusive。Docker 和操作机透明网络环境下的结果不能用于证明生产线路等级或回程。
- ping 有三个结果，NAT 有 `goecs.stun/v1` 数据；测速与硬件 sections 明确 skipped。

这份实际 stdout 超过原有 256 KiB 采集上限。适配器已将远程原始采集上限调整为 2 MiB，持久化仍分别限制 `output` 64 KiB、`data` 128 KiB，以及完整记录 256 KiB。超大 JSON 保留上游 section/component 身份与状态，缩短测量 payload，并通过 `adapter_truncated`、`payload_truncated` 和封装警告明确缺失，不再直接丢弃所有结构化数据。这是原始采集与保存上限的修正，不是流量预算放宽。

该样本容器按所属标识清理，metadata 确认容器不存在且无残留容器进程。它证明官方模块组合与输出契约；适配器的独立验证见下节。

### 节点适配器与清理

执行负责人移交的最终结果：`MPX_FUSION_TEST_ASSETS=/tmp/mpx-ecs-proof.7cMw8X go test ./internal/node -run TestFusionOfficialDocker -v -count=1` 通过，总耗时 61.01 秒。测试使用官方 arm64 程序，在 Docker 中执行真实节点适配器的安装、运行与清理脚本。

两次八秒调用方时限均返回取消证据，分别保留 771 与 374 字节原始文本；结束后未残留 goecs 进程或本次临时目录。最小报告为 done、3865 字节，无警告。时限 60 秒且关闭测速与硬件的完整适配器运行返回 partial，执行错误为空，`output` 为 1895 字节，`data` 为 81720 字节；上游流媒体有 344 条结果，邮件能力 102/205 可用，公网 IP 数据不可用。partial 保留这些未完成项，不能判成全模块成功。

超大真实 JSON fixture 的原文件为 283674 字节，适配后 `data` 为 20447 字节，仍保留全部七个组件的原始名称、schema 和状态；封装标记 truncated、partial。执行负责人同时确认全仓库 race 检查、vet 与构建通过。这些结果来自独立执行移交，文档更新未重复运行测评。

上述证据覆盖 Docker 中的适配器和两次取消清理，尚未证明真实 SSH 断开后的清理。默认测速开启的完整命令、全部实际代理入口、可选硬件和生产验收仍需独立记录；未据此发布、部署或声明生产通过。

## 轻量引擎入口与保存

以下是 issue #7 已完成的原有流程证据。它现在由 `--engine light` 显式选择；`--network-only` 与 control 位置仍使用轻量网络执行器，不是新的 node 默认 Fusion 证据。

原有完整 CheckupNode 测速开启；`--all` 读取实际节点列表后顺序执行，一个失败保留部分报告并继续，取消停止后续节点。旧默认 API `{}`、显式 `speed:false`、`network_only:true` 有 HTTP handler 和 CLI 参数解析的 mock 回归。完整报告合并 summary、health、drift、network 与 entrances；GET 最新记录兼容旧网络报告。测量时节点地址保留，后续修改节点不会误写旧地址。

控制层验证全程单槽、同节点位置一分钟限制、网络运行时 SQLite 可用、取消保存部分证据、节点删除级联、合法长节点名、服务端重新读取节点身份。存储每节点每位置只留一条 JSON，node/control 共两条，单条最多 256 KiB。

## 真实网络工具

使用本机实际 curl、可控 HTTP 服务和 SOCKS 服务，核对目标命中和字节计数：GET、POST 以及直连/SOCKS 各 1 KiB 与 1 MiB PUT。代理失败时目标零命中；无直连回退。PUT 声明长度、接收长度与 curl size_upload 一致且不带 Transfer-Encoding。

本地 fixture 包括未知长度超大 chunked 响应、畸形 Content-Length、重定向、超时、取消、临时目录清理和即时输出超限取消。旧 curl shim 去掉 --max-filesize 后，文件大小硬限制仍生效；不会依赖新 curl 的未知长度处理。真实 loopback ping/traceroute 解析以及中断 ping 保留部分 RTT、丢包未知均检查。供应商 schema fixture 验证字段缺失不填 false、缺密钥环境显示 skipped、Bogon-only 质量结果 partial，以及默认 GeoIP 所选地址一致。

mtr 0.95 的 `--report` 会覆盖前面的 `--json` 输出模式。调用使用 `--json --no-dns --report-cycles 1 --max-ttl 12`，JSON 本身已包含报告模式。可执行 fixture 复现旧参数返回文本但退出成功，验证修复后完整 shell/Executor/route 调用解析出 hop；取消时限仍生效。本地未安装 mtr，实际节点工具的验证由父代理执行。路由单目标共享 20 秒预算，mtr 与 traceroute 回退使用相同截止时间；HTTP、DNS 和 ping 单次时限以及全局网络时限保持原值。延迟 1.2 秒的可执行 mtr fixture 在 HTTP 时限 1 秒时成功；100 毫秒调用方时限可提前取消并明确报告超时。Executor fixture 核对两次路由命令共用同一截止时间及有界远程 watchdog。

独立审阅实际执行 remote-style watchdog：exec sleep 20 在一秒看门狗后结束，exec true 提前正常返回，HTTP curl 看门狗后退出。返回后同进程组中未残留 curl、sleep 或 watchdog。没有把无发送计数的计划样本当成丢包。

## 公开端点小样本

执行 `MPX_DIAGNOSTICS_PUBLIC_TEST=1 MPX_DIAGNOSTICS_REPORT=/tmp/issue7-public-diagnostics.json go test ./internal/diagnostics -run TestPublicEndpointsManual -count=1 -v`，用时 24.56 秒。

Cloudflare 下载/上传各 1024 字节；Clouvider LA 下载 1048576 字节、上传 1024 字节，均成功。总实际应用数据 1052592 字节，保守预留 1768448 字节，小于该次 8 MiB 预算。ipify IPv4/IPv6 与 ipwho.is GeoIP/ASN 成功；无密钥的数据源跳过。当前操作机有透明网络接管，地区主机解析出现 198.18.*，因此该次证明接口契约与有界执行，不用于跨洲线路性能结论。

来源：Cloudflare speed 接口；LibreSpeed 官方注册表 `https://librespeed.org/backend-servers/servers.php` 与 garbage.php 的 ckSize 块契约；`https://ipwhois.io/documentation`、`https://ipapi.is/developers.html`、`https://docs.abuseipdb.com/#check-endpoint`。带密钥供应商调用只验证文档与 fixture，未使用真实密钥；不宣称实际信誉查询已成功。

## 能力修复与范围

[能力检测验证记录](./capability-validation.md) 记录 v3 单目标错误隔离、8 MiB 正文和 8 KiB 元数据硬上限，真实 Xray/Hysteria2/TUIC 验证。入口探测与直连网络测量分开标明，完整报告统一呈现；测速不改变用途候选。

以上证据是本地与公开小样本验证；生产验收另按实际执行结果记录。未提供本地到生产的用户网络、长期最大带宽、回程路由或无来源的 IP 质量评分。
