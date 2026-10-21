---
kind: part
parent: README.md
code: [internal/cli/init.go, internal/cli/init_steps.go, internal/cli/bootstrap.go, internal/cli/update.go, internal/cli/update_apply.go, internal/cli/update_run.go, internal/cli/ui.go, internal/selfupdate/, internal/release/, internal/updatecheck/, internal/buildinfo/, internal/xrayrelease/, internal/singboxbuild/, internal/server/update_nodes.go, internal/ui/update_local.go, deploy/, scripts/dev-deploy.py, scripts/dev-deploy-remote.py, scripts/dev-subdiff/]
verified: 2026-10-21 @ 439f2df
---
# 安装与升级

> **30 秒版**：从一台能 root SSH 的裸 VPS 开始，`mpx init`（或 `mpx ui` 的首次安装向导）在你电脑上经 SSH 把控制面和第一个节点装好，最后打印订阅地址。之后升级也不用登录：`mpx update`（或 `mpx ui` 的「更新」入口）能升控制面、各节点核心和本机 CLI，失败自动回到上一版，本机也能手动回退。

## 边界

负责：安装脚本、`init` / `bootstrap`、发布件校验、版本号、查更新、分层升级、一键编排与回滚。

不负责：节点上的 MPX 组件（没有）；代码签名（明确不做，只用 SHA256SUMS）；发版流水线本身（`.github/workflows/release.yml`，步骤见 [CONTRIBUTING](../../CONTRIBUTING.md)）。

## 怎么运转

1. **装**：`mpx init` 是 CLI 里唯一会 SSH 的地方（decisions 第 32 条）。它只做 SSH 编排：上传或下载 `mpx`、跑 [deploy/install.sh](../../deploy/install.sh) 装成 systemd 服务，再在 VPS 上跑 `mpx bootstrap`，后者仍是 `/api` 客户端，负责建节点、入站、用户。重复跑安全，已有对象复用。
2. **校验**：每个发布带 SHA256SUMS，下载后先核哈希（[internal/release](../../internal/release/)）。安装脚本装最新版时取不到 SHA256SUMS 就拒装（v0.9.2 起；点名的老版本发布时还没有这个文件，才提示后照装）；桌面自更新缺清单一律拒绝。版本号只能来自编译期注入，拿不到就是空，不编假版本（版本号规则出自 decisions 第 37 条，仍有效；该条的「只提醒不代劳」与 changelog 部分已被第 39 条取代）。
3. **升控制面**：控制面进程以 `mpx` 用户运行，写不了 `/usr/local/bin`；所以 `POST /api/update/control` 只落一个请求文件，由 root 级的 `mpx-update.path` / `.service` 触发 `mpx update run control`。安装目录是「版本目录加符号链接」，保留两版，回滚就是换链接；升级后健康检查，不过自动换回上一版。方案见 [自动升级方案](../internal/update-rfc.md)。
4. **升节点核心**：`mpx update apply nodes` 让控制面对每台节点逐台升级。xray 包由节点自己从 GitHub 拉（控制面只递 URL 和取自官方 `.dgst` 的摘要），新核心先过 `xray run -test` 验现有配置再顶替，任何一台失败就回滚这台、整批停下。目标版本钉在每个 MPX 版本里（`xrayrelease.RecommendedVersion`），不追上游 latest；有 sing-box 的节点由控制面按 `singboxbuild.DefaultVersion` 编译后推送（官方发布件不带 `with_v2ray_api`，撑不起按用户计量）。跳不跳过按**现场**版本判：缓存（上一轮巡检）说已是目标版本时仍会 SSH 问一次，问不到才退回缓存 —— 否则两次巡检之间被手工换过核心的节点会被一直 skip。
5. **一键与进度**：节点核心升级是控制面里的任务，事件按 seq 增量拉取，单次响应 < 4KB，页面不可见时暂停轮询；`mpx update apply nodes` 在终端逐条打同样的进度。页面只放一个「更新」按钮，按 控制面 → 节点核心 → 本机 的顺序把三层升完（decisions 第 39 条，v0.8.0 起三层齐）；本机一层在桌面构建里是桌面程序本身（取 `mpx_desktop_*` 归档），升完原进程 exec 新二进制重启，`mpx.prev` 留上一版。
6. **本机升级留痕**：命令行与页面发起的本机 CLI 升级写同一份 `update.json`，`mpx update show` 才看得到上一次的结果。页面那条路不认识 cli 包，靠 [internal/ui](../../internal/ui/) 的 `Options.CLIUpdated` 回调把结果交回 cli 侧落盘；命令行的 trigger 记 `manual`，页面记 `ui`。
7. **本机开发版**：在工作区运行 `bash scripts/dev-install.sh`，把启动器装到 PATH 已包含的 `~/.local/bin/mpx-dev`，二进制放在 `~/.local/share/mpx-dev/`。启动器按 HEAD、已跟踪改动和未忽略的未跟踪文件内容计算指纹，变化才编译；`mpx-dev-update` 强制重编，不拉代码或切分支。编译失败时普通启动明确提示后使用上次成功版本，强制重编则返回失败；没有可用旧版就停止。重新运行 `mpx-dev app` 会关闭旧源码版本的开发窗口，相同版本提示使用现有窗口。`mpx-dev version` 显示 `dev-提交号-clean/dirty`。它与正式版共用控制面配置；桌面状态按 `mpx-dev` 独立，UI 默认随机端口，并忽略发布版的重启交接。开发版禁止本机发布件更新、回退与页面一键更新，改用重编命令，避免覆盖开发二进制或正式版更新记录。


