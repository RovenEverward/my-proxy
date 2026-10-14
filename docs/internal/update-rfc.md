# RFC:三部分自动升级与 `mpx update`

> 状态:已批准(2026-10-15,用户采纳全部推荐项)· M0 进行中 · 起草时基于 main 6d163fb
> 这份 RFC 推翻了 `internal/model/model.go`(`UpdateInfo` 注释)、`internal/updatecheck` 包注释、`docs/design.md`「不做的事:自动升级自己」和 `docs/internal/decisions.md` §37 里的同一条决定:「升级永远是一次显式的人工决定」。这条决定落地时要在这四处一起改(见 M5)。

## 结论(先看这里)

1. **三部分是谁、怎么换,按代码实际情况来:**

   | 部分 | 实际是什么(代码事实) | 升级时换的东西 | 由谁执行 |
   | --- | --- | --- | --- |
   | 用户面 | 本机 `mpx` 二进制。`mpx ui` 是同一个二进制。install-cli.sh 默认装在 `~/.local/bin/mpx` | 这个二进制 | CLI 自己(用户有写权限) |
   | 控制面 | `/usr/local/bin/mpx serve`,systemd 单元 `mpx.service`,以 `User=mpx` 身份运行,并开着 `ProtectSystem=strict` | 同一个 `mpx` 二进制 | **新增的 root 级 `mpx-update` oneshot 单元加 timer**。控制面进程自己写不了 `/usr/local/bin` |
   | 节点 | **节点上没有 mpx 组件**(design.md:「节点侧常驻 agent:不做」)。只有核心:xray(`/usr/local/lib/mpx/xray` 加 geodata)和可选的 sing-box(`/usr/local/lib/mpx/sing-box`),各有一个 systemd 单元 | 核心二进制 | 控制面经 SSH 执行(复用 `InstallCore` / `InstallSingBox` 现有的「.new → 校验 → mv」流程) |

2. **每一部分都按同一条流程走:**
   1. 下载;
   2. 校验 SHA256 和签名;
   3. 落成 `.new` 并试跑;
   4. 原子替换,同时保留 `.prev`;
   5. 健康检查;
   6. 失败就自动换回 `.prev`。

   当前最大的缺口是**发布件既没有 checksum,也没有签名**(release.yml 只上传 tar.gz/zip)。所以 **M0 先给发布件加签名**,之后才谈自动。

3. **顺序是控制面先、节点后,用户面独立。**
   - 节点核心的版本**由控制面那一版决定**,不追上游 latest。原因是节点配置由控制面渲染。
   - 混合版本期间,旧核心照常跑旧配置。控制面只在节点核心换成功之后,才下发需要新核心的配置。

4. **`mpx update` 是「显示加触发」升级的命令,不是审计。**
   - 它显示三部分各自的当前版本、最新/目标版本、进行中的进度,以及**最近一次**升级的结果(成功/失败、是否回退、原因)。
   - 同一个命令也能触发升级。
   - 状态每个部分只留最近一条,**不做升级历史或审计日志**。

5. **自动策略:**
   - 默认开;
   - 只跟稳定发布(GitHub `releases/latest`,本来就排除 prerelease);
   - 发布满 48 小时才自动装;
   - 只在维护窗口内(默认控制面本地时间 03:00–05:00)执行;
   - 三部分都能单独关。

## 问题陈述

用户要三部分自动升级、失败能可靠回退,并且一条 `mpx update` 就能看清现在各在哪一版、升级是什么状态,也能动手升级。

现状(都已在代码里核对过):

