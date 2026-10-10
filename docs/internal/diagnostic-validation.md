# 一键体检验证记录

验证目标是默认一条命令完成健康、全部代理入口和网络体检，同时证明流量、时间、输出、存储与取消边界。对应 issue #7。

## 默认入口与保存

`mpx node diagnose ID` 默认调用完整 CheckupNode，测速开启；`--all` 读取实际节点列表后顺序执行，一个失败保留部分报告并继续，取消停止后续节点。API `{}`、显式 `speed:false`、`network_only:true` 均有真实 HTTP handler 和 CLI 参数解析的 mock 回归。完整报告合并 summary、health、drift、network 与 entrances；GET 最新记录兼容旧网络报告。测量时节点地址保留，后续修改节点不会误写旧地址。

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
