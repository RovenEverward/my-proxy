# RFC:MPX 桌面程序与「UI 优先」

> 状态:**已批准**(2026-10-16:框架定 MyGo;Linux 桌面包要做;.deb + tar.gz;所有安装包都不签名;Q4 只做普通窗口;Q6 钥匙串另开 RFC;Q7 恢复留在终端;Q8 桌面自更新用 SHA256SUMS) · 起草基于 my-proxy main `787c0f1`,定稿对照 `1d0884c`(v0.6.4)与 [egoist/mygo](https://github.com/egoist/mygo) v0.5.5 · magpie 对照 /workspace/magpie-src
> 这份 RFC 推翻了 `ui-rfc.md`「原生桌面壳不做」那条 Non-goal(见 §4.1);页面仍以 `mpx ui` 为准,壳只换显示窗口。

## 结论(先看这里)

1. **桌面程序 = 现有 `mpx` 二进制加一个 `mpx app` 子命令**,用系统 WebView 开一个窗口(框架 **MyGo**;穿刺 S1 不过则换 Wails v3,见 §4.1b),窗口里装的就是现在 `mpx ui` 的同一套页面和同一个本地转发层。不另写前端,不引入 Node 打包链。这正是 magpie 的做法(见 §2)。
2. **凭据不动位置**:仍是 `os.UserConfigDir()/mpx/config.json` 里的 profile(控制面地址 + 设备 token),桌面程序和 CLI、`mpx ui` 共用一份;token 仍只在本地进程内存里,不交给页面。
3. **补齐顺序(用户已定)**:远程升级 CLI(M1–M3)与两个向导已在 v0.6.x 完成;接下来是 UI「更新」pill(U1b)、其余 CLI 有而 UI 没有的缺口(U4),以及桌面壳穿刺与打包(D1 起)。桌面壳不阻塞 UI 补齐,因为补到 `mpx ui` 里的东西壳里自动就有。
4. **控制面 API 只加不改**;桌面程序不需要控制面新增任何接口,UI 补齐需要的新接口逐项列在 §4。

## 1. 目标与能力清单

| 能力 | 状态 |
| --- | --- |
| 所有日常管理都能在界面里点完(用户、套餐、节点、入站、分流、设置) | 设计内,大部分已有(§3) |
| 远程升级 CLI / 控制面(节点核心仍待 M4) | CLI 已有(`mpx update`,v0.6.1+);UI「更新」pill ✅ v0.6.5 |
| 首次安装向导(空白状态下装好控制面) | ✅ v0.6.4([向导方案](wizard-rfc.md) B) |
| 用 SSH 密码接入新机器的向导 | ✅ v0.6.4([向导方案](wizard-rfc.md) A) |
| 双击即开的桌面程序(macOS / Windows / Linux) | 设计内,壳在补齐之后 |
| 安装包签名(Apple 公证、Windows Authenticode) | 不做(用户 2026-10-16 决定),代价见 §8 |
| 菜单栏 / 托盘常驻、开机自启 | 不做(Q4 已定只做普通窗口) |
| 桌面程序自更新 | 设计内,复用 update-rfc 的 SHA256SUMS 流程 |
| 从备份恢复进界面 | 不做:整库替换且要重启控制面,`settings.js` 已明确"只在终端里做"(保持现状,除非用户改主意) |
| 用户登录 VPS 做任何事 | 不做:违反核心原则 |
| 手机原生 App | 不做:手机浏览器开 `mpx ui` 已够用,原生壳收益小 |

## 2. magpie 桌面程序技术栈(查证结果)

| 结论 | 出处 |
| --- | --- |
| 框架是 **Wails v3**(`github.com/wailsapp/wails/v3 v3.0.0-beta.24`),**仍是 beta** | `go.mod` |
| 单一 Go 二进制;前端是普通静态文件,用 `//go:embed assets` 打进二进制,经 `application.AssetOptions{Handler: ...}` 交给 WebView——前端就是一个 `http.Handler`,没有 Node 构建步骤 | `internal/gui/api.go:142`、`internal/gui/app.go:342-350` |
| 同一份代码出两种构建:默认带 GUI;`-tags nogui` 出纯命令行版(`CGO_ENABLED=0`,可任意交叉编译);GUI 源文件头是 `//go:build !nogui` | `Makefile` 的 `cli`、`release-cli` 目标;`internal/gui/app.go:1` |
| GUI 版要链系统 WebView:macOS 用 cgo 原生编;Linux 用 GTK3 + WebKitGTK 4.1(`gtk3` tag,为兼容老发行版不用 Wails 默认的 GTK4/WebKitGTK 6),只能本机编、一机一架构;Windows 用 WebView2、无需 cgo,可交叉编 amd64/arm64,`-H windowsgui` 去掉控制台窗口,图标/版本/DPI 清单由 go-winres 生成 | `Makefile` 注释与 `release-windows`、`release-linux` 目标;`build/windows/winres.json` |
| 平台:GUI 有 macOS(最低 12)、Windows amd64/arm64、Linux;CLI 有 darwin/linux/windows 各 amd64/arm64 加 android/arm64(Termux,只保留网页 UI 和命令行) | `Makefile` 的 `TARGETS` 与 macOS/Android 注释 |
| macOS 打包是手工拼 `.app`(Info.plist、icns、Assets.car),菜单栏应用、`LSUIElement` 不占 Dock | `Makefile` 的 `app` 目标、`build/darwin/` |
| 托盘图标 + 下拉面板 + 常规窗口;单实例;注册 `magpie://` 链接;开机自启;Windows 自建开始菜单快捷方式 | `internal/gui/app.go`(`Run`、`SingleInstance`、`registerScheme`、`shortcut.Ensure`、`autostart.Refresh`) |
| 签名/公证:README 称 "Mac builds are signed and notarised";**签名在另一个仓库 yetone/magpie-releases 里做,签名密钥只在那里**,本仓库只发 `repository_dispatch` 触发 | `README.md:52`;`.github/workflows/release.yml` 顶部注释与 `trigger-release` job |
| Windows 是否做代码签名:**未知**(本仓库没有相关代码,magpie-releases 未查) | — |
| CI 发版:推 `v*` tag → 触发 magpie-releases 构建、签名、公证、发布 → 本仓库等对方 16 个资产齐了再镜像过来;中文发布说明由那边调用 DeepSeek 生成,缺中文会自动开 issue | `.github/workflows/release.yml` |
| 自动更新:读 `https://usemagpie.ai/api/latest`(可用 `MAGPIE_UPDATE_FEED` 改),返回版本、说明和每个文件的 SHA-256;下载后校验哈希;macOS 版替换整个 `.app`,且要求新包 `codesign --verify` 通过、团队 ID 与已装的一致;Windows/Linux 和 CLI 直接替换自身二进制(无签名校验),Windows 先把运行中的 exe 改名 `.old`;目录无写权限时 macOS / Linux(pkexec)弹管理员密码 | `internal/update/update.go` 包注释、`Feed`、`sameSigner`;`internal/update/admin.go`;`internal/update/aside_test.go` |
| 开发模式:`make dev` 页面从磁盘读、保存即刷新,Go 改动只重启后端 | `Makefile` 的 `dev` / `dev-once` |

## 3. MPX 现有功能的 UI 覆盖

依据:`internal/cli/*.go` 的全部子命令;`internal/cli/ui.go` 的放行表 `uiAllow`;`internal/ui/assets/*.js` 实际发出的请求。

| 功能 | CLI | UI |
| --- | --- | --- |
| 用户增删改、启停、续期、重置流量、吊销订阅、看链接 | `mpx user …`、`renew` | ✅ 用户页 |
| 用户 CSV 导入导出 | `mpx user import/export` | ✅ 用户页 |
| 套餐增删改 | `mpx plan list/add/edit/show/rm` | ✅ 套餐页;`plan suggest` ❌ |
| 节点列表/详情/增删改、下发、排水、定位、体检 | `mpx node …`、`locate`、`diagnose` | ✅ 节点页 |
| 节点核心安装 | `mpx node install` | ✅ 节点页有入口 |
| **用 SSH 密码接入新节点** | `mpx node add --ssh-password`(密码随 `POST /api/nodes` 的 `ssh_password` 字段送控制面) | ✅ 向导 A(v0.6.4) |
| 节点改名(批量 relabel) | `mpx node relabel` | ❌(单个改名在编辑里可做) |
| 节点在线 IP、现场状态 | `mpx node online/status` | 部分:列表显示上次状态,现场查询 ❌ |
| 核心部署字段 | `mpx core show/edit` | 部分:节点页显示,编辑未核实 |
| 能力检测配置与现场探测 | `mpx probe/capabilities/capability-set` | ❌ |
| 入站与对外入口(host) | `mpx inbound …`、`mpx host …` | ✅ 入站页 |
| 分流规则、策略组、迁移、模式 | `mpx rule …`、`mpx group …` | ✅ 分流页 |
| 订阅模板、通用设置、webhook 测试 | `mpx settings template/show/set/test` | ✅ 设置页 |
| User-Agent 识别规则 | `mpx settings rules …` | ✅ 设置页「识别规则」标签(细项未逐一核对) |
| 下载备份 | `mpx backup` | ✅ |
| 从备份恢复 | `mpx restore` | ❌(有意不做) |
| 用途(v0.7.0 已删除) | — | 已随 #49 移除 |
| **首次安装** | `mpx init`(SSH 到新 VPS 装控制面+节点) | ✅ 向导 B(v0.6.4) |
| 控制面本机建初始数据 | `mpx bootstrap`(在控制面上跑) | ❌,也不该进 UI |
| 设备接入 / 设备密码 | `mpx connect`、`mpx connect password` | ❌(UI 启动本身依赖已接入的 profile) |
| 设备列表与吊销 | `mpx device list/revoke` | ❌(device-connect M4 已计划) |
| 登录 / profile 切换 | `mpx login`、`--profile` | ❌,页面只显示当前 profile |
| **远程升级** | `mpx update show/apply/rollback`(M1–M3,v0.6.1+) | ❌ UI(M6 顶部「更新」pill 未做) |
| 体检 doctor、全局状态 | `mpx doctor`、`mpx status` | 概览页部分覆盖,doctor ❌ |
| 本机直连体检 | `mpx diagnose`(本地) | ❌ |
| 补全脚本、`serve` | `completion`、`serve` | 不适用 |

## 4. 关键决策与理由

### 4.1 桌面程序选型

| 方案 | 做法 | 好处 | 代价 |
| --- | --- | --- | --- |
| **A. Wails v3(推荐)** | `mpx app` 开 WebView,`AssetOptions.Handler` 直接挂现有 `ui.Server` | 与 magpie 同路;页面零改动;仍是一个 Go 二进制;包小(系统 WebView) | 引入 cgo(macOS/Linux 要原生 runner);v3 是 beta;CLI 构建要分 `nogui` tag 保持 `CGO_ENABLED=0` |
| B. Wails v2 | 同上,用稳定版 | 非 beta | v2 的资产与多窗口/托盘模型不同,magpie 经验用不上;v3 正式版出来后还要迁 |
| C. Tauri | Rust 壳 + sidecar 跑 mpx | 包小、更新器成熟 | 第二种语言和工具链;两个进程;对单人维护是额外负担 |
| D. Electron | Node 壳 + sidecar | 生态最全 | 包 100MB 级;Node 工具链;与"最小机制"冲突 |
| E. 不做壳,只做启动器 | 一个双击就 `mpx ui` 并开默认浏览器的 .app/.exe | 零新依赖,一天能做完 | 体验是浏览器标签页,不像应用;无托盘 |

推荐 A。理由:MPX 已经是"Go 二进制内嵌静态页 + 本地转发"(`internal/ui/ui.go`),和 magpie 的结构一一对应,壳只是换一个显示窗口。E 作为 A 的退路:若穿刺 S1 不过(见 §6),先发 E。

### 4.1b 新增备选:MyGo(2026-10-16 调研)

**它是什么**:[egoist/mygo](https://github.com/egoist/mygo)(官网 https://mygo.egoist.dev),Go 的桌面应用框架,MIT 许可。窗口可以显示系统 WebView 里的网页(macOS WKWebView、Linux WebKitGTK、Windows WebView2),也可以显示它自己在 GPU 上绘制的 Go 原生界面,两种可混用。同名的其他 GitHub 仓库(mayuanucas/mygo 是 LeetCode 题解、aisk/mygo 等)都不是 GUI 框架;missuo/motrix-mygo、vfasky/mygo-vaultwarden 是用 MyGo 写的应用。

查证事实(2026-10-16 再核,GitHub API + [文档站](https://mygo.egoist.dev/docs/distribution)):

| 项 | 事实 | 出处 |
| --- | --- | --- |
| 热度 | 2142 star,44 个未关 issue;**仓库 2026-09-26 才创建** | GitHub API |
| 活跃度 | 最近推送 2026-10-16 16:14(UTC+8);最新发版 v0.5.5 于 2026-10-16 08:26(UTC+8);仍以作者 egoist 为主 | GitHub API |
| 版本 | 0.x(v0.5.5),无稳定承诺 | releases |
| Go 版本 | 要求 Go 1.27.1;MPX `go.mod` 也是 1.27.1,无冲突 | 两边 `go.mod` |
| 渲染 | 网页窗口用系统 WebView;原生 UI 自绘(Metal / D3D11 / OpenGL) | `README.md`、`docs/ui` |
| cgo | **不用 cgo**:macOS、Linux 经 purego `dlopen` 运行时加载系统库,Windows 走 `syscall` | `docs/architecture.md`、`go.mod`(`ebitengine/purego`) |
| 交叉编译 | 我的 demo 在 Linux box 上 `CGO_ENABLED=0` 编出了 linux/amd64、linux/arm64、windows/amd64、darwin/arm64,约 12–13 MB;linux 产物 `ldd` 只依赖 libc/libdl/libpthread | `/tmp/mygo-demo` 实测 |
| Linux 运行依赖 | GTK 3 + WebKitGTK 4.1(4.0 兜底),运行时才加载;缺库时报 "install WebKitGTK: libwebkit2gtk-4.1-0 on Debian/Ubuntu, webkit2gtk4.1 on Fedora" | demo 在 box 上实测(box 无 WebKitGTK,窗口没打开,报的就是这句);`internal/linux/` |
| 能否加载现有 mpx ui | 能直接给窗口一个 URL:`WindowOptions{URL: "http://127.0.0.1:端口/"}`,`ui.Server` 照旧在回环端口上跑即可;也可把 `embed.FS` 交给它的 `mygo://` 协议。完整页面 + CSP 的实际效果**未验证**(box 没有 WebKitGTK) | `window.go:359`、`frontend.go`、`protocol.go:343` |
| 打包 | `mygo build -platform ...` 出:macOS `.app`/dmg;Windows 安装器(开始菜单、卸载项);Linux 可执行文件 + `.desktop` + 图标 + `tar.gz`(带 `install.sh`)+ **`.deb`(Depends 自动写 `libgtk-3-0 \| libgtk-3-0t64, libwebkit2gtk-4.1-0`)**;没有 AppImage/rpm/Flatpak | `docs/distribution.md`、`cmd/mygo/deb.go:154` |
| 签名 | 支持 Apple 签名与公证、可跳过(`-skip-notarize`);dmg 与签名需要 Mac | `docs/distribution.md` |
| 自动更新 | 内置 `mygo.Updater`:ed25519 签名的更新清单(`mygo keygen` 生成密钥,公钥编进程序)、增量更新、类似 Sparkle 的更新窗口;不给私钥时 `mygo build` 跳过更新文件 | `updater.go`、`docs/updates.md` |
| CI | 有 GitHub Actions 文档 | `docs/github-actions.md`(未细读) |
| 文档 | 文档站完整,覆盖窗口、菜单、托盘、前端、打包、更新、架构 | `docs/` |

**MyGo 对比 Wails v3**

| 维度 | MyGo | Wails v3 |
| --- | --- | --- |
| 成熟度 | 0.5.x,三周大,基本单人维护 | v3.0.0-beta.24,v2 已有多年社区;magpie 在生产里用 |
| cgo | 不用;一台 Linux runner 能编全部平台 | macOS/Linux 要 cgo、原生 runner;Windows 可交叉编 |
| 对 MPX 发版的影响 | 发版矩阵仍可全部 `CGO_ENABLED=0`,和现在的 CLI 包同一套 | 要为桌面包另开 macOS / Linux 原生 job |
| Linux 依赖 | GTK3 + WebKitGTK 4.1,运行时 dlopen,缺库给出清楚提示 | GTK3 + WebKitGTK 4.1(`gtk3` tag),编译时链接,缺库则程序起不来 |
| 加载现有页面 | 给 URL 即可(或 `embed.FS`) | `AssetOptions.Handler` 直接挂 `http.Handler` |
| Linux 包格式 | 自带 tar.gz + install.sh、.deb | 本身不出包(magpie 只发裸二进制) |
| 自动更新 | 内置,ed25519 + 增量 | 无内置;magpie 自写了一套 |
| 主要风险 | 项目太新,API 可能变,作者精力是单点 | beta,cgo 构建复杂 |

**已定(2026-10-16)**:用 MyGo,以穿刺 S1 为准;不过则换 Wails v3。理由:MPX 的壳只做"开一个窗口指向本地 `ui.Server`",用到的框架面极小,换框架代价低;MyGo 不用 cgo,正好保住 MPX 现有"一个纯 Go 发版矩阵"的最小机制,还白给 .deb 和更新器。它太新这一点用两条兜住:锁定具体版本,壳代码收在一个包里;穿刺不过或日后断更就换回 Wails v3,页面不受影响。

### 4.1c Linux 桌面包

- 格式:`.deb`(Debian/Ubuntu,Depends 写 `libgtk-3-0 | libgtk-3-0t64, libwebkit2gtk-4.1-0`,装包时由 apt 拉依赖)+ `tar.gz`(带 install.sh,给其他发行版)。暂不做 AppImage/Flatpak/rpm:WebKitGTK 打进 AppImage 体积大且容易与系统冲突,先看是否有人要。
- 依赖处理:不把 GTK/WebKitGTK 打进包,用系统的。程序启动时缺 WebKitGTK 就给出安装命令(MyGo 自带这句提示),并提示可以改用 `mpx ui`(浏览器版,无任何图形库依赖)。
- 纯命令行包 `mpx_linux_*.tar.gz` 保持不变,服务器和无桌面环境的用户继续用它。

### 4.2 与 `mpx ui`、CLI 的关系

- 同一个二进制、同一个 `ui.Server`、同一份放行表 `uiAllow`。页面能做的事仍以放行表为界,桌面版不多开任何能力。
- `mpx ui` 继续存在(Linux 服务器上的用户、手机、无 GUI 环境)。桌面版只是"`mpx ui` 不开浏览器,开自己的窗口"。
- 发布件:现有 5 个 CLI 包保持 `CGO_ENABLED=0` 不变(`-tags nogui`);另加桌面包(macOS `.app` zip、Windows exe)。macOS 的 `.app` 里的二进制同样能当 CLI 用,是否装 shim 见 Q5。
- 页面里的一次性 key 登录(`Server.login`)在 WebView 中改由壳直接注入会话,不再走浏览器跳转;这是本地改动,不碰控制面。

### 4.3 数据与凭据

- 控制面地址和设备 token:`os.UserConfigDir()/mpx/config.json`(`internal/cli/config.go`),桌面、CLI、`mpx ui` 共用,不新增存储。
- 是否把 token 挪进系统钥匙串:不在本 RFC 内做(会改 CLI 行为),记为 Q6。
- SSH 密码(接入向导):只在请求体里出现一次,和现在 `node add --ssh-password` 一样随 `POST /api/nodes` 交给控制面、用完即弃;页面不存、本地不落盘、日志不打。
- `mpx init` 例外:控制面还不存在,所以 SSH 是**本机进程**直接连新 VPS。向导里这一步由本地 `ui.Server` 新增一个只在回环地址、只在"未连控制面"状态下开放的本地端点来驱动,不经控制面 API。
- WebView 自己的缓存/存储:只放界面偏好(主题、语言),不放 token。

### 4.4 UI 补齐顺序与需要的接口(全部新增,不改旧的)

1. **远程升级 UI**(update-rfc M6):**CLI/控制面 M1–M3 已在 v0.6.1–v0.6.3**;还差 `mpx ui` 顶部「更新」pill(magpie 风格:有新版时出现,弹窗列出版本说明,按钮「稍后 / 重启以更新」)。本机 CLI 升级走本地进程;`POST /api/update/control` 已有。
2. **接入向导**:**已完成**(v0.6.4,向导 A)。
3. **首次安装向导**:**已完成**(v0.6.4,向导 B)。
4. 其余缺口按使用频率:设备列表与吊销(device-connect M4)、设备接入/切换 profile、能力检测、节点现场状态与在线 IP、批量 relabel、doctor。每项都是"给已有 API 加页面",若缺接口则只加。

## 5. 设计原则(约束本 RFC 所有批次)

- 用户永远不用登录 VPS;一切在自己设备上完成。
- 用够用的最小机制:壳复用现有页面和转发层,不另起前端或第二种语言。
- 控制面 API 只增不破:旧客户端、旧 CLI 必须照常工作;要破坏兼容先停下来问。
- 桌面程序不扩权:能做的事以 `uiAllow` 为界。

## 6. 待定项与穿刺项

- **S1 MyGo 能否直接加载 `ui.Server`**:在 macOS、Windows、Ubuntu(装了 WebKitGTK 4.1)上用窗口 `URL` 指向本地 `ui.Server`,登录到控制面、看到节点列表;CSP(`internal/ui/ui.go` 的 `csp`)不需放宽。不过就改用 Wails v3 再做一次;还不过则退到方案 E(启动器开默认浏览器)。
  - **S1 清单**(最小可运行):① `go get github.com/egoist/mygo@v0.5.5`;② 起现有 `ui.Server` 于 `127.0.0.1:随机端口`;③ `mygo.NewWindow`/`WindowOptions{URL:...}` 打开该地址;④ 手测登录与节点列表;⑤ Linux 缺 WebKitGTK 时必须给出安装提示并仍可回退 `mpx ui`。
  - **本机 box 限制**(2026-10-16):有 GTK3,无 `libwebkit2gtk-4.1-0`,未装显示服务时无法完成开窗手测;可交叉编出 linux/windows/darwin 产物做 S2 的编译半截。
- **S2 CI 构建**:一台 ubuntu runner 用 `CGO_ENABLED=0` 编出三平台桌面包(macOS 不签名时能否不用 Mac 打出可用的 `.app`/zip 待验证,dmg 需要 Mac);现有 CLI 包行为不变。`mygo build -platform ...` 可出 `.deb` 与 `tar.gz`+`install.sh`。
- **S3 未签名包的首次打开**:未签名的 macOS 包会被 Gatekeeper 拦(需右键打开或去隔离属性),Windows 会出 SmartScreen 提示。实际提示措辞与用户需要的步骤未验证。
- 未知:Wails v3 正式版时间(仅作兜底时需要)。

## 7. 实现批次

| 批次 | 内容 | 通过条件 |
| --- | --- | --- |
| U1a | 远程升级 CLI/控制面(update-rfc M1–M3) | ✅ v0.6.1–v0.6.3,144 已验证 apply/rollback |
| U1b | UI「更新」pill(update-rfc M6,magpie 风格) | ✅ v0.6.5 |
| U2 | SSH 密码接入向导 | ✅ v0.6.4 |
| U3 | 首次安装向导 | ✅ v0.6.4 |
| U4 | 其余缺口(§4.4 第 4 条) | §3 表中除"有意不做/不适用"外全部 ✅ |
| D1 | 穿刺 S1、S2(MyGo 加载 `ui.Server`;CI 交叉编三平台桌面包) | 见 §6;本机 Linux box 需 WebKitGTK 4.1 才能开窗 |
| D2 | `mpx app` + macOS / Windows / Linux(.deb、tar.gz)桌面包进发版,均不签名 | 双击打开即进入当前 profile 的界面;CLI 包不变 |
| D3 | 桌面自更新(复用 update-rfc 的 SHA256SUMS;**不用** MyGo ed25519 Updater,见 Q8) | 旧版桌面点升级后变成新版,哈希不符时拒绝 |

U1b 与 D1 可并行;D 依赖的页面能力由 U 提供。

## 8. 风险

- 框架不成熟:MyGo 0.x 且三周大、Wails v3 仍是 beta;缓解:锁版本,壳层代码集中在一个包,页面不依赖框架运行时 API,两者可互换。
- 若兜底换到 Wails v3 会引入 cgo、发版矩阵变复杂;缓解:首选 MyGo 无 cgo;CLI 包保持纯 Go,桌面包单独 job。
- 所有包不签名(用户已定):macOS Gatekeeper 首次打开会拦(要右键打开或在设置里放行),Windows 出 SmartScreen 提示;README 和下载页要写明步骤。Linux 不受影响。
- 首次安装向导在本地执行 SSH,本地端点若暴露会被同机其他程序调用;缓解:仅回环、仅未配置状态、沿用一次性 key 会话。

## 9. 需要用户拍板

已定(2026-10-16):
- **Q1′** 框架用 **MyGo**;穿刺 S1 不过再换 Wails v3。
- **Q2** 要做 Linux 桌面包(.deb + tar.gz)。
- **Q3** 所有安装包都不签名。
- **Q4** 只做普通窗口(不做托盘/开机自启)。
- **Q5** 不装 CLI shim(`.app` 里的二进制仍可当 CLI 用)。
- **Q6** 钥匙串另开 RFC。
- **Q7** 从备份恢复留在终端。
- **Q8** 桌面自更新用 **SHA256SUMS**(与 CLI/控制面一致),不用 MyGo ed25519 Updater。

无未决拍板项。下一步是 U1b(更新 pill)与 D1(S1/S2 穿刺)。
