# 路线图:分批演进

按「先不丢数据 → 可观测性 → 规模化 → 协议支持」排。每批独立可交付、可验证,做完一批提交一批。

排队的依据:有些缺口是**错了也看不出来**(流量静默丢账),有些是你**知道自己缺**但没时间做(备份、分页),有些是**不做就落后**(协议支持)。前者优先。

## 批 1:用量与计量(已完成)

**为什么**:动手前,采样是 `statsquery -reset`(读走即清零),而 xray 的计数在进程内存、没有持久化。节点重启或 xray 崩一次,两次采样之间(以及重启后到下次采样前)的用量从零重计,`used_traffic` 累加的是「从重启那一刻起」的量 —— 重启前没来得及采的账**静默消失**,而且事后分不清「用户没用」和「账被吞了」。

**做什么**:
- 采样从「读走即清零」改成「读累计值,自己存上次值算差」;
- 差值出现负数(累计值变小 = xray 重启过)→ 记为异常,打日志,不静默;
- 新增 `traffic_samples` 历史表(用户 × 节点 × 时刻 × 累计值),用量可重算、可画曲线、重启可被检测;
- 上次累计值持久化在历史表里(不需要单独的状态字段)。

**涉及**:`internal/node/xrayapi.go`、`internal/control/control.go`、`internal/store`(新表 + 迁移)、`internal/model`(如需新结构)、相关测试(含 `live_test.go`,它断言 `-reset` 语义,会破,要改成断言「累计值两次读不变」)。

**验证**:单测覆盖差值计算、重启检测、历史落表;live 测试改为累计语义;`go test ./... -race` 全绿。

**结果**:已按上面做完。`traffic_samples` 里该用户在该节点的最近一行就是「上次值」;读数回退按 xray 重启处理(记日志,该次读数成为新基准)。见 [decisions.md](decisions.md) 第 9 条。

## 批 2:备份与恢复(已完成)

**为什么**:`users.key` 是派生 UUID/密码的**种子**,只存在 SQLite 里。库没了 = 所有人的凭证都复现不出来,必须重配。现在没有 `mpx backup`、没有导出、没做过恢复演练。

**做什么**:
- `mpx backup`(导出到文件,含 schema + 数据)、`mpx restore`(从文件恢复,支持重建);
- 恢复演练写进验证记录。

**验证**:备份 → 删库 → 恢复 → 用户/节点/入站全在,订阅 token 不变,能出订阅。

**结果**:已按上面做完。备份是 gzip 过的 tar(`manifest.json` + 一致快照 `mpx.db` + 可选的系统 ssh 私钥 `id_ed25519`,**不含管理 token**,理由见 [decisions.md](decisions.md) 第 16 条);快照用 `VACUUM INTO`(实测 modernc.org/sqlite 支持,不锁写、不用停服务)。恢复走「校验 + 暂存」:`POST /api/restore` 只把校验过的库写成 `<db>.restore`,真正换入在 `Serve()` 打开库之前——旧库改名 `<db>.pre-restore-<UTC 时间戳>` 留着回退,没有控制面也能恢复(手动把 `mpx.db` 改名成 `<db>.restore` 再启动)。验证:备份→删库→手动放 `.restore`→启动→节点/入站/用户全在、订阅 token 不变、能出订阅(见 `internal/server/backup_test.go` 的 `TestRestoreFlowOfflineSwap`)。

## 批 3:可观测性(已完成)

**为什么**:`node list` 看不出 xray 是否在跑、最后一次成功接触、核心版本、证书还有几天到期、配置有没有被人在节点上改过(漂移);节点挂了唯一的传感器是用户来骂;一个死节点(远端命令无超时,`ConnectTimeout=10` 只管握手)能把整轮周期任务拖到永久落后。

**做什么**:
- `node status`:每节点状态面(可达、xray active、核心版本、配置漂移、证书到期天数);
- `mpx doctor`:一条命令跑完控制面/节点/订阅/采样全链路自检;
- 远端命令超时(每命令独立超时,单节点失败不拖垮整轮);
- 告警通道:节点失败、控制面失败、证书临期 —— 至少先落日志汇总,接口留好。

**涉及**:`internal/node`(超时)、`internal/control`(状态采集)、`internal/cli`(`node status` / `doctor`)、`internal/server`(状态 API)。

**验证**:真机跑 `node status` / `doctor`;造一个死节点看整轮不被拖垮。

**结果**:三部分都做完了。

- **不被拖垮**(3a):每条远端命令都有 deadline(调用点按操作给预算,`node` 层兜底 15 分钟,调用方自带更短的 deadline 时不覆盖),超时和连不上分开报;失败节点按 30 秒起步、5 分钟封顶退避,窗口内跳过且只记一条日志,成功清零;控制面心跳经 `GET /api/status` 和 `mpx status` 读出来,每轮超过周期打 Warn。
- **状态可见**(3b):检查一台节点用**一条** ssh 脚本读全四样(服务在不在跑、核心版本、配置 sha256、每张证书的到期时间),结果进 `node_status` 表;下发成功才在 `nodes` 上记基准哈希,两者一比对出漂移。`GET /api/nodes` 与 `mpx node list` 读缓存(一次 ssh 都不发),`?fresh=1` 与 `mpx node status` 现场查。
- **自检与告警**(3b):`GET /api/doctor` / `mpx doctor` 跑控制面、每台节点(走的就是 `?fresh=1` 那条路)、六种订阅格式、采样表,有 fail 时退出码 3;周期任务每 10 个 Tick 巡检一遍节点,`LogAlerter` 按 Key 冷却 30 分钟去重、条件消失打「已恢复」,Webhook 留批 4。

