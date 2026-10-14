<#
.SYNOPSIS
  在操作机(Windows)上装一个 mpx 命令行。

.DESCRIPTION
  装和升级是同一条命令,重复跑是安全的。二进制从哪儿来,按下面的顺序试,第一条走通
  就停:

    1. -From 指给你的文件
    2. 从 GitHub Releases 下载 windows 发布件
    3. 用本机的 Go 从源码编(在仓库里跑,或者先 git clone 一份)

  装完把安装目录加进当前用户的 PATH(不用管理员权限)。

.EXAMPLE
  .\deploy\install-cli.ps1
.EXAMPLE
  .\deploy\install-cli.ps1 -From C:\tmp\mpx.exe
.EXAMPLE
  .\deploy\install-cli.ps1 -Version v0.2.0
.EXAMPLE
  .\deploy\install-cli.ps1 -Uninstall
#>

[CmdletBinding()]
param(
	[string]$From = '',
	[string]$Version = 'latest',
	[string]$Prefix = '',
	[switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 默认还是 TLS 1.0,连 GitHub 会被拒。
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = if ($env:MPX_REPO) { $env:MPX_REPO } else { 'RovenEverward/my-proxy' }
$Ref = if ($env:MPX_REF) { $env:MPX_REF } else { 'main' }
$DownloadBase = if ($env:MPX_DOWNLOAD_BASE) { $env:MPX_DOWNLOAD_BASE } else { "https://github.com/$Repo/releases" }

if (-not $Prefix) {
	$Prefix = if ($env:MPX_PREFIX) { $env:MPX_PREFIX } else { Join-Path $env:LOCALAPPDATA 'Programs\mpx' }
}

function Write-Note($msg) { Write-Host "  $msg" }
function Die($msg) { Write-Host "错误:$msg" -ForegroundColor Red; exit 1 }

$Dest = Join-Path $Prefix 'mpx.exe'

if ($Uninstall) {
	if (Test-Path $Dest) {
		Remove-Item $Dest -Force
		Write-Host "已删除 $Dest"
	} else {
		Write-Host "$Dest 本来就不在"
	}
	exit 0
}

# ---- 这个系统要哪一份 -------------------------------------------------------

switch ($env:PROCESSOR_ARCHITECTURE) {
	'AMD64' { $Arch = 'amd64' }
	'ARM64' { $Arch = 'arm64' }
	'x86' {
		# 32 位 PowerShell 跑在 64 位系统上时会看到 x86,再看一眼真实架构。
		if ($env:PROCESSOR_ARCHITEW6432 -eq 'AMD64') { $Arch = 'amd64' }
		elseif ($env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $Arch = 'arm64' }
		else { Die "不支持的架构:$($env:PROCESSOR_ARCHITECTURE)" }
	}
	default { Die "不支持的架构:$($env:PROCESSOR_ARCHITECTURE)" }
}

$Archive = "mpx_windows_$Arch.zip"

# 在仓库里吗?往上找 go.mod。
$RepoRoot = ''
$dir = Split-Path -Parent $MyInvocation.MyCommand.Path
while ($dir) {
	if ((Test-Path (Join-Path $dir 'go.mod')) -and (Test-Path (Join-Path $dir 'cmd\mpx\main.go'))) {
		$RepoRoot = $dir
		break
	}
	$parent = Split-Path -Parent $dir
	if ($parent -eq $dir) { break }
	$dir = $parent
}

$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("mpx-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $Tmp -Force | Out-Null

$Src = ''

try {
	# ---- 1. 手边的二进制 ----------------------------------------------------
	if ($From) {
		if (-not (Test-Path $From)) { Die "-From 指的文件不存在:$From" }
		$Src = (Resolve-Path $From).Path
		Write-Host "==> 用手边的二进制 $Src"
	} else {
		foreach ($candidate in @('.\mpx.exe', (Join-Path (Split-Path -Parent $MyInvocation.MyCommand.Path) 'mpx.exe'))) {
			if (Test-Path $candidate) { $Src = (Resolve-Path $candidate).Path; break }
		}
		if ($Src) { Write-Host "==> 用手边的二进制 $Src" }
	}

	# ---- 2. 下载发布件 ------------------------------------------------------
	if (-not $Src) {
		if ($Version -eq 'latest') {
			$Url = "$DownloadBase/latest/download/$Archive"
		} else {
			$Url = "$DownloadBase/download/$Version/$Archive"
		}
		Write-Host "==> 下载 $Url"
		$zip = Join-Path $Tmp $Archive
		try {
			Invoke-WebRequest -Uri $Url -OutFile $zip -UseBasicParsing
			# 核对 SHA256SUMS。PowerShell 没有现成的 ed25519,这里只验哈希;要验签名请在
			# 有 OpenSSL 3 的机器上用 install-cli.sh。早于签名发布的版本没有 SHA256SUMS,提示后放行。
			$sums = $null
			try { $sums = (Invoke-WebRequest -Uri ($Url.Substring(0, $Url.LastIndexOf('/')) + '/SHA256SUMS') -UseBasicParsing).Content } catch { }
			if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
			if (-not $sums) {
				Write-Note '这个版本发布时还没有 SHA256SUMS(早于签名发布),没法校验,只靠 HTTPS'
			} else {
				$line = ($sums -split "`n") | Where-Object { ($_ -split '\s+')[1] -in @($Archive, "*$Archive") } | Select-Object -First 1
				if (-not $line) { Die "SHA256SUMS 里没有 $Archive,拒绝安装" }
				$want = ($line -split '\s+')[0].ToLower()
				$got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLower()
				if ($got -ne $want) { Die "$Archive 的 sha256 与 SHA256SUMS 不符,拒绝安装" }
				Write-Host '==> sha256 ✓(PowerShell 不验签名)'
			}
			Expand-Archive -Path $zip -DestinationPath (Join-Path $Tmp 'extract') -Force
			$found = Get-ChildItem -Path (Join-Path $Tmp 'extract') -Filter 'mpx.exe' -Recurse |
				Select-Object -First 1
			if ($found) { $Src = $found.FullName }
			else { Write-Note '发布件里没有 mpx.exe' }
		} catch {
			Write-Note '这个版本没有发布件,换下一条路'
		}
	}

	# ---- 3. 从源码编 --------------------------------------------------------
	if (-not $Src) {
		if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
			Die '三条路都没走通。装上 Go(https://go.dev/dl/),或者把 mpx.exe 放到 .\ 再跑一次。'
		}
		$root = $RepoRoot
		if (-not $root) {
			if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
				Die '本地没有源码,也没有 git。把仓库 clone 下来再跑这个脚本。'
			}
			Write-Host "==> 本地没有源码,clone 一份 $Repo"
			$root = Join-Path $Tmp 'src'
			& git clone --depth 1 --branch $Ref "https://github.com/$Repo.git" $root 2>&1 | Out-Null
			if ($LASTEXITCODE -ne 0) { Die "clone 不下来(仓库还没公开?)。" }
		}
		Write-Host '==> 用本机的 Go 编译(这要一会儿)'
		$built = Join-Path $Tmp 'mpx-built.exe'
		Push-Location $root
		try {
			& go build -trimpath -o $built ./cmd/mpx
			if ($LASTEXITCODE -ne 0) { Die '编译失败。' }
		} finally {
			Pop-Location
		}
		$Src = $built
		Write-Host '==> 用刚编出来的'
	}

	# ---- 装上去并确认它真的能跑 ---------------------------------------------
	# 先落到 .new 试跑一次:半路失败的话,原来那个还能用。
	if (-not (Test-Path $Prefix)) { New-Item -ItemType Directory -Path $Prefix -Force | Out-Null }
	$new = "$Dest.new"
	Copy-Item $Src $new -Force
	try {
		& $new --help 2>&1 | Out-Null
		if ($LASTEXITCODE -ne 0) { throw "exit $LASTEXITCODE" }
	} catch {
		Remove-Item $new -Force -ErrorAction SilentlyContinue
		Die '这个二进制在本机跑不起来(--help 就失败了),已放弃替换。'
	}
	Move-Item $new $Dest -Force

	Write-Host ''
	Write-Host "装好了:$Dest"

	# ---- 把安装目录加进用户 PATH --------------------------------------------
	$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
	if (-not $userPath) { $userPath = '' }
	$parts = $userPath -split ';' | Where-Object { $_ }
	if ($parts -notcontains $Prefix) {
		$joined = (@($parts) + $Prefix) -join ';'
		[Environment]::SetEnvironmentVariable('Path', $joined, 'User')
		Write-Host ''
		Write-Host "已把 $Prefix 加进你的 PATH。重开一个终端,新的 PATH 才生效。"
	}

	Write-Host @'

接下来接上控制面:

  mpx login --server http://127.0.0.1:8080 --token '<管理 token>'

控制面只监听回环,公网那台机器上先拉一条隧道:

  ssh -N -L 8080:127.0.0.1:8080 root@<控制面地址>

'@
} finally {
	Remove-Item $Tmp -Recurse -Force -ErrorAction SilentlyContinue
}
