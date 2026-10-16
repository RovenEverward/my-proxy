# RFC：MPX 管理 UI（`mpx ui`）

状态：**已采纳**（2026-10-14）· 依据：my-proxy main `1fc97ed`，yetone/magpie 默认分支浅克隆

> 拍板结果：推翻「不做 Web UI」（改为「不做控制面直接对外的管理 UI」，见 [设计](../design.md#不做的事)）；形态选 B（本机 `mpx ui`）；M0 只读；M0 不引入 Playwright，M1 起引入。快到期阈值定为 7 天，快用完用 `notify_usage_percent`。

## 结论（先读这段）

1. **这件事要先推翻一条已写下的 Non-goal。** `docs/design.md`「不做的事」里明确写着「Web UI —— 入口是 CLI；UI 得跟着模型一起演进」，roadmap 批 4 也专门说明订阅页「不是运营 UI」。做管理 UI 要先改这一条，并把理由换成新的约束，不能悄悄做。
2. **推荐方案：在 CLI 里加一个 `mpx ui` 子命令，控制面一行不改。** 它在操作机本地起一个只监听 127.0.0.1 的小 HTTP 服务，页面用 `go:embed` 打进 `mpx` 二进制。页面发来的 `/api/*` 请求由它原样转给 profile 里的控制面，并在转发时带上 CLI 已经保存的 token。浏览器用一次性链接里的随机 key 登录（照搬 magpie `magpie web` 的做法）。
3. **UI 和 CLI 用同一套 `/api`，能力边界也一样：** 只有 CLI 已有的命令才有界面，不新增后端接口。`design.md` 里「每条命令都能用 curl 等价做出来」，到这里就是「每个页面操作都对应一条已有的 CLI 命令」。
4. **技术栈：** 不用框架，也不用打包工具（原生 JS + 一份 CSS），视觉沿用文档站和订阅页的 skeleton 风格。这样 Go 工具链之外不会多出 node 构建步骤。
5. **分 4 个里程碑**，每个都能单独合并，PR 里附截图。M0 先做只读的骨架，确认可行再加写操作。

## 1. 问题与目标

**问题。** 用户数到了几百上千以后，「看全局」靠 CLI 很费劲：谁快到期、谁超限了、哪个节点在退避或排水，都得连敲几条 `list` / `status` / `show` 再在脑子里拼起来。roadmap 批 4 已经指出「每个用户的问题都要手工敲一条 CLI，就卡在 100 这个量级」，分页和批量操作缓解了一部分，但还是没有一个能**一眼看全**的地方。

**目标。**
- 一个总览页回答 `design.md` 提出的那几个问题：谁在哪些节点上、谁到期了、谁超限了、节点是否健康。
- 日常写操作（加人、续期、停用/启用、重置流量、吊销订阅、排水）点几下就能完成，确认方式和 CLI 一致。
- 不改控制面，不改部署形态，不扩大暴露面。

**Non-goals（这次明确不做）。**
- 多管理员、权限分级、审计：「管理员与多租户」那条 Non-goal 保留不动。
- 让控制面在公网直接提供管理页面（理由见 §3 方案 B 的取舍）。
- CLI 没有的功能，比如流量历史曲线、告警中心、实时日志。现在的 `/api` 不提供这些数据，UI 不编造也不新增。
- `restore` 这种破坏性的整库操作，留在 CLI，页面上最多显示「请在终端执行 …」。（原先这里还有 `mpx init` / `bootstrap` 这类 SSH 编排；2026-10-16 起改由向导做，见 [向导方案](wizard-rfc.md)。）
- ~~原生桌面壳~~：**已推翻**(2026-10-16)。桌面壳用 MyGo 包装现有 `mpx ui`，不做托盘；方案见 [桌面程序方案](desktop-rfc.md)。`mpx ui`（浏览器）继续保留。
- 多语言。和现在的 CLI、文档保持一致，只做中文。

## 2. 参考对象：yetone/magpie 的事实与取舍

仓库存在，主语言 Go，形态确实是「一个二进制里同时有 UI 和命令行」。下面每条都标了代码出处。

| 事实 | 出处 |
| --- | --- |
| 同一个二进制：不带参数或用 `magpie app` 时打开 GUI（Wails v3 托盘加窗口），`magpie web` 在浏览器里提供同一页面，`magpie tui` 和各子命令是终端入口 | `docs/subsystems/gui-shell.md`「Runtime path」，`internal/cli/web_cli.go` |
| 前端是用 `//go:embed assets` 内嵌的静态文件，**不用框架、不用打包工具**（`app.js` 每个视图维护一个 state 对象，自己渲染；没有绑定生成器） | `internal/gui/api.go:141`，gui-shell.md 首段 |
| 页面通过同一个 handler 上的 JSON API（`/api/state`、`/api/set`…）和 Go 交互，每次写操作都返回新的 state | gui-shell.md「Runtime path」第 4 步 |
| Web 模式的认证：启动时生成一个本次运行有效的随机 key，放进链接 `/?k=…`；首次访问换成 cookie（设了 `MAGPIE_WEB_KEY` 才长期保留）；默认监听 `127.0.0.1:3430`，加 `--lan` 才监听全部地址，并提示「拿到链接的人能改 magpie、能看到 key」，可以配 `--tls-cert/--tls-key` | `internal/cli/web_cli.go`，`internal/gui/web.go` `StartWeb` / `webHandler` |
| 带 key 的响应压缩时考虑了 BREACH，脚本按内容哈希命名，避免缓存旧版本 | `web.go:336` 注释，gui-shell.md「API and assets」 |
| `-tags nogui` 编出纯终端版本，可以交叉编译；GUI 版本依赖 cgo 和平台 webview，只能在各平台本机编译 | `Makefile` 的 `cli` 和 `build` 两个目标 |
| 浏览器回归测试用 Playwright，同时跑 Chromium 和 WebKit；前端规模约 5.1 万行 JS | `internal/gui/tests/`，`wc -l assets/*.js` |
| 子系统设计文档里写有 UI 规则，例如「一次点击绝不会让页面滚动」「不用原生 `<select>`」「状态用圆点表示，不用彩色左边框」 | gui-shell.md「Shared UI rules」 |

**值得借鉴：**
- 前端用 `go:embed` 打进二进制，不引入框架和打包工具，发版仍然只有一个文件，不需要 node 构建链。
- UI 只是 API 的一个客户端，不另开一套能力。
- 回环监听加一次性链接 key 换 cookie，以及监听非回环地址时给出明确警告。
- 脚本按内容哈希命名，避免浏览器拿到旧版本。

**不适合 MPX（当时的判断；桌面壳后来单独定了）：**
- ~~Wails 原生壳、托盘、cgo~~：托盘仍不做；壳改用 **MyGo**（无 cgo，可交叉编），见 [桌面程序方案](desktop-rfc.md)。浏览器版 `mpx ui` 仍是默认入口。
- magpie 的 UI 和后端跑在同一个进程里（它管的是本机网关）；MPX 的真相在远程控制面上，所以要换成「本地页面 + 转发到远程 `/api`」的结构——桌面壳也只是换窗口，不改这条。
- 5 万行 JS 的规模、多语言和插件运行时：都远超 MPX 需要的「最小充分」。

## 3. 推荐架构

```
浏览器 ──127.0.0.1:随机端口──▶ mpx ui（操作机，嵌入 assets）
                                 ├─ GET /           内嵌页面
                                 └─ /api/*  ──转发并带上 Bearer token──▶ 控制面 /api（不改）
```

- `mpx ui [--profile P] [--addr 127.0.0.1:7480] [--no-open]`：读取和其他命令相同的 server/token（`--server` > `MPX_SERVER` > profile，见 `internal/cli/config.go:157`）。
- 只把 `/api/` 前缀转发给控制面，其他路径一律返回 404。`/sub` 不经过 `mpx ui`，订阅页保持原样。
- 页面拿不到 token，token 只在 `mpx ui` 进程内存里。浏览器靠一次性 key 换来的 HttpOnly、SameSite=Strict cookie 访问 `mpx ui`。
- 控制面一行不改：鉴权仍然是 `requireAuth` 的 Bearer 校验（`internal/server/server.go:186`），也仍然只监听 `127.0.0.1:8080`（`deploy/mpx.service`），通过反代或隧道访问。

**备选方案对比：**

| 方案 | 做法 | 优点 | 代价 / 为什么不首选 |
| --- | --- | --- | --- |
| **B. `mpx ui` 本地转发（推荐）** | 见上 | 控制面零改动，暴露面零增加；token 不进浏览器；和 CLI 共用 profile；符合「CLI 是唯一入口」的精神 | 每次用都要在操作机上跑一个进程；手机上用不了 |
| A. 控制面内嵌，挂在 `/ui` | `mpx serve` 直接提供页面，浏览器登录时输入 token | 有浏览器就能用，手机也可以 | 管理页面和 `/sub` 一起通过反代暴露到公网；要给控制面新加 cookie 会话、CSRF 防护、登录限流；token 存进浏览器；改动的正是「不做 Web UI」想守住的那条边界 |
| C. 独立 SPA（React/Vite）加 CORS | 单独部署前端 | 生态丰富 | 引入 node 构建、CORS、第二个发布物，和「单二进制、零依赖」冲突 |
| D. TUI | 在终端里做交互界面 | 不碰浏览器 | 和用户要的「既有 UI 又有命令行」不符，表格和截图展示也弱 |

## 4. 技术选型

- **前端：** 原生 HTML/JS（ES 模块，不需要构建）加一份 CSS，按 magpie 的方式内嵌，文件名带内容哈希。直接复用 `doc-site-skeleton.html` 的样式变量（字体、配色、细线卡片、深浅主题）。
- **后端：** 在 `internal/cli` 下新加 `ui.go`，用 `net/http/httputil.ReverseProxy` 转发，不加新依赖。
- **测试：** Go 测试覆盖转发、key 和 cookie、路径白名单；页面层面先用 Go 的 `httptest` 加真实控制面（`server.New`）做端到端测试。是否引入 Playwright 列为 Open Question。

## 5. 页面信息架构（以现有 CLI 能力为界）

| 页面 | 读 | 写（对应的 CLI） |
| --- | --- | --- |
| 总览 | `GET /api/status`、`/api/doctor` | 无 |
| 用户 | `GET /api/users?limit&offset&status&search&inbound_id`、`/users/{u}`、`/users/{u}/links` | add / edit / rm / enable / disable / renew / reset-traffic / revoke-sub；CSV 导入导出（`user import/export`） |
| 节点 | `GET /api/nodes`、`/nodes/{id}`、`/online`、`/config`、`/capabilities`、`/diagnostics` | add / edit / rm / sync / install / drain / undrain / probe / diagnose；核心部署（`core edit`） |
| 入站与入口 | `GET /api/inbounds`、`/inbounds/{id}/hosts` | inbound 和 host 的增删改 |
| 分流规则 | `GET /api/routing`、`/api/rules`、`/api/proxy-groups`、`/api/rule-catalog` | rule 增删改、final、test、migrate；group 增删改、preview |
| 设置 | `GET /api/settings`、模板、UA 规则 | settings set（**整体替换**，界面必须先读后写）、template、rules、test-webhook |
| 备份 | 无 | 下载 `GET /api/backup`；`restore` 只提示在终端执行 |

破坏性操作（rm、revoke-sub、restore、settings 整体替换）都要二次确认，文案和订阅页「换地址」的确认框保持一致。

## 6. 认证与安全

- **威胁：** 同机其他用户或恶意网页访问到本地端口。缓解：只监听回环地址；一次性 key 换 HttpOnly + SameSite=Strict cookie；校验 `Host` 和 `Origin`，防 DNS rebinding；`Content-Security-Policy: default-src 'self'`，禁止 iframe 嵌入。
- **不提供 `--lan`。** 要在其他设备上用，就走 SSH 隧道。以后真有需要，再仿照 magpie 的警告文案加上。
- **token 永不下发到页面。** 页面里显示 `links` 时，会出现订阅地址和凭证，这和 CLI 现有行为一致，在页面上注明即可。
- 控制面的 API 限流（`ratelimit.go`）照常生效，UI 每隔一段时间刷新一次（不低于 10s），不搞实时推送。

## 7. 视觉风格

沿用文档站和订阅页现有的 skeleton：同一套字体、配色、细线卡片和深浅主题；左侧分组导航（参考 magpie 1024px 宽度以上的侧栏）；状态用圆点表示（正常 / 已停用 / 已过期 / 已超限 / 排水中）；表格里中文按**显示宽度**对齐（顺带避开之前发现的 `user list` 对齐 bug）。每个里程碑的 PR 都附浅色、深色、手机三张截图。

## 8. 里程碑（每个都能单独合并）

| 阶段 | 内容 | 验收标准 |
| --- | --- | --- |
| **M0 骨架与只读总览** | `mpx ui` 命令、转发、key 和 cookie、内嵌资源；总览页 | 本地控制面加种子数据能打开；没有 key 访问返回 401；非 `/api` 路径返回 404；`Origin` 不对时拒绝；`go test -race` 通过；附 3 张截图 |
| **M1 用户只读到读写** | 用户列表（分页、筛选、搜索）、详情、链接；启用/停用/续期/重置/吊销/增删改 | 每个操作和对应 CLI 命令的请求体逐字节一致（测试断言）；1000 个用户时翻页流畅；附截图 |
| **M2 节点与入站** | 节点状态、排水、下发、安装、体检；入站和入口的增删改 | 下发、安装失败时页面显示控制面原样的错误（含 501 未接节点通道）；附截图 |
| **M3 分流、设置与收尾** | 分流规则页与迁移、设置（先读后写）、模板、UA 规则、备份下载；文档站增加 `mpx ui` 页面；修改 design.md 里 Non-goal 的措辞 | settings 改一个字段后其他字段不丢失（测试）；文档构建和死链检查通过 |

## 9. 风险

- **UI 和模型不同步**（原来那条 Non-goal 的理由）：靠「UI 只用现有 API、请求体和 CLI 共用」的测试约束；每次 API 有变动，在 PR 检查清单里加一项「UI 是否需要跟着改」。
- **`settings set` 是整体替换**：界面处理不当会清掉字段。M3 专门用测试覆盖。
- **手机上没法用**（方案 B 的代价）：如果用户真正想要的是「随时随地用手机管」，方案 B 就不满足，需要改选 A，安全工作量也会明显增加。
- **前端规模膨胀**：magpie 已经到 5 万行。约束：只做 §5 表里的内容，新页面要先有对应的 CLI 命令。

## 10. Open Questions（已拍板，留作记录）

1. **是否推翻 design.md 里「不做 Web UI」这一条？** 推荐：推翻，改成「不做由控制面直接提供的、对外暴露的管理 UI；本地 `mpx ui` 只作为 CLI 的一种界面」。
2. **形态选 B（本地 `mpx ui`）还是 A（控制面内嵌，挂在 `/ui`）？** 推荐 B。如果你主要在手机上管理，就选 A，我会另外补一份安全设计。
3. **M0 是否只做只读？** 推荐是：先确认观感和数据，再从 M1 开始开放写操作。
4. **是否引入 Playwright 做浏览器回归测试（会多出 node 测试依赖，仓库里已有 package.json 给文档站用）？** 推荐：M0 先不引入，只附截图；到 M1 页面有交互以后再引入。
