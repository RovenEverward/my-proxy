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
| `rules` | `[]Rule` | 内置列表 | User-Agent 路由匹配规则列表。留空即用内置默认。详见下文「UA 识别规则」。 |
| `templates` | `map` | `{}` | 各格式的自定义客户端配置模板。键为格式名（`clash-meta`、`sing-box`、`xray`），值为空或缺省时使用内置模板。 |

---

## 2. User-Agent 订阅自动分流规则 (Rules)

当客户端请求 `/sub/<token>`（未显式指定 `/clash`、`/sing-box` 等后缀）时，控制面会提取 HTTP 请求头中的 `User-Agent`，按 `rules` 列表中的正则**从上到下逐条匹配**，命中第一条即返回对应格式。

### 内置匹配规则（按优先级顺序）

`rules` 为空（出厂状态，或老库升级上来时该列是空的）就使用下面这份内置列表，因此新装的实例开箱就能把主流客户端认出来：

| 优先级 | 正则模式 (`pattern`) | 映射格式 (`result`) | 匹配的目标客户端 |
| :---: | :--- | :--- | :--- |
| 1 | `(?i)karing` | `clash-meta` | Karing |
| 2 | `(?i)mihomo\|clash-verge\|clash\.meta\|clashmeta\|flclash\|stash` | `clash-meta` | Mihomo Party, Clash Verge (Meta 内核), FlClash, Stash |
| 3 | `(?i)clash` | `clash` | 原版 Clash, Clash for Windows, ClashX |
| 4 | `(?i)sing-box\|sfi/\|sfa/\|sfm/\|hiddify\|nekobox` | `sing-box` | sing-box 官方全平台客户端, Hiddify, NekoBox |
| 5 | `(?i)xray` | `xray` | xray 原生客户端 / CLI |
| 6 | `(?i)v2rayn\|v2rayng` | `base64-links` | v2rayN, v2rayNG |
| 7 | `(?i)shadowrocket` | `base64-links` | Shadowrocket (小火箭) |
| 8 | `(?i)quantumult` | `base64-links` | Quantumult X |
| 9 | `(?i)surge\|loon` | `base64-links` | Surge, Loon |
| 默认兜底 | *(全部未命中)* | `links` | 纯文本每行一个分享链接 (`vless://...`) |

> 顺序有讲究：Karing 与 `clash-verge` 这类客户端会在同一个 UA 里同时声称自己是好几种
> 客户端，所以把窄的（`mihomo`、`clash-verge`）排在宽的（`clash`）前面，否则宽规则会先把
> 它们接走、拿到不含 Meta 特性的配置。

### 管理规则

```bash
# 看当前规则，以及它来自内置默认还是自己配过
mpx settings rules list

# 拿一个 User-Agent 试一下会被认成什么格式（不会真的发请求）
mpx settings rules test "clash-verge/1.7.7"

# 追加一条：命中这个正则的客户端拿 clash-meta
mpx settings rules add '(?i)我的客户端' --result clash-meta

# 插到第 1 位（优先级最高）；--index 省略则追加到最后
mpx settings rules add '(?i)最优先' -r xray --index 1

# 把第 3 条挪到第 1 位；删掉第 2 条
mpx settings rules move 3 1
mpx settings rules rm 2

# 清掉自定义规则，回到内置默认
mpx settings rules reset
```

`--result` 只认 `clash-meta`、`clash`、`sing-box`、`xray`、`links`、`base64-links`、`block`
七种；`block` 会让命中的客户端收到 404。

> **想让某个客户端拿到纯链接**：规则是"命中什么就给什么"，所以配一条
> `(?i)那个客户端` → `links` 即可；把规则整表清空是回到内置默认，不是关掉分流。
> 真要全站关掉分流，配一条 `(?i).*` → `links`。

> **首次改动会落库**：内置规则是"空即默认"，因此第一次 `add` / `rm` / `move` 会把当前这份
> 完整规则表写进库里 —— 写进去的就是内置默认那份，行为不变，只是从此以库里的为准。
> 想退回去就 `mpx settings rules reset`。

