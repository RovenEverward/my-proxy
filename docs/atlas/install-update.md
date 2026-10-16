---
kind: part
parent: README.md
code: [internal/cli/init.go, internal/cli/init_steps.go, internal/cli/bootstrap.go, internal/cli/update.go, internal/selfupdate/, internal/release/, internal/updatecheck/, internal/buildinfo/, deploy/]
verified: 2026-10-17 @ 36a3f1f
---
# 安装与升级

> **30 秒版**：从一台能 root SSH 的裸 VPS 开始，`mpx init`（或 `mpx ui` 的首次安装向导）在你电脑上经 SSH 把控制面和第一个节点装好，最后打印订阅地址。之后升级也不用登录：`mpx update` 能远程升级本机 CLI 和控制面，失败自动回到上一版，也能手动回滚。

## 边界

负责：安装脚本、`init` / `bootstrap`、发布件校验、版本号、查更新、远程升级与回滚。

不负责：节点上的 MPX 组件（没有）；代码签名（明确不做，只用 SHA256SUMS）；发版流水线本身（`.github/workflows/release.yml`，步骤见 [CONTRIBUTING](../../CONTRIBUTING.md)）。

## 怎么运转

1. **装**：`mpx init` 是 CLI 里唯一会 SSH 的地方（decisions 第 32 条）。它只做 SSH 编排：上传或下载 `mpx`、跑 [deploy/install.sh](../../deploy/install.sh) 装成 systemd 服务，再在 VPS 上跑 `mpx bootstrap`，后者仍是 `/api` 客户端，负责建节点、入站、用户。重复跑安全，已有对象复用。
2. **校验**：每个发布带 SHA256SUMS，下载后先核哈希（[internal/release](../../internal/release/)）。版本号只能来自编译期注入，拿不到就是空，不编假版本（decisions 第 37 条）。
3. **升级**：控制面进程以 `mpx` 用户运行，写不了 `/usr/local/bin`；所以 `POST /api/update/control` 只落一个请求文件，由 root 级的 `mpx-update.path` / `.service` 触发 `mpx update run control`。安装目录是「版本目录加符号链接」，保留两版，回滚就是换链接。方案见 [自动升级方案](../internal/update-rfc.md)。

## 坑

- 从 v0.6.0 或更早升级 → 老布局没有版本目录和升级单元 → 先在 VPS 上跑一次 `install.sh --version v0.6.1` 或更新版本。
- 升级 v0.7.0 前库还是 `template` 模式 → 控制面拒绝启动 → 先用 v0.6.6 迁移（[CHANGELOG](../../CHANGELOG.md)）。
- 源码编出的伪版本 → 比不出新旧时一律算「没有更新」，不报「落后 1 个」（decisions 第 37 条的实机教训）。
- `mpx init` 至今只支持单机全包（控制面和节点同一台）；分开部署走手工步骤（[缺口与待定项](../internal/open-questions.md) 第 4 节）。
- 发布件文件名是升级程序写死的 → 老版本按固定名字下载压缩包 → 改发布格式时压缩包必须继续保留。
