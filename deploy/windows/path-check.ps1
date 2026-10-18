# CI 用:在 Windows runner 上验证 setup.exe / 卸载 / install-cli.ps1 不会弄坏长的用户 PATH。
# 只在一次性的 CI 机器上跑 —— 它会改当前用户的 PATH(最后会恢复原值)。
param([Parameter(Mandatory)][string]$Setup, [Parameter(Mandatory)][string]$Exe)
$ErrorActionPreference = 'Stop'
$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
$opt = [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames
$orig = $key.GetValue('Path', $null, $opt); $origKind = if ($orig -ne $null) { $key.GetValueKind('Path') } else { $null }
function RawPath { [string]$key.GetValue('Path', '', $opt) }
function Expect($what, $want) {
  $got = RawPath; $kind = $key.GetValueKind('Path')
  if ($got -cne $want) { throw "$what：PATH 不对(长度 $($got.Length),期望 $($want.Length))`n前 200 字:$($got.Substring(0, [Math]::Min(200, $got.Length)))" }
  if ($kind -ne [Microsoft.Win32.RegistryValueKind]::ExpandString) { throw "$what：PATH 类型变成了 $kind" }
  Write-Host "OK $what (长度 $($got.Length))"
}
try {
  # 9000+ 字:远超 NSIS 的 1024(和 8192)缓冲;带 %VAR%、空条目。
  $parts = @('%USERPROFILE%\bin', '', '%LOCALAPPDATA%\Microsoft\WindowsApps')
  for ($i = 0; ($parts -join ';').Length -lt 9000; $i++) { $parts += "C:\Tools\long-directory-name-for-path-test-$i" }
  $long = $parts -join ';'
  $key.SetValue('Path', $long, [Microsoft.Win32.RegistryValueKind]::ExpandString)

  $dir = 'C:\mpx-path-test'
  Start-Process -FilePath $Setup -ArgumentList '/S', "/D=$dir" -Wait
  if (-not (Test-Path "$dir\mpx.exe")) { throw 'setup.exe 没装上 mpx.exe' }
  Expect 'setup.exe /S 之后' "$long;$dir"

  Start-Process -FilePath "$dir\uninstall.exe" -ArgumentList '/S', "_?=$dir" -Wait
  Expect '卸载之后' $long

  $cli = 'C:\mpx-cli-test'
  $env:MPX_PREFIX = $cli
  # 用 pwsh:install-cli.ps1 是无 BOM 的 UTF-8(README 里的用法是 irm | iex,按 UTF-8 解码),
  # Windows PowerShell 5.1 用 -File 直接跑会按 ANSI 读错中文。
  & pwsh -NoProfile -ExecutionPolicy Bypass -File "$PSScriptRoot\..\install-cli.ps1" -From $Exe
  if ($LASTEXITCODE -ne 0) { throw "install-cli.ps1 退出码 $LASTEXITCODE" }
  Expect 'install-cli.ps1 之后' "$long;$cli"
} finally {
  if ($orig -ne $null) { $key.SetValue('Path', $orig, $origKind) } else { $key.DeleteValue('Path', $false) }
}
