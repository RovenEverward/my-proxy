# 全局配置字典 (Settings)

本文列出控制面全局设置（`Settings`）中所有字段的名称、类型、默认值与作用，以及客户端识别与模板定制规则。

---

## 1. 全局配置字段

通过 `mpx settings show` 查看当前配置，通过 `mpx settings set -f <json_file>` 进行批量更新。

| 字段名 | 类型 | 默认值 | 说明与取值范围 |
| :--- | :--- | :--- | :--- |
| `sub_url_prefix` | `string` | `""` | 订阅链接的基础 URL 前缀（如 `https://sub.example.com:8080`）。CLI 拼装链接以及 Web 订阅页生成分享地址时使用。 |
| `profile_title` | `string` | `""` | 客户端订阅文件中展示的订阅名称 / 配置文件标题。支持 `{USERNAME}` 变量占位符，渲染时会自动替换为具体订阅用户的用户名。 |
| `support_link` | `string` | `""` | 客服支持或工单系统链接，部分客户端（如 sing-box / clash-meta）在元数据中展示。 |
| `update_interval` | `int` | `24` | 提示客户端自动更新订阅的间隔周期，单位为**小时**。会写入客户端配置的对应字段中（如 Clash 的 `interval`）。 |
| `webhook_url` | `string` | `""` | 事件通知回调地址。不为空时，控制面会在用户即将到期、超额或节点下线时向其发送 POST 请求。 |
| `notify_days_left` | `int` | `3` | 用户到期预警阈值（天数）。距过期时间小于等于此天数时触发通知。 |
| `notify_usage_percent` | `int` | `80` | 用户流量使用率预警阈值（百分比整数，如 `80`）。已用流量达到限额的该百分比时触发通知。 |
| `rules` | `[]Rule` | 内置列表 | User-Agent 路由匹配规则列表。详见下文「UA 识别规则」。 |
| `templates` | `map` | `{}` | 各格式的自定义客户端配置模板。键为格式名（`clash-meta`、`sing-box`、`xray`），值为空或缺省时使用内置模板。 |

---

## 2. User-Agent 订阅自动分流规则 (Rules)

当客户端请求 `/sub/<token>`（未显式指定 `/clash`、`/sing-box` 等后缀）时，控制面会提取 HTTP 请求头中的 `User-Agent`，按 `rules` 列表中的正则**从上到下逐条匹配**，命中第一条即返回对应格式。

### 内置匹配规则（按优先级顺序）

| 优先级 | 正则模式 (`pattern`) | 映射格式 (`result`) | 匹配的目标客户端 |
| :---: | :--- | :--- | :--- |
| 1 | `(?i)clash\.meta\|mihomo` | `clash-meta` | Mihomo Party, Clash Verge (Meta 内核) |
| 2 | `(?i)clash` | `clash` | 原版 Clash, Clash for Windows, ClashX |
| 3 | `(?i)sing-box\|sfi\|sfa` | `sing-box` | sing-box 官方全平台客户端 |
| 4 | `(?i)v2rayn\|v2rayng` | `base64-links` | v2rayN, v2rayNG |
| 5 | `(?i)shadowrocket` | `base64-links` | Shadowrocket (小火箭) |
| 6 | `(?i)quantumult%20x` | `base64-links` | Quantumult X |
| 7 | `(?i)xray` | `xray` | xray 原生客户端 / CLI |
| 默认兜底 | *(全部未命中)* | `links` | 纯文本每行一个分享链接 (`vless://...`) |

> **浏览器拦截例外**：
> 如果请求未命中上述特定规则，且请求头 `Accept` 中包含 `text/html`（表明是网页浏览器访问），控制面会拦截并返回 **HTML 订阅仪表盘网页**，而不是纯文本链接。

---

## 3. 自定义客户端模板 (Templates)

如果需要修改 Clash 或 sing-box 订阅中包含的分流规则（如注入自定义的国内直连、广告拦截规则组），可以使用自定义模板：

```bash
# 获取当前使用的模板
mpx settings template get clash-meta

# 设置自定义模板文件
mpx settings template set clash-meta -f my-template.yaml

# 恢复使用内置默认模板
mpx settings template del clash-meta
```

模板中使用标准的 Go `text/template` 语法，内置变量包含 `.Nodes`、`.ProfileTitle`、`.UpdateInterval` 等核心上下文。
