---
kind: part
parent: README.md
code: [internal/cli/doctor.go, internal/server/doctor.go, internal/model/external.go, internal/store/external.go, internal/server/external_names.go]
verified: 2026-11-24 @ 5de6b0c
---
# 自检与端点元数据

> **30 秒版**：控制面内置只读健康自检 `mpx doctor`（核查 SQLite、后台心跳、每台节点现场 SSH 状态、六种订阅格式与采样表，仅 fail 时退出码为 3），并提供外部订阅端点的地区/命名清洗层。重型主动探测、能力检测与 IP 质量评级均不在控制面核心内，按需排查直接交给操作者或 AI Agent。

## 边界

负责：手动触发的轻量全链路自检（`mpx doctor`）、外部订阅端点地区清洗与手工覆写（`PATCH /api/sources/{id}/endpoints/{key}`）。

不负责：周期巡检（[节点](nodes.md) 的后台循环）；自动修复问题（只报告）；主动代理探测、IP 风险评级或常驻测速（按需排障与本机 CDN 优选直接由操作者或 AI Agent 执行）。

## 怎么运转

1. **自检（`mpx doctor`）**：调用 `GET /api/doctor`，由控制面依次检查 SQLite、后台循环心跳、每台节点的现场状态（可达性、服务活跃、配置漂移、证书余期）、六种订阅渲染和采样表；有任一项 `fail` 返回退出码 `3`，仅有 `warn` 返回 `0`。
2. **外部端点名称清洗（UC-17）**：外部来源刷新时根据节点原名中的旗帜、ISO 国家码、常见地区写法及来源默认地区自动生成 `external_metadata`（`源名 · 旗帜 城市/地区 字母`）；无法识别的端点保留 `源名 · 原名`，并可通过 `PATCH /api/sources/{id}/endpoints/{key}` 手工修正。

## 坑

- `mpx doctor` 会对每台节点发起一次现场 SSH 状态检查，节点多或失联时耗时会接近单节点超时预算；若只需秒级看控制面心跳与退避状态，使用无 SSH 的 `mpx status`。
