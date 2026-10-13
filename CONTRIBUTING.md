# Contributing to mpx

感谢你参与 mpx 的开发与贡献！本文档记录本地开发、测试与代码规范要求。

---

## 1. 编码与工具链要求

- **Go 版本**：以 `go.mod` 里的 `go` 指令为准（当前 1.27.1）。Go 会自动下载所需工具链，本地装一个较新的版本即可。
- **格式化**：提交前必须执行 `gofmt -s -w .`
- **静态检查**：代码需通过 `go vet ./...`
- **代码规范**：遵循 Effective Go。业务判断与领域模型集中于 `internal/model/`（纯计算、无副作用），存储与持久化位于 `internal/store/`，网络下发集中于 `internal/node/`。

---

## 2. 本地测试

在提交代码或发起 Pull Request 之前，必须确保所有单元测试与竞态检测通过：

```bash
# 1. 编译验证
go build ./...

# 2. 静态检查
go vet ./...

# 3. 运行单元测试（开启竞态检测）
go test ./... -count=1 -race

# 4. 代码格式检查
gofmt -l .
```

---

## 3. 真实 Xray 核心集成测试 (End-to-End)

一部分测试需要与**真实的 xray 二进制**交互以验证渲染出的服务端配置与客户端配置合法性，以及 gRPC 统计与用户增删契约。如果没有提供二进制，测试会自动跳过。

运行真实测试的方法：

```bash
# 1. 从 https://github.com/XTLS/Xray-core/releases 下载并解压出本地平台的 xray 二进制
# 2. 设置环境变量并执行测试
export MPX_XRAY_BIN=/path/to/xray
go test ./... -count=1
```

测试覆盖内容：
- 将生成的 JSON 配置传递给 `xray -test -config` 验证语法。
- 启动临时 xray 进程，验证 `xray api adu`、`xray api rmu`、`statsquery` 接口交互契约。

---

## 4. 文档站

`docs/` 下的 markdown 由 [VitePress](https://vitepress.dev/) 渲染成一个站点，分「使用指南」「参考手册」「内部工程」三个页签。设计文档是 `docs/design.md`（工程文档区的入口），用户文档放在 `docs/user/`，内部工程文档放在 `docs/internal/`，站点导航与侧栏在 `docs/.vitepress/config.mts`。

新增或改名文档时，**必须同步更新 `docs/.vitepress/config.mts` 的 `nav` 与 `sidebar`**，否则页面不在导航里。相对链接指向目录时，要用 `xxx/index.md`，不要用 `xxx/README.md`。

```bash
# 装一次即可
npm install

# 本地预览（改文件自动刷新，终端会打印访问地址）
npm run docs:dev

# 构建静态站到 docs/.vitepress/dist/
npm run docs:build
```

`docs/.vitepress/dist/` 与 `docs/.vitepress/cache/` 是构建产物，已加进 `.gitignore`，不要提交。

---

## 5. 安装脚本与发布

`deploy/` 下有三个安装脚本，都按同一个顺序找二进制：**手边的 → 发布件 → 用本机 Go 从源码编**。三条路都断了才报错。

| 脚本 | 装到哪 | 平台 |
| --- | --- | --- |
| `deploy/install.sh` | 控制面（`/usr/local/bin/mpx` + systemd 单元） | Linux |
| `deploy/install-cli.sh` | 操作机上的 CLI | macOS / Linux |
| `deploy/install-cli.ps1` | 操作机上的 CLI | Windows |

改脚本时注意几条约束：

- **失败不能破坏现有部署。** 新二进制先落到 `.new` 跑一次 `--help`，跑得起来才替换。改这块时要保持这个顺序。
- **重跑必须安全。** `install.sh` 不覆盖已有的 `/etc/mpx/env`、数据库和 SSH 密钥。
- **归档名要和 `.github/workflows/release.yml` 对齐**：`mpx_<goos>_<goarch>.tar.gz`，Windows 是 `mpx_windows_amd64.zip`。改了一边必须改另一边。
- 脚本支持 `MPX_REPO`、`MPX_REF`、`MPX_DOWNLOAD_BASE`、`MPX_RAW_BASE` 几个环境变量，可以在本地用 `python3 -m http.server` 造一个假发布站来验证下载层，不必真发版。

发布走 tag：推一个 `v*` tag，`.github/workflows/release.yml` 会交叉编译五个平台并创建 Release。

---

## 6. 提交约定

- 提交信息请使用简明清晰的动词开头（如 `fix: ...`, `feat: ...`, `docs: ...`）。
- 任何涉及协议渲染、存储 Schema、CLI 参数变更的改动，需同步更新 `docs/` 下的对应契约与使用文档。