| 事实 | 出处 |
| --- | --- |
| 控制面每 6 小时问一次 GitHub `releases/latest`,只报 Current / Latest / Behind,不下载 | `internal/updatecheck`(`DefaultInterval = 6h`)、`control/loop.go` |
| 版本号只来自编译期 ldflags;拿不到就是空串,这时不判断「有新版」 | `buildinfo`、decisions §37 |
| 发布件有 5 个平台(linux amd64/arm64、darwin amd64/arm64、windows amd64),**没有 SHA256SUMS,没有签名** | `.github/workflows/release.yml` |
| install.sh / install-cli.sh 会落到 `.new`,`--help` 试跑后 `mv -f`;**不保留上一版**,也不校验哈希 | `deploy/install*.sh` |
| 控制面单元以 `User=mpx`、`ProtectSystem=strict` 运行,只有 `/var/lib/mpx` 可写 | `deploy/mpx.service` |
| xray 核心:控制面下载官方 zip 并缓存,经 SSH 推送;`.new` 用节点现有配置 `-test` 校验通过后才 `mv`;**不保留上一版**;不指定版本时取上游 latest;**不校验上游 digest** | `xrayrelease`、`node/install.go` |
| sing-box:在控制面上从官方源码编译,版本和 commit 钉死(`DefaultVersion v1.14.3` 加 40 位 revision),缓存里记了 sha256 并会复核 | `singboxbuild` |
| 节点重启核心会把这台节点上所有连接断一次;控制面启动时不会主动重启 xray | decisions 第 24 行、`node-sync` 确认文案 |
| 节点核心版本只是**观察值**(`NodeStatus.Version`),库里不存「期望版本」 | `model.go` |
| **CLI 和控制面之间没有任何版本兼容检查** | `cli/client.go`、`server` 里都没有相关逻辑 |
| 库迁移只往前(建表 IF NOT EXISTS,再 `addMissingColumns`),没有降级迁移 | `store/store.go` |

## 拟定方案

### 1. 发布件供应链(前提)

- **release.yml 增加两样东西:**
  - `SHA256SUMS`,覆盖全部发布件;
  - `SHA256SUMS.sig`,用 ed25519 对 SHA256SUMS 签名。私钥放在 GitHub Actions secret 里,**公钥编进 mpx 二进制**(Go 标准库 `crypto/ed25519` 就能验,不新增依赖)。
- **验证顺序:**
  1. 先验签名;
  2. 再对哈希;
  3. 最后解包。

  任何一步失败都直接停下,不碰现有文件。
- **防降级:**自动升级只接受比当前版本更高的版本(复用 `Ahead`);降级只能手动指定 `--version`。
- **xray 上游:**同样要校验 digest。XTLS 发布页**是否每个 zip 都附带 `.dgst` 摘要文件需要实测确认**(本 RFC 没有核实,不作为事实写入)。有就验,没有就只靠 TLS,并在 `mpx update` 里显示「未校验摘要」。
- **轮换公钥:**新版本的二进制里同时带新旧两把公钥,过一个发布周期后再去掉旧的。

### 2. 用户面(本机 CLI / `mpx ui`)

**升级流程:**
1. 找到当前可执行文件 `os.Executable()`;
2. 在同一目录写入 `mpx.new`;
3. 跑 `mpx.new --version`,确认输出正好是目标版本;
4. 把 `mpx` 改名为 `mpx.prev`;
5. 把 `mpx.new` 改名为 `mpx`。

同目录 rename 是原子的。Windows 上不能覆盖正在运行的 exe,但可以先改名再落新文件。

**健康检查:**`--version` 符合预期;如果配置了 server,再做一次 `GET /api/status` 能通。不通时**不回退**,因为那多半是网络问题,不是二进制坏了。只有新二进制根本跑不起来才回退。

**生效时机:**正在运行的 `mpx ui` 不中断,下次启动生效。`mpx update` 会提示「mpx ui 仍是旧版,重启后生效」。

**自动方式:**任意命令执行后**最多每 24 小时**检查一次(结果缓存在配置目录),只**提示**,不在后台替换。原因是 CLI 没有常驻进程,替换动作会发生在用户正在跑别的命令的时候。是否改成静默自动替换,见待拍板问题 1。

