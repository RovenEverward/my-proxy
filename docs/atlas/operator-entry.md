---
kind: part
parent: README.md
code: [internal/cli/, internal/ui/, internal/pin/, cmd/mpx/]
verified: 2026-10-18 @ f1a7cad
---
# 本机入口：CLI、mpx ui、设备接入

> **30 秒版**：操作者只在自己电脑上干活。`mpx` 命令行是 `/api` 的纯客户端；`mpx ui` 在本机起一个只听回环地址的小服务，把页面请求带上 CLI 那份 token 原样转给控制面；新设备用 `mpx connect` 加设备密码接入，按证书指纹认控制面。

## 边界

负责：把操作拼成请求、把结果显示给人；多控制面配置档；设备接入与吊销；本机才能做的事（切换 profile、接入另一套控制面、自检、relabel 查 IP 归属、首次安装向导）。

不负责：碰数据库和节点（`mpx init` 是唯一例外，因为那时控制面还不存在，见 [安装与升级](install-update.md)）；长出控制面接口之外的能力。

## 怎么运转

```mermaid
flowchart LR
  B["浏览器"] -- "127.0.0.1 · 一次性会话" --> U["mpx ui（internal/ui）"]
  U -- "/api/*：按白名单原样转发" --> CP["控制面"]
  U -- "/local/*：本机执行" --> L["本地配置档 · 自检 · relabel · 首次安装"]
  T["终端 mpx（internal/cli）"] -- "HTTPS + token" --> CP
```

1. CLI 每条命令都能用 `curl` 等价做出来，不缓存数据，每次都问控制面（[internal/cli](../../internal/cli/)）。配置档写在 `~/.config/mpx`，能装多个控制面，写前原子替换（decisions 第 31 条）。
2. `mpx ui` 的页面是内嵌的原生 JS（[internal/ui/assets](../../internal/ui/assets/)）。转发层只放行白名单里的接口（[internal/ui/ui.go](../../internal/ui/ui.go) 的 allow 规则）；需要在客户端机器上动手的事走 `/local/*` 本地接口。为什么是本机转发、怎么防跨站和 DNS rebinding，见 [管理 UI 方案](../internal/ui-rfc.md)。正常模式下页面也能接入另一套控制面（`/local/profiles/connect*`：同一套取指纹→核对→设备密码的手续，存成指名的新配置档再切过去，不覆盖已有档）；`/ui/meta` 带当前档的证书指纹（设置页「当前控制面」显示、配置档下拉摆前 16 位用）。
   连接方式只在节点详情的「连接方式」卡片管理；旧地址 `#inbounds`（含子路径）跳到节点列表。详情头部有未应用改动、配置不一致或从未应用时才出现「应用到节点」主按钮，其余时候它和刷新状态、暂停 / 恢复分配、编辑一起收在「⋯」菜单。
3. 设备接入：控制面自签证书，CLI 记指纹（像 SSH 的 known_hosts）；`mpx connect` 用设备密码换一个设备 token，每台设备各自可吊销（[internal/pin](../../internal/pin/)，[设备接入方案](../internal/device-connect-rfc.md)）。
4. 桌面程序（v0.9.0 起）用 MyGo 把 `mpx ui` 装进窗口，不扩权：桌面构建无参数启动就开窗，未接入时窗口里做首次安装或接入已有控制面，一键更新的本机一层升级桌面程序自己（[internal/cli/app.go](../../internal/cli/app.go)，[桌面程序方案](../internal/desktop-rfc.md)）。

## 坑

- 给 UI 加功能时直接调一个新接口 → 不在转发白名单里会被拒绝 → 先确认 CLI 有对应能力，再加白名单。
- UI 轮询远端任务（如向导）→ 容易拉大量数据 → 用增量轮询、小响应、空闲退避、页面隐藏时暂停（[向导方案](../internal/wizard-rfc.md) 的连接性能约束）。
- 窗口里不出现命令行用语（v0.9.1 起）→ 本地报错按桌面 / 浏览器分开说；控制面原话带命令的错误和自检建议另给 `ui_hint`，页面优先显示，CLI 照旧用原话（[对外契约](../internal/contracts.md)）→ 新加报错时别把 `mpx …` 写进只会出现在窗口里的文字。
- 撞上控制面限流 → CLI 自己等，不把 429 甩给使用者（decisions 第 33 条）。
- `mpx login` 指向临时控制面 → 曾覆盖正在用的配置 → 现在按配置档区分，写前原子替换（[路线图](../internal/roadmap.md) 批 8）。
