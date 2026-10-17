# 快速开始

跟着做完，你会拿到一条订阅链接，导进客户端就能用。全程在你自己电脑上的 MPX 窗口里点，不用登录 VPS。

**你需要：** 一台 Linux VPS 的 IP 和 root 密码，外加你自己的电脑（macOS、Windows、Linux 都行）。

## 1. 下载并安装 MPX

<div class="cta">
<a class="btn btn-lg" href="https://github.com/RovenEverward/my-proxy/releases/latest/download/MPX-Windows-x64-setup.exe">下载 Windows 版</a>
<a class="btn btn-lg" href="https://github.com/RovenEverward/my-proxy/releases/latest/download/MPX-macOS.dmg">下载 macOS 版</a>
<a class="btn btn-lg" href="https://github.com/RovenEverward/my-proxy/releases/latest/download/MPX-Linux-amd64.deb">下载 Linux 版（.deb）</a>
</div>

- **Windows：** 双击安装，不需要管理员权限。
- **macOS：** 打开下载的 `.dmg`，把 MPX 拖进「应用程序」。Intel 和 Apple 芯片用同一个文件。
- **Linux：** 双击 `.deb` 安装，或 `sudo apt install ./MPX-Linux-amd64.deb`。

### 第一次打开被系统拦住？

安装包没有签名，第一次打开时系统会拦一下，放行一次，以后就不再问了。

- **Windows（SmartScreen）：** 出现「Windows 已保护你的电脑」时，点 **更多信息**，再点 **仍要运行**。
- **macOS（Gatekeeper）：** 提示「无法验证开发者」时先点 **完成**（或「取消」），然后任选一种：
  - 在「应用程序」里**右键**（或按住 Control 点）MPX，选 **打开**，在弹窗里再点 **打开**；
  - 或打开 **系统设置 → 隐私与安全性**，在页面下方找到关于 MPX 的提示，点 **仍要打开**，输入登录密码确认。
- **Linux：** 不会拦。

::: details 其他 Linux、ARM 机器、所有安装包
- ARM 的 Debian / Ubuntu：[MPX-Linux-arm64.deb](https://github.com/RovenEverward/my-proxy/releases/latest/download/MPX-Linux-arm64.deb)
- 其他发行版：在[最新发布页](https://github.com/RovenEverward/my-proxy/releases/latest)下载 `MPX-<版本>-Linux-amd64.tar.gz`（ARM 用 `arm64`），解压后进 `MPX/` 目录运行 `./install.sh`。
- Linux 桌面版要用系统的 WebKitGTK 4.1（Debian / Ubuntu 上叫 `libwebkit2gtk-4.1-0`）。装 `.deb` 会自动带上，用 tar.gz 的话要自己装。
- 全部安装包（含命令行版 `mpx-cli_…`）和校验文件 `SHA256SUMS` 在[最新发布页](https://github.com/RovenEverward/my-proxy/releases/latest)。
:::

## 2. 打开 MPX，填 VPS 的 IP 和密码

打开 MPX，点 **首次安装（新服务器）**。

<Shot name="setup-welcome" cap="欢迎页" size="dialog" />

1. **服务器地址** 填 VPS 的 IP，例如 `203.0.113.10`。
2. **登录方式** 选 **SSH 密码**，填 root 密码。
3. 点 **检查机器**，看到「可以安装」后点 **下一步**。

<Shot name="setup-host" cap="连接 VPS" size="dialog" />

密码只用这一次，MPX 不会保存。

::: details 已经在别的电脑上装过了？
在欢迎页点 **接入已有控制面**，不用再装一遍。见[在多台设备上管理](/guides/devices)。
:::

::: details SSH 端口不是 22，或者已经配好了免密登录？
在这一页改 **SSH 端口**，或者把登录方式换成 **这台电脑已能用密钥登录**。
:::

## 3. 起个用户名、设个设备密码，开始安装

1. **第一个用户** 随便起个名字，例如 `alice`。节点的显示名（如 🇯🇵 东京 A）按 VPS 的 IP 自动识别，不用填。
2. 设一个**设备密码**，至少 12 位，再输一次确认。以后用别的电脑管理时要输它，它不是 VPS 的密码，记好。
3. 点 **开始安装**，等每一步都打上勾。大约 5–10 分钟，看 VPS 网速。

<Shot name="setup-settings" cap="设置" size="dialog" />

某一步失败的话，窗口里会写原因。改好后点 **重新运行**。

## 4. 导入订阅，开始用

装完会显示订阅链接和二维码：

- **手机：** 用客户端扫二维码。
- **电脑：** 点复制，在 Clash Verge、sing-box、v2rayN 这类客户端里「从链接导入订阅」。

<Shot name="setup-done" cap="完成" size="dialog" />

点 **进入管理界面**，看到总览页就说明全部装好了。从现在起，不用再登录 VPS。

<Shot name="overview" cap="总览" />

::: details 装好的是什么？
默认是 VLESS + REALITY，端口 443，不用域名和证书。想了解背后的结构，见[控制面与节点](/concepts/architecture)。
:::

## 接下来

- 给朋友开账号 → [添加用户并分发订阅](/guides/add-users)
- 再加一台服务器 → [添加节点](/guides/add-nodes)
- 用另一台电脑管理 → [在多台设备上管理](/guides/devices)