**目录不可写时**(例如 `/usr/local/bin`)直接报出原因,提示用 sudo 重跑 `mpx update apply cli`,不自行提权。

### 3. 控制面

**为什么要另一个进程:**控制面进程是 `User=mpx` 且 `ProtectSystem=strict`,换不了 `/usr/local/bin/mpx`。如果放宽这层沙箱,等于让对外服务的进程能改写自己。所以新增:

- `mpx-update.service`:`Type=oneshot`,root 运行,执行 `mpx update run control`;
- `mpx-update.timer`:每 6 小时一次,和 updatecheck 的间隔一致。是否真的动手由策略决定(窗口、48 小时冷却)。

install.sh 负责装上和启用这两个单元。手动触发时,`mpx update apply control` 在控制面 API 里只**登记一个请求**:写到 `/var/lib/mpx/update/request`。这个目录 mpx 用户可写。root 单元用一个 `.path` 单元监听这个文件来执行。控制面进程始终不需要 root。

**目录布局:**

```
/usr/local/lib/mpx/releases/<version>/mpx   # 每一版一个目录,只保留当前版和上一版
/usr/local/bin/mpx -> 上面某一版             # 符号链接,用 rename 原子切换
/var/lib/mpx/update/state.json              # 最近一次升级状态(见 §6)
/var/lib/mpx/update/pre-<from>.db           # 升级前的库快照,只留一份
```

**升级流程:**
1. 下载并校验(§1);
2. `mpx.new --version`;
3. **库快照**:用 `internal/backup` 现有的快照能力,只取数据库;
4. 切换符号链接;
5. `systemctl restart mpx`;
6. **健康检查**:60 秒内 `systemctl is-active`,并且本机 `GET /api/status` 返回 200,`version` 等于目标版本;
7. 失败时把符号链接切回上一版,再重启。

**库怎么办:**迁移是只加不删的(只有 `IF NOT EXISTS` 和补列),所以旧二进制读新库一般能跑,但**代码没有保证这一点**。规则如下:

- 新版本**从来没健康过**(说明还没接受过任何写入)时,回退会连库一起恢复成快照;
- 新版本健康过,之后才被手动降级时,**不恢复库**,否则会丢掉这段时间的写入。这时旧版本如果起不来,就进入 `failed` 状态等人处理。

**不会断用户连接:**控制面重启不重启 xray(已有行为),所以升级控制面不会断用户。

### 4. 节点(只升级核心)

**目标版本由控制面决定:**

- **sing-box 已经是这样**:每个 mpx 发布钉死 version 加 revision,控制面升级后,目标版本自然就是新的那一对。
- **xray 建议也改成钉死**:每个 mpx 发布声明一个经过测试的 xray 版本,替代现在的「不指定就 latest」。见待拍板问题 2。

**换核心的流程**沿用现有的「推送 `.new`,用节点现有配置 `-test`,通过后 `mv`」,另外补三件事:

- **保留上一版:**`mv` 前先 `cp` 一份到 `xray.prev` / `sing-box.prev`,只留一份;
- **重启后做健康检查:**`CoreIsActive`;`CoreGeneration` 发生变化(证明确实是新进程);观察到的 `Version` 等于目标版本;xray API 端口可以连上;
- **失败回退:**把 `.prev` 换回去,`systemctl restart`,再做一遍健康检查。回退之后仍然不健康的,标记为 `failed`,**这台节点暂停自动升级**,也不再继续升后面的节点。

**批次策略:**
1. 先挑一台节点(金丝雀),健康后观察 10 分钟;
2. 其余节点**一次一台**地升;
3. 任何一台失败就停下整批。

**代价:**每台节点重启核心,会把这台节点上所有连接断一次。所以必须在维护窗口里做。是否要先排水,见待拍板问题 5。

**不在节点上装任何 mpx 组件。**design.md 里「节点零维护」这条不推翻。

### 5. 版本兼容与升级顺序

