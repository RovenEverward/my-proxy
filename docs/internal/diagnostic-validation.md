# 一键体检验证记录

Issue #11 将 `mpx node diagnose ID` / `--all` 的 node 默认引擎改为官方 Go ECS 融合怪 v0.2.12，保留健康、漂移与全部获授权入口检查。本页区分已完成的官方程序样本与 issue #7 的轻量引擎证据。官方测速仍未在默认预算内完成，下面分别记录基础组件、实际 SSH 流程、独立补测和输出边界的证据，不将部分报告宣称为全模块成功。

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

原始适配器曾将 283674 字节真实 JSON fixture 缩到 20447 字节，虽然保留七个组件的名称、schema 和状态，却过早删除了大量结果行。生产首节点也复现：结构化数据仅 37482 字节，却只留下 32 条媒体、40 条邮件、一个风险供应商和一个回程目标，尚未用满 128 KiB。

修复后先按全局实际 JSON 大小决定是否精简。对固定版本和已知 schema，仅移除目录、DNSBL 逐探针、RDAP、对等方列表、NT3 逐次样本和邮件重复 kind；保留结果行、错误全文、地区、分类依据、统计值与原单位。`payload_omissions` 逐字段记录原大小和条数，`adapter_compacted` 标明语义精简。异常超限时透明省略整个载荷，保留组件状态与省略原因，不再递归截断名字或错误。

同一真实七模块样本按 Go 编码为 137250 字节，修复后为 67133 字节；360 条媒体结果、205 条邮件关键字段、八个供应商与 DNSBL 计数、12 条回程结果均由深相等检查核对。九模块合成压力样本含 200 条 NT3 和 60 条测速，220771 字节压至 128858 字节，保留全部核心行。后者是合成预算验证，不是九模块生产报告。Fusion、控制层、server、CLI 的 race 检查以及 vet、构建通过。已丢失的生产详情无法从旧报告恢复，需要新测量。

上述证据覆盖 Docker 中的适配器和两次取消清理，尚未证明真实 SSH 断开后的清理。默认测速开启的完整命令、全部实际代理入口、可选硬件和生产验收仍需独立记录；未据此发布、部署或声明生产通过。

### 测速分阶段与证据保留

生产旧单次全模块调用出现两类问题：首节点 816.6 秒返回部分数据，日本节点 873.6 秒后返回合法 `canceled` JSON，但全部类型化组件缺失。上游测速与其他模块共用完成通道，内部取消可能丢掉已经完成的模块，不能将退出码或 JSON 合法当作验收成功。

适配器改为同一总预算内先执行非测速模块、再执行测速；新测量仅合入固定 schema 的 `speed.registry` 和 speed section。基线已保留的载荷优先于新增测速，超出预算先省略新增测速展开数据，再省略整个新增测速载荷，并显示理由。阶段时间、错误与上游状态放在 `adapter_stages`，合并标志 `adapter_composed` 与上游报告区分。

真实官方 Docker 测试在首阶段完成后运行第二阶段，八秒后取消实际测速进程。初次验证总耗时 42.54 秒，适配器耗时 41.402 秒，返回 canceled 和 context cancellation；保留 120117 字节基线结构化数据，其中媒体 346 条、邮件能力 102/205 可用，风险和回程原有不可用状态保留。未残留 goecs 或临时目录。

最终 `TestFusionOfficialStagesDocker` 两轮共 97.498 秒：调用方取消轮耗时 44.0505 秒，保留 120215 字节数据；60 秒总预算自然结束轮耗时 52.0401 秒，保留 120487 字节数据。自然结束时官方测速仍 canceled 且未给出测速组件，封装保留明确失败原因，首阶段媒体数据不丢失。两轮均验证无残留进程和目录。这是取消与预算耗尽时保留证据的验证，不证明生产测速完成。

126000 和 130700 字节基线加 200 KiB 新测速的预算测试核对基线载荷字节完全相等。数字预算处理使用 `UseNumber`，核对 `9007199254740993` 和高精度小数在普通与语义精简路径保持原值。CLI 显示各阶段状态和省略清单，整体超时不会被基线成功改写成整体成功。

### 官方测速缺失时的独立补测

真实分阶段首节点保留八个基线组件：360 条媒体结果、205 条邮件能力、93 条省份延迟、IP 风险、回程与 NAT。基础阶段 42.195 秒；测速阶段 769.583 秒后官方 canceled，没有测速组件。第二节点也保留八个基线组件。固定版本上游等待全部测速组完成才发布结构化结果，降低站点数不能保证截止前返回数据。

默认将每运营商成功目标从两站降为一站，减少工作量。补测仅针对合法官方报告缺少有效吞吐记录、测速开启且用户未取消的情况；原 Fusion 截止时间、外层截止与当前加 50 秒取最早值。两站各下载/上传 1 MiB，8 MiB 应用数据预算，不运行 IP、路由或其他诊断。独立 `network` 保留来源与失败，Fusion 的原始字节、状态、错误均保持原值。

本地真实 curl fixture 验证四个请求、失败继续、取消与目录/连接清理；控制层模拟有界 SSH 验证同一捕获地址、SQLite 可用、正数有效测速跳过、时间耗尽与 speed 关闭跳过，以及部分结果不覆盖原 Fusion 报告。没有将 fixture 当作生产吞吐证据。

### 四节点生产验收与报告传输

2026-10-11 在已管理的四个 Linux amd64 节点上执行一次原生 `mpx node diagnose --all -o json`，分阶段适配器版本 `72d7c21`。四节点各保留八个基础组件，基础耗时约 42.2、76.5、54.4、42.5 秒；官方测速均在约 812 秒总执行后 canceled，没有有效测速组件。每条已保存报告约 113–122 KiB，健康正常且部署配置一致；八个实际入口的出口与 YouTube 可用，Netflix 解析未知。节点直连媒体结果不能代替入口结果。

原命令最终返回四条“诊断响应超过 260 KiB”，原因是通用 HTTP JSON 缩进将嵌套报告膨胀到接收上限以外；测量与数据库保存已完成。诊断接口改用紧凑 JSON，并关闭只针对 HTML 的转义，保持 JSON 语义、256 KiB 存储与 CLI 响应上限。回归覆盖真实 handler、嵌套媒体/邮件/省份延迟、部分错误报告及接近存储上限的字符，读取已有记录验收，不重新测量。

随后生产升级 `9535d6e`，用只读一次性 harness 以生产 mpx 用户调用同一 `diagnostics.RunSpeedFallback`，四节点各上下行两个站点，共 16 个方向成功、每方向 1 MiB。它是独立补充验收，不改写 Fusion 记录、不冒充已完成的官方测速或新版本完整原生命令。控制层回归覆盖自动补测编排；实际 HTTP 来源、速率和时限保留在补充 JSON。

升级后所有节点、入站、主机、settings、用户配置与环境一致，累计流量单调；SQLite quick_check 为 ok，六种订阅 HTTP 200，未重启代理核心。完整报告与独立补充数据分别保存，官方错误与取消状态继续保留。

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
