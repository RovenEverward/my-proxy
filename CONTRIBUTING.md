# Contributing to mpx

感谢你参与 mpx 的开发与贡献！本文档记录本地开发、测试与代码规范要求。

---

## 1. 编码与工具链要求

- **Go 版本**：Go 1.22+
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

## 4. 提交约定

- 提交信息请使用简明清晰的动词开头（如 `fix: ...`, `feat: ...`, `docs: ...`）。
- 任何涉及协议渲染、存储 Schema、CLI 参数变更的改动，需同步更新 `docs/` 下的对应契约与使用文档。