> **浏览器拦截例外**：
> 如果请求**没有命中任何规则**，且请求头 `Accept` 中包含 `text/html`（表明是网页浏览器访问），控制面会拦截并返回 **HTML 订阅仪表盘网页**，而不是纯文本链接。
> 规则命中过（哪怕命中的就是 `links`）或路径里点名了格式，都按客户端请求处理，不给页面。

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

模板是 YAML（clash-meta）或 JSON（sing-box / xray）配置底稿；服务端把已授权节点注入底稿。Clash Meta 底稿需要保留 `rule-providers` 键。标题中的 `{USERNAME}` 由订阅响应头展开。

## 4. 用途订阅

用途配置独立于全局设置，通过管理 token 保护的 `mpx purpose` / `/api/purposes` 管理。先为节点或 host 手动标注，再保存用途：

```json
{"region":"US","line":"premium","purposes":["media"]}
```

```bash
mpx purpose metadata set nodes 1 -f metadata.json
mpx purpose metadata get hosts 2
mpx purpose create media -f media.json
mpx purpose list
mpx purpose get media
mpx purpose set media -f media.json
mpx purpose preview media alice clash-meta
mpx purpose del media
```

`create` / `set` 接受完整 JSON（`-f -` 读 stdin），预览输出真实配置并包含用户凭证。下面配置让流媒体域名走 media 组、其他代理流量走显式指定的 daily 组；先创建 `kind: daily` 的 daily 用途。没有 `daily_purpose` 时，其他流量沿用原始默认或自定义模板路由。

```json
{
  "slug":"media", "name":"流媒体", "description":"美国精品线路",
  "kind":"media", "regions":["US"], "lines":["premium"], "tags":["media"],
  "domains":["netflix.com","nflxvideo.net","youtube.com","googlevideo.com"],
  "strategy":"url-test", "test_url":"https://www.gstatic.com/generate_204", "interval":300,
  "daily_purpose":"daily"
}
```

`slug` 是稳定的 URL 标识：1–48 位小写字母、数字或连字符，以字母开头。`kind` 支持 daily / media / download / game。regions 和 lines 同时约束，tags 与元数据 purposes 有任一交集即可；空条件不限制。host 非空字段覆盖 node 对应字段。daily 将模板的代理路由目标改写为候选组，保留直连/拒绝决定；其他用途为 domains 增加优先域名后缀规则。候选组支持 select / url-test。media 未给 domains 时有常见流媒体默认域名；download / game 必须显式配置域名。

`required_capabilities` 可选 `egress`、`youtube`、`netflix`（全部满足，最多 3 项，不允许重复），`capability_region` 可选两位大写地区代码，必须同时要求 `egress` 或 `netflix`；youtube 不提供地区观测。留空表示手动标签阶段；要求能力但检测未接入时会报错。`templates` 可按 `clash-meta` / `sing-box` 覆盖本用途底稿。`daily_purpose` 只能指向 daily 用途；目标不存在、被删除、无候选或能力要求不满足时明确报错。

订阅地址为 `/sub/<token>/clash-meta?purpose=media` 或 `/sub/<token>/sing-box?purpose=media`；浏览器打开 `/sub/<token>?purpose=media` 可选择用途、复制地址。用途不增加任何授权：停用、到期、超限、排水、入站权限及容量仍生效。未知/已删除用途返回 404，候选为空或客户端格式不支持返回 422 并提示默认订阅地址。用途请求每次重新筛选；修改配置或标签同时使默认订阅缓存失效。

此批用途路由支持 Mihomo（clash-meta）和 sing-box。links、base64、普通 Clash 与 Xray 保持默认订阅可用，但选择用途时明确提示格式限制。下载与游戏只是可配置标签/规则组：客户端 url-test 测的是 HTTP 延迟，下载吞吐、游戏 RTT、抖动、丢包需在本地实际观察；WireGuard 在 sing-box 用途中可手动选择，不参加 urltest。
