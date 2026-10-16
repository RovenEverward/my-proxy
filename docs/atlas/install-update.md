---
kind: part
parent: README.md
code: [internal/cli/init.go, internal/cli/init_steps.go, internal/cli/bootstrap.go, internal/cli/update.go, internal/cli/update_apply.go, internal/cli/update_run.go, internal/selfupdate/, internal/release/, internal/updatecheck/, internal/buildinfo/, internal/xrayrelease/, internal/singboxbuild/, internal/server/update_nodes.go, deploy/]
verified: 2026-10-17 @ 65a3d5e
---
# 安装与升级

> **30 秒版**：从一台能 root SSH 的裸 VPS 开始，`mpx init`（或 `mpx ui` 的首次安装向导）在你电脑上经 SSH 把控制面和第一个节点装好，最后打印订阅地址。之后升级也不用登录：`mpx update`（或 `mpx ui` 的「更新」入口）能升控制面、各节点核心和本机 CLI，失败自动回到上一版，本机也能手动回退。

## 边界

负责：安装脚本、`init` / `bootstrap`、发布件校验、版本号、查更新、分层升级、一键编排与回滚。

不负责：节点上的 MPX 组件（没有）；代码签名（明确不做，只用 SHA256SUMS）；发版流水线本身（`.github/workflows/release.yml`，步骤见 [CONTRIBUTING](../../CONTRIBUTING.md)）。

## 怎么运转

1. **装**：`mpx init` 是 CLI 里唯一会 SSH 的地方（decisions 第 32 条）。它只做 SSH 编排：上传或下载 `mpx`、跑 [deploy/install.sh](../../deploy/install.sh) 装成 systemd 服务，再在 VPS 上跑 `mpx bootstrap`，后者仍是 `/api` 客户端，负责建节点、入站、用户。重复跑安全，已有对象复用。
2. **校验**：每个发布带 SHA256SUMS，下载后先核哈希（[internal/release](../../internal/release/)）。版本号只能来自编译期注入，拿不到就是空，不编假版本（版本号规则出自 decisions 第 37 条，仍有效；该条的「只提醒不代劳」与 changelog 部分已被第 39 条取代）。
3. **升控制面**：控制面进程以 `mpx` 用户运行，写不了 `/usr/local/bin`；所以 `POST /api/update/control` 只落一个请求文件，由 root 级的 `mpx-update.path` / `.service` 触发 `mpx update run control`。安装目录是「版本目录加符号链接」，保留两版，回滚就是换链接；升级后健康检查，不过自动换回上一版。方案见 [自动升级方案](../internal/update-rfc.md)。
4. **升节点核心**：`mpx update apply nodes` 让控制面对每台节点逐台升级。xray 包由节点自己从 GitHub 拉（控制面只递 URL 和取自官方 `.dgst` 的摘要），新核心先过 `xray run -test` 验现有配置再顶替，任何一台失败就回滚这台、整批停下。目标版本钉在每个 MPX 版本里（`xrayrelease.RecommendedVersion`），不追上游 latest；有 sing-box 的节点由控制面按 `singboxbuild.DefaultVersion` 编译后推送（官方发布件不带 `with_v2ray_api`，撑不起按用户计量）。
5. **一键与进度**：节点核心升级是控制面里的任务，事件按 seq 增量拉取，单次响应 < 4KB，页面不可见时暂停轮询；`mpx update apply nodes` 在终端逐条打同样的进度。`mpx ui` 只放一个「更新」按钮，按 控制面 → 节点核心 → 本机 的顺序把三层升完，本机升完 `mpx ui` 自动重启（decisions 第 39 条；页面目前编排了控制面与本机，节点层随该条并入）。

## 坑

- 从 v0.6.0 或更早升级 → 老布局没有版本目录和升级单元 → 先在 VPS 上跑一次 `install.sh --version v0.6.1` 或更新版本。
- 升级 v0.7.0 前库还是 `template` 模式 → 控制面拒绝启动 → 先用 v0.6.6 迁移（[CHANGELOG](../../CHANGELOG.md)）。
- 源码编出的伪版本 → 比不出新旧时一律算「没有更新」，不报「落后 1 个」（decisions 第 37 条的实机教训）。
- 节点连不上 GitHub → `apply nodes` 拉不动 xray 包 → 这台失败、整批停下；推送整包的 `mpx node install` 还在。
- `mpx init` 至今只支持单机全包（控制面和节点同一台）；分开部署走手工步骤（[缺口与待定项](../internal/open-questions.md) 第 4 节）。
- 发布件文件名是升级程序写死的 → 老版本按固定名字下载压缩包 → 改发布格式时压缩包必须继续保留。
