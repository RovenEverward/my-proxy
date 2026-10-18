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
  # Windows PowerShell 5.1 是用户机器上的默认:两种用法都在 5.1 下跑。
  #   1) -File 直接跑(靠 BOM 认出 UTF-8);
  #   2) README 的 irm | iex:从本机 HTTP 取脚本再执行(-From 只能这样传,所以用 scriptblock 代替 iex)。
  & powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$PSScriptRoot\..\install-cli.ps1" -From $Exe
  if ($LASTEXITCODE -ne 0) { throw "install-cli.ps1(5.1 -File)退出码 $LASTEXITCODE" }
  Expect 'install-cli.ps1(5.1 -File)之后' "$long;$cli"
  $key.SetValue('Path', $long, [Microsoft.Win32.RegistryValueKind]::ExpandString)
  # 真的走 irm:本机起一个 HTTP 服务按 text/plain; charset=utf-8 发脚本(和 raw.githubusercontent.com 一样)。
  $port = Get-Random -Minimum 20000 -Maximum 40000
  $srv = Start-Job -ArgumentList "$PSScriptRoot\..\install-cli.ps1", $port -ScriptBlock {
    param($file, $port)
    $l = [Net.HttpListener]::new(); $l.Prefixes.Add("http://127.0.0.1:$port/"); $l.Start()
    $ctx = $l.GetContext(); $b = [IO.File]::ReadAllBytes($file)
    $ctx.Response.ContentType = 'text/plain; charset=utf-8'; $ctx.Response.OutputStream.Write($b, 0, $b.Length); $ctx.Response.Close(); $l.Stop()
  }
  Start-Sleep -Seconds 2
  $wrap = Join-Path $env:TEMP 'mpx-irm-iex.ps1'
  Set-Content -Path $wrap -Encoding ASCII -Value '$t = Invoke-RestMethod -UseBasicParsing $args[0]; & ([scriptblock]::Create($t)) -From $args[1]; exit $LASTEXITCODE'
  & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $wrap "http://127.0.0.1:$port/install-cli.ps1" $Exe
  $rc = $LASTEXITCODE
  Receive-Job $srv -Wait -AutoRemoveJob | Out-Null
  if ($rc -ne 0) { throw "install-cli.ps1(5.1 irm)退出码 $rc" }
  Expect 'install-cli.ps1(5.1 irm)之后' "$long;$cli"
} finally {
  if ($orig -ne $null) { $key.SetValue('Path', $orig, $origKind) } else { $key.DeleteValue('Path', $false) }
}
