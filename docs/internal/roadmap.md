# 路线图:分批演进

按「先不丢数据 → 可观测性 → 规模化 → 协议支持」排。每批独立可交付、可验证,做完一批提交一批。批 7、批 8 都是事后补的(前者是审计出来的漏项,后者由一次真实事故触发),不在原来的顺序里。

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

## 批 5:协议支持(已完成)

**为什么**:今天只有 vless / vmess / trojan × tcp / ws / grpc × none / tls / reality。缺的、且重要的:hysteria2、tuic(丢包网络刚需)、shadowsocks-2022、wireguard;传输层 xhttp / splithttp、httpupgrade;客户端特性 mux / fragment。

**注意**:这是加性的,但要同时动四层 —— 模型枚举、四个渲染器、节点侧配置渲染、测试。是产品决策不是运维决策,单独成批,不混进上面任何一类。

**结果**:加了 shadowsocks-2022 与 wireguard 两个协议、xhttp / splithttp / httpupgrade 三个传输、mux 与 fragment 两个客户端开关。hysteria2 与 tuic 不在 xray 26.3.27 的协议清单里,这一批不做(记 [open-questions.md](open-questions.md))。wireguard 的隧道地址按用户 key 哈希派生,服务器 peer 带 `allowedIPs`,理由见 [decisions.md](decisions.md) 第 29 条。

**验证**:每种新协议 × 传输 × 安全层组合过 `xray -test`,客户端实测出网。

## 批 6:性能与稳定性(已完成)

**为什么**:成本低、顺手、但都是真实损耗。订阅每次现渲染无缓存无 ETag 无 gzip;`/sub` 与 `/api` 无速率限制(泄露的 token 就是个查询接口);维护节点靠手改 `inbound_ids`,没有 drain / 容量上限;每次远端命令各付一次 ssh 握手(`node sync` 6 次、`node install` 14 次)。

**做了什么**:
- 订阅缓存 + ETag / If-None-Match + gzip —— `internal/server/subcache.go`,gzip 是缓存时压好的、每次响应只换头;
- `/api` 与 `/sub` 速率限制 —— `internal/server/ratelimit.go`,两个独立桶、`/sub` 松 `/api` 紧、按 RemoteAddr 分桶不解析 XFF;
- `mpx node drain / undrain` —— 订阅层的开关,不动 xray;`inbound --max-users` 同理,见 [decisions.md](decisions.md) 第 30 条;
- SSH `ControlMaster` 复用 —— `sshArgs` 加 `ControlMaster=auto` + `ControlPath=<TMPDIR>/mpx-ssh-%C` + `ControlPersist=60s`,一轮巡检的几条远端命令共用一条 TCP;
- `mpx node online` —— `xray api statsgetallonlineusers` 一次拿全在线名单,不落库不缓存。

**验证**:`gofmt` / `go vet` / `go test ./...` 绿;缓存命中、ETag-304、drain 后订阅少一台节点、`--max-users` 满后新订阅不再发该入站、`mpx node online` 拿到名单这几条都过了一遍。

## 批 7:补上改的能力(已完成)

**为什么**:做一次「API 与 CLI 的差集」审计发现的,不是口头提到才想起来。store 层与 server 层的增删改查**早就齐了**(`store.UpdateNode` / `UpdateInbound` / `UpdateHost` / `UpdateUser`,以及 `handleUpdateNode` / `handleUpdateInbound` / `handleUpdateHost` / `handleUpdateUser`),缺的只在 CLI:`node` / `inbound` / `host` / `user` 四类都只能增删不能改,四个 `PUT` 端点没有对应命令。结果是节点改名这种小事也得手搓 `curl` 拼一整个对象,而拼错的代价是把没写到的字段一起抹掉。

**做什么**:补 `node edit` / `inbound edit` / `host edit` / `user edit`。

