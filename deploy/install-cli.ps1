[CmdletBinding()]
param(
	[string]$From = '',
	[string]$Version = 'latest',
	[string]$Prefix = '',
	[switch]$Uninstall
)

# T decodes \uXXXX escapes so this file stays pure ASCII without a BOM: 5.1 -File
# misreads a BOM-less UTF-8 file as ANSI, and a BOM breaks irm | iex.
function T([string]$s) { [regex]::Unescape($s) }

$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 still defaults to TLS 1.0, and GitHub refuses those connections.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo = if ($env:MPX_REPO) { $env:MPX_REPO } else { 'RovenEverward/my-proxy' }
$Ref = if ($env:MPX_REF) { $env:MPX_REF } else { 'main' }
$DownloadBase = if ($env:MPX_DOWNLOAD_BASE) { $env:MPX_DOWNLOAD_BASE } else { "https://github.com/$Repo/releases" }

if (-not $Prefix) {
	$Prefix = if ($env:MPX_PREFIX) { $env:MPX_PREFIX } else { Join-Path $env:LOCALAPPDATA 'Programs\mpx' }
}

function Write-Note($msg) { Write-Host "  $msg" }
function Die($msg) { Write-Host "$(T '\u9519\u8bef:')$msg" -ForegroundColor Red; exit 1 }

$Dest = Join-Path $Prefix 'mpx.exe'

if ($Uninstall) {
	if (Test-Path $Dest) {
		Remove-Item $Dest -Force
		Write-Host "$(T '\u5df2\u5220\u9664') $Dest"
	} else {
		Write-Host "$Dest $(T '\u672c\u6765\u5c31\u4e0d\u5728')"
	}
	exit 0
}

# ---- Which build does this machine need? ------------------------------------

switch ($env:PROCESSOR_ARCHITECTURE) {
	'AMD64' { $Arch = 'amd64' }
	'ARM64' { $Arch = 'arm64' }
	'x86' {
		# 32-bit PowerShell on a 64-bit system reports x86; check the real architecture.
		if ($env:PROCESSOR_ARCHITEW6432 -eq 'AMD64') { $Arch = 'amd64' }
		elseif ($env:PROCESSOR_ARCHITEW6432 -eq 'ARM64') { $Arch = 'arm64' }
		else { Die "$(T '\u4e0d\u652f\u6301\u7684\u67b6\u6784:')$($env:PROCESSOR_ARCHITECTURE)" }
	}
	default { Die "$(T '\u4e0d\u652f\u6301\u7684\u67b6\u6784:')$($env:PROCESSOR_ARCHITECTURE)" }
}

$Archive = "mpx_windows_$Arch.zip"

