# 从零搭建多节点代理管理服务

完成本教程后，你将拥有一台运行中的 mpx 控制面、一台完成初始化的代理节点（VLESS REALITY 协议），并成功为第一个用户生成客户端订阅。

## 前置准备

1. **控制面机器**：一台可运行 Linux 二进制的服务器，具有 systemd 环境。
2. **节点机器**：一台带有公网 IP 的 Linux 服务器（如 Ubuntu 22.04+），已开启 SSH 且可通过公钥免密登录。
3. **操作机**：你日常用的电脑（macOS、Linux 或 Windows），用来跑 `mpx` 命令行。

不需要预先安装 Go，也不需要自己编译。

---

## 懒人路径：一条命令全搞定

如果你只有**一台** VPS，想同时当控制面和节点，那下面是全部要做的事 —— 在你的笔记本电脑上跑：

```bash
mpx init --host <VPS 的 IP> --ssh-password
```

它会提示你输入一次 VPS 的 SSH 密码，然后自动完成：装控制面（监听 `0.0.0.0:8080`）→ 把控制面公钥装进本机 sshd → 建节点、入站（VLESS + TCP + REALITY）、对外入口、订阅前缀、用户 → 装 xray 核心并下发配置 → 打印订阅链接。

跑完屏幕上会给你一条订阅地址，直接填进客户端就能用：

```
搞定。把下面这条填进客户端(clash / sing-box / v2rayN 都认它):

  http://203.0.113.10:8080/sub/xxxxxxxx

格式            订阅地址                                             说明
auto          http://203.0.113.10:8080/sub/xxxxxxxx                按 User-Agent 自动选,推荐填这个
clash-meta    http://203.0.113.10:8080/sub/xxxxxxxx/clash-meta     Clash / mihomo
...
```

重复跑是安全的：已经存在的节点、入站、入口、用户和订阅前缀都会被复用，不会建出第二份。

> **`mpx init` 只支持单机全包**（控制面与节点同一台机器）。要分开部署、或多节点，就走下面的手工步骤。
>
> **它做的事就是下面的第 1~5 步**，只是替你在 VPS 上跑完了。想看它到底做了什么、想改成 TLS 反代，手工步骤仍然是完整且推荐的路子。

常用参数：

| 参数 | 默认 | 说明 |
| :--- | :--- | :--- |
| `--host` | *(必填)* | VPS 的地址 |
| `--ssh-user` | `root` | SSH 登录用户，必须是 root |
| `--ssh-password` | — | 带它才提示输密码；不带则用密钥（`--identity` 指定私钥） |
| `--binary` | — | 指定一个本机编好的 Linux `mpx` 传上去，适合控制面连不上 GitHub 的情况 |
| `--addr` | `0.0.0.0:8080` | 控制面监听地址 |
| `--username` | `user1` | 默认用户 |
| `--sni` / `--reality-dest` | `www.cloudflare.com` | REALITY 借用的目标站点 |
| `--sub-prefix` | `http://<host>:8080` | 订阅地址前缀，换了域名要改它 |

> **明文 HTTP 的警告**：`--addr 0.0.0.0:8080` 意味着管理接口和订阅都暴露在公网明文 HTTP 上，token 是唯一凭据。长期用请在前面放一个 TLS 反向代理，再把 `--sub-prefix` 换成 `https://` 的域名。

---

## 手工路径

## 第 1 步：部署控制面

在控制面机器上执行一行安装命令：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install.sh | sudo bash
```

脚本会自动完成：下载对应架构的二进制、创建 `mpx` 系统用户、生成管理 Token 与 SSH 密钥对、写入 systemd 单元并启动服务。命令结束时会打印出以下关键凭证：

- **管理 Token**（保存在 `/etc/mpx/env`）
- **控制面公钥**（路径为 `/var/lib/mpx/.ssh/id_ed25519.pub`）

> **装和更新是同一条命令**：以后升级版本再跑一次即可。已经存在的 Token、数据库和密钥不会被覆盖。

如果控制面机器无法访问 GitHub，可以先把二进制传上去，再用手边的脚本装：

```bash
# 在操作机上
GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/mpx ./cmd/mpx
scp -r /tmp/mpx deploy root@<控制面IP>:/tmp/

# 在控制面机器上
sudo /tmp/deploy/install.sh /tmp/mpx
```

> **开发试跑**：如果只想在本地体验，不必装成服务，直接前台运行：
> ```bash
> go build -o mpx ./cmd/mpx
> ./mpx serve --db ~/mpx.db
> ```
> 不设 Token 时默认仅监听本地回环地址（127.0.0.1:8080）。

---

## 第 2 步：安装 CLI 并登录

在你的日常操作机上装 `mpx` 命令行。

macOS / Linux：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash
```

Windows（PowerShell）：

```powershell
irm https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.ps1 | iex
```

装好后接上控制面。控制面只监听回环地址，先从操作机拉一条 SSH 隧道：

```bash
ssh -N -L 8080:127.0.0.1:8080 root@<控制面IP>
```

另开一个终端登录：

```bash
mpx login --server http://127.0.0.1:8080 --token "<管理Token>"
```

验证连接：