**注意**:控制面的 `PUT` 是**整体替换**,所以命令必须读-改-写,而「这次到底改没改」只能靠 `cmd.Flags().Changed(name)` 判断 —— 空串和 0 都是有含义的值(`--limit 0` 是不限流量,`--expire ""` 是永不过期),拿零值当「没给」就永远改不回这些状态。`host edit` 另外要求 `--inbound`,因为控制面没有「按 id 读一个入口」的接口。

**结果**:四个命令都补上了,顺带修了 `user list` 的表头 —— 它列了 9 列却没有「可用」,而 CSV 导出一直有「可用性」,同一个用户在两处会对不上;「重置策略」的空值也归一成 `none`,与导出一致。`node edit` 改地址时会提示这会让订阅里没显式写地址的入口跟着变(`internal/render/ir.go` 里 `firstNonEmpty(h.Address, node.Address)` 的回退),`inbound edit` 改端口/传输/安全层时会提示要跑 `node sync` 才生效。

**验证**:新增 `internal/cli/edit_test.go`(假控制面记下每个请求):只发改过的字段、其余原样带回;能改回空状态;`host edit` 找不到目标时一个请求都不发、缺 `--inbound` 时同样不发;只有端口/传输/安全层变化才提示下发;表格的「可用」列与 CSV 对同一个用户给同一答案。`gofmt` / `go vet` / `go test ./... -race` 全绿。

## 批 8:CLI 配置档与原子写(已完成)

**为什么**:一次真实事故。为试一条命令,`mpx login` 被指向临时起的本机控制面,把用户**正在用的**地址与 token 覆盖了,没有任何提示,之后每条命令都打到那个临时服务上、看到的是一份假数据。第一反应的两条对策(「有旧配置就拒绝」「备份成 `.bak`」)都错:前者挡不住 —— 配置本来就是那次命令创建的,而且换控制面是 `login` 的正常用途;后者只在同机同用户下有用,换机器就没了。

**做什么**:配置文件能装多套(profile),写前原子替换(同目录临时文件 → 写 → fsync → rename),覆盖时回显「从什么变成什么」;只给 `--profile` 算切换(要求档已存在),不带参数只查看且不写文件;`--server`/`--token` 保持「只影响本次、不碰配置文件」。

**注意**:判断「这次改没改」用 `Flags().Changed()`,不是看值是不是零(空串和 0 都有含义)。`--profile` 必须注册成根命令的 persistent flag 且**只注册一次** —— 在同一层重复注册会被忽略,而且 `cmd.Flags().Changed("profile")` 恒为 false,得用 `InheritedFlags()` 查继承来的。

**结果**:`internal/cli/config.go` 重写(多档 + 迁移旧格式 + 原子写),`login` 加了切换与回显语义,`--profile` 成为全局 flag。理由与两条被否掉的对策见 [decisions.md](decisions.md) 第 31 条。

**验证**:`internal/cli/config_test.go`(9 个)+ `internal/cli/login_test.go`(6 个)。测试隔离必须用 `HOME` —— macOS 的 `os.UserConfigDir` 忽略 `XDG_CONFIG_HOME`。另外同一个测试进程里连跑多次命令时,cobra 的 `Changed` 标记和 persistent flag 的值会留到下一轮,调用前要显式清;真实使用是每次一个进程,不受影响。

## 批 9:一键路径 `mpx init`(已完成)

**为什么**:前面每一批都在把「控制面能做什么」做厚,而**第一次用它的人要做的事一件都没少**:装控制面、拿 token、装 CLI、开隧道、登录、纳管节点、建入站、建入口、下发、设订阅前缀、建用户、取链接 —— 十二步,分散在两台机器上,中间任何一步的报错都只是一个片段。真机跑通之后这条路径的样子已经清楚了,缺的只是把它合起来。用户的说法是最短的那版:「在客户机上 mpx init + vps 上的资料 user+password,然后在 vps 上安装好控制面和节点用户」。

**做什么**:单机全包(控制面与节点同一台 VPS)的一条命令:

```bash
mpx init --host <VPS IP> --ssh-password
```

它替用户跑完那十二步。分工是硬的,不是「顺手都塞进 init」:

