---
kind: part
parent: README.md
code: [internal/diagnostics/, internal/fusion/, internal/control/capability.go, internal/control/checkup.go, internal/node/capability.go, internal/cli/doctor.go, internal/cli/capability.go, internal/reach/, internal/cli/reach.go, internal/ui/reach.go]
verified: 2026-11-13 @ c4faa0e + working tree
---
# 体检与诊断

> **30 秒版**：四种「看一看」：**能力检测**经某个入口真的连出去，看出口 IP 在哪、能不能解锁特定网站，结果给策略组做筛选条件；**一键体检**（`mpx node diagnose`）在节点上跑一套有时间和体积上限的网络测量；**自检**（`mpx doctor`）把整套系统查一遍：控制面的库和后台任务、每台节点的现场状态、六种订阅格式各渲染一次、采样表；**触达检查**（`mpx node check`、节点列表「检查触达」）从本机与控制面各探一轮入站 TCP/UDP，给出「双通 / 境内不通 / 都不通 / 未知」判定。另有 `mpx diagnose` 在操作机本地跑同样有上限的网络测量。

## 边界

负责：手动触发的、有上限的测量和报告。

不负责：周期巡检（[节点](nodes.md) 的后台循环）；修复问题（只报告）。

## 怎么运转

1. 能力检测在控制面起临时 SOCKS 客户端，经指定入站或入口发请求，检测流量计入那个用户的配额；检测器版本是证据指纹的一部分，升级后旧结果显示未知（[能力检测验证](../internal/capability-validation.md)）。
2. 一键体检默认引擎是固定版本的官方 Go ECS 融合怪，经 SSH 在节点上跑，报告受传输预算约束（[一键体检验证](../internal/diagnostic-validation.md)）。
3. 自检只有 fail 才让退出码非 0（固定为 3，warn 不算），可以直接当健康检查脚本用（decisions 第 23 条，[internal/cli/doctor.go](../../internal/cli/doctor.go)）。`mpx ui` 的「自检」页走本地接口，由操作机上的 `mpx` 执行。
4. 控制面给有漂移的节点检查项带 `node_id` 与 `ui_action: "apply"`（只增，v0.9.10 起）。界面据此在体检页放行内「应用到<节点>」：点击只记一个 10 秒有效的标记并跳到节点详情，由详情页弹确认框，用户确认才真的下发；节点连不上时详情头部的「应用到节点」是 `aria-disabled`（仍可聚焦、读屏读得到原因，点击被界面统一拦下），提示先点「刷新状态」。
5. 触达检查的探针只有一份（`internal/reach/`）：操作机（`mpx node check`、`mpx ui` 的 `/local/reach/check`）与控制面（`POST /api/reach/probe`）跑同一份 `Probe`，各探各的，操作机合并明细与判定再 `POST /api/reach/results` 回存（表 `reach_checks`，按节点存最近一次）。判定按入站自己的协议选格：TCP 系看 TCP 格，QUIC/UDP 系（wireguard、hysteria2、tuic、network=udp）看 UDP 格；另一协议的探测只进明细不参与判定——在 UDP 端口上探 TCP 必失败，混进判定会把「该协议本来就不该通」误读成「节点不通」。UDP 超时只记 unknown（服务端可以合法沉默），节点全 UDP 入站且全超时判「未知」不判「不通」。页面数据走 `GET /api/reach/last`（含逐入站明细）与 `GET /api/reach`（摘要），列表双列与筛选只在查过一轮后出现。

## 坑

- 体检报告超出传输预算 → 被截断会丢掉最有用的部分 → 已有几次修复（#13、#14、#16），改报告格式时先看预算。
- 官方测速在默认预算内跑不完 → 别把部分报告说成全模块成功。
- 能力检测失败 → 结果保留「未知」，不当成「不可用」。
- 触达检查里 UDP 的语义弱于 TCP：回包才可达，沉默不算不通；判定列的「不通」来自 TCP 超时/拒绝或 UDP 端口不可达，UDP 超时全部落「未知」。
