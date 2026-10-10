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

### 懒人路径：一条命令

只有**一台** VPS，想同时当控制面和节点？先在你的笔记本上装好命令行，然后一条命令搞定：

```bash
# 1. 装命令行（macOS / Linux，装到 ~/.local/bin/mpx）
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash

# 2. 一条命令装完控制面 + 节点 + 订阅
mpx init --host <VPS 公网 IP> --ssh-password
```

第 2 步会提示你输一次 VPS 的 SSH 密码，然后替你把所有事做完：

| 步 | 动作 |
| :--- | :--- |
| 1 | 把你的公钥装到 VPS（只用密码这一次，之后全程密钥） |
| 2 | 在 VPS 上装控制面（systemd 服务，监听 `0.0.0.0:8080`） |
| 3 | 把控制面自己的公钥写进它的 `authorized_keys` |
| 4 | 建节点 `default`、入站 `vless-443`（VLESS + TCP + REALITY）、对外入口 |
| 5 | 把订阅前缀指向 `http://<IP>:8080` |
| 6 | 建用户 `user1`，装 xray 核心并下发配置 |
| 7 | 打印订阅链接和一条 `mpx login` |

跑完屏幕上就有一条订阅地址，填进客户端即可（Clash / sing-box / v2rayN / Shadowrocket 都认，选 `auto` 那条会按 User-Agent 自动选格式）：

```
  http://203.0.113.10:8080/sub/xxxxxxxx
```

重复跑是安全的：已有的节点、入站、入口、用户和订阅前缀都会被复用，不会建出第二份。

常用参数：

| 参数 | 默认 | 说明 |
| :--- | :--- | :--- |
| `--host` | *(必填)* | VPS 的地址 |
| `--ssh-user` | `root` | SSH 登录用户，必须是 root（装 systemd 服务、写 `/etc/mpx` 都要 root） |
| `--ssh-password` | — | 带它才提示输密码；不带则用密钥（`--identity` 指定私钥） |
| `--ssh-port` | `22` | SSH 端口 |
| `--binary` | — | 指定本机编好的 Linux `mpx` 传上去，适合 VPS 连不上 GitHub 的情况 |
| `--addr` | `0.0.0.0:8080` | 控制面监听地址 |
| `--username` / `--name` | `user1` / `default` | 默认用户与节点名 |
| `--sni` / `--reality-dest` | `www.cloudflare.com` | REALITY 借用的目标站点 |
| `--sub-prefix` | `http://<host>:8080` | 订阅地址前缀，换了域名要改它 |

装完之后日常管理用普通命令（`mpx login` 那行的 token 就是凭据）：

```bash
mpx login --server http://<VPS IP>:8080 --token '<跑完打印的 token>'

mpx node list      # 看节点状态
mpx user add bob   # 加人
mpx user links bob # 拿他的订阅
mpx status         # 总览
```

> **只支持单机全包**（控制面与节点同一台机器）。要分开部署、或多节点，走下面的完整步骤。
>
> **明文 HTTP 的警告**：`--addr` 默认 `0.0.0.0:8080` 意味着管理接口和订阅都暴露在公网明文 HTTP 上，token 是唯一凭据。长期使用请在前面放一个 TLS 反向代理，再用 `--sub-prefix https://你的域名` 把订阅前缀换成 https（**前缀里不要带 `/sub`**，它是自动接上的）。
>
> `mpx init` 做的就是下面的第 1~5 步，只是替你在 VPS 上跑完了。想看清每一步、或要配 TLS 反代，[新手教程](docs/user/tutorial/getting-started.md) 仍是完整且推荐的路子。

### 装控制面

在要当控制面的那台 Linux 机器上跑一行（有 systemd 即可，不用先装 Go）：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install.sh | sudo bash
```

它会装好二进制、建好 `mpx` 系统用户、随机生成管理 token 与 SSH 密钥、写好 systemd
单元并启动服务，跑完把 token 和公钥打印出来。**装和更新是同一条命令，重复跑不会
动已有的 token、数据库和密钥。**

想升级就是再跑一遍同一条命令。`mpx status` 会告诉你现在装的是哪一版、有没有更新的发布：

```
版本       v0.2.0
更新       落后 1 个发布 → v0.3.0,见 https://github.com/RovenEverward/my-proxy/releases/tag/v0.3.0
```

控制面**只提醒，不会自己替换二进制** —— 换掉的正是控制面自己，换来一个起不来的版本就连
「查一下现在什么状态」都没了。升级始终由你跑上面那条命令完成。**每次改了什么、要注意什么，
写在 [Releases 页](https://github.com/RovenEverward/my-proxy/releases) 上**（仓库里没有单独的
CHANGELOG.md）。

没有发布件、也不方便联网时，脚本会退回本机编译。也可以把二进制直接递给它：

```bash
sudo ./deploy/install.sh /tmp/mpx
```

### 装命令行

在你自己日常用的机器上（macOS / Linux），上面懒人路径第 1 步用的就是这条：

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

在线文档：**https://RovenEverward.github.io/my-proxy/**

`docs/` 下的 markdown 由 [VitePress](https://vitepress.dev/) 渲染成一个站点，分「使用指南」「参考手册」「内部工程」三个页签。推 `main` 后由 [.github/workflows/docs.yml](.github/workflows/docs.yml) 自动发布到 GitHub Pages；本地这样跑：

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
- [架构决策记录 (37 条关键 ADR)](docs/internal/decisions.md)
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
