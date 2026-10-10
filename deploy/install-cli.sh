#!/usr/bin/env bash
#
# 在操作机(macOS / Linux)上装一个 mpx 命令行。
#
# 装和升级是同一条命令,重复跑是安全的。二进制从哪儿来,按下面的顺序试,第一条走通
# 就停:
#
#   1. --from 指给你的文件,或者仓库里已经编好的 ./mpx
#   2. 从 GitHub Releases 下载你这个系统对应的发布件
#   3. 用本机的 Go 从源码编(在仓库里跑,或者先 git clone 一份)
#
# 用法:
#   ./deploy/install-cli.sh                     # 自动挑一条走得通的路
#   ./deploy/install-cli.sh --from /tmp/mpx     # 装手边这个二进制
#   ./deploy/install-cli.sh --version v0.2.0    # 装指定发布版本(默认 latest)
#   ./deploy/install-cli.sh --prefix /usr/local/bin
#   ./deploy/install-cli.sh --uninstall
#
# 远程一行装(仓库发布之后):
#   curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install-cli.sh | bash

set -euo pipefail

# 被 `curl | bash` 这么跑的时候 BASH_SOURCE 是空的,退回当前目录。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || pwd)"

MPX_REPO="${MPX_REPO:-RovenEverward/my-proxy}"
MPX_REF="${MPX_REF:-main}"
# 想走自建镜像就把这个换成 https://<你的域名>/mpx
DOWNLOAD_BASE="${MPX_DOWNLOAD_BASE:-https://github.com/$MPX_REPO/releases}"

die() {
	printf '错误:%s\n' "$1" >&2
	exit 1
}

note() {
	printf '  %s\n' "$1"
}

usage() {
	cat <<'EOF'
在操作机(macOS / Linux)上装一个 mpx 命令行。

用法:
  ./deploy/install-cli.sh                     # 自动挑一条走得通的路
  ./deploy/install-cli.sh --from /tmp/mpx     # 装手边这个二进制
  ./deploy/install-cli.sh --version v0.2.0    # 装指定发布版本(默认 latest)
  ./deploy/install-cli.sh --prefix /usr/local/bin
  ./deploy/install-cli.sh --uninstall

二进制按顺序找:--from 或仓库里的 ./mpx → GitHub Releases → 用本机 Go 从源码编。
EOF
	exit 0
}

# ---- 参数 -------------------------------------------------------------------

FROM=
VERSION=latest
PREFIX="${MPX_PREFIX:-$HOME/.local/bin}"
UNINSTALL=

while [[ $# -gt 0 ]]; do
	case "$1" in
		--from) FROM="${2:-}"; shift 2 ;;
		--version) VERSION="${2:-}"; shift 2 ;;
		--prefix) PREFIX="${2:-}"; shift 2 ;;
		--uninstall) UNINSTALL=1; shift ;;
		-h|--help) usage ;;
		-*) die "不认识的参数:$1(试试 --help)" ;;
		*) FROM="$1"; shift ;;
	esac
done

DEST="$PREFIX/mpx"

if [[ -n "$UNINSTALL" ]]; then
	if [[ -f "$DEST" ]]; then
		rm -f "$DEST"
		echo "已删除 $DEST"
	else
		echo "$DEST 本来就不在"
	fi
	exit 0
fi

# ---- 这个系统要哪一份 -------------------------------------------------------

case "$(uname -s)" in
	Darwin) OS=darwin ;;
	Linux) OS=linux ;;
	MINGW*|MSYS*|CYGWIN*)
		die "这是 Windows 的 shell。用 PowerShell 那个:deploy/install-cli.ps1" ;;
	*) die "不认识的系统:$(uname -s)" ;;
esac

case "$(uname -m)" in
	x86_64|amd64) ARCH=amd64 ;;
	arm64|aarch64) ARCH=arm64 ;;
	*) die "不认识的架构:$(uname -m)" ;;
esac

ARCHIVE="mpx_${OS}_${ARCH}.tar.gz"

if command -v curl >/dev/null; then
	FETCH="curl -fsSL"
elif command -v wget >/dev/null; then
	FETCH="wget -qO-"
else
	FETCH=
fi

# ---- 三条来路 ---------------------------------------------------------------

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# 在仓库里吗?往上找 go.mod。
REPO_ROOT=
dir="$SCRIPT_DIR"
while [[ "$dir" != / ]]; do
	if [[ -f "$dir/go.mod" && -f "$dir/cmd/mpx/main.go" ]]; then
		REPO_ROOT="$dir"
		break
	fi
	dir="$(dirname "$dir")"
