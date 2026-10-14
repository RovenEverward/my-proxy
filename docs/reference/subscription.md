# 订阅接口

订阅地址不需要管理 token，地址里的 token 就是凭据。

## 地址

| 地址 | 返回 |
| :--- | :--- |
| `/sub/<token>` | 按 User-Agent 规则选格式；浏览器请求返回订阅页 |
| `/sub/<token>/links` | 分享链接 |
| `/sub/<token>/base64-links` | Base64 编码的分享链接 |
| `/sub/<token>/clash` | Clash |
| `/sub/<token>/clash-meta` | Clash Meta / mihomo |
| `/sub/<token>/sing-box` | sing-box |
| `/sub/<token>/xray` | xray |

## 何时返回订阅页

只有请求头 `Accept` 中点名 `text/html` 时，`/sub/<token>` 才返回 HTML 订阅页。客户端通常不带 `Accept` 或只带 `*/*`，因此拿到的是配置。

## 响应头

`clash-verge` 请求 `/sub/<token>` 的真实响应：

```http
HTTP/1.1 200 OK
Content-Disposition: attachment; filename=alice
Content-Type: text/yaml; charset=utf-8
Profile-Title: alice
Profile-Update-Interval: 24
Profile-Web-Page-Url: http://127.0.0.1:8080/sub/F5qwEAdMULZZRk7Y
Subscription-Userinfo: upload=0; download=0; total=107374182400; expire=1798732799
```

| 头 | 来源 |
| :--- | :--- |
| `Profile-Title` | 设置 `profile_title`，变量替换后 |
| `Profile-Update-Interval` | 设置 `update_interval`（小时） |
| `Profile-Web-Page-Url` | 该用户的订阅页地址 |
| `Subscription-Userinfo` | 上传、下载、总量（字节），到期时间（Unix 秒） |
