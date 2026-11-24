<div align="center">

<picture><source media="(prefers-color-scheme: dark)" srcset="docs/public/lockup-dark.svg"><img src="docs/public/lockup-light.svg" height="48" alt="MPX"></picture>

# MPX

### Agent-Native 极简多节点代理编排核心

**单一纯 Go 二进制 + SQLite：声明式多节点 `xray` / `sing-box` 编排、原子 SSH 下发、7×24 多格式订阅分发，为极客与 AI Agent 设计。**

[![Release](https://img.shields.io/github/v/release/RovenEverward/my-proxy?label=release&color=111111)](https://github.com/RovenEverward/my-proxy/releases/latest) [![Go](https://img.shields.io/github/go-mod/go-version/RovenEverward/my-proxy?color=111111)](go.mod) [![License](https://img.shields.io/badge/license-MIT-111111)](LICENSE)<br>
![macOS](https://img.shields.io/badge/macOS-000000?logo=apple&logoColor=white) ![Linux](https://img.shields.io/badge/Linux-FCC624?logo=linux&logoColor=black) ![Windows](https://img.shields.io/badge/Windows-0078D4?logo=windows&logoColor=white)

**[发布页](https://github.com/RovenEverward/my-proxy/releases/latest)** &nbsp;·&nbsp; **[Agent Skill](skills/mpx/SKILL.md)** &nbsp;·&nbsp; **[文档](https://RovenEverward.github.io/my-proxy/)** &nbsp;·&nbsp; **[更新记录](CHANGELOG.md)**

</div>

<br>

## 为什么是 MPX？

AI Agent 已经能直接 SSH 运维服务器，重型 Web 面板、自升级守护进程与常驻探测器的维护成本远高于价值；但把多台代理 VPS 和几十上百个朋友的订阅稳妥管起来，仍有三件不能靠临时脚本凑合的确定性底层活：

1. **声明式多节点配置编排与原子同步**：控制面持有唯一真相（SQLite），节点不装任何 MPX 组件。`mpx node sync --dry-run` 先预检哈希变化，`mpx node sync` 经 SSH 下发前自动跑核心校验、失败自动回滚；加减用户与超限停用走每 30 秒一次的无重启热收敛（Xray gRPC），不踢在线连接。
2. **7×24 确定性多格式订阅引擎**：统一聚合自建节点（VLESS + REALITY、Hysteria2、TUIC、Trojan、WireGuard、WS/gRPC/XHTTP）与外部机场订阅源，结合策略组、分流规则与 `MetaCubeX/meta-rules-dat` 二进制规则集镜像（`.mrs` / `.srs`），一条 `/sub/<token>` 按客户端 User-Agent 自动输出 Clash Meta、sing-box、Xray、Base64 订阅，浏览器打开则为轻量自助用量与二维码页。
3. **Agent-Native 交互面**：全命令支持 `-o json` 结构化输出、`--ssh` 免密通道（复用 `~/.ssh/config` 与本地密钥，不落 token）、清晰退出码（`doctor` 故障返回 `3`）以及随仓分发的官方 Agent Skill（[skills/mpx/SKILL.md](skills/mpx/SKILL.md)）。

> **什么不在核心里？** MPX 不再内置 Web 管理台、密码会话系统、内置 ACME 签发器、套餐分层、交互初始化向导、自升级与远程装核、主动 IP 探测/评级或常驻 CDN 测速——这些高灵活度或平台运维工作直接由操作者或 AI Agent 通过 SSH / `acme.sh` 完成，结果经 `mpx` CLI 写回确定性核心。

## 快速开始

### 1. 编译单一纯 Go 二进制

零 CGO、零前端依赖、零特殊 build tags：

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o mpx ./cmd/mpx
```

### 2. 启动控制面并接入 CLI

在控制面 VPS 上参考 [deploy/mpx.service](deploy/mpx.service) 与 [deploy/mpx.env.example](deploy/mpx.env.example) 启动 `mpx serve`（配置 `MPX_TOKEN`、监听地址与数据库路径），本机通过 SSH 通道接入：

```bash
# 推荐：复用 ~/.ssh/config 别名，免指纹、不存 token
mpx connect https://203.0.113.10:8080 --ssh my-vps

# 或使用环境变量直接调用管理 API
MPX_SERVER=https://203.0.113.10:8080 MPX_TOKEN=<token> mpx status -o json
```

### 3. 交给 AI Agent（或直接 CLI 运维）

将本仓 [skills/mpx/SKILL.md](skills/mpx/SKILL.md) 装入你的 Coding Agent（Antigravity / Claude Code / Codex / Cursor 等），即可直接用自然语言完成节点编排、入站与证书更新、按需本机 CDN 优选、用户配额与分流规则调整。

日常 CLI 示例（全部支持 `-o json`）：

```bash
mpx status -o json                                   # 只读总览：控制面心跳、对象数量、退避状态
mpx doctor -o json                                   # 只读全链路自检：DB、节点 SSH、6 种订阅格式（失败退出码 3）
mpx node add jp-1 --address 198.51.100.7             # 登记新节点
mpx node sync --dry-run jp-1 -o json                 # 下发前预检配置哈希与漂移（零 SSH、不重启）
mpx node sync jp-1                                   # 校验通过后原子下发并重载核心
mpx host add --inbound 1 --address 198.51.100.8      # 更新 CDN / 入口地址（立即进订阅，无需重启节点）
mpx user add alice --limit 100GB --reset-strategy monthly --expire 2026-12-31
mpx user links alice -o json                         # 获取各客户端订阅地址
mpx rule add cidr 203.0.113.10/32 DIRECT --at 1      # 新增分流规则（支持 mpx rule test 预检）
mpx source grant 1 --user alice                      # 按人授权外部订阅源（或 --everyone 全员授权）
mpx backup -o ./mpx-backup.tar.gz                    # 在线热备 SQLite + 控制面 SSH 私钥
```

## 架构一览

```text
  AI Agent (+ skills/mpx/SKILL.md) / mpx CLI (-o json)      客户端 (Clash Meta / sing-box / v2rayN)
                         │                                                    │
                         │ SSH 通道 (mpx api-relay) 或 Bearer MPX_TOKEN       │ HTTP(S) /sub/<token>
                         ▼                                                    ▼
        ┌────────────────────────────── 控制面 (单 Go 二进制) ──────────────────────────────┐
        │  SQLite 唯一真相: 节点 / 入站 / 对外入口 / 用户 / 分流规则 / 外部订阅源            │
        │  订阅渲染引擎:    clash-meta / sing-box / xray / base64 / 轻量浏览器用量页        │
        │  后台循环 (30s):  流量采样(7天滚动清理) → 周期重置 → 无重启人员收敛 → 巡检与退避  │
        └───────────────────────────────────────┬───────────────────────────────────────────┘
                                                │ SSH 原子下发 + 核心原生统计 API（节点零组件）
                              ┌─────────────────┼─────────────────┐
                              ▼                 ▼                 ▼
                           节点 A            节点 B            节点 C    (Xray / sing-box)
```

- 用户代理流量直连节点，不经过控制面；控制面重启不影响已建立的代理连接。
- 单个端口按首字节分流：HTTPS 给 `/api`，HTTP 给 `/sub`；也可通过 `--sub-addr` 拆分独立纯订阅监听，或用 `mpx mirror` 在节点上开放订阅镜像。

<details>
<summary>Shell 补全与本地调试</summary>

```bash
source <(mpx completion bash)
mpx completion zsh > "${fpath[1]}/_mpx"
mpx completion fish > ~/.config/fish/completions/mpx.fish

# 本地前台运行控制面
go run ./cmd/mpx serve --db ./mpx.db
```

</details>

## 文档与开发

- **官方 Agent Skill**：[skills/mpx/SKILL.md](skills/mpx/SKILL.md)
- **更新记录**：[CHANGELOG.md](CHANGELOG.md)
- **贡献指南**：[CONTRIBUTING.md](CONTRIBUTING.md)
- **架构地图（仓库内）**：[docs/atlas/README.md](docs/atlas/README.md)

提交前只需纯 Go 工具链即可完成全部校验：

```bash
go vet ./...
go test ./... -count=1 -race
gofmt -l .
python3 scripts/atlas_audit.py
```

## 许可证

[MIT](LICENSE)