8. **开发控制面部署**：`mpx-dev deploy` 仅接受 clean 工作区，不拉代码。连接信息读本机 `~/.config/mpx-dev/deploy.json`，SSH 主机指纹必须预先核实并存在同目录的 `known_hosts`；私钥从配置指定的本机备份归档中临时读取，不入仓库。交叉编译后先导出所有用户的三种订阅，停服务并快照数据库、备份二进制，远端备份在root 专用的 `/var/lib/mpx-dev-deploy/backups/`，同时校验复制到 `~/mpx-backups/`，然后才切换版本链接。健康检查、presence 或订阅差异不符就恢复数据库与二进制；未确认的 SSH 会话关闭也恢复。只放行已批准的默认 DNS 和手动分组变更，其余字节变化均阻止确认。`mpx-dev deploy --rollback` 恢复上次成功部署前的备份，并先备份当前状态。备份含机密，只允许属主访问。部署期间短暂停服务，并暂停发布件更新监听；结束恢复监听原状态。开发控制面在 API 和 root 更新执行入口均拒绝发布件更新，改用部署命令。

本机部署配置字段：`ssh_host`、`ssh_port`、`ssh_user`、`ssh_key_archive`（含 `id_ed25519` 的归档）、`service`、`binary`（版本链接）、`real_binary`（现有版本文件）、`db`、`addr`、`env_file`、`machine`（`x86_64` 或 `aarch64`）。配置权限必须为 0600；服务端需有 Python 3、SQLite 和 systemd。机器断电或强制杀死远端部署进程时，`/var/lib/mpx-dev-deploy/pending.json` 会阻止下一次部署，应从其指向的备份先恢复，不能直接删除标记重试。

## 坑

- 从 v0.6.0 或更早升级 → 老布局没有版本目录和升级单元 → 先在 VPS 上跑一次 `install.sh --version v0.6.1` 或更新版本。
- 补跑 `install.sh` 升级时没传 `--addr` → 脚本读现有 mpx.service 的 ExecStart 里的 `--addr`（`--addr=值` 也认）沿用；只有旧单元不存在或读不出 `--addr` 才按仓库默认回环装并说明。mpx-update.service 健康检查的地址从这个最终地址推出（`update_addr`，v0.9.2 起）：通配地址连 `127.0.0.1:端口`，绑在具体地址上就连那个地址；以前固定打 `127.0.0.1` 且只看这次传没传 `--addr`，端口不是 8080 时检查会失败。
- 升级 v0.7.0 前库还是 `template` 模式 → 控制面拒绝启动 → 先用 v0.6.6 迁移（[CHANGELOG](../../CHANGELOG.md)）。
- 源码编出的伪版本 → 比不出新旧时一律算「没有更新」，不报「落后 1 个」（decisions 第 37 条的实机教训）。
- 节点连不上 GitHub → `apply nodes` 拉不动 xray 包 → 这台失败、整批停下；推送整包的 `mpx node install` 还在。
- `mpx init` 至今只支持单机全包（控制面和节点同一台）；分开部署走手工步骤（[缺口与待定项](../internal/open-questions.md) 第 4 节）。
- 发布件文件名是升级程序写死的 → 老版本按固定名字下载压缩包 → 改发布格式时压缩包必须继续保留。
