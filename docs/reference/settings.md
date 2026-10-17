# 订阅设置

在 **设置 → 通用** 里修改，点 **保存** 生效。保存只改你动过的项，其他设置保持不变。

<Shot name="settings-general" cap="设置 · 通用" />

## 通用

| 界面上的名字 | 类型 | 默认 | 说明 |
| :--- | :--- | :--- | :--- |
| 订阅地址前缀 | 网址 | 空 | 发给用户的订阅链接以它开头。为空时用控制面自己的地址 |
| 订阅标题 | 文字 | `{USERNAME}` | 客户端里显示的订阅名。`{USERNAME}` 替换成用户名 |
| 客户端刷新间隔（小时） | 整数 | `24` | 客户端多久自动更新一次订阅 |
| 支持链接 | 网址 | 空 | 客户端里显示的支持页面 |
| Webhook | 网址 | 空 | 到期、超限等通知发到这里；为空不发通知。旁边的 **测试** 发一条测试通知 |
| 到期前提醒（天） | 整数 | `3` | 到期前多少天提醒；`0` 表示关闭 |
| 用量提醒（%） | 整数 | `80` | 用量达到多少百分比时提醒；`0` 表示关闭 |

## 识别规则

**设置 → 识别规则** 决定每个客户端拿到哪种格式。从上到下匹配 User-Agent，第一条命中生效；都不命中返回 `links`。

内置规则：

| 顺序 | User-Agent 正则 | 返回 |
| :--- | :--- | :--- |
| 1 | `(?i)karing` | `clash-meta` |
| 2 | `(?i)mihomo\|clash-verge\|clash\.meta\|clashmeta\|flclash\|stash` | `clash-meta` |
| 3 | `(?i)clash` | `clash` |
| 4 | `(?i)sing-box\|sfi/\|sfa/\|sfm/\|hiddify\|nekobox` | `sing-box` |
| 5 | `(?i)xray` | `xray` |
| 6 | `(?i)v2rayn\|v2rayng` | `base64-links` |
| 7 | `(?i)shadowrocket` | `base64-links` |
| 8 | `(?i)quantumult` | `base64-links` |
| 9 | `(?i)surge\|loon` | `base64-links` |

可返回的格式：`clash-meta`、`clash`、`sing-box`、`xray`、`links`、`base64-links`、`block`（客户端拿不到订阅）。

## 模板

模板是 `clash-meta`、`sing-box` 和 `xray` 三种订阅的底稿：控制面把每个用户能用的节点填进模板，模板里的其余部分（分流、策略组、DNS 等）原样下发给所有人。

在 **设置 → 模板** 里点 **查看 / 自定义** 修改某种格式的模板；改过的格式标为「自定义」，点 **恢复内置** 改回内置模板。没有改过的格式用内置模板。