- **`mpx init`(笔记本)** 只做 SSH 编排:探测对面是什么 → 上传脚本与可选二进制 → 跑 `install.sh --addr 0.0.0.0:8080` → 读 `/etc/mpx/env` 拿 token → 远端调 `bootstrap` → 透传输出。**它不碰数据库、不渲染配置、不认识节点**。
- **`mpx bootstrap`(VPS 侧,Hidden 子命令)** 才是建对象的那个:它是控制面本机的一个普通 `/api` 客户端,打 `127.0.0.1:8080`,token 自己从环境文件读 —— 所以 token **不进 SSH 命令行**,不进两端的 `ps` 与 shell 历史。

为此给 `internal/node` 加了带身份/密码的 SSH 入口(`Auth` / `RunAs` / `RunAsIO` / `PutFileAs` / `ShellQuote`),`deploy/install.sh` 加了 `--addr`,`internal/cli/client.go` 的 `doWithin` 拆出了重试循环。

**注意**:

- 这是 **CLI 里唯一一处会 SSH** 的地方,而第 5 条定过「只有控制面能 SSH」。它不是推翻那条边界,是被划出的精确例外 —— `init` 是**在控制面存在之前**运行的那条命令,装控制面的那一刻 `/api` 还不存在。理由与被否掉的两条替代路子(SSH 隧道映射 8080 回本机、纯 shell 版本)见 [decisions.md](decisions.md) 第 32 条。
- 默认值里 `--sni` / `--reality-dest` 取 `www.cloudflare.com` 而不是微软,是批 3 那条真机经验的直接后果(见 [status.md](status.md) 的「没验过的」一段)。
- **幂等是要求,不是附加**:重复跑要复用已有的节点/入站/入口/用户/订阅前缀,不能建出第二份。这条在实测里逐项验过。
- 密码只用于**第一次**装公钥(askpass 占着 stdin,没法同时给远端喂标准输入),之后全程走密钥。

**验证**:真容器端到端(Ubuntu 22.04 + systemd + sshd,从 macOS 连过去)跑完整条路径,并在节点侧与控制面侧分别复核(核心就位、服务 active、版本、配置里有 vless、`?fresh=1` 无漂移、订阅内容可用);重复跑验幂等。**这条路径上暴露了三个单测抓不到的真 bug**:macOS 的 `ControlPath` 超 unix socket 长度上限、`bootstrap` 的十几次请求撞掉 `/api` 限流桶、建用户因零值静默变成「停用」。三个的成因与修法见 [decisions.md](decisions.md) 第 33–35 条与 [status.md](status.md)。`gofmt` / `go vet` / `go test ./... -race` 全绿。

**没做的**:只在容器里跑过,没有在真正的公网 VPS 上跑过 —— 跨公网的 SSH 时延掉线、国内机器走 `--binary` 那条路、sshd 只允许密钥登录这三种情况还没有实测记录。分开部署与多节点仍然走手工步骤,没有为了「一键」把它们塞进同一条命令。

## 批次顺序为什么这么排

1. 批 1 优先,因为它**错了也看不出来**(其余批次你都知道自己缺);
2. 批 2 在批 1 之后,因为历史表是备份的一部分内容;
3. 批 3 与批 2 无依赖,可并行;
4. 批 4 依赖批 3 的状态数据(通知要基于它);
5. 批 5、批 6 独立,可插队;
6. 批 7 是审计出来的漏项,不属于「你早就知道自己缺」那一类,所以先做 —— 它卡的是日常操作,不是能力边界;
7. 批 8 由一次真实事故触发,不是规划出来的;它和批 7 独立,但同样卡日常操作;
8. 批 9 排在最后,因为**它依赖前面所有批**:一条命令要能装控制面(批 3 的装与更新)、要能装核心与下发(批 3 的节点状态)、要能验订阅(批 6)、要能建用户(批 4)。它是把已有的能力收成一个入口,不是新增能力 —— 所以只能等它们都在。它和批 8 都在处理「人第一次/日常怎么用」,但批 8 修的是一个会静默发生的事故,批 9 是把一条走通了十二步的路收成一步。
