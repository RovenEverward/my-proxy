# 现状:做到哪、验到什么程度

**能用。** 从配置渲染、下发回滚、xray API 增删用户与流量统计,到六种订阅格式和 User-Agent 规则,都在真实 xray 上跑通过;并且在两台真机上端到端跑通了「装核心 → 下发 → 订阅 → 真实出网流量 → 加人不断别人的连接」。

## 验证现状

| 项 | 怎么验的 |
| --- | --- |
| 节点侧配置被真 xray 接受 | 8 种协议×传输×安全层组合,逐份 `xray -test -config`,得到 `Configuration OK.` |
| 节点层四个 API 操作 | 起真 xray,加人 → 客户端穿过节点取到源站 → 读到 `uplink=78 downlink=1919` → 摘人(批 1 之前还验过「再读清零」;改成读累计值后 live 断言已改,本机无 xray 未重跑) |
| 下发与回滚 | 真跑那条 shell,确认备份内容、坏配置被拦、原文件回滚、校验失败不重启 |
| 六种订阅格式 | 活服务上逐种取回,Content-Type 与内容都用真解析器核对 |
| xray 订阅可用 | 每份配置单独喂给真 xray `-test`,全部通过 |
| User-Agent 规则 | 四种 UA 实测;改成 `block` 后同一 UA 得到 404 |
| 四类用户状态 | `user list` 打出 active / expired / limited / disabled |
| 到期语义 | `--expire 2030-01-01` → `2030-01-01T15:59:59Z` |
| 换订阅 token | token 变、`key` 不变 |
| 空 token + 非回环 | 启动被拒,退出码 1 |
| 控制面安装脚本 | 真机重复跑:二进制换掉、`/etc/mpx/env` 与私钥的 md5 不变、单元文件留 `.bak`、用户数据还在;坏二进制被拒且原文件保住;非 root 被拒 |
| 经 ssh 打远端机器 | 两台 Ubuntu 24.04 真机,`BatchMode=yes` 免密,控制面 `HOME=/var/lib/mpx` |
| `node install` | 裸机器到能用一条命令;再次跑命中缓存不下载,三个文件的 mtime 一个都没动(内容比对生效);旧核心 `/usr/local/bin/xray` 一个字节没动,配置与订阅内容前后 md5 一致 |
| 建用户的 `enabled` 三态 | 真机 curl:不带 `enabled` 建用户得到 `active`;停用后只改流量上限的一次 PUT 仍是 `disabled`;显式 `enabled: true` 才回到 `active` |
| 真实出网 | 从本机走两个节点的订阅配置,出口 IP 分别是两台节点,`generate_204` = 204;流量落到 `user list` |
| 加人/摘人不重启 | 加人后不 `node sync`,等 45 秒:节点上 `config.json` 的 `%y %s` 前后完全一致;停用/启用后同一份旧客户端配置先失效又恢复,而 `MainPID` 与 `NRestarts` 全程不变 |
| 用量与计量 | 单测覆盖差值(首次采样 / 正常 / 计数回退)、重启记日志、历史落表、节点不可达不写历史;live 断言由「读走即清零」改成「累计值只增不减」,本机无 xray 未重跑 |
| 备份与恢复 | 单测:造有数据的库 → 建归档 → 解归档 → 数据在、manifest 行数对;四类坏输入全报错且不留 `.restore`;HTTP 侧 `GET /api/backup` 能解出数据、`POST /api/restore` 合法 200 且 `.restore` 存在、非法 400 且不存在、已暂存再传不带 force 拒绝带 force 覆盖;换入函数预置「另一份库」→ 主库变快照数据、旧库留 `<db>.pre-restore-*`、`.restore` 没了;全流程演练(备份 → 删库 → 手动放 `.restore` → 启动 → 数据全在、订阅 token 不变、能出订阅) |
| 远端命令超时 | 单测:假 `ssh` 脚本睡 30 秒,200ms 的 deadline 把它打断并报「命令超时(节点卡住了)」,耗时远小于脚本本身;ctx 自带 150ms 的 deadline 优先于 `SSH.Timeout`;脚本模拟 `Connection refused` 时仍报 `ssh` 原话、不含「命令超时」;零值兜底不小于 10 分钟 |
| 失败退避与心跳 | 单测(假时钟):失败一轮后窗口内不再调用 runner,「在退避中」只记一条;等待 30s → 1m → 2m → 4m → 封顶 5m,窗口过后重试;成功后清零且日志说「退避清零」;一轮耗时超过 interval 打 Warn;未跑过时 `last_tick_at` 为空、`ticks=0` |
| `GET /api/status` / `mpx status` | 单测:响应 16 个字段齐全、启动时间/最后 Tick/耗时/计数/退避逐个对得上,没接后台任务也 200 且给 `backoffs:[]`,无 token 与错 token 均 401;CLI 用假服务端走完整命令链,表格含心跳各字段与退避行 |
| 节点状态与漂移 | 单测:哈希取不到走哨兵(`${hash:-hasherr}`,不是整条管道的 `\|\|`)、`openssl` 的退出码真的被带出来(127 = 没装 openssl,别的非 0 = 文件不是证书)、证书按入站 ID 排序而不按输出先后、证书带上入站名;列表带缓存状态且**一次 ssh 都不发**、`?fresh=1` 查一次并落库、紧接着的读看到的就是它;没查过时 `status` 为 `null`(不是「没问题」);漂移的四种答案(未下发 / 未知 / 是 / 否)逐一断言 |
| `mpx doctor` | 单测:健康节点判 ok 且整体无 fail;从没下发过判 **warn** 而不是 fail;连不上判 fail 且带一句下一步;一台节点只出一行结论,结论取最重的那条(不受检查顺序影响);退出码 —— 有 fail 是 3、只有 warn 是 0 |
| 告警与巡检 | 单测(假 Alerter):报出去的 Key 在条件消失后被销;不可达那一轮**只加不减**(网络抖动不会留下「已恢复」);节点恢复后所有 Key 在同一轮里销掉;`LogAlerter` 冷却期内只报一次、窗口外再报、没报过的 Key 销不掉任何东西;巡检第 1 个 Tick 就跑、之后每 10 个 Tick 一次;整轮预算用完后剩下的留到下一轮并记日志 |
| 提醒(批 4c) | 单测(假 webhook 服务端):到期提醒只发一次、续费销账后再临近能重发、超限提醒与周期重置同理、没配地址不发也不记账、投递失败不记账且下一轮重试成功、停用用户跳过;`PostWebhook` 2xx 算成功、非 2xx 带状态码报错、空地址报错;`WebhookAlerter` 冷却内只发一条、恢复发 resolved 且带回原告警、没报过的 Key 不销;`FanoutAlerter` 每个出口都收到;设置校验(负天数 / 百分比越界 / 非 URL / 非 http(s) / 缺主机名全 400);`POST /api/settings/test-webhook` 没配地址 400、配好 200 且对面收到 `test` 事件、对面 502 时如实回 502。活服务上端到端走了一遍(真 webhook 接收端):`settings test` 收到 `test` 事件、alice 只剩 2 天时收到一条 `user_notice` 且后续几轮不再重复、续期到明年后销账、再改回临近重新收到一条;对面挂掉时那条只进日志(`发用户通知 webhook 失败 user=alice kind=expiring`)不记账,对面起回来后下一轮重试成功 |
| 旧备份兼容新表 | 单测:造一份 `DROP TABLE node_status` 的快照(等于批 3 之前备份出来的库),`Validate` 仍然通过,`store.Open` 会把表补出来并能写状态 —— 恢复不能卡在「备份里没有新表」上。`user_notices` 刻意不在必需表清单里:它是流水账,没有它照样起来 |
| 订阅缓存 + 速率限制(批 6) | `GET /sub/{token}` 渲染一次之后进 `subCache`;同 token+format 第二次命中缓存;任何一条管理写入(节点/入站/host/用户/设置/轮换 sub token)都会全清缓存;`If-None-Match` 命中 ETag 返 304;`Accept-Encoding: gzip` 拿 gzip 压缩缓存 —— gzip 数据是缓存时压好的,每次响应只换头;`/sub` 与 `/api` 各有独立桶(`/sub` 10 qps burst 30、`/api` 5 burst 10),按 RemoteAddr 分桶不解析 `X-Forwarded-For`(前面没有可信代理,认了等于把限流钥匙交给请求方) |
| 排水与容量(批 6) | `mpx node drain` 置 `draining`,订阅里不再发这台节点的入口,`undrain` 恢复;`inbound --max-users N` 后占用满 ≥ N 的入站不进订阅;占用数按「active + 授权该入站」算,授权为空对每个入站都记。排水/上限不动 xray 现状,已连上的等它自然漂走,要硬摘用收敛或 `node rm` |
| `mpx node online` | 直连 `xray api statsgetallonlineusers` 一次拿全,不遍历每个用户发 `statsonlineiplist`;不落库不缓存 —— 答案下一秒就过期 |
| SSH ControlMaster | `sshArgs` 加 `-o ControlMaster=auto -o ControlPath=<TMPDIR>/mpx-ssh-%C -o ControlPersist=60s`,一轮巡检的几条远端命令共用一条 TCP,不再每次各付一次握手 |
| 四个 `edit` 命令 | 单测(假控制面记下每个请求):只发改过的字段、其余原样带回;能把字段改回空状态(`--limit 0` / `--expire ""` / `--address ""` / `--alpn ""`);`host edit` 从入站列表里找到目标、找不到时一个 `PUT` 都不发、缺 `--inbound` 时连请求都不发;只有端口/传输/安全层变化才提示下发;表格的「可用」列与 CSV 的「可用性」列对同一个用户给同一答案,空的重置策略归一成 `none` |
| 配置档与原子写 | 单测:`HOME` 指到临时目录 —— **macOS 的 `os.UserConfigDir` 忽略 `XDG_CONFIG_HOME`,直接读 `$HOME/Library/Application Support`,所以隔离只能靠 `HOME`**(第一次用 `XDG_CONFIG_HOME` 隔离无效,写到了真实配置上,已恢复)。覆盖:旧格式自动折成 `default` 档、读操作不改文件、保存不再写回旧字段、目录里不留临时文件、文件 600/目录 700、挑错档名会列出现有档、档名排序稳定、`--profile` > `MPX_PROFILE` > 文件 `current`、`--server` 不碰配置文件;命令级:存第二个档不动第一个、只给 `--profile` 是切换、切到不存在的档报错且不建档、不带参数只看且不写文件、覆盖时回显地址/token 变化且没给的项保留 |
| `mpx init` 一键路径 | **真容器端到端**(Ubuntu 22.04 + systemd + sshd,从 macOS 跑):装控制面 → 公钥进本机 `authorized_keys` → 建节点/入站/入口/订阅前缀/用户 → 装 xray 并下发 → 打印订阅。节点侧复核:`/usr/local/lib/mpx/{xray,geoip.dat,geosite.dat}` 就位、`systemctl is-active mpx-xray` = active、`xray version` = `26.3.27`、`/etc/mpx/xray/config.json` 里有 vless;控制面侧 `GET /api/nodes/1?fresh=1` → `reachable:true, active:true`,且 `observed_hash == deployed_hash`(**无漂移**)。**幂等**:第二次跑每一步都走「已经在了,复用它」,不建第二份。订阅内容实测:`/sub/<token>/links` 给出可用的 `vless://...@<IP>:443?security=reality&type=tcp&flow=xtls-rprx-vision&sni=www.cloudflare.com&pbk=...&sid=...`,clash-meta 正常带出规则源 |
| `mpx init` 的**真实用户路径**(纯发布件) | 上一条用的是本机编的 `--binary`;这一条把控制面**彻底清空**(删 `/etc/mpx`、`/var/lib/mpx`、两个 unit、`/usr/local/bin/mpx`、`authorized_keys` 里的公钥)之后,改用**从 Releases 下的 CLI**、**不带 `--binary`** 重跑:VPS 自己从 `raw.githubusercontent.com` 取 `install.sh`、自己从 `releases/latest` 下二进制(`curl` 报 `Downloading ...`)、自己装好。跑完同样复核:节点 `reachable/active`、`observed_hash == deployed_hash`、订阅 `links`/`auto`/`clash` 全 200、重复跑逐项「已经在了,复用它」。**这一条顺带证明了发布件里确实带着 `bootstrap`** —— 否则 `init` 会在「装上的 mpx 不认 bootstrap 子命令」那一步停下。它覆盖了本机上传脚本那条路**之外**的另一个分支:仓库不在手边时由 VPS 自取 |
| 这条路径上踩到的三个真 bug | 都是端到端才暴露、单测抓不到的:①**macOS 上 `ControlPath` 太长**(`os.TempDir()` 五十多字符 + 40 字符散列键超 unix socket 104 字节上限,报 `unix_listener: path ... too long`),改用 `/tmp`,测试按展开长度验上限(decisions #34);②**bootstrap 的十几次请求撞 `/api` 限流**(5/秒、突发 10,几十毫秒内必然打空桶),修在客户端重试而非放宽桶,重试安全依据是路由顺序 `requireAuth(rateLimit(api))`——限流在业务之前,429 表示没落库(decisions #33);③**建用户静默变成「停用」**(`model.User.Enabled` 裸 bool 无 `omitempty` → 发 `"enabled":false` → 服务端 `*bool` 读成显式停用,订阅打开只有一行「订阅已停用」,而命令看起来完全成功),改成显式 `true` 并在建完检查返回值(decisions #35) |
| 全量 | `gofmt` 干净、`go vet` 通过、`go test ./... -race` 九个包全绿 |

生产代码 16635 行,测试 14841 行。

**没验过的**:`mpx init` 只在**本机 docker 容器**里端到端跑过(Ubuntu 22.04 + systemd + sshd,从 macOS 连过去),没有在一台真正的公网 VPS 上跑过 —— 因此「跨公网 SSH 的时延与掉线」、「国内机器连不上 raw.githubusercontent.com 时走 `--binary` 那条路」、「VPS 上 sshd 关掉了密码登录、只能用密钥走 `--identity`」这三件事还没有实测记录(第三件的密码路已在容器里验过,密钥路只有单测)。批 3b 的**真机演练**仍然没做 —— 在两台节点上跑 `mpx node list` / `node status` / `doctor`,并造一个死节点看整轮不被拖垮。控制面连节点的私钥只在控制面那台机器的 `/var/lib/mpx/.ssh/id_ed25519`,本机没有,所以这一批的验证全在单测里;测试用的真 xray(`26.3.27`)是从 GitHub release 取回放在 `/tmp/xray-bin/`,用 `MPX_XRAY_BIN` 指过去跑。另外经跳板机连节点没做过(见 [open-questions.md](open-questions.md) 第 1 节)。REALITY 从本机所在的网络到其中一台节点的路径不通 —— 做过对照实验,用的是节点上自产密钥的最小 REALITY 服务端,本机自连成功、从我的网络过去连连接记录都没有,所以那是链路问题,不是渲染问题。附带一条经验:**`reality_dest` 要挑节点自己真能连上的域名**,同一个节点上 `www.microsoft.com:443` 连自连都失败,换 `www.cloudflare.com:443` 立刻好 —— 这也是 `mpx init` 默认值选 cloudflare 的原因。

## 两个教训

### 一、漏项在实现末段才暴露

有一批的存在本身就是证据:目标里明写的 xray 订阅格式,在设计阶段因为「用户口头没提」被砍掉了,直到全部实现完才回头补。

**对策**:能力清单要按「目标里的每一项」逐条对,而不是按「讨论中提到的」。这就是下面那张对照表的来源。

## 目标逐项对照

拿立项时的目标文本逐项对,避免「口头说的」和「写下来的」漏掉一边。

| 目标里的说法 | 状态 | 说明 |
| --- | --- | --- |
| 服务端持有 node / inbound / host / user / 订阅规则 | 已实现 | 五类对象各一张表,外加流量采样表;见 [model.md](model.md) |
| 渲染并托管 Clash 订阅 | 已实现 | 含 Clash Meta;meta 版走完整规则模板 |
| 渲染并托管 sing-box 订阅 | 已实现 | 内嵌模板:112 个远程规则集(sing-geosite/sing-geoip)+ 主题分组;规则集直连下载、域名本机解析,节点挂了也能启动(见 decisions.md #28) |
| 渲染并托管 xray 订阅 | 已实现 | 见教训一:末段才补 |
| 分享链接与 base64 | 已实现 | |
| 按 User-Agent 返回对应客户端格式 | 已实现 | 另支持路径指定格式、`block` 返回 404 |
| 配额 | 已实现 | `data_limit` + 状态判定 |
| 到期 | 已实现 | 解释成当地当天 23:59:59 |
| 流量统计 | 已实现 | 节点 `statsquery` 读累计值 → 与上次采样算差后落库;读数落 `traffic_samples` |
| 面向数百至上千用户 | 已实现 | 单库单进程;每轮开销与节点数成正比,与用户数无关 |
| 本机操作的 CLI 客户端 | 已实现 | |
| 不复用 Marzneshin 代码 | 已实现 | 全新 Go 实现,只在模型与字段命名上参考 |
| **操作订阅的 Clash 与 sing-box 规则** | **已实现** | 整份模板取改传(`settings template`),粒度已定 |

### 二、边界值没定义会变成不报错的空转

`reset_strategy` 的取值没定义过「认不出怎么办」。实现出来是:

```
ResetPeriod("") 返回 0
  → 每轮「推进」完还是同一时刻
  → 后台任务死循环、永不返回、不报错
  → 采样与收敛全部静默停摆
```

修法是三层一起:

- **模型层**让非法值不参与轮转(`NextResetAt` / `AdvanceReset` 认不出就返回原值);
- **入口层**把空串归一成 `none`,把认不出的值拒绝掉;
- **测试**三层各加一条。

**对策**:枚举、策略、状态类字段,设计时就要写明「认不出的取值怎么办」。

## 实现批次

按这个顺序做的,记录在此以便判断后续批次怎么排。

| # | 批次 | 说明 |
| --- | --- | --- |
| 1 | 模型层 | 枚举、结构体、状态判定、密钥派生、默认值。纯逻辑,无 IO,先做。 |
| 2 | 渲染层 | 中间表示 `Endpoint` + `Resolve`,再在其上做 links / clash / sing-box。同样是纯函数。 |
| 3 | 节点层 | SSH 通道、`config.json` 下发与回滚、xray api 的四个操作。契约是拿真 xray(26.3.27)钉死的,不是照文档写的。 |
| 4 | 存储层 | SQLite、五张表、CRUD。 |
| 5 | 控制层 | 收敛决策抽成纯函数,再往上包周期任务。 |
| 6 | 服务层 + CLI | HTTP 接口与命令行。 |
| 7 | 补 xray 订阅格式 | 见教训一。 |
| 8 | 测试与收口 | 补 control / node 两个包的测试,修掉教训二那个缺陷。 |
| 9 | 装与更新 | 真实机器跑起来之后才暴露的一批:节点上的核心从哪来(`node install` + `internal/xrayrelease`)、控制面自己怎么装和更新(`deploy/install.sh`)、节点上占自己的一份路径与服务名、下发之后确认服务真的起来了。 |
| 10 | 备份与恢复 | 批 2:`mpx backup` / `mpx restore`,归档 = manifest + 一致快照 `mpx.db` + 可选系统 ssh 私钥,不含管理 token;恢复 = 校验 + 暂存 + 重启换入,旧库留 `<db>.pre-restore-*` 可回退。 |
| 11 | 超时、退避与心跳 | 批 3a:每条远端命令一条 deadline(调用点给预算、`node` 层兜 15 分钟、ctx 自带 deadline 不覆盖);失败节点按 30s 起步、5 分钟封顶退避,成功清零;控制面心跳经 `GET /api/status` 与 `mpx status` 可读。 |
| 12 | 节点状态、漂移与自检 | 批 3b:一条 ssh 读回服务/版本/配置哈希/证书到期,`node_status` 表存观察值、`nodes.deployed_hash` 存基准;`mpx node status` 现场查、`?fresh=1` 绕开缓存;`GET /api/doctor` 与 `mpx doctor` 一屏看完控制面、每台节点、六种订阅格式;告警「报一轮、比一轮」、每 10 个 Tick(第 1 个也跑)巡检、整轮 45 秒预算。 |
| 13 | 规模化(进行中) | 批 4a:用户列表分页与筛选(`?limit=&offset=&status=&search=&inbound_id=`,响应带筛选后的 `total`);`rm`/`enable`/`disable`/`renew`/`links` 收多个用户名并汇总成败;`user export` / `import` 走 CSV。批 4b(订阅页 + 自助换 token)已完成;4c(通知)未做。 |
| 14 | 规模化 · 批 4a 真机验证 | 本机起真实控制面 + 57 个用户实测:默认一页 50 个、第二页 offset 正确、`--status disabled --search u3` 只回 u3;`limit=1001`/`offset=-1`/`status=bogus` 各回 400 且提示列可选值;`offset` 超界返回 `users: []` 而非 null;批量停用 3 个成功 0 失败退出 0、掺一个不存在的名字则 1 成功 1 失败退出 1;`renew --days 30` 从各人到期日起算、两个都给与都不给都报错;CSV 导出→导入「3 个更新」且 `sub_token`/用量原样;改上限/备注/入站再导入生效(250GB、`inbound_ids=[2,3]`);可用性写错报「可用性只能是 启用 / 停用」该行失败其余照常;列数不一致带行号;缺「用户名」列整份拒绝;连续三行坏引号停下;`-o json` 批量输出是数组且含 `ok:false`/`error`;单名字输出形状不变;重复名字只执行一次 |
| 15 | 规模化 · 批 4b 真机验证 | 本机起真实控制面实测:浏览器(带 `Accept: text/html` 与浏览器 UA)打开订阅地址拿到 HTML 页,含状态徽章、流量条(`0 B / 10 GB`)、到期、六个分享链接、换地址按钮,且正文不含 `vless://` 明文;客户端(不带 Accept / `*/*`)照旧拿 links、`/clash` 路径的浏览器请求照旧拿 YAML;响应头带 `Profile-Web-Page-Url`;`POST /sub/{token}/rotate` 换出新 token,旧地址 404、新地址 200 且仍是 alice 的订阅(`Content-Disposition: filename=alice`),管理接口 `user links` 也指向新 token;页面与 CLI 拼地址一致(共用 `subBaseURL`)。测试进程已 kill、/tmp/mpx4b 已删 |
| 16 | 批 5:协议支持 | 模型加 `ProtocolShadowsocks` / `ProtocolWireguard` / `NetworkXHTTP` / `NetworkSplitHTTP` / `NetworkHTTPUpgrade`,`Inbound` 加 `XHTTPPath` / `XHTTPHost` / `XHTTPMode` / `WGSecretKey` / `WGAddress` / `WGMTU` / `Mux` / `Fragment`;`internal/model/keys.go` 加 `DeriveSS2022Key` / `DeriveWGKeypair`;`internal/store` 加 8 列;`internal/server` 校验 + `ensureWireguard` 生成服务器私钥;四个渲染器(serverconfig / xray / clash / sing-box / links)各加了新协议与新传输的分支;`wgClientAddr` 用 `sha256("wgaddr:"+key)` 把用户映射进服务端网段(跳过 .0/.1/广播),服务端 peer 的 `allowedIPs` 与客户端地址同源。验证:节点侧 5 份配置(含 wireguard)过 `xray -test`;客户端 xray / clash / sing-box 各 5 份配置过 `xray -test` / mihomo `-t` / `sing-box check` |
| 17 | 补四个 `edit` 命令 | 功能缺失审计的结论:store 与 server 两层的 CRUD 早就齐了(`store.UpdateNode/UpdateInbound/UpdateHost/UpdateUser`、`handleUpdateNode` / `handleUpdateInbound` / `handleUpdateHost` / `handleUpdateUser`),缺的只在 CLI —— `node` / `inbound` / `host` / `user` 四类都只能增删不能改,四个 `PUT` 端点没有对应命令,改个名字得手搓 `curl`。补齐 `node edit` / `inbound edit` / `host edit` / `user edit`。`host edit` 因为控制面没有「按 id 读一个入口」的接口(`GET /api/inbounds/{id}/hosts` 是唯一的读法),要求 `--inbound` 并从该入站的列表里找。顺带修了用户表格:它列了 9 列却没有「可用」,而 CSV 导出一直有「可用性」,同一个用户在两处会对不上;「重置策略」空值也归一成 `none`(与导出一致)。验证:新增 `internal/cli/edit_test.go`(假控制面记下每个请求,断言只发改过的字段、能改回空状态、`host edit` 找不到入口时不发 `PUT`、缺 `--inbound` 时不发请求、只有端口/传输/安全层变化才提示下发);`gofmt` 干净、`go vet` 通过、`go test ./... -race` 九个包全绿 |
| 18 | 一键路径 `mpx init` | 用户的原话是「在客户机上 mpx init + vps 上的资料 user+password,然后在 vps 上安装好控制面和节点用户」,于是加了一条单机全包的最短路径:**笔记本上一条 `mpx init --host <IP> --ssh-password`**,替用户跑完上面第 1~5 步那一整套。分工是硬的:`mpx init`(笔记本)只做 SSH 编排 —— 探测 → 文件上传(可选 `--binary`,给连不上 GitHub 的机器)→ 跑 `install.sh --addr 0.0.0.0:8080` → 读 `/etc/mpx/env` 拿 token → 远端调 `mpx bootstrap` → 透传输出;`mpx bootstrap`(VPS 侧,Hidden 子命令)才是建对象的那个,它就是个普通的 `/api` 客户端,打 `127.0.0.1:8080`,token 自己从环境文件读、**不进 SSH 命令行**(所以不进两端 `ps` 与 shell 历史)。为此给 `internal/node` 加了带身份/密码的 SSH 入口(`Auth` / `RunAs` / `RunAsIO` / `PutFileAs` / `ShellQuote`),并把 `doWithin` 拆出重试循环。这是 CLI 里唯一一处 SSH,理由见 decisions #32。端到端(真容器)与三个真 bug 见上面「`mpx init` 一键路径」与「这条路径上踩到的三个真 bug」两行 |
