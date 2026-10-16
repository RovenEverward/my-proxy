# MPX
自建 xray / sing-box 多节点代理的控制面：一台控制面管住所有节点，操作者只在自己电脑上用 `mpx`、`mpx ui` 操作。

## 地图
- `cmd/mpx/`：唯一的二进制入口；CLI、控制面（`mpx serve`）、`mpx ui` 都是它
- `internal/server/`、`internal/store/`、`internal/model/`：控制面的接口、SQLite、领域对象
- `internal/control/`、`internal/node/`：后台循环与 SSH 驱动节点
- `internal/render/`：订阅渲染与节点配置渲染（两套，不共用）
- `internal/cli/`、`internal/ui/`：本机入口；`deploy/`：安装脚本与 systemd 单元
- 整体怎么配合、每块的边界和坑见 [docs/atlas/README.md](docs/atlas/README.md)；决策日志在 [docs/internal/decisions.md](docs/internal/decisions.md)

## 验证
- `go vet ./... && go test ./... -count=1`：改 Go 代码必须通过（约 1 分钟）
- `npx playwright test -c internal/ui/e2e`：改 `internal/ui/` 必须通过（约 5 分钟，先 `npm ci`）
- `npm run docs:build`：改 `docs/` 下用户文档必须通过
- `python3 scripts/atlas_audit.py`：改 atlas 或新增文档时跑

## 硬约束
- 控制面 API 只增不破：旧 CLI、旧 `mpx ui` 必须照常工作；要破坏兼容先停下来问主人
- 节点上不装任何 MPX 组件，只用系统 `ssh` 和核心自带的 API
- `mpx ui` 不长出控制面接口之外的能力，每个写操作要有等价命令；管理页不由控制面对外提供
- 改结构走 `node sync`（会重启），改名单走周期收敛（不重启）；不要让名单变化触发重启
- 发布件保留压缩包：已发布的 `mpx update` 和安装脚本按固定文件名下载

## 坑与排查
- `purpose.go`（model / store / server）现在只放地区元数据，「用途」已在 v0.7.0 删除
- 建用户必须显式带 `enabled`，零值会建出停用用户
- 策略组的「标签」条件自 v0.7.0 起没有来源，匹配不到节点

## 改动前先读
| 如果你要改… | 先读 |
|---|---|
| 表结构、`/api`、备份（internal/server/*、internal/store/*、internal/backup/*） | [控制面与数据](docs/atlas/control-plane.md) |
| 下发、收敛、巡检、核心安装（internal/control/*、internal/node/*、internal/render/xray.go） | [节点](docs/atlas/nodes.md) |
| 用户、套餐、流量、重置（internal/model/plan.go、internal/store/user.go、internal/cli/user*.go） | [用户、套餐与计量](docs/atlas/users.md) |
| 订阅、规则、策略组（internal/server/subscribe.go、internal/render/clash.go、singbox.go、routing*.go） | [订阅与分流](docs/atlas/subscription.md) |
| CLI、`mpx ui`、设备接入（internal/cli/*、internal/ui/*、internal/pin/*） | [本机入口](docs/atlas/operator-entry.md) |
| 安装、升级、发版（internal/cli/init*.go、internal/cli/update*.go、internal/selfupdate/*、deploy/*） | [安装与升级](docs/atlas/install-update.md) |
| 能力检测、体检、自检（internal/diagnostics/*、internal/fusion/*、internal/cli/doctor.go） | [体检与诊断](docs/atlas/diagnostics.md) |

## 维护
- 改了某一块的边界、流程，或踩到新坑：同一次改动里更新对应的 atlas 节点，刷新 verified。
- 硬约束、`docs/internal/decisions.md`、`docs/atlas/ideas/`、根节点的 30 秒版和愿景与思想属于守护层：不直接改，在改动说明里单独列出建议，等主人确认。
- 代码和守护层冲突时，停下来问主人，不要自行二选一。
- 提交前跑 `python3 scripts/atlas_audit.py`。
- 提交、PR、文档里不写个人真实身份信息。
