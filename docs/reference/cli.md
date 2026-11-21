# 命令行

日常管理都在 `/admin/` 网页管理台里完成。`mpx` 命令行是备用入口，适合写脚本，以及少数界面里不提供的操作（含装机用的 `mpx init`）。

## 安装

一行脚本（Windows 用 PowerShell 对应的一行，见[快速开始](/tutorial/quickstart)）：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash
```

各平台安装包在[最新发布页](https://github.com/RovenEverward/my-proxy/releases/latest)，名字以 `mpx-cli_` 开头。

装好后用管理员账号密码接入控制面：

```bash
mpx connect <域名或IP>:8080 --username admin
```

细节见[管理台访问](/guides/admin-access#在别的电脑上用命令行)。

## 只有命令行能做的事

| 命令 | 作用 |
| :--- | :--- |
| `mpx restore <备份文件>` | 从备份恢复控制面，见[备份控制面](/guides/backup) |
| `mpx user import` / `mpx user export` | 用 CSV 批量导入、导出用户 |
| `mpx settings rules test '<User-Agent>'` | 测试某个客户端会拿到哪种格式 |

## 怎样查命令

```bash
mpx --help
mpx user add --help
```

每个子命令的 `--help` 列出全部参数，并带可运行的例子。

## 外部订阅源

`mpx source` 管理外部订阅、刷新任务和接收用户。真实订阅地址通过交互输入或 `--url-stdin` 提供。来源详情与预览不返回节点凭证。

完整流程与策略组来源条件见[混合外部订阅](../guides/external-sources)。