验证为单测(含假时钟、假 `ssh` 脚本、`xray -test` 真二进制);**真机演练还没做** —— 控制面连节点的私钥只在控制面那台机器的 `/var/lib/mpx/.ssh/id_ed25519`,本机没有,要等下一次上真机时补(见 [status.md](status.md))。做法上有三处按复核结果改了子 agent 的实现:证书告警的 Key 以前对不上、永远销不掉;巡检第 1 个 Tick 不跑且整轮无上限;`doctor` 的结论会被检查顺序决定。理由见 [decisions.md](decisions.md) 第 20–23 条。

## 批 4:规模化(已完成)

**为什么**:从 100 到 1000 用户的分水岭不在这批的任何单项,而在这批的合集:每个用户的问题都要手工敲一条 CLI,就卡在 100 这个量级。

**做什么**:
- 分页与筛选(`user list` 到一千人就是刷屏);
- 批量操作:CSV 导入导出、批量启用/停用/续期;
- 订阅页:能看到用量、到期日、流量条的 HTML 页(注意:这是产品组成部分,不是运营 UI,和「不做 Web UI」不冲突);
- 通知:快到期、快超限、节点故障(至少邮件/Webhook 接口);
- 自助:用户自己取链接、换 token。

**结果(4a 已完成)**:列表分页与筛选(`?limit=&offset=&status=&search=&inbound_id=`,响应给筛选后的 `total`;筛选复用 `model` 里那份唯一的状态与授权判定,不翻写成 SQL,理由见 [decisions.md](decisions.md) 第 24 条);`rm` / `enable` / `disable` / `renew` / `links` 收多个用户名、逐个执行并汇总成败;`export` / `import` 走 CSV,逐行独立提交,理由见第 25 条。**4b(订阅页 + 自助换 token)已完成**:同一个订阅地址,浏览器打开是订阅页(状态、流量、到期、各格式分享链接、换地址按钮),客户端照旧拿配置 —— 页面只在「没人认领的默认」这一步且 `Accept` 点名 `text/html` 时出现,理由见第 26 条;`POST /sub/{token}/rotate` 换 token,旧地址当场 404。**4c(通知)已完成**:到期与超限提醒按用户 × 类别记账(`user_notices`)只发一次,发出去了才记、条件消失就销,阈值写 0 表示关,理由见 [decisions.md](decisions.md) 第 27 条;节点故障复用告警那条线(冷却 + 「已恢复」),与 `LogAlerter` 一起走 `FanoutAlerter`;`mpx settings show` 打出通知地址与两个阈值,`mpx settings test` 往配置好的 Webhook 发一条测试事件(`POST /api/settings/test-webhook`)。

**验证**:1000 个用户的列表与批量操作耗时达标(宽松上限);订阅页与换地址真机验证过(浏览器拿页面、客户端拿配置、旧地址 404、新地址可用、`Profile-Web-Page-Url` 正确);通知全在单测里验(假 webhook 服务端):同一件事只发一次、续费/重置后能再发、发失败不记账且下一轮重试、没配地址不发也不记账、停用用户跳过、设置校验与测试接口的 200/400/502。

## 批 5:协议支持(产品决策,单独成批)

**为什么**:今天只有 vless / vmess / trojan × tcp / ws / grpc × none / tls / reality。缺的、且重要的:hysteria2、tuic(丢包网络刚需)、shadowsocks-2022、wireguard;传输层 xhttp / splithttp、httpupgrade;客户端特性 mux / fragment。

**注意**:这是加性的,但要同时动四层 —— 模型枚举、四个渲染器、节点侧配置渲染、测试。是产品决策不是运维决策,单独成批,不混进上面任何一类。

**验证**:每种新协议 × 传输 × 安全层组合过 `xray -test`,客户端实测出网。

## 批 6:性能与稳定性

**为什么**:成本低、顺手、但都是真实损耗。订阅每次现渲染无缓存无 ETag 无 gzip;`/sub` 与 `/api` 无速率限制(泄露的 token 就是个查询接口);维护节点靠手改 `inbound_ids`,没有 drain / 容量上限;每次远端命令各付一次 ssh 握手(`node sync` 6 次、`node install` 14 次)。

**做什么**:
- 订阅缓存 + ETag / If-None-Match + gzip;
- `/api` 与 `/sub` 速率限制;
- `node drain`(维护前挪走用户)、每节点/入站容量上限;
- SSH 连接复用(`ControlMaster` / `ControlPersist`,代价:连接状态要管);
- 在线/并发视角:xray `StatsService` 有 `GetStatsOnlineIpList` / `GetStatsOnlineUsers`,数据已经在手上只是没取,这是做并发限制、判断账号共享最便宜的一步。

**验证**:压测订阅缓存命中;真机 drain 一个节点看用户被挪走;在线用户数能查出来。

## 批次顺序为什么这么排

1. 批 1 优先,因为它**错了也看不出来**(其余批次你都知道自己缺);
2. 批 2 在批 1 之后,因为历史表是备份的一部分内容;
3. 批 3 与批 2 无依赖,可并行;
4. 批 4 依赖批 3 的状态数据(通知要基于它);
5. 批 5、批 6 独立,可插队。
