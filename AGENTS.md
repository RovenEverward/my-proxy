# MPX
自建 xray / sing-box 多节点代理的 Agent-Native 极简控制面与 CLI：纯 Go 单体 + SQLite 管住所有节点，操作者或 AI Agent 通过 `mpx` 命令行（`-o json` / `--ssh` 通道）管理。

## 地图
- `cmd/mpx/`：唯一的纯 Go 二进制入口；CLI 与控制面（`mpx serve`）都是它
- `internal/server/`、`internal/store/`、`internal/model/`：控制面的接口、SQLite、领域对象
- `internal/control/`、`internal/node/`：后台循环与 SSH 驱动节点
- `internal/render/`：订阅渲染与节点配置渲染（两套，不共用）
- `internal/cli/`：操作机命令行入口；`skills/mpx/SKILL.md`：随仓分发的官方 Agent Skill；`deploy/`：部署脚本与 systemd 单元
- 整体怎么配合、每块的边界和坑见 [docs/atlas/README.md](docs/atlas/README.md)

## 验证
- `go vet ./... && go test ./... -count=1`：改 Go 代码必须通过（纯 Go，无需 Node.js 与额外 build tags）
- `npm run docs:build`：改 `docs/` 下用户文档必须通过
- `python3 scripts/atlas_audit.py`：改 atlas 或新增文档时跑

## 硬约束
- 控制面 API 与订阅契约优先稳定：客户端订阅路径与格式零破坏，管理接口默认只增不破；要破坏兼容先停下来问主人
- 节点上不装任何 MPX 组件，只用系统 `ssh` 和核心自带的 API
- 保持纯 Go 单体与 Agent-Native 契约：不引入前端构建依赖、密码会话系统、内置自升级或常驻探测进程；所有命令支持 `-o json`、非交互执行与清晰退出码
- 改结构走 `node sync`（会重启，先 `--dry-run` 预检），改名单走周期收敛（不重启）；不要让名单变化触发重启

## 坑与排查
- `metadata.go`（model / store / server）只放地区元数据（`LocationMetadata`），「用途」已在 v0.7.0 删除
- 建用户必须显式带 `enabled`，零值会建出停用用户
- 策略组的「标签」条件自 v0.7.0 起没有来源，匹配不到节点

## 改动前先读
| 如果你要改… | 先读 |
|---|---|
| 表结构、`/api`、备份（internal/server/*、internal/store/*、internal/backup/*） | [控制面与数据](docs/atlas/control-plane.md) |
| 下发、收敛、巡检（internal/control/*、internal/node/*、internal/render/serverconfig.go、singbox_server.go） | [节点](docs/atlas/nodes.md) |
| 用户、流量、重置（internal/model/user.go、internal/store/user.go、internal/cli/user*.go） | [用户与计量](docs/atlas/users.md) |
| 订阅、规则、规则集镜像、策略组（internal/server/subscribe.go、internal/render/clash.go、singbox.go、routing*.go） | [订阅与分流](docs/atlas/subscription.md) |
| CLI、SSH 通道与官方 Skill（internal/cli/*、internal/pin/*、skills/mpx/） | [操作入口](docs/atlas/operator-entry.md) |
| 编译、systemd 部署与迁移（internal/buildinfo/*、deploy/*） | [部署与运维](docs/atlas/install-update.md) |
| 自检与外部端点清洗（internal/cli/doctor.go、internal/server/doctor.go、internal/server/external_names.go） | [自检与端点元数据](docs/atlas/diagnostics.md) |

## 维护
- 改了某一块的边界、流程，或踩到新坑：同一次改动里更新对应的 atlas 节点，刷新 verified。
- 硬约束、`docs/atlas/ideas/`、根节点的 30 秒版和愿景与思想属于守护层：不直接改，在改动说明里单独列出建议，等主人确认。
- 代码和守护层冲突时，停下来问主人，不要自行二选一。
- 提交前跑 `python3 scripts/atlas_audit.py`。
- 提交、PR、文档里不写个人真实身份信息。