# Inside a repo? Walk up until go.mod is found.
$RepoRoot = ''
# Under irm | iex there is no script file: MyCommand.Path is empty and Split-Path would fail.
$SelfPath = $MyInvocation.MyCommand.Path
$dir = if ($SelfPath) { Split-Path -Parent $SelfPath } else { '' }
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
	# ---- 1. A binary you already have ------------------------------------------
	if ($From) {
		if (-not (Test-Path $From)) { Die "-From $(T '\u6307\u7684\u6587\u4ef6\u4e0d\u5b58\u5728:')$From" }
		$Src = (Resolve-Path $From).Path
		Write-Host "$(T '==> \u7528\u624b\u8fb9\u7684\u4e8c\u8fdb\u5236') $Src"
	} else {
		foreach ($candidate in @('.\mpx.exe') + $(if ($SelfPath) { @(Join-Path (Split-Path -Parent $SelfPath) 'mpx.exe') } else { @() })) {
			if (Test-Path $candidate) { $Src = (Resolve-Path $candidate).Path; break }
		}
		if ($Src) { Write-Host "$(T '==> \u7528\u624b\u8fb9\u7684\u4e8c\u8fdb\u5236') $Src" }
	}

	# ---- 2. Download a release artifact ----------------------------------------
	if (-not $Src) {
		if ($Version -eq 'latest') {
			$Url = "$DownloadBase/latest/download/$Archive"
		} else {
			$Url = "$DownloadBase/download/$Version/$Archive"
		}
		Write-Host "$(T '==> \u4e0b\u8f7d') $Url"
		$zip = Join-Path $Tmp $Archive
		try {
			Invoke-WebRequest -Uri $Url -OutFile $zip -UseBasicParsing
			# Verify SHA256SUMS from the same release (no signing; see docs/internal/update-rfc.md).
			# Releases older than SHA256SUMS have no manifest: warn, then install anyway.
			$sums = $null
			try { $sums = (Invoke-WebRequest -Uri ($Url.Substring(0, $Url.LastIndexOf('/')) + '/SHA256SUMS') -UseBasicParsing).Content } catch { }
			if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
			if (-not $sums) {
				Write-Note "$(T '\u8fd9\u4e2a\u7248\u672c\u53d1\u5e03\u65f6\u8fd8\u6ca1\u6709 SHA256SUMS,\u6ca1\u6cd5\u6821\u9a8c,\u7167\u5e38\u5b89\u88c5')"
			} else {
				$line = ($sums -split "`n") | Where-Object { ($_ -split '\s+')[1] -in @($Archive, "*$Archive") } | Select-Object -First 1
				if (-not $line) { Die "SHA256SUMS $(T '\u91cc\u6ca1\u6709') $Archive$(T ',\u62d2\u7edd\u5b89\u88c5')" }
				$want = ($line -split '\s+')[0].ToLower()
				$got = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLower()
				if ($got -ne $want) { Die "$Archive$(T ' \u7684 sha256 \u4e0e SHA256SUMS \u4e0d\u7b26,\u62d2\u7edd\u5b89\u88c5(\u4e0b\u8f7d\u635f\u574f\u6216\u88ab\u66ff\u6362)')" }
				Write-Host "$(T '==> sha256 \u2713')"
			}
			Expand-Archive -Path $zip -DestinationPath (Join-Path $Tmp 'extract') -Force
			$found = Get-ChildItem -Path (Join-Path $Tmp 'extract') -Filter 'mpx.exe' -Recurse |
				Select-Object -First 1
			if ($found) { $Src = $found.FullName }
			else { Write-Note "$(T '\u53d1\u5e03\u4ef6\u91cc\u6ca1\u6709 mpx.exe')" }
		} catch {
			Write-Note "$(T '\u8fd9\u4e2a\u7248\u672c\u6ca1\u6709\u53d1\u5e03\u4ef6,\u6362\u4e0b\u4e00\u6761\u8def')"
		}
	}

	# ---- 3. Build from source --------------------------------------------------
	if (-not $Src) {
		if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
			Die "$(T '\u4e09\u6761\u8def\u90fd\u6ca1\u8d70\u901a\u3002\u88c5\u4e0a Go(https://go.dev/dl/),\u6216\u8005\u628a mpx.exe \u653e\u5230 .\\ \u518d\u8dd1\u4e00\u6b21\u3002')"
		}
		$root = $RepoRoot
		if (-not $root) {
			if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
				Die "$(T '\u672c\u5730\u6ca1\u6709\u6e90\u7801,\u4e5f\u6ca1\u6709 git\u3002\u628a\u4ed3\u5e93 clone \u4e0b\u6765\u518d\u8dd1\u8fd9\u4e2a\u811a\u672c\u3002')"
			}
			Write-Host "$(T '==> \u672c\u5730\u6ca1\u6709\u6e90\u7801,clone \u4e00\u4efd') $Repo"
			$root = Join-Path $Tmp 'src'
			& git clone --depth 1 --branch $Ref "https://github.com/$Repo.git" $root 2>&1 | Out-Null
			if ($LASTEXITCODE -ne 0) { Die "$(T 'clone \u4e0d\u4e0b\u6765(\u4ed3\u5e93\u8fd8\u6ca1\u516c\u5f00?)')$(T '\u3002')" }
		}
		Write-Host "$(T '==> \u7528\u672c\u673a\u7684 Go \u7f16\u8bd1(\u8fd9\u8981\u4e00\u4f1a\u513f)')"
		$built = Join-Path $Tmp 'mpx-built.exe'
		Push-Location $root
		try {
			& go build -trimpath -o $built ./cmd/mpx
			if ($LASTEXITCODE -ne 0) { Die "$(T '\u7f16\u8bd1\u5931\u8d25\u3002')" }
		} finally {
			Pop-Location
		}
		$Src = $built
		Write-Host "$(T '==> \u7528\u521a\u7f16\u51fa\u6765\u7684')"
	}

	# ---- Install it and prove it runs ------------------------------------------
	# Land in a .new file and trial-run it first: if that fails, the old one still works.
	if (-not (Test-Path $Prefix)) { New-Item -ItemType Directory -Path $Prefix -Force | Out-Null }
	# The temp file must end in .exe: Windows only executes PATHEXT extensions, so
	# a file named mpx.exe.new would always fail the trial run below.
	$new = Join-Path $Prefix 'mpx.new.exe'
	Copy-Item $Src $new -Force
	try {
		& $new --help 2>&1 | Out-Null
		if ($LASTEXITCODE -ne 0) { throw "exit $LASTEXITCODE" }
	} catch {
		Remove-Item $new -Force -ErrorAction SilentlyContinue
		Die "$(T '\u8fd9\u4e2a\u4e8c\u8fdb\u5236\u5728\u672c\u673a\u8dd1\u4e0d\u8d77\u6765(--help \u5c31\u5931\u8d25\u4e86),\u5df2\u653e\u5f03\u66ff\u6362\u3002')"
	}
	Move-Item $new $Dest -Force

	Write-Host ''
	Write-Host "$(T '\u88c5\u597d\u4e86:')$Dest"

	# ---- Add the install dir to the user PATH ----------------------------------
	# Read and write HKCU\Environment\Path literally: [Environment]::Get/SetEnvironmentVariable
	# expands %VAR%, turns REG_EXPAND_SZ into REG_SZ and drops empty entries. Here we read
	# the raw value, write it back with the same kind and only append one entry; the old
	# value is backed up to %LOCALAPPDATA%\mpx\path-backup; if it cannot be read, nothing
	# is written and we say to add it by hand.
	$pathDone = $false
	try {
		$envKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
		$names = @($envKey.GetValueNames())
		if ($names -contains 'Path') {
			$userPath = [string]$envKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
			$kind = $envKey.GetValueKind('Path')
		} else {
			$userPath = ''
			$kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
		}
		if ($kind -ne [Microsoft.Win32.RegistryValueKind]::String -and $kind -ne [Microsoft.Win32.RegistryValueKind]::ExpandString) { throw "$(T '\u7528\u6237 PATH \u7684\u7c7b\u578b\u662f ')$kind" }
		$norm = { param($p) $p.Trim().Trim('"').TrimEnd('\', '/') }
		$want = & $norm $Prefix
		$has = @($userPath -split ';' | Where-Object { $_ -and ((& $norm $_) -ieq $want) }).Count -gt 0
		if (-not $has) {
			$bdir = Join-Path $env:LOCALAPPDATA 'mpx\path-backup'
			New-Item -ItemType Directory -Path $bdir -Force | Out-Null
			[IO.File]::WriteAllText((Join-Path $bdir ('path-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.txt')), $userPath)
			$joined = if ($userPath -eq '') { $Prefix } elseif ($userPath.EndsWith(';')) { $userPath + $Prefix } else { $userPath + ';' + $Prefix }
			$envKey.SetValue('Path', $joined, $kind)
			$check = [string]$envKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
			if ($check -ne $joined) { $envKey.SetValue('Path', $userPath, $kind); throw "$(T '\u5199\u5b8c\u8bfb\u56de\u6765\u7684 PATH \u4e0d\u5bf9,\u5df2\u6062\u590d\u539f\u503c')" }
			# Broadcast WM_SETTINGCHANGE: set a temporary user variable and delete it; .NET broadcasts.
			[Environment]::SetEnvironmentVariable('MPX_PATH_REFRESH', '1', 'User')
			[Environment]::SetEnvironmentVariable('MPX_PATH_REFRESH', $null, 'User')
			Write-Host ''
			Write-Host "$(T '\u5df2\u628a') $Prefix $(T '\u52a0\u8fdb\u4f60\u7684 PATH(\u539f\u503c\u5907\u4efd\u5728') $bdir$(T ')\u3002\u91cd\u5f00\u4e00\u4e2a\u7ec8\u7aef,\u65b0\u7684 PATH \u624d\u751f\u6548\u3002')"
		}
		$envKey.Close()
		$pathDone = $true
	} catch {
		Write-Warning "$(T '\u6ca1\u80fd\u628a') $Prefix $(T '\u52a0\u8fdb\u7528\u6237 PATH,\u4f60\u7684 PATH \u6ca1\u6709\u88ab\u6539\u52a8:')$($_.Exception.Message)$(T '\u3002\u8bf7\u624b\u52a8\u628a\u5b83\u52a0\u8fdb\u7528\u6237\u73af\u5883\u53d8\u91cf Path\u3002')"
	}

	Write-Host "$(T @'

\u63a5\u4e0b\u6765\u63a5\u4e0a\u63a7\u5236\u9762:

  mpx login --server http://127.0.0.1:8080 --token '<\u7ba1\u7406 token>'

\u63a7\u5236\u9762\u53ea\u76d1\u542c\u56de\u73af,\u516c\u7f51\u90a3\u53f0\u673a\u5668\u4e0a\u5148\u62c9\u4e00\u6761\u96a7\u9053:

  ssh -N -L 8080:127.0.0.1:8080 root@<\u63a7\u5236\u9762\u5730\u5740>

'@)"
} finally {
	Remove-Item $Tmp -Recurse -Force -ErrorAction SilentlyContinue
}