```bash
mpx status
```

预期看到控制面心跳信息：
```
启动时间: 2026-10-09 11:00:00 (运行 2 分钟)
后台周期: 30 秒 (上一轮耗时 120ms, 累计 4 轮)
对象数量: 节点 0, 入站 0, 对外入口 0, 用户 0
退避节点: 无
```

---

## 第 3 步：纳管并初始化节点

mpx 下发配置使用 SSH。纳管节点有两种授权方式：

**方式 A：自动引导（推荐）**
直接带上 `--ssh-password` 参数，CLI 会提示输入一次远程服务器的 SSH 密码。控制面会自动将自身的公钥推送到节点的 `authorized_keys` 中：
```bash
mpx node add hk --address 203.0.113.10 --ssh-password
```

**方式 B：手工部署公钥**
如果节点已禁用密码登录：
```bash
# 在控制面机器执行，查看公钥
cat /var/lib/mpx/.ssh/id_ed25519.pub
```
将输出的内容追加到节点机器的 `/root/.ssh/authorized_keys` 中，然后直接注册：
```bash
mpx node add hk --address 203.0.113.10
```

完成节点添加后，执行一键安装（全自动下载 xray 核心、推送 geodata、配置 systemd 单元并启动）：
```bash
mpx node install 1
```

输出反馈：
```
已在节点 1 上装好并启动 xray 26.3.27
```

---

## 第 4 步：创建入站通道与对外入口

为节点添加一个当下最主流且免证书申请的 **VLESS + TCP + REALITY** 入站：

```bash
# 1. 创建入站规则（REALITY 密钥对如不指定，系统会自动随机生成）
mpx inbound add --node 1 --protocol vless --port 443 \
  --network tcp --security reality \
  --sni www.cloudflare.com --reality-dest www.cloudflare.com:443

# 2. 为入站添加入口标识（用于订阅中显示的节点名称）
mpx host add --inbound 1 --remark "香港 01 REALITY"

# 3. 将新入站结构下发并同步到节点
mpx node sync 1
```

> **注意**：修改端口、协议或证书等**结构性变更**必须执行 `node sync` 重启 xray 进程使配置生效。

---

## 第 5 步：设置订阅前缀并开通用户

配置外网访问订阅时的公共域名或 IP 前缀：

```bash
echo '{"sub_url_prefix":"http://<控制面IP>:8080","profile_title":"我的私有节点"}' \
  | mpx settings set -f -
```

创建你的第一个用户：

```bash
mpx user add alice --limit 100GB --reset-strategy monthly --expire 2026-12-31
```

获取该用户的订阅链接：

```bash
mpx user links alice
```

输出终端显示各个客户端的专属订阅链接：
```
auto          http://<控制面IP>:8080/sub/<token>          按 User-Agent 自动选格式
links         http://<控制面IP>:8080/sub/<token>/links    分享链接(vless:// 等)
clash-meta    http://<控制面IP>:8080/sub/<token>/clash-meta
sing-box      http://<控制面IP>:8080/sub/<token>/sing-box
```

---

## 第 6 步：验证与连通性测试

1. **浏览器自检**：在电脑浏览器直接打开 `http://<控制面IP>:8080/sub/<token>`，你将看到一个网页仪表盘，展示当前用户的流量额度（0B / 100GB）、到期日及节点快捷导入链接。
2. **客户端导入**：将该地址复制到 Clash Verge、sing-box 或 v2rayN 中更新订阅。
3. **系统健康自检**：
   ```bash
   mpx doctor
   ```
   所有 11 项检查应全为 `ok`，退出码为 0。

至此，多节点代理管理系统已成功跑通！下一步可阅读 [日常运维指南](../guides/node-operations.md) 了解节点扩容与维护。

---

## 第 7 步：以后怎么升级

`mpx status` 里有两行专门回答这个问题：

```
版本       v0.2.0
更新       落后 1 个发布 → v0.3.0,见 https://github.com/RovenEverward/my-proxy/releases/tag/v0.3.0
```

「更新」一行会把发布说明的摘要一并带出来。控制面自己**只查不问、只提醒不动手** —— 自动
替换的正是控制面本身，换来一个起不来的版本，连「查一下现在什么状态」这条退路都没了。所以
升级由你在控制面机器上手工跑一次：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install.sh | sudo bash
```

这条命令和当初安装时**完全一样**，重复跑不会动已有的 token、数据库和 SSH 密钥 —— 只会把
二进制换成新版再把服务重启起来。装完 `mpx --version` 确认一下，然后重跑 `mpx doctor` 看各项
是否仍然全 `ok`。

> **版本号从哪来？** 发布件里的版本是发布流程写进去的。如果你是从源码直接 `go build` 的，
> `mpx --version` 会显示「未知」并说明原因 —— 那是缺版本号，不是命令坏了；更新检查也会
> 因此比不出新旧，只会告诉你「已是最新」。

**每次改了什么都写在 [GitHub Releases 页](https://github.com/RovenEverward/my-proxy/releases) 上**，
仓库里没有单独的 CHANGELOG.md：发布说明跟发布走，什么时候发的、对应哪个二进制、要留意什么，
全在一处。
