---
kind: part
parent: README.md
code: [internal/buildinfo/, deploy/]
verified: 2026-11-24 @ 3cd7512
---
# 部署与运维

> **30 秒版**：MPX 是单一纯 Go 静态二进制，无内置自升级守护进程、交互初始化向导或远程装核流水线。构建用 `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" ./cmd/mpx`，控制面以 systemd 服务（`deploy/mpx.service`）运行，节点上的 `xray` / `sing-box` 与控制面二进制升级均由操作者或 AI Agent 经 SSH 直接部署。

## 边界

负责：版本号注入（`internal/buildinfo`）、systemd 单元模板与部署脚本参考（[deploy/](../../deploy/)）。

不负责：控制面自升级与版本轮询、节点核心远程下载/编译安装、交互式 `init` / `bootstrap` 向导、控制面内嵌 ACME 证书签发（域名 TLS 由外部反代或 `acme.sh` 管理）。

## 怎么运转

1. **编译**：`CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o mpx ./cmd/mpx`，零 CGO 与外部构建标签依赖。
2. **控制面运行**：按 `deploy/mpx.service` 与 `deploy/mpx.env.example` 配置 `MPX_TOKEN`、监听地址与数据库路径（默认 `/var/lib/mpx/mpx.db`），启动 `mpx serve`。
3. **升级与迁移**：替换 `/usr/local/bin/mpx` 并重启 `mpx.service`；备份恢复通过 `mpx backup` / `mpx restore` 或停服后直接备份 SQLite 文件完成。

## 坑

- 迁移控制面时，落到 `/var/lib/mpx/.ssh/` 的节点私钥须确保属主与权限为 `mpx:mpx` 和 `0700`/`0600`，否则控制面进程无权 SSH 到节点。
- 升级 v0.7.0 前库还是 `template` 模式 → 控制面拒绝启动 → 需先完成分流规则表迁移。
