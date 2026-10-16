<div align="center">

<img src="docs/public/logo.svg" width="96" alt="mpx">

# mpx

### 自建多节点代理，在自己电脑上管完。

**一台控制面，若干 Xray / sing-box 节点，一个本地命令行 `mpx`。**<br>
加节点、开账号、改分流、发订阅，都在你自己的电脑上做，装好之后不用再登录 VPS。

[![Release](https://img.shields.io/github/v/release/RovenEverward/my-proxy?label=release&color=111111)](https://github.com/RovenEverward/my-proxy/releases/latest) [![Go](https://img.shields.io/github/go-mod/go-version/RovenEverward/my-proxy?color=111111)](go.mod) [![License](https://img.shields.io/badge/license-MIT-111111)](LICENSE)<br>
![macOS](https://img.shields.io/badge/macOS-000000?logo=apple&logoColor=white) ![Linux](https://img.shields.io/badge/Linux-FCC624?logo=linux&logoColor=black) ![Windows](https://img.shields.io/badge/Windows-0078D4?logo=windows&logoColor=white)

**[文档](https://RovenEverward.github.io/my-proxy/)** &nbsp;·&nbsp; **[快速开始](https://RovenEverward.github.io/my-proxy/tutorial/quickstart)** &nbsp;·&nbsp; **[命令行参考](https://RovenEverward.github.io/my-proxy/reference/cli)** &nbsp;·&nbsp; **[更新记录](CHANGELOG.md)**

<br>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/readme/rules-dark.png">
  <img src="docs/assets/readme/rules-light.png" width="900" alt="mpx ui 的分流规则页：规则从上往下匹配，每条指向直连、拦截或一个策略组，右侧可以测试一个网址会走哪里">
</picture>

</div>

<br>

## 适合你吗？

手上有几台 VPS，想给自己和几个朋友用，又不想每次加人、换节点都 SSH 上去改 JSON。mpx 把这些事收进一个控制面，你在自己电脑上用 `mpx` 或 `mpx ui` 操作：

- **一条命令起步**：`mpx init` 把一台新 VPS 装成控制面加节点，最后打印一条能直接用的订阅地址。
- **人和流量分开管**：每个人有自己的订阅、流量上限、重置周期和到期日；改完 30 秒内同步到节点，不重启。
- **订阅自动适配客户端**：同一条地址，Clash Meta、sing-box、v2rayN 各自拿到认识的格式，浏览器打开是用量页。

只有一台机器、只有你一个人用、也不打算加人的话，手写一份配置就够了。

## 快速开始

准备一台能用 root SSH 登录的 Linux VPS，和你自己的电脑（macOS、Linux 或 Windows）。

**1 · 装命令行。**

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash
```

<sub>装到 `~/.local/bin/mpx`。Windows 用 PowerShell：`irm https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.ps1 | iex`。两个脚本都按同一个顺序找二进制：手边的 → 发布件 → 用本机 Go 从源码编。</sub>

**2 · 装控制面和第一个节点。** 把 `203.0.113.10` 换成你的 VPS 公网 IP：

```bash
mpx init --host 203.0.113.10 --ssh-password
```

它先请你设一个**设备密码**（以后别的设备接入时用，不是 VPS 密码），再要一次 SSH 密码，之后全程改用密钥。接着装好控制面（systemd 服务，监听 `0.0.0.0:8080`）、建节点 `default` 和一个 VLESS + REALITY 入站、建用户 `user1`、装 xray 并下发配置。最后打印订阅地址，以及一行给其他设备用的接入命令：

```text
别的设备接入这个控制面,装好 mpx 后跑这一行,再输入刚设的设备密码:
  mpx connect 203.0.113.10:8080 --fp SHA256:DU4SswBA8LrUy6O0MfhRRxLJZ2p8GPREpV2Zfxtj9Wg
```

**3 · 填进客户端。** 把输出里 `auto` 那条订阅地址导入 Clash Verge、sing-box、v2rayN 或 Shadowrocket。

重复跑 `mpx init` 是安全的，已有的节点、入站、用户都会复用。

<details>
<summary><code>mpx init</code> 的常用参数</summary>

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
| `--sub-prefix` | `http://<host>:8080` | 订阅地址前缀，换了域名要改它（不要带 `/sub`） |

非交互运行时用环境变量 `MPX_DEVICE_PASSWORD` 给出设备密码。`mpx init` 只做单机全包（控制面和节点在同一台）；要分开部署或加更多节点，看 `mpx node add`、`mpx inbound add`、`mpx host add` 的 `--help`，里面有能直接跑的例子。

</details>

之后的日常操作：

```bash
mpx status                                     # 总览：控制面、节点、版本、有没有新发布
mpx node add tyo-1 --address 198.51.100.7      # 再加一台节点，控制面按 IP 自动起显示名
mpx user add bob --limit 100GB --reset-strategy monthly --expire 2026-12-31
mpx user links bob                             # 拿他的订阅地址
mpx node drain la-1                            # 维护前排空：不踢活跃连接，新订阅不再给它
mpx doctor                                     # 只读自检：控制面、每台节点、各种订阅格式
mpx ui                                         # 在本机浏览器里打开管理界面
```

## 能做什么

### 在浏览器里管，和命令行一一对应

`mpx ui` 在本机起一个只给自己用的管理界面。每个按钮都对应一条 `mpx` 命令，表单底部实时显示等价命令；token 不进浏览器，删除这类危险操作要逐字输入名字确认。

<table>
<tr>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/readme/nodes-dark.png">
  <img src="docs/assets/readme/nodes-light.png" alt="节点页：每台节点以旗帜加城市加字母命名，显示状态、核心和 SSH">
</picture>
</td>
<td width="50%">
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/readme/users-dark.png">
  <img src="docs/assets/readme/users-light.png" alt="用户页：每个人的用量、到期日、重置周期和可用入站">
</picture>
</td>
</tr>
</table>

### 节点和账号

- **免证书防探测**：原生支持 VLESS REALITY，借用主流站点的 TLS 指纹，不用域名和证书。
- **多协议**：Xray 用户通过 gRPC 热更新；Hysteria2 / TUIC 跑在独立的 sing-box 服务上，两种核心的流量计入同一个用户配额（QUIC 名单变化会重启该节点的 QUIC 连接）。
- **节点自动起名**：新节点按 IP 查 ipwho.is，起成 `🇺🇸 洛杉矶 A` 这样的显示名，订阅里也用它。关掉用 `mpx serve --auto-locate=false` 或 `MPX_AUTO_LOCATE=0`；手动改用 `mpx node edit la-1 --region US --city 洛杉矶`，老节点批量起名用 `mpx node relabel --all --dry-run` 先看对照表。
- **用名字指定对象**：要写 id 的地方都能写名字（不区分大小写）、显示名或唯一前缀，入站写成 `节点/tag`，例如 `mpx inbound show la/reality`。命中多个时列出候选，不会猜。

### 订阅和分流

- **按客户端下发**：按 `User-Agent` 返回 Clash Meta、sing-box 或 Base64 订阅，浏览器打开是用量和能力页。
- **分流规则** *（在 main 上，下一版发布）*：一张从上往下匹配的规则表，每条指向直连、拦截或策略组；内置常用规则组，可以给某个用户单独加规则，也能测试一个网址会走哪里。对应 `mpx rule`、`mpx group`。旧的「用途」会在升级时自动迁移成规则，计划在 v0.7.0 删除。
- **套餐** *（在 main 上，下一版发布）*：按节点给一组人授权，每个用户挂一个套餐；自带包含全部节点的「默认套餐」。对应 `mpx plan`、`mpx user add --plan`。

### 运维

- **多设备接入**：别的电脑用 `mpx connect` 加设备密码接入，每台设备一枚自己的 token，丢了用 `mpx device` 吊销。
- **一键体检**：`mpx node diagnose ID` 或 `--all` 汇总节点健康、配置漂移、真实代理入口和上游测评（默认运行 Go ECS 融合怪 v0.2.12；有流量上限的节点用 `--engine light`）。
- **下发不怕写坏**：下发前自动跑 `xray -test`，失败秒级回滚；节点掉线按指数退避重试。
- **备份**：内置 SQLite 在线热备，`mpx backup` 下载，`mpx restore` 暂存换入。

## 它怎么连起来

```
  mpx CLI / mpx ui (你的电脑)                 客户端 (mihomo / sing-box / v2rayN)
         │                                               │
         │ /api (设备 token, HTTPS)                      │ /sub/<token> (订阅)
         ▼                                               ▼
    ┌─────────────────────────── 控制面 ───────────────────────────┐
    │  SQLite: 节点 / 入站 / 对外入口 / 用户 / 分流规则 / 设置      │
    │  订阅分发: links / clash-meta / sing-box / xray / HTML 用量页 │
    │  后台循环: 流量采样 → 周期重置 → 人员收敛 → 节点探活         │
    └──────────────────────────────┬───────────────────────────────┘
                                   │ SSH 声明式下发 + 各核心统计 API
                 ┌─────────────────┼─────────────────┐
                 ▼                 ▼                 ▼
              节点 A            节点 B            节点 C    (Xray / sing-box)
```

用户流量直连各节点，不经过控制面。控制面在 `8080` 一个端口上同时提供订阅（HTTP）和管理接口（HTTPS，用安装时生成的证书，命令行按指纹认它）。订阅地址是明文 HTTP，长期使用可以在前面放一个 TLS 反向代理，再用 `--sub-prefix https://你的域名` 换前缀。

## 升级

在控制面那台机器上再跑一遍安装命令。装和更新是同一条命令，不会动已有的 token、数据库和密钥：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install.sh | sudo bash
```

`mpx status` 会显示现在装的版本和有没有更新的发布。控制面只提醒，不会自己替换二进制。每版改了什么写在 [CHANGELOG.md](CHANGELOG.md) 和 [Releases 页](https://github.com/RovenEverward/my-proxy/releases)。没有发布件、也不方便联网时，可以把二进制直接递给脚本：`sudo ./deploy/install.sh /tmp/mpx`。

<details>
<summary>Tab 补全、本地试用</summary>

节点名、入站、用户名都能按 Tab 补全：

```bash
source <(mpx completion bash)                  # bash，写进 ~/.bashrc 长期生效
mpx completion zsh > "${fpath[1]}/_mpx"        # zsh
mpx completion fish > ~/.config/fish/completions/mpx.fish
```

不装服务，直接在本机跑一个前台控制面试试：

```bash
go build -o mpx ./cmd/mpx
./mpx serve --db ~/mpx.db
```

完整步骤见[在本机试用](https://RovenEverward.github.io/my-proxy/tutorial/local-trial)。

</details>

## 文档

- **教程**：[快速开始](https://RovenEverward.github.io/my-proxy/tutorial/quickstart) ｜ [在本机试用](https://RovenEverward.github.io/my-proxy/tutorial/local-trial)
- **指南**：[添加用户并分发订阅](https://RovenEverward.github.io/my-proxy/guides/add-users) ｜ [在多台设备上管理](https://RovenEverward.github.io/my-proxy/guides/devices) ｜ [用 mpx ui 在浏览器里管理](https://RovenEverward.github.io/my-proxy/guides/web-ui) ｜ [让某个客户端拿到指定格式](https://RovenEverward.github.io/my-proxy/guides/client-format) ｜ [用 mpx doctor 排查](https://RovenEverward.github.io/my-proxy/guides/troubleshoot)
- **参考**：[命令行](https://RovenEverward.github.io/my-proxy/reference/cli) ｜ [订阅设置](https://RovenEverward.github.io/my-proxy/reference/settings) ｜ [订阅接口](https://RovenEverward.github.io/my-proxy/reference/subscription)
- **原理**：[控制面与节点](https://RovenEverward.github.io/my-proxy/concepts/architecture)
- **设计与内部决策**（只在仓库里）：[设计文档](docs/design.md) ｜ [内部文档索引](docs/internal/index.md) ｜ [对外契约](docs/internal/contracts.md) ｜ [数据模型](docs/internal/model.md) ｜ [架构决策记录](docs/internal/decisions.md) ｜ [路线图](docs/internal/roadmap.md) ｜ [验证记录](docs/internal/status.md)

文档站由 `docs/` 下的 markdown 经 [VitePress](https://vitepress.dev/) 生成，推 `main` 后由 [docs.yml](.github/workflows/docs.yml) 发布。本地预览：`npm install && npm run docs:dev`。

## 参与开发

见 [CONTRIBUTING.md](CONTRIBUTING.md)。提交前跑：

```bash
go test ./... -count=1 -race
gofmt -l .
```

## 致谢

README 的排版参考了 [magpie](https://github.com/yetone/magpie)。

## 许可证

[MIT](LICENSE)
