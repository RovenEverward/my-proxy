# mpx

轻量的自建多节点代理控制面：一台控制面 + 若干 Xray / sing-box 节点 + 一个本地 CLI 工具。

用户流量直连各节点，节点配置通过 SSH 声明式下发。Xray 用户通过 gRPC 动态维护；Hysteria2 / TUIC 使用独立 sing-box 服务，两核心流量计入同一用户配额。用途订阅可以按手动标签和实际入口能力选择候选。

---

## 30 秒看懂

```
  mpx CLI (本机管理)                          终端客户端 (mihomo / sing-box / v2rayN)
         │                                               │
         │ /api (管理鉴权)                               │ /sub/<token> (无鉴权订阅)
         ▼                                               ▼
    ┌─────────────────────────── 控制面 ───────────────────────────┐
    │  SQLite: 节点 / 入站 / 对外入口 / 用户 / 订阅分流规则 / 设置  │
    │  订阅分发: links / clash-meta / sing-box / xray / HTML 仪表盘 │
    │  后台循环: 流量采样 → 周期重置 → 人员收敛 → 节点探活         │
    └──────────────────────────────┬───────────────────────────────┘
                                   │ SSH + 各核心统计 API
                 ┌─────────────────┼─────────────────┐
                 ▼                 ▼                 ▼
              节点 A            节点 B            节点 C    (Xray / sing-box)
```

- **免证书防探测**：原生支持 VLESS REALITY，零配置借用主流域名 TLS 指纹。
- **用户与统一配额**：Xray 用户通过 gRPC 热更新；Hysteria2 / TUIC 使用独立凭据和逐用户计量，名单变化会重启该节点全部 QUIC 连接。
- **用途订阅**：按日常、流媒体、下载和游戏用途选择候选与路由策略，保留原有直连、拦截规则。
- **真实入口能力**：通过指定代理入口检测出口 IP、地区、YouTube 连通性和 Netflix 页面证据，按有效结果筛选用途候选。
- **一键体检**：`mpx node diagnose ID` 或 `--all` 默认运行官方 Go ECS 融合怪 v0.2.12，汇总节点健康、配置漂移、全部获授权的真实代理入口与上游测评。测速默认开启，CPU、内存与磁盘压测需显式启用；有流量上限的原有体检可用 `--engine light`。
- **多协议与客户端**：按 `User-Agent` 下发 Clash Meta、sing-box 或 Base64 订阅；浏览器呈现用量与能力结果。Hysteria2 / TUIC 支持现代客户端及分享链接。
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
> `mpx init` 做的就是下面的第 1~5 步，只是替你在 VPS 上跑完了。想看清每一步、或要配 TLS 反代，各命令组的 `--help`（`mpx node add`、`mpx inbound add`、`mpx host add`）带可运行的例子。

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

### 用名字指定对象，Tab 补全

要指定节点或入站的地方，可以写 id，也可以写名字（不区分大小写）、唯一前缀；入站还可以写成 `节点/tag`。命中多个时会列出候选让你写具体些：

```bash
mpx node online la-1          # 等价于 mpx node online 1
mpx inbound show la/reality   # 节点 la-… 上 tag 为 reality… 的入站
```

开启 shell 补全，节点名、入站、用户名都能按 Tab 补全：

```bash
source <(mpx completion bash)                  # bash，写进 ~/.bashrc 长期生效
mpx completion zsh > "${fpath[1]}/_mpx"        # zsh
mpx completion fish > ~/.config/fish/completions/mpx.fish
```

> **想先本地试一把？** 不装服务，直接跑前台进程：
> ```bash
> go build -o mpx ./cmd/mpx
> ./mpx serve --db ~/mpx.db
> ```

> **生产环境部署？** 见文档站的[快速开始](https://RovenEverward.github.io/my-proxy/tutorial/quickstart)，长期使用前在控制面前放 TLS 反向代理。

---

## 文档站

在线文档：**https://RovenEverward.github.io/my-proxy/**

`docs/` 下的 markdown 由 [VitePress](https://vitepress.dev/) 渲染成一个站点，按教程、指南、参考、原理四类组织。推 `main` 后由 [.github/workflows/docs.yml](.github/workflows/docs.yml) 自动发布到 GitHub Pages；本地这样跑：

```bash
npm install        # 装一次即可
npm run docs:dev   # 本地预览，改文件自动刷新，终端会打印访问地址
npm run docs:build # 构建静态站到 docs/.vitepress/dist/
```

markdown 原文在 `docs/` 下：`tutorial/`、`guides/`、`reference/`、`concepts/` 是用户文档；[设计文档](docs/design.md) 和 [docs/internal/](docs/internal/) 是工程文档，只留在仓库里，不进站点。

### 📖 用户文档
- **教程**：[快速开始](https://RovenEverward.github.io/my-proxy/tutorial/quickstart) ｜ [在本机试用](https://RovenEverward.github.io/my-proxy/tutorial/local-trial)
- **指南**：[添加用户并分发订阅](https://RovenEverward.github.io/my-proxy/guides/add-users) ｜ [让某个客户端拿到指定格式](https://RovenEverward.github.io/my-proxy/guides/client-format) ｜ [用 mpx doctor 排查](https://RovenEverward.github.io/my-proxy/guides/troubleshoot)
- **参考**：[命令行](https://RovenEverward.github.io/my-proxy/reference/cli) ｜ [订阅设置](https://RovenEverward.github.io/my-proxy/reference/settings) ｜ [订阅接口](https://RovenEverward.github.io/my-proxy/reference/subscription)
- **原理**：[控制面与节点](https://RovenEverward.github.io/my-proxy/concepts/architecture)

### 🛠 架构设计与内部决策
- **设计**：[它为什么要有、基本形态是什么、哪些事不做](docs/design.md)
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
