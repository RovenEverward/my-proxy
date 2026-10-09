# 告警与到期通知配置

mpx 具备后台巡检与事件触发告警能力。当用户的流量即将耗尽、账号临近过期，或管理节点掉线时，系统可通过 HTTP Webhook 发送实时结构化事件通知。

---

## 1. 配置通知通道

通知通道使用全局设置进行管理，通常指向你的企业微信、飞书、Telegram 机器人网关，或自建通知中心。

### 设置 Webhook 地址与阈值

通过配置 JSON 更新设置：

```bash
cat << 'EOF' | mpx settings set -f -
{
  "webhook_url": "https://alerts.example.com/api/mpx-webhook",
  "webhook_secret": "my-shared-signing-secret",
  "expire_warning_days": 3,
  "traffic_warning_percent": 80
}
EOF
```

配置项解释：
- `webhook_url`：接收 POST 通知的完整 HTTP(S) URL。
- `webhook_secret`：可选的签名密钥。配置后，控制面发出的请求头会带上 HMAC-SHA256 签名 `X-Mpx-Signature`，防止伪造。
- `expire_warning_days`：到期前多少天触发预警事件（默认 3 天）。
- `traffic_warning_percent`：已用流量达到百分之多少时触发超额预警（如 80 表示达到 80% 触发）。

---

## 2. 验证通知配置

配置完毕后，无需等待真实事件触发，可直接发送一条测试消息验证通道连通性：

```bash
mpx settings test
```

预期输出：
```
已向 https://alerts.example.com/api/mpx-webhook 发送测试事件: 响应 200 OK (耗时 45ms)
```

如果目标服务返回 4xx/5xx 或网络超时，CLI 会直接打印详细的 HTTP 状态码与远端返回体，便于排查网络防火墙或鉴权配置。

---

## 3. Webhook 事件负载格式（Payload）

当系统触发事件时，控制面会发起 `POST` 请求，请求体编码为 `application/json`。

### 事件 1：用户即将到期 (`user.expiring`)

```json
{
  "event": "user.expiring",
  "timestamp": "2026-10-09T11:45:00Z",
  "data": {
    "username": "alice",
    "expire_at": "2026-10-12T00:00:00Z",
    "days_left": 2
  }
}
```

### 事件 2：用户流量超额预警 (`user.traffic_warning`)

```json
{
  "event": "user.traffic_warning",
  "timestamp": "2026-10-09T11:45:00Z",
  "data": {
    "username": "bob",
    "used_bytes": 85899345920,
    "limit_bytes": 107374182400,
    "percent": 80
  }
}
```

### 事件 3：节点异常掉线 (`node.offline`)

```json
{
  "event": "node.offline",
  "timestamp": "2026-10-09T11:45:00Z",
  "data": {
    "node_id": 2,
    "node_name": "us-01",
    "address": "198.51.100.15",
    "error": "dial tcp 198.51.100.15:22: i/o timeout",
    "consecutive_failures": 3
  }
}
```