done

try_local() {
	# 不用数组:macOS 自带 bash 3.2,`"${arr[@]}"` 在 set -u 下会报 unbound variable。
	if [[ -n "$FROM" ]]; then
		[[ -f "$FROM" ]] || die "--from 指的文件不存在:$FROM"
		SRC="$FROM"
		return 0
	fi
	for candidate in "$SCRIPT_DIR/mpx" "./mpx" "/tmp/mpx"; do
		if [[ -f "$candidate" ]]; then
			SRC="$candidate"
			return 0
		fi
	done
	return 1
}

try_download() {
	[[ -n "$FETCH" ]] || return 1
	local url
	if [[ "$VERSION" == latest ]]; then
		url="$DOWNLOAD_BASE/latest/download/$ARCHIVE"
	else
		url="$DOWNLOAD_BASE/download/$VERSION/$ARCHIVE"
	fi
	echo "==> 下载 $url"
	# 没发布过就是 404,这一层安安静静让给下一层,别拿 curl 的报错吓人。
	if ! $FETCH "$url" > "$TMP/$ARCHIVE" 2>/dev/null; then
		note "这个版本没有发布件,换下一条路"
		return 1
	fi
	mkdir -p "$TMP/extract"
	# 用 -print -quit 而不是 `| head -1`:head 提前退出会给 find 一个 SIGPIPE,
	# pipefail 下整个管道就成了失败。
	if ! tar -xzf "$TMP/$ARCHIVE" -C "$TMP/extract"; then
		note "发布件解不开"
		return 1
	fi
	local found
	found="$(find "$TMP/extract" -type f -name mpx -print -quit)"
	[[ -n "$found" ]] || { note "发布件里没有 mpx"; return 1; }
	SRC="$found"
	return 0
}

try_build() {
	command -v go >/dev/null || return 1
	local root="$REPO_ROOT"
	if [[ -z "$root" ]]; then
		command -v git >/dev/null || return 1
		echo "==> 本地没有源码,clone 一份 $MPX_REPO"
		if ! git clone --depth 1 --branch "$MPX_REF" \
			"https://github.com/$MPX_REPO.git" "$TMP/src" >/dev/null 2>&1; then
			note "clone 不下来(仓库还没公开?)"
			return 1
		fi
		root="$TMP/src"
	fi
	echo "==> 用本机的 Go 编译(这要一会儿)"
	if ! ( cd "$root" && go build -trimpath -o "$TMP/mpx-built" ./cmd/mpx ); then
		note "编译失败"
		return 1
	fi
	SRC="$TMP/mpx-built"
	return 0
}

SRC=
if try_local; then
	echo "==> 用手边的二进制 $SRC"
elif try_download; then
	echo "==> 用下载下来的发布件"
elif try_build; then
	echo "==> 用刚编出来的"
else
	die "三条路都没走通。装上 Go(https://go.dev/dl/),或者把二进制放到 ./mpx 再跑一次。"
fi

# ---- 装上去并确认它真的能跑 -------------------------------------------------

# 先落到 .new 试跑一次:半路失败的话,原来那个还能用。
mkdir -p "$PREFIX"
install -m 0755 "$SRC" "$DEST.new"
if ! "$DEST.new" --help >/dev/null 2>&1; then
	rm -f "$DEST.new"
	die "这个二进制在 $(uname -s) 上跑不起来,已放弃替换。检查编译时的 GOOS/GOARCH。"
fi
mv -f "$DEST.new" "$DEST"

echo
echo "装好了:$DEST"

case ":$PATH:" in
	*":$PREFIX:"*) ;;
	*)
		echo
		echo "$PREFIX 不在 PATH 里,把它加进去(按你用的 shell 挑一行):"
		echo "  echo 'export PATH=\"$PREFIX:\$PATH\"' >> ~/.zshrc   # zsh"
		echo "  echo 'export PATH=\"$PREFIX:\$PATH\"' >> ~/.bashrc  # bash"
		echo "然后重开一个终端,或者 source 一下那个文件。"
		;;
esac

cat <<'EOF'

接下来接上控制面:

  mpx login --server http://127.0.0.1:8080 --token '<管理 token>'

控制面只监听回环,公网那台机器上先拉一条隧道:

  ssh -N -L 8080:127.0.0.1:8080 root@<控制面地址>

EOF
