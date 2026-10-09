# 协议与传输层支持矩阵

本文列出 mpx 支持的代理协议、传输方式、安全层组合矩阵，以及各个客户端格式的兼容支持情况。

---

## 1. 协议组合矩阵

在执行 `mpx inbound add` 时，`--protocol`、`--network` 与 `--security` 必须满足底层核心的组合约束：

| 入站协议 (`--protocol`) | 传输方式 (`--network`) | 安全层 (`--security`) | 适用场景与特性说明 |
| :--- | :--- | :--- | :--- |
| **`vless`** (推荐) | `tcp` | `reality` | 免证书防主动探测首选，直接借用目标 SNI（如微软、苹果）的 TLS 握手特征。 |
| **`vless`** | `tcp` / `ws` / `grpc` / `xhttp` | `tls` | 配合自有合法域名证书，支持 CDN 代理（WebSocket）或 HTTP/2 传输。 |
| **`vmess`** | `tcp` / `ws` / `grpc` | `none` / `tls` | 兼容传统老旧客户端。 |
| **`trojan`** | `tcp` / `ws` / `grpc` | `tls` | 伪装为标准 HTTPS 流量，要求节点具备合规的 TLS 证书。 |
| **`shadowsocks`** | `tcp` | `none` | 仅支持 **Shadowsocks-2022**（加密算法固定为 `2022-blake3-aes-128-gcm`），不提供过时的流加密。 |
| **`wireguard`** | *(L3 独占)* | `none` | L3 级网络协议，自身完成端到端握手加密，不可在其外层套用 TLS 或 REALITY。 |

> **关于 `xhttp` (SplitHTTP / HTTPUpgrade)**：
> xray 官方已将 splithttp 与 httpupgrade 统一收敛为 `xhttp`。在渲染订阅时，针对旧版客户端会自动保持下发兼容的传输类型名称。

---

## 2. 客户端格式对协议特性的支持情况

不同客户端对较新传输协议与特性的支持程度不同。下表反映了各订阅格式的渲染能力：

| 客户端格式 (`ClientFormat`) | 格式扩展名 | VLESS REALITY | Shadowsocks-2022 | gRPC | xhttp |
| :--- | :--- | :---: | :---: | :---: | :---: |
| **`clash-meta`** (mihomo) | YAML |  支持 |  支持 |  支持 |  支持 |
| **`clash`** (原生旧版) | YAML | ❌ 不支持 | ❌ 不支持 | ❌ 不支持 | ❌ 不支持 |
| **`sing-box`** | JSON |  支持 |  支持 |  支持 | ⚠️ 部分版本 |
| **`xray`** | JSON |  支持 |  支持 |  支持 |  支持 |
| **`links` / `base64-links`** | URI / Base64 |  标准 `vless://` |  标准 `ss://` |  支持 |  支持 |

> **提示**：如果使用现代协议（如 VLESS REALITY 或 Shadowsocks-2022），客户端请优先选择 **Clash Verge Rev (Mihomo 内核)** 或 **sing-box**。原生 Clash 内核由于已停止更新，无法识别 REALITY 字段。
