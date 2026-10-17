---
kind: part
parent: README.md
code: [internal/diagnostics/, internal/fusion/, internal/control/capability.go, internal/control/checkup.go, internal/node/capability.go, internal/cli/doctor.go, internal/cli/capability.go]
verified: 2026-10-18 @ 95c78cd
---
# 体检与诊断

> **30 秒版**：三种「看一看」：**能力检测**经某个入口真的连出去，看出口 IP 在哪、能不能解锁特定网站，结果给策略组做筛选条件；**一键体检**（`mpx node diagnose`）在节点上跑一套有时间和体积上限的网络测量；**自检**（`mpx doctor`）把整套系统查一遍：控制面的库和后台任务、每台节点的现场状态、六种订阅格式各渲染一次、采样表。另有 `mpx diagnose` 在操作机本地跑同样有上限的网络测量。

## 边界

负责：手动触发的、有上限的测量和报告。

不负责：周期巡检（[节点](nodes.md) 的后台循环）；修复问题（只报告）。

## 怎么运转

1. 能力检测在控制面起临时 SOCKS 客户端，经指定入站或入口发请求，检测流量计入那个用户的配额；检测器版本是证据指纹的一部分，升级后旧结果显示未知（[能力检测验证](../internal/capability-validation.md)）。
2. 一键体检默认引擎是固定版本的官方 Go ECS 融合怪，经 SSH 在节点上跑，报告受传输预算约束（[一键体检验证](../internal/diagnostic-validation.md)）。
3. 自检只有 fail 才让退出码非 0（固定为 3，warn 不算），可以直接当健康检查脚本用（decisions 第 23 条，[internal/cli/doctor.go](../../internal/cli/doctor.go)）。`mpx ui` 的「自检」页走本地接口，由操作机上的 `mpx` 执行。
4. 控制面给有漂移的节点检查项带 `node_id` 与 `ui_action: "apply"`（只增，v0.9.10 起）。界面据此在体检页放行内「应用到<节点>」：点击只记一个 10 秒有效的标记并跳到节点详情，由详情页弹确认框，用户确认才真的下发；节点连不上时详情头部的「应用到节点」是 `aria-disabled`（仍可聚焦、读屏读得到原因，点击被界面统一拦下），提示先点「刷新状态」。

## 坑

- 体检报告超出传输预算 → 被截断会丢掉最有用的部分 → 已有几次修复（#13、#14、#16），改报告格式时先看预算。
- 官方测速在默认预算内跑不完 → 别把部分报告说成全模块成功。
- 能力检测失败 → 结果保留「未知」，不当成「不可用」。