| 场景 | 行为 |
| --- | --- |
| 控制面新、节点核心旧(升级中途) | 控制面继续给这台节点渲染旧核心能接受的配置。新核心才有的特性,只对 `Version` 已达标的节点生效。需要在渲染端加「最低核心版本」判断;**目前代码里没有这层**,要在 M4 新增 |
| 控制面旧、节点核心新 | 不会自动出现,因为节点目标版本由控制面决定。人为手动装了更新的核心时,只显示提示 |
| CLI 和控制面版本不一致 | 新增:CLI 读取 `/api/status.version`。主版本或次版本不同时只**警告**(在 `mpx update` 和 `mpx status` 里显示),不阻止使用。目前代码里没有这类检查 |
| 控制面升级失败并已回退 | 节点不动,因为节点批次只在控制面健康之后才开始 |

**顺序:**控制面先升(窗口开始),健康之后再升节点。CLI 不参与这个顺序。

### 6. 状态:只留最近一次

每个部分只存一份最小状态,覆盖写入,没有历史:

```json
{ "component": "control", "state": "idle|checking|downloading|verifying|switching|health-check|rolling-back",
  "from": "v0.3.3", "to": "v0.3.4", "started_at": "...", "finished_at": "...",
  "result": "ok|failed|rolled-back", "trigger": "auto|manual", "error": "健康检查超时:/api/status 60s 内未返回 200" }
```

| 部分 | 存在哪里 |
| --- | --- |
| CLI | `~/.config/mpx/update.json`(和现有配置放在同一个目录) |
| 控制面 | `/var/lib/mpx/update/state.json`。root 单元负责写;控制面只读,并通过新增的 `GET /api/update` 提供出去。**这是一次控制面 API 新增**,在 M1 里单独评审 |
| 节点 | 数据库里每个节点、每个核心存一行「最近一次升级」(新表或补列),由控制面写 |

### 7. `mpx update` 命令

| 子命令 | 作用 |
| --- | --- |
| `mpx update` | 显示三部分各自的当前版本、目标/最新版本、进行中的阶段,以及最近一次结果 |
| `mpx update apply [cli\|control\|nodes\|all] [--version V] [--node ID]` | 立即触发升级。`all` 的顺序是 control → nodes → cli。会先打印计划,再按 y 确认 |
| `mpx update rollback [control\|nodes --node ID\|cli]` | 换回 `.prev` |
| `mpx update policy [--auto on\|off] [--part …] [--window 03:00-05:00] [--soak 48h]` | 查看或修改自动策略。策略存在控制面库里;CLI 自己的开关存在本地配置里 |

输出样例(**示例,并非真实输出**):

```
$ mpx update
部分        当前       目标       状态
本机 CLI    v0.3.3     v0.3.4     可升级 · mpx update apply cli
控制面      v0.3.4     v0.3.4     最新 · 上次 10-14 03:02 v0.3.3→v0.3.4 成功(自动)
节点 hk-1   xray 26.3.27  26.3.27  最新
节点 jp-2   xray 25.12.8  26.3.27  失败已回退 · 10-14 03:21 健康检查:API 端口 10086 未响应
自动升级:开 · 窗口 03:00–05:00(控制面本地时间) · 发布满 48h 才自动装
```

```
$ mpx update apply control
计划:控制面 v0.3.3 → v0.3.4(已验签名)。会重启 mpx.service,用户连接不受影响。继续? [y/N] y
下载 ✓  校验 ✓  快照库 ✓  切换 ✓  重启 ✓  健康检查 … ✓ (6s)
已升级到 v0.3.4。
```

### 8. UI

总览页加一张「版本」卡片,内容与 `mpx update` 相同。另有一个「升级」按钮,点了弹确认框,确认框里显示等价命令 `mpx update apply …`。放在最后一期(M6)。`mpx ui` 自己的版本,就是本机 CLI 那一行。

## 失败模式

