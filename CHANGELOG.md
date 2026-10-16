# 更新记录

每个版本的完整说明见 [GitHub Releases](https://github.com/RovenEverward/my-proxy/releases)。本文件从 v0.5.0 开始记录。

## v0.6.6 (2026-10-16)

### 变更
- `mpx ui` 去掉「用途」页与相关写操作放行（issue #49 / R6 第一刀）。分流请用「分流」页（`mpx rule` / `mpx group`）；节点地区仍可在节点编辑里改。控制面 `/api/purposes*` 与 `mpx purpose` 命令仍在，计划 v0.7.0 删除。

包含：#81。

## v0.6.5 (2026-10-16)

本版主题：`mpx ui` 顶部「更新」pill。

### 新增
- 有新版本时，`mpx ui` 顶栏出现「更新」按钮（magpie 风格）。点开弹窗看本机 CLI 与控制面各自的当前→最新、发布说明摘要，以及等价命令；「稍后」本会话内不再提示，「重启以更新」先升级控制面再升级本机 CLI。本机二进制换掉后需关掉终端里的 `mpx ui` 再开一次才生效。
- 本机接口 `/local/update`（看版本）、`/local/update/cli`（升级本机 CLI）、`/local/update/control`（转发控制面升级请求）；`/ui/meta` 增加 `cli_version`。
- 桌面程序方案入库：`docs/internal/desktop-rfc.md`（框架定 MyGo，Linux 出 .deb + tar.gz，不签名，自更新走 SHA256SUMS）。

### 兼容性
- 控制面 API 无变更；`POST /api/update/control` 已在上个版本，本版只是让 `mpx ui` 放行并加上入口。
- 新增的 `/local/update*` 只在本机 `mpx ui` 进程里。

包含：#78、#79。

## v0.6.4 (2026-10-16)

本版主题：`mpx ui` 里点着就能加节点、装控制面。

### 新增
- 节点页「添加节点」改为向导：填 VPS 地址和 SSH 密码，依次装控制面公钥、登记节点、建 REALITY 443 入站和入口、装核心并下发，完成页显示自动识别的显示名和会进哪些套餐。每一步都对应一条已有命令（`node add --ssh-password`、`inbound add`、`host add`、`node install`），失败时可以返回修改、重试这一步或删除刚加的节点。已能免密 SSH 的机器仍可点「已能免密 SSH，直接登记」走原来的表单。
- 首次安装向导：还没配置控制面时，`mpx ui` 不再报错退出，而是打开首次安装向导（连接、检查、设置、安装、完成），和 `mpx init` 走同一条路径；装完本机自动接入、页面直接进入管理界面，并显示订阅二维码和给别的电脑用的 `mpx connect` 命令。已配置时可用 `mpx ui --setup` 进入。
- 安装进度只拉增量：每次响应小于 4 KB，长时间无变化时轮询从 2 秒放慢到 5、10 秒，页面不可见时暂停。

### 安全
- SSH 密码只从浏览器经本机 `mpx ui` 交给一次 SSH 会话，用来装公钥；不落盘、不进日志和进程参数，请求发出后页面立即清空，等价命令里只写 `--ssh-password`。

### 兼容性
- 控制面 `/api` 没有变化；新增的 `/local/setup/*`、`/local/qr.svg` 只在本机 `mpx ui` 进程里，不经过控制面。
- `mpx init` 内部拆成了可复用的步骤序列，终端输出与以前逐字节一致。

包含：#65、#68、#69、#71、#72、#73、#74、#75、#76。

## v0.6.3 (2026-10-16)

### 修复
- `mpx update` / `mpx update show` 现在会让控制面先向发布站重新检查一次最新版本，新版发布后立刻就能看到；以前要等控制面后台检查（6 小时一轮）或重启。手动检查一分钟内最多一次，避免耗光 GitHub 接口配额。
- 在已经设过设备密码的机器上重装或升级时，`install.sh` 不再提示「设备密码还没设」；脚本会问一次本机控制面，按实际情况提示。

### 兼容性
- `GET /api/update` 新增可选参数 `refresh=1`（只新增）：带上时先强制检查上游再返回；不带时行为不变。旧控制面会忽略这个参数，新 CLI 连旧控制面照常可用。

包含：#70。

## v0.6.2 (2026-10-16)

### 文档
- README「升级」补充 `mpx update apply control` 的阶段报告说明：控制面重启的几秒里连不上属于正常。

本版只改文档，用来验证从 v0.6.1 在本机升级控制面（`mpx update apply control`）与回退。

## v0.6.1 (2026-10-16)

本版主题：在本机升级 CLI 和控制面（`mpx update`）。

### 版本查看
- 新增 `mpx update`（`mpx update show` 等价）：一行显示本机 CLI、控制面、每台节点每个核心各自的当前版本与目标/最新版本，只读、不升级、不连节点。源码编译的版本显示「未知（源码编译）」，不报「有新版」。
- 新增只读接口 `GET /api/update`，返回控制面自己与各节点核心的版本；控制面没有这个接口时（旧版本），`mpx update` 回退到 `/api/status` 里已有的版本信息，并说明节点核心版本取不到。
- CLI 与控制面主版本或次版本不一致时，`mpx update` 与 `mpx status` 在 stderr 打一行警告，不影响退出码；`--json` 输出不受影响。
- 新增 `mpx update apply cli` 与 `mpx update rollback cli`：从发布站取回指定版本（默认最新）、核对 SHA256SUMS 后替换本机 CLI，旧文件留在 `<mpx>.prev`；`--version` 选版本，`--allow-downgrade` 允许降级，`-y/--yes` 跳过确认（非交互环境必须加）。`nodes` 部件暂未支持。`mpx update` 会在本机 CLI 那行附上最近一次结果（成功/失败/已回退），`-o json` 增加 `cli_last` 字段。
- 新增 `mpx update apply control` 与 `mpx update rollback control`：先在控制面登记升级/回退请求（`--version` 选版本，默认最新；`-y/--yes` 跳过确认，非交互环境必须加），再轮询 `GET /api/update` 报告下载、校验、切换、健康检查各阶段，直到升级成功、失败或自动回退；控制面重启期间连不上不算失败，超时后提示用 `mpx update` 查看结果。旧控制面没有 `/api/update` 时提示先用 `install.sh` 升级一次，进行中会先拒绝。`mpx update` 会在控制面那行附上最近一次结果（进行中报阶段）。
- 新增 `POST /api/update/control`（只新增）：登记一次控制面升级/回退请求（`version` 可留空表示最新，另有 `allow_downgrade`、`action`）；请求文件由 root 的一次性单元执行，`GET /api/update` 的 `control` 部分相应附带最近一次执行状态 `last`；新增隐藏命令 `mpx update run control`，由 `mpx-update.service` 调用。

### 安装与升级
- 安装脚本改为「版本目录 + 符号链接」布局：每版装在 `/usr/local/lib/mpx/releases/<版本>/mpx`，`/usr/local/bin/mpx` 改为指向当前版的符号链接，切换用 rename 原子完成，只保留当前版与上一版；旧布局（普通文件）会在升级时按自身版本收进版本目录当上一版，重复安装同一版本安全。`install.sh` 同时装上并启用 `mpx-update.service` 与 `mpx-update.path`（`.path` 监视 `/var/lib/mpx/update/request`），控制面装好后即可在本机用 `mpx update apply control` 升级，不必每次重跑 `install.sh`。
### 文档
- README「升级」一节改为在本机用 `mpx update show / apply cli / apply control / rollback` 操作，并说明从 v0.6.0 及以前升级时需在 VPS 上先跑一次 `install.sh --version v0.6.1`。

### 兼容性
- 控制面 API 只新增：`GET /api/update`、`POST /api/update/control`。

包含：#57–#64。

## v0.6.0 (2026-10-16)

### 分流规则（替代用途）
- 新增「分流规则」：从上到下匹配，第一条命中的生效，都没命中走「其他全部」。规则类型有域名后缀、关键词、内置规则组（`mpx rule catalog`）、GeoIP、CIDR；去向可以是直连、拦截或策略组。每个用户还可以有自己的规则，排在全局规则前面。
- 策略组按地区、协议、能力检测、标签自动收进入口，新加的节点符合条件会自动进组；可选自动测速（auto）或手动选择（manual）。
- 由规则表生成 Clash Meta 与 sing-box 的分流部分。
- 命令：`mpx rule list/add/rm/move/toggle/test/final/catalog/migrate/mode`、`mpx group list/add/edit/rm/preview`；对应新增 `/api/rules`、`/api/proxy-groups` 等管理接口（只新增）。
- 管理界面新增「分流规则」页。
- **升级时自动迁移**：首次启动时，旧模板和用途自动转换成规则表，只迁移一次；`mpx rule migrate --report` 查看迁移报告。如需临时退回旧模板，可用 `mpx rule mode template`（该回退开关同样会在 v0.7.0 删除，见下）。

### 套餐（权限组）
- 新增套餐（权限组）：`mpx plan list/add/edit/show/rm/suggest`，以及 `GET/POST /api/plans`、`GET/PUT/DELETE /api/plans/{id}`、`GET /api/plans/{id}/preview`。按节点授权为主（含以后新加的入站），入站可选；空套餐表示什么都不能用，「全部」用 `all_nodes` 表达。
- 用户新增 `plan_id`（省略即保持原套餐；不能用 `null` 或 `--plan ""` 把用户移出套餐，以免权限悄悄回落到全部节点，要放宽请改挂到默认套餐）；`POST /api/users` 支持 `plan_id`，`GET /api/users?plan=<id>` 按套餐筛选；`POST /api/proxy-groups/preview?plan=<id>` 只看套餐范围内的成员。CLI 增加 `mpx user add/edit --plan`、`mpx user list --plan`，CSV 导出/导入增加「套餐」列（导入时空列不改归属）。
- 系统自带「默认套餐」（`all_nodes`，可编辑不可删除）。升级时自动创建，原有「全部入站」的用户自动归入；`inbound_ids` 非空的用户按相同集合自动建迁移套餐，并且不清空原有 `inbound_ids`，保证升级前后每个用户能用的入站完全一致。删除还有用户挂着的套餐会被拒绝（返回用户数），需用 `?reassign=<id>`（`mpx plan rm --reassign`）改挂到一个具体套餐（可以是默认套餐）后再删，`reassign` 为空同样被拒绝；改挂与删除在同一个事务里完成。
- 套餐或用户归属变化后，订阅缓存与节点收敛沿用改用户时的同一条路径，xray 下发的用户名单随之更新。
- 管理界面新增「套餐」页：默认套餐固定在第一行；用户页可以按套餐筛选，编辑用户时必须选择套餐；删除套餐时必须指定把用户转到哪个套餐。套餐已包含全部节点的用户，不能再单独加入站。

### 管理界面
- 全局改为无框风格：去掉按钮、输入框、卡片、列表、表格上的描边，改用浅底色、留白和圆角区分层次；深色模式用明度台阶区分层次。

### 文档
- README 改版：居中首屏、管理界面截图（亮色 / 深色），以及「适不适合用 → 三步快速开始 → 常用命令 → 功能介绍」的结构。

### 废弃项，v0.7.0 删除（见 #49）
- 分流改由「分流规则」管理（`mpx rule`、`mpx group`、UI 的分流规则页）。升级后首次启动时，旧模板和用途会自动迁移成规则表；`mpx rule migrate --report` 可以查看迁移报告。
- 从 v0.6.0 起，`/api/purposes*` 和 `?purpose=` 的响应带 `Deprecation: true` 头，`mpx purpose` 每次运行都会打警告。它们仍然可用，但改动不再影响 rules 模式下的订阅。
- **v0.7.0 会删除**以下内容，请在此之前改用分流规则：
  - 接口 `/api/purposes`、`/api/purposes/{slug}`、`/api/purposes/{slug}/preview/{username}/{format}`；
  - `mpx purpose` 全部子命令；
  - 订阅地址的 `?purpose=` 兼容，以及 `purposes` 表；
  - 旧模板回退：`routing_mode=template`、`PUT /api/routing`（`mpx rule mode` 只保留查看）；
  - `/api/nodes|hosts/{id}/metadata` 中的 `line`、`purposes` 字段（地区、城市保留）。

### 兼容性
- 控制面 API 只新增、不修改；用途相关接口在本版本仍然可用。
- 升级时自动完成两项迁移（分流规则、默认套餐），每个用户能用的入站与升级前一致。

包含：#43–#55。

## v0.5.1 (2026-10-15)

### 修复
- 控制面查更新时,中文发布说明超过 2000 字节会 panic 并反复重启;改为按字符数截断。
- UI SSH 列不再显示 `root@:22`,改为 `用户@节点地址:端口`。

## v0.5.0 (2026-10-15)

### 用名字指定节点和入站
- 原来要写 id 的地方,现在都可以写名字(不区分大小写)、显示名或唯一前缀:`mpx node sync la-1`、`mpx node sync 洛杉矶A`。入站写成 `节点/tag`,例如 `la-1/reality`。命中多个时列出候选,不会猜。
- 纯数字仍然按 id 处理;节点名恰好是数字时写 `name:2`。新建节点或改名时不能再用纯数字。
- 写操作靠前缀命中时,会先回显实际解析到的对象,例如「→ 节点 3 la-2(🇺🇸 洛杉矶 B)」。
- Shell 补全:`mpx completion bash|zsh|fish|powershell` 生成的脚本能补全节点、入站、用户名。
- `mpx node list` 第一列是显示名,第二列是内部名,ID 放到最后一列。

### 节点显示名
- 显示名格式:旗帜 + 城市(没有城市时用国家中文名) + 字母,例如 `🇺🇸 洛杉矶 A`、`🇯🇵 日本 B`。字母在同一地区同一城市内按 A、B…Z、AA 分配;删除节点不会让其他节点重排。
- **新建节点时自动识别**:控制面按节点 IP 查询 ipwho.is,用内置的城市对照表转成中文,再分配字母。识别失败不影响建节点,只给出提示。手动设过的值不会被覆盖。节点地址是域名时不自动识别。关闭方法:`mpx serve --auto-locate=false`,或设置环境变量 `MPX_AUTO_LOCATE=0`。注意:节点 IP 会发给 ipwho.is。
- 手动设置:`mpx node edit la-1 --region US --city 洛杉矶 [--letter B]`。
- 重新识别:`mpx node locate la-1`,只显示建议;加 `--apply` 才写入。
- 已有节点批量起名:`mpx node relabel --all --dry-run` 先看「旧名 → 新名」对照表,去掉 `--dry-run` 后确认写入。**升级时不会自动执行**。

### 订阅里的名字
- 入口没有手写 remark 时,默认名从节点名改为显示名(节点还没有显示名时仍用节点名)。
- 同一节点在一份订阅里有多个这样的入口时,自动加协议后缀:`🇺🇸 洛杉矶 A · Reality`、`🇺🇸 洛杉矶 A · Hy2`。仍然重名的再加序号。
- remark 里可以用新变量 `{NODE_DISPLAY}`。`{NODE_NAME}` 仍然是内部名,已有的 remark 不会变。
- **会看到的变化**:一个节点有多个默认名入口时,客户端里的名字会从「la-1」「la-1 2」变成「la-1 · Reality」「la-1 · WS」;跑过 `relabel` 后会再变成显示名。客户端里可能需要重新选一次节点。

### 管理界面(mpx ui)
- 节点列表和详情页以显示名为主;编辑抽屉里新增地区、城市、字母三项和「重新识别」按钮。保存时发出的请求与 `mpx node edit` 一致。
- 修复:命令块间距不对;写操作后列表不刷新;侧栏里的长地址溢出;并排的表单字段没有顶部对齐。

### 其他
- 安装脚本不再自动使用 /tmp/mpx 等残留的旧文件,安装前会核对版本。

### 兼容性
- 控制面 API 只新增、不修改:
  - 节点 metadata 新增 `city`、`letter`,PUT 时省略这两项则保留原值;
  - 节点视图新增只读字段 `display`;
  - 新增 `POST /api/nodes/{id}/locate`;
  - `POST /api/nodes` 的响应新增 `display`、`locate_note`。
- v0.4.0 说过「下一个版本只接受 HTTPS」,**本版本没有做这项改动**:仍然接受明文 HTTP 的管理请求,并会给出警告。

包含:#31、#32、#33–#36、#37、#38、#39、#40。
