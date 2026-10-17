# 命令行

日常管理都在 MPX 桌面程序里完成。`mpx` 命令行是备用入口，适合写脚本，以及少数界面里不提供的操作。

## 安装

在[最新发布页](https://github.com/RovenEverward/my-proxy/releases/latest)下载命令行安装包（名字以 `mpx_` 开头，不带 `desktop`）。macOS 和 Linux 也可以运行：

```bash
curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash
```

命令行和桌面程序共用同一份接入信息：这台电脑已经在 MPX 里接入了控制面，命令行直接可用。

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

每个子命令的 `--help` 列出全部参数，并带可运行的例子。界面里每个表单底部的 **等价命令** 也给出了对应的命令。
