# 更新记录

每个版本的完整说明见 [GitHub Releases](https://github.com/RovenEverward/my-proxy/releases)。本文件从 v0.5.0 开始记录。

## Unreleased

### 套餐（API 与 CLI）
- 新增套餐（权限组）：`mpx plan list/add/edit/show/rm/suggest`，以及 `GET/POST /api/plans`、`GET/PUT/DELETE /api/plans/{id}`、`GET /api/plans/{id}/preview`。按节点授权为主（含以后新加的入站），入站可选；空套餐表示什么都不能用，「全部」用 `all_nodes` 表达。
- 用户新增 `plan_id`（省略即保持原套餐，显式 `null` 表示摘掉）；`POST /api/users` 支持 `plan_id`，`GET /api/users?plan=<id>` 按套餐筛选；`POST /api/proxy-groups/preview?plan=<id>` 只看套餐范围内的成员。CLI 增加 `mpx user add/edit --plan`、`mpx user list --plan`，CSV 导出/导入增加「套餐」列（导入时空列不改归属）。
- 系统自带「默认套餐」（`all_nodes`，可编辑不可删除）。升级时自动创建，原有「全部入站」的用户自动归入；`inbound_ids` 非空的用户按相同集合自动建迁移套餐，并且不清空原有 `inbound_ids`，保证升级前后每个用户能用的入站完全一致。删除还有用户挂着的套餐会被拒绝（返回用户数），可用 `?reassign=<id>` 改挂或 `?reassign=` 摘掉后删除；改挂与删除在同一个事务里完成。
- 套餐或用户归属变化后，订阅缓存与节点收敛沿用改用户时的同一条路径，xray 下发的用户名单随之更新。

### 用途废弃，计划在 v0.7.0 删除
- 分流改由「分流规则」管理（`mpx rule`、`mpx group`、UI 的分流规则页）。升级后首次启动时，旧模板和用途会自动迁移成规则表；`mpx rule migrate --report` 可以查看迁移报告。
- 从 v0.6.0 起，`/api/purposes*` 和 `?purpose=` 的响应带 `Deprecation: true` 头，`mpx purpose` 每次运行都会打警告。它们仍然可用，但改动不再影响 rules 模式下的订阅。
- **v0.7.0 会删除**用途的接口、`mpx purpose` 命令、`purposes` 表和 `?purpose=` 兼容。请在此之前改用分流规则。

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
