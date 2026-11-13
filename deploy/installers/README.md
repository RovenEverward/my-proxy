# 包管理器安装渠道

UC-07：一条命令同时得到桌面程序与 `mpx` 命令行，升级沿用 app 自更新。

## macOS — Homebrew

tap 仓库 `RovenEverward/homebrew-mpx`，`Casks/mpx.rb`：

- 装 `MPX.app`，用 `binary` stanza 把 `MPX.app/Contents/MacOS/mpx` 软链进
  Homebrew bin —— CLI 与 app 永远同版本，不引入第二个版本源。
- `auto_updates true`：升级由 MPX 自身「更新」承担，`brew upgrade` 仍可用。
- `postflight` 去 quarantine：包未签名，带着 quarantine 的 unsigned 二进制会被
  内核直接杀掉（Killed: 9）。签名落地后删掉这个 stanza。

用户安装：

```bash
brew install --cask RovenEverward/mpx/mpx
```

版本与 SHA256 由 `release.yml` 的 `bump-installers` job 写回 tap，手工修也可。

## Windows — winget

用户安装：

```powershell
winget install RovenEverward.MPX
```

发布件就是 `MPX-<ver>-Windows-x64-setup.exe`（NSIS、按用户安装、无需管理员）。
安装器把 `%LOCALAPPDATA%\Programs\MPX` 加进用户 PATH（`MPX.exe winpath`），
`MPX.exe` 大小写不敏感，`mpx` 子命令即可使用；带子命令时 desktop=1 二进制照
CLI 工作，并通过 `internal/cli/console_windows.go` 把输出挂回终端。

manifest 提交用 `wingetcreate`：

- **首次**：需手动跑一次 `wingetcreate submit` 或提交 PR 到
  `microsoft/winget-pkgs`，包 id 约定 `RovenEverward.MPX`。
- **后续**：`release.yml` 的 `bump-installers` 用 `wingetcreate update --submit`
  自动提交，需要 repo secret `WINGET_GITHUB_TOKEN`（public_repo 权限的 PAT，
  且该账号有 winget-pkgs 的 fork）。未配置时跳过并打 warning，不阻塞发布。

## 手动验证清单

- macOS：`brew install --cask mpx` → `/Applications/MPX.app` 存在、
  `brew --prefix`/bin/mpx 可执行、`mpx --version` 与 app 一致。
- Windows：setup.exe /S 装完，新终端 `mpx --version` 输出；卸载后 PATH 恢复。
  CI 的 `path-check.ps1` 覆盖长 PATH 场景（`mpx.nsi` 与 `mpx-desktop.nsi`）。
