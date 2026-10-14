# 订阅设置

用 `mpx settings show` 查看，`mpx -o json settings show` 取得 JSON。`mpx settings set -f FILE` 整体替换：**文件里没写的字段会被清空**，包括 `templates`。只改规则或模板时用 `settings rules` 和 `settings template` 子命令。

## 字段

| 字段 | 类型 | 默认 | 说明 |
| :--- | :--- | :--- | :--- |
| `sub_url_prefix` | string | 空 | 订阅地址前缀。为空时用请求自身的协议和主机 |
| `profile_title` | string | `{USERNAME}` | 客户端里显示的订阅名 |
| `support_link` | string | 空 | 支持链接 |
| `update_interval` | int（小时） | `24` | 客户端自动更新间隔 |
| `webhook_url` | string | 空 | 通知投递地址；为空不发通知 |
| `notify_days_left` | int | `3` | 到期前多少天提醒 |
| `notify_usage_percent` | int | `80` | 用量达到多少百分比提醒 |
| `rules` | 数组 | 内置 9 条 | User-Agent 规则，见下 |
| `templates` | 对象 | `null` | 各格式的客户端模板，键为 `clash-meta` / `sing-box` / `xray`；缺键或空串用内置模板 |

## 内置 User-Agent 规则

从上到下匹配，第一条命中生效；都不命中返回 `links`。

| 顺序 | 正则 | 结果 |
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

规则结果可取：`clash-meta`、`clash`、`sing-box`、`xray`、`links`、`base64-links`、`block`（返回 404）。

## 样本

```json
{
  "sub_url_prefix": "",
  "profile_title": "{USERNAME}",
  "support_link": "",
  "update_interval": 24,
  "webhook_url": "",
  "notify_days_left": 3,
  "notify_usage_percent": 80,
  "rules": [
    { "pattern": "(?i)karing", "result": "clash-meta" }
  ],
  "templates": null
}
```

（`rules` 截取了第一条。）