| 失败 | 发现方式 | 处理 |
| --- | --- | --- |
| 下载中断或超时 | HTTP 错误 | 不动现有文件,记为 `failed`,下个窗口再试 |
| 签名或哈希不符 | 校验失败 | 停下,**不自动重试同一版本**,显示「签名不符」,需要人处理 |
| 新二进制跑不起来(架构错误等) | `.new --version` 失败 | 删掉 `.new`,现有版本不动 |
| 控制面新版本起不来或 API 不通 | 60 秒健康检查失败 | 切回上一版,并恢复库快照(新版本从没健康过),状态为 `rolled-back` |
| 回退之后仍然起不来 | 第二次健康检查失败 | 状态为 `failed`,关掉自动升级,`mpx update` 给出 `journalctl -u mpx` 和手动 `ln -sf` 的命令 |
| 升级途中控制面机器断电 | 重启后 state 停在中间阶段 | `mpx-update` 下次启动时发现阶段不是 `idle`,先按「失败」处理,确认当前链接指向的版本能健康运行 |
| 节点核心 `-test` 不通过 | 校验失败 | 不替换(已有行为),这台节点暂停 |
| 节点核心重启后不健康 | 健康检查 | 换回 `.prev` 并重启;仍然失败就 `failed`,整批停下 |
| 节点 SSH 不通 | `Reachable=false` | 跳过这台,下个窗口再试,不算失败 |
| GitHub 限流 | 403 或 429 | 沿用 6 小时间隔,不升级 |
| CLI 所在目录不可写 | rename 报 EACCES | 提示用 sudo 重跑,不自行提权 |
| 有坏版本发布到了 latest | 48 小时冷却期内撤下 | 冷却期就是为这种情况设的。撤下后 `releases/latest` 会回到上一版,自动升级不会再选中它 |

## 替代方案

| 方案 | 不选的原因 |
| --- | --- |
| 控制面进程直接替换自己 | 需要放宽 `ProtectSystem=strict`,让对外服务的进程有权改写自己 |
| 节点上装 mpx agent 自己升级 | 推翻「节点零维护、只用 SSH」;核心本来就能由控制面推送 |
| 只用 apt/yum 仓库 | 要额外维护打包渠道,macOS 和 Windows 的 CLI 也覆盖不到 |
| cosign / GitHub attestation | 验证要额外的工具或网络服务;ed25519 加嵌入公钥只用标准库 |
| 保留完整升级历史和审计 | 用户已明确不要。只留最近一次,足够回答「现在怎样、上次怎样」 |

## 里程碑(每期都能独立合并)

| 期 | 内容 | 验收标准 |
| --- | --- | --- |
| **M0 签名发布件** | release.yml 生成 SHA256SUMS 和 .sig;Go 里加 `verify` 包,内嵌公钥;install*.sh 校验 SHA256SUMS | 打一个测试 tag 后发布页出现两个新文件;篡改一个字节后验签失败的测试 FAIL→PASS;老的 install 路径照常可用 |
| **M1 `mpx update` 只读** | 显示三部分当前/最新/目标版本;新增 `GET /api/update`(只读,先只返回版本);CLI 和控制面版本不一致时警告 | 真实控制面上跑出三部分版本;源码编译版显示「未知」,不报「有新版」 |
| **M2 CLI 手动升级与回退** | `apply cli` / `rollback cli`、`.prev`、`~/.config/mpx/update.json` | linux/darwin/windows 上从 vN 升到 vN+1 再回退的 e2e;用坏签名的发布件测试时现有二进制不变 |
| **M3 控制面手动升级与回退** | `mpx-update` service/timer/path 单元由 install.sh 安装;符号链接布局;库快照;健康检查加回退;state.json | 在 VM 上:正常升级成功;注入一个「起不来」的版本,60 秒内自动回退且库内容一致;升级中途 kill 后能自愈 |
| **M4 节点核心升级与回退** | 钉死 xray 目标版本(等问题 2 拍板);`.prev`;健康检查;金丝雀加逐台;渲染端加最低核心版本判断;每节点最近状态 | 两台测试节点:正常升级;注入坏核心后自动回退且整批停下;混合版本期间两台都能拿到可用配置 |
| **M5 自动策略** | 开关、窗口、48 小时冷却、`mpx update policy`;**同步改 model.go、updatecheck 包注释、design.md、decisions.md §37**,写明推翻的理由和新的兜底(签名、.prev、健康检查、冷却) | 时钟注入测试:窗口外不动、冷却期内不动、关闭后不动;文档里不再有「升级永远是人工决定」 |
| **M6 UI** | 总览「版本」卡片加触发按钮(带确认和等价命令) | Playwright:状态展示和 `mpx update` 一致;触发发出的请求和 CLI 一致 |

