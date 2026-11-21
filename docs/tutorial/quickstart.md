# 快速开始

跟着做完，你会拿到一条订阅链接，导进客户端就能用。全程在你自己电脑的终端里敲 `mpx`，管理在浏览器里的 `/admin/` 网页管理台，不用登录 VPS。

**你需要：** 一台 Linux VPS 的 IP 和 root 密码，外加你自己的电脑（macOS、Windows、Linux 都行），以及一个托管在 Cloudflare 的域名（可选，但有了它管理台才有有效证书、能公网访问）。

## 1. 安装 mpx 命令行

**macOS / Linux**（终端里一行）：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash
```

**Windows**（PowerShell 里一行）：

```powershell
irm https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.ps1 | iex
```

<DownloadCta />

::: details 想用安装包，或者脚本装完了 PATH 里没有
- 安装包在[最新发布页](https://github.com/RovenEverward/my-proxy/releases/latest)，名字以 `mpx-cli_` 开头：Windows 是 `setup.exe`，macOS 是 `.pkg`（Intel 与 Apple 芯片通用），Debian / Ubuntu 是 `.deb`，RHEL / Fedora 是 `.rpm`。
- 安装包没有签名：**Windows** SmartScreen 提示时点「更多信息 → 仍要运行」；**macOS** 在「系统设置 → 隐私与安全性」下方点「仍要打开」。
- 校验文件 `SHA256SUMS` 在同一发布里。
:::

## 2. 一条命令装好整套

```bash
mpx init --host 203.0.113.10 --ssh-password
```

- `--host` 填 VPS 的 IP；`--ssh-password` 让命令行提示你输 root 密码（只用这一次，装完公钥后就全程走密钥）。已经配好免密登录可以不带它，或加 `--identity` 指一把私钥。
- 中途会请你设一个**管理员账号**（默认用户名 `admin`）和密码——它同时是网页管理台和 `mpx connect` 的登录凭据，记好。
- 脚本化场景用 `--admin-password` 或 `--admin-password-stdin` 给密码；都不给会跳过建号与自动接入，事后在服务器上 `mpx admin create` 补建。

它会做完：装控制面（systemd，监听 `0.0.0.0:8080`）→ 注册节点 → 建 VLESS + REALITY 入站（443 端口，不用域名和证书）→ 建一个用户 → 装代理核心并下发配置 → 打印**订阅链接**和一行 **`mpx connect` 命令**（给别的电脑接入用）。

大约 5–10 分钟，看 VPS 网速；某一步失败会写明原因，改好后重跑同一条命令即可（幂等）。

## 3. 打开管理台，确认一切正常

管理台由控制面自己伺服，地址是 `http(s)://<控制面地址>:8080/admin/`——**公网路径只收 TLS 或回环**，所以默认（自签证书、裸 IP）推荐走 SSH 隧道到回环：

```bash
ssh -N -L 8080:127.0.0.1:8080 root@203.0.113.10
# 浏览器打开 http://127.0.0.1:8080/admin/
```

用第 2 步设的管理员账号密码登录，看到总览页就说明全部装好了。

::: details 想直接用 https://域名/admin/ 打开（推荐长期使用）
在管理台 **系统 → 域名与证书** 填一个托管在 Cloudflare 的域名（DNS 记录指向 VPS）和一张 CF API token（Zone.DNS 编辑权限），保存后点 **立即签发**，控制面走 DNS-01 自己签证书，不用开 80 端口。签好后 `https://<域名>/admin/` 在任意浏览器直接可开。命令行等价：`mpx acme set --domain cp.example.net --cf-token-stdin`。
:::

## 4. 导入订阅，开始用

`mpx init` 打印的订阅链接：

- **手机：** 在管理台的用户详情里有二维码，客户端扫一下。
- **电脑：** 复制链接，在 Clash Verge、sing-box、v2rayN 这类客户端里「从链接导入订阅」。

<Shot name="overview" cap="总览" />

::: details 装好的是什么？
默认是 VLESS + REALITY，端口 443，不用域名和证书。想了解背后的结构，见[控制面与节点](/concepts/architecture)。
:::

## 接下来

- 给朋友开账号 → [添加用户并分发订阅](/guides/add-users)
- 再加一台服务器 → [添加节点](/guides/add-nodes)
- 登录、域名、忘密码、别的电脑接入 → [管理台访问](/guides/admin-access)
