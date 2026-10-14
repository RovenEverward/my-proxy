# 命令行参考

全部内容以 `mpx --help` 和各子命令的 `--help` 为准；子命令的 `--help` 带可运行的例子。

## 全局参数

| 参数 | 类型 | 默认 | 说明 |
| :--- | :--- | :--- | :--- |
| `--server` | string | — | 控制面地址 |
| `--token` | string | — | 管理 token |
| `--profile` | string | 配置文件里的 `current` | 使用哪个配置档 |
| `-o, --output` | string | `table` | `table` 或 `json` |
| `-v, --version` | — | — | 打印版本 |

## 连接参数的取值顺序

地址和 token 各自按以下顺序取第一个非空值：

1. 命令行参数 `--server` / `--token`
2. 环境变量 `MPX_SERVER` / `MPX_TOKEN`
3. 配置文件中所选配置档的 `server` / `token`

配置档按 `--profile`、`MPX_PROFILE`、配置文件的 `current` 依次选择。

## 配置文件

路径：用户配置目录下的 `mpx/config.json`（Linux `~/.config/mpx/config.json`，macOS `~/Library/Application Support/mpx/config.json`）。由 `mpx login` 写入：

```json
{
  "current": "default",
  "profiles": {
    "default": {
      "server": "http://127.0.0.1:8080",
      "token": "demo-token"
    }
  }
}
```

## 命令

| 命令 | 作用 |
| :--- | :--- |
| `init` | 在一台新 VPS 上装好控制面、节点、入站、用户，打印订阅地址 |
| `login` | 保存控制面地址与 token；不带参数时显示当前值 |
| `serve` | 启动控制面 |
| `status` | 查看控制面状态（只读） |
| `doctor` | 逐项自检（只读） |
| `node` | 管理节点：`add` `list` `show` `edit` `rm` `install` `sync` `config` `status` `online` `drain` `undrain` `core` `probe` `capabilities` `capability-set` `diagnose` `diagnostics` |
| `inbound` | 管理入站：`add` `list` `show` `edit` `rm` |
| `host` | 管理入站的对外入口：`add` `list` `edit` `rm` |
| `user` | 管理用户：`add` `list` `edit` `rm` `enable` `disable` `renew` `reset-traffic` `revoke-sub` `links` `import` `export` |
| `purpose` | 管理用途分组：`create` `set` `get` `list` `del` `metadata` `preview` |
| `settings` | 订阅设置：`show` `set` `rules` `template` `test` |
| `backup` / `restore` | 下载备份；把备份暂存到控制面，重启后生效 |
| `diagnose` | 在本机直连或经 SOCKS 代理做综合体检，不需要登录控制面 |
| `ui` | 在本机浏览器里管理控制面，见[用 mpx ui 在浏览器里管理](/guides/web-ui) |

## mpx serve

| 参数 | 默认 | 说明 |
| :--- | :--- | :--- |
| `--addr` | `127.0.0.1:8080` | HTTP 监听地址 |
| `--db` | 用户配置目录下 `mpx/mpx.db` | SQLite 数据库路径 |
| `--interval` | `30s` | 同步用户名单与采集流量的间隔 |

token 为空时控制面不校验鉴权。对公网提供服务时必须设置 token 并显式给出 `--addr`。

## mpx ui

| 参数 | 默认 | 说明 |
| :--- | :--- | :--- |
| `--addr` | `127.0.0.1:7480` | 监听地址，只能是回环地址 |
| `--no-open` | 关 | 只打印一次性链接，不打开浏览器 |

## mpx init

| 参数 | 默认 | 说明 |
| :--- | :--- | :--- |
| `--host` | 必填 | VPS 地址或 IP |
| `--ssh-user` | `root` | 必须是 root |
| `--ssh-port` | `22` | |
| `--ssh-password` | 关 | 提示输入一次密码，装好公钥后改用密钥 |
| `--identity` | ssh 默认身份 | 私钥路径 |
| `--addr` | `0.0.0.0:8080` | 控制面监听地址 |
| `--api-port` | 取自 `--addr` | 控制面端口 |
| `--name` | `default` | 节点名 |
| `--remark` | 节点名 | 客户端里显示的节点名 |
| `--username` | `user1` | 要建的用户名 |
| `--sni` | `www.cloudflare.com` | REALITY 的 SNI |
| `--reality-dest` | `www.cloudflare.com:443` | REALITY 回落目标 |
| `--sub-prefix` | `http://<host>:<端口>` | 订阅地址前缀；设过一次后，不显式给出就不再覆盖 |
| `--binary` | 从 GitHub Releases 下载 | 上传本机的 Linux `mpx` 二进制 |
| `--install-script` | 仓库里的 `install.sh` | 使用哪份安装脚本 |

重复运行会复用已有的节点、入站、入口和用户。

## mpx user add

| 参数 | 默认 | 说明 |
| :--- | :--- | :--- |
| `--limit` | 不限 | 流量上限，如 `100GB`、`1.5TB`；`0` 表示不限 |
| `--reset-strategy` | `none` | `none` / `daily` / `weekly` / `monthly` |
| `--expire` | 不过期 | `YYYY-MM-DD`，当地时区当天 23:59:59 |
| `--inbounds` | 全部 | 入站 id，逗号分隔 |
| `--note` | — | 备注 |
| `--disabled` | 关 | 建好后先停用 |

`mpx user edit` 只改给出的字段。改回空值要显式给：`--limit 0`、`--expire ""`、`--inbounds ""`、`--note ""`。
