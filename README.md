# mpx

轻量、可靠的自建 xray 多节点管理控制面：一台控制面 + 若干代理节点 + 一个本地 CLI 工具。

用户流量直连各节点，控制面不介入数据转发通路；节点配置通过 SSH 声明式下发，用户名单与用量通过 xray gRPC API 动态维护。

---

## 30 秒看懂

```
  mpx CLI (本机管理)                          终端客户端 (Clash / sing-box / v2rayN)
         │                                               │
         │ /api (管理鉴权)                               │ /sub/<token> (无鉴权订阅)
         ▼                                               ▼
    ┌─────────────────────────── 控制面 ───────────────────────────┐
    │  SQLite: 节点 / 入站 / 对外入口 / 用户 / 订阅分流规则 / 设置  │
    │  订阅分发: links / clash-meta / sing-box / xray / HTML 仪表盘 │
    │  后台循环: 流量采样 → 周期重置 → 人员收敛 → 节点探活         │
    └──────────────────────────────┬───────────────────────────────┘
                                   │ ssh + xray gRPC API
                 ┌─────────────────┼─────────────────┐
                 ▼                 ▼                 ▼
              节点 A            节点 B            节点 C    (各跑一个 xray)
```

- **免证书防探测**：原生支持 VLESS REALITY，零配置借用主流域名 TLS 指纹。
- **动态用户收敛**：增删改查用户实时通过 gRPC 热生效，**不中断节点现有连接，不重启服务**。
- **全客户端自适应**：按 `User-Agent` 自动下发 Clash Meta、sing-box 或 Base64 订阅；浏览器访问自动呈现用量仪表盘。
- **节点平滑维护**：支持 `drain` 优雅排空模式，维护时不强踢活跃连接，新订阅平滑切走。
- **高韧性运维**：下发前自动运行 `xray -test`，失败秒级回滚；节点掉线指数退避重试；内置 SQLite 在线热备与暂存换入。

---

## 快速上手 (Quick Start)

### 装控制面

在要当控制面的那台 Linux 机器上跑一行（有 systemd 即可，不用先装 Go）：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install.sh | sudo bash
```

它会装好二进制、建好 `mpx` 系统用户、随机生成管理 token 与 SSH 密钥、写好 systemd
单元并启动服务，跑完把 token 和公钥打印出来。**装和更新是同一条命令，重复跑不会
动已有的 token、数据库和密钥。**

没有发布件、也不方便联网时，脚本会退回本机编译。也可以把二进制直接递给它：

```bash
sudo ./deploy/install.sh /tmp/mpx
```

### 装命令行

在你自己日常用的机器上（macOS / Linux）：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash
```

Windows 上用 PowerShell：

```powershell
irm https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.ps1 | iex
```

两个脚本都按同一个顺序找二进制：手边的 → 发布件 → 用本机 Go 从源码编。

### 接上并跑通

```bash
mpx login --server http://127.0.0.1:8080 --token '<管理 token>'
mpx user add alice --limit 100GB --reset-strategy monthly
mpx user links alice
```

控制面只监听回环地址。要从别的机器连，先拉一条隧道：

```bash
ssh -N -L 8080:127.0.0.1:8080 root@<控制面地址>
```

> **想先本地试一把？** 不装服务，直接跑前台进程：
> ```bash
> go build -o mpx ./cmd/mpx
> ./mpx serve --db ~/mpx.db
> ```

> **生产环境部署？** 请阅读 [新手教程：从零搭建多节点代理服务](docs/user/tutorial/getting-started.md)。

---

## 文档站

`docs/` 下的 markdown 由 [VitePress](https://vitepress.dev/) 渲染成一个站点，分「使用指南」「参考手册」「内部工程」三个页签：

```bash
npm install        # 装一次即可
npm run docs:dev   # 本地预览，改文件自动刷新，终端会打印访问地址
npm run docs:build # 构建静态站到 docs/.vitepress/dist/
```

markdown 原文仍在 `docs/` 目录下，用户文档在 [docs/user/](docs/user/)，内部工程文档在 [docs/internal/](docs/internal/)。

### 📖 用户与运维指南
- **索引**：[用户文档导航](docs/user/index.md)
- **入门教程**：[从零搭建多节点代理管理服务](docs/user/tutorial/getting-started.md)
- **终端用户**：[客户端订阅导入与 Web 仪表盘使用](docs/user/guides/client-setup.md)
- **节点运维**：[节点安装、配置同步、平滑排空 (Drain) 与在线活跃查询](docs/user/guides/node-operations.md)
- **用户管理**：[配额管理、周期重置、到期顺延与 CSV 批量导入导出](docs/user/guides/user-management.md)
- **告警通知**：[Webhook 事件推送与到期/超限阈值配置](docs/user/guides/notifications.md)
- **灾备迁移**：[在线热备、包校验与跨机器恢复实操](docs/user/guides/backup-and-restore.md)
- **故障排查**：[Doctor 诊断指南与常见问题速查](docs/user/guides/troubleshooting.md)
- **配置字典**：[Settings 全局配置与 UA 规则](docs/user/reference/settings.md) ｜ [协议组合矩阵](docs/user/reference/protocols.md) ｜ [CSV 格式规格](docs/user/reference/csv-format.md)

### 🛠 架构设计与内部决策
- **索引**：[mpx 是怎么搭起来的](docs/internal/index.md)
- [对外契约 (CLI、API、订阅、节点)](docs/internal/contracts.md)
- [核心领域数据模型](docs/internal/model.md)
- [架构决策记录 (30 条关键 ADR)](docs/internal/decisions.md)
- [版本批次与演进路线](docs/internal/roadmap.md)
- [验证矩阵与测试记录](docs/internal/status.md)

---

## 参与贡献与开发

详见 [CONTRIBUTING.md](CONTRIBUTING.md)。

```bash
go test ./... -count=1 -race
gofmt -l .
```

---

## 开源协议

[MIT](LICENSE)