## 已拍板(2026-10-15 全部采纳推荐)

1. **CLI 要不要后台静默自动替换?**推荐**不要**,只提示,由 `mpx update apply cli` 一键完成。CLI 没有常驻进程,静默替换只能发生在用户执行别的命令时,出错的时机很难预料。控制面和节点才是真正需要自动升级的部分。
2. **节点 xray 的目标版本是钉死在 mpx 发布里,还是跟上游 latest?**推荐**钉死**,和 sing-box 一样。代价是安全修复要等 mpx 发一版。保留 `mpx node install --xray-version` 作为手动的逃生口。
3. **签名方案:**推荐 **ed25519 私钥放 GitHub secret,公钥嵌进二进制**。需要你确认可以新增这个 secret,并决定私钥的离线备份放在哪里。
4. **自动升级的默认值:**推荐**默认开**,窗口 03:00–05:00,冷却 48 小时。老用户升级到带这项功能的版本时,是否也默认开启?推荐**开,但第一次在 `mpx status` 里提示一次**。
5. **节点自动升级前要不要先排水?**推荐**不排水**,只在窗口内逐台重启。排水要等客户端刷新订阅,通常是小时级,窗口装不下。如果在意凌晨那一次断线,可以把节点设成「只手动升级」。

## M0 上线步骤(合并 M0 之前由维护者执行)

M0 的代码里公钥是**占位**(`internal/release.PublicKeys` 为空、两个安装脚本里 `MPX_RELEASE_PUBKEY=""`)。占位期间:

- 安装脚本只校验 SHA256SUMS 的哈希,并提示「还没配置签名公钥」;`MPX_REQUIRE_SIGNATURE=1` 时拒绝安装。
- `release.yml` **会拒绝发布**:没有 secret、或仓库里的公钥还是占位、或两者不是一对,发布 job 直接失败,不会发出未签名的发布。

上线要做的三步:

1. **在离线可信机器上生成私钥**(OpenSSL 3):

   ```sh
   openssl genpkey -algorithm ed25519 -out mpx-release-signing.pem
   openssl pkey -in mpx-release-signing.pem -pubout | sed '1d;$d' | tr -d '\n'; echo   # 打印公钥(一行 base64)
   ```

   私钥另做离线备份。丢了只能轮换,不能补签。
2. **设 GitHub secret**:仓库 Settings → Secrets and variables → Actions,新建 `MPX_RELEASE_SIGNING_KEY`,值是 `mpx-release-signing.pem` 的完整内容(含 BEGIN/END 两行)。
3. **把公钥写进仓库三处**,再跑 `go test ./internal/release`(`TestScriptsCarrySameKeys` 会检查三处是否一致):
   - `internal/release/release.go`:`var PublicKeys = []string{"<公钥>"}`
   - `deploy/install.sh` 与 `deploy/install-cli.sh`:`MPX_RELEASE_PUBKEY="<公钥>"`

轮换时把新公钥放在 `PublicKeys` 的第一位,同时保留旧的那把;脚本里只放新的。换 secret 后过一个发布周期,再把旧公钥去掉。
