#!/usr/bin/env bash
#
# 在控制面这台机器上安装或更新 mpx。装和更新是同一条命令。
#
# 每次都是「换二进制 → 补齐用户和目录 → 备好 token 与 SSH 密钥 → 写单元 → 重启」,
# 已经存在的东西(token、数据库、密钥)一律不动,所以重复跑是安全的。
#
# 这个脚本只管控制面。节点上装 xray 是 `mpx node install` 的事(由控制面自己 SSH
# 过去做);操作者本机的命令行用 deploy/install-cli.sh(或 Windows 上的
# deploy/install-cli.ps1)。
#
# 用法:
#   sudo ./deploy/install.sh [mpx 二进制的路径]
#   sudo ./deploy/install.sh --version v0.2.0
#
# 也可以不下载仓库、直接一行装(脚本会自己去取二进制和 systemd 单元):
#   curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install.sh | sudo bash
#
# 二进制按顺序找:参数给的那个 → 脚本旁边的 mpx → 当前目录的 mpx → /tmp/mpx
#               → GitHub Releases 的 linux 发布件 → 用本机 Go 从源码编。

set -euo pipefail

# 被 `curl | sudo bash` 这么跑的时候 BASH_SOURCE 是空的,退回当前目录。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || pwd)"

MPX_REPO="${MPX_REPO:-RovenEverward/my-proxy}"
MPX_REF="${MPX_REF:-main}"
# 想走自建镜像就把这个换成 https://<你的域名>/mpx
DOWNLOAD_BASE="${MPX_DOWNLOAD_BASE:-https://github.com/$MPX_REPO/releases}"
RAW_BASE="${MPX_RAW_BASE:-https://raw.githubusercontent.com/$MPX_REPO/$MPX_REF}"

die() {
	printf '错误:%s\n' "$1" >&2
	exit 1
}

note() {
	printf '    %s\n' "$1"
}

usage() {
	cat <<'EOF'
在控制面这台机器上安装或更新 mpx。

用法:
  sudo ./deploy/install.sh [mpx 二进制的路径]
  sudo ./deploy/install.sh --version v0.2.0

二进制按顺序找:参数 → 脚本旁边 → ./mpx → /tmp/mpx → GitHub Releases → 本机 Go 编译。
EOF
	exit 0
}

# ---- 参数 -------------------------------------------------------------------

BIN_ARG=
VERSION=latest

while [[ $# -gt 0 ]]; do
	case "$1" in
		--version) VERSION="${2:-}"; shift 2 ;;
		-h|--help) usage ;;
		-*) die "不认识的参数:$1(试试 --help)" ;;
		*) BIN_ARG="$1"; shift ;;
	esac
done

[[ ${EUID} -eq 0 ]] || die "要 root 才能装服务:sudo $0 $*"

# ---- 这个系统要哪一份 -------------------------------------------------------

case "$(uname -s)" in
	Linux) OS=linux ;;
	*) die "控制面要在 Linux 上跑(这个系统是 $(uname -s))。装 CLI 请用 deploy/install-cli.sh。" ;;
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

# 从源码编。用 sudo 跑的时候本机 Go 多半装在那个普通用户名下,root 的 PATH 里没有,
# 所以退回去借他的环境跑一次。
build_binary() {
	local root="$1" out="$2"
	if command -v go >/dev/null; then
		( cd "$root" && go build -trimpath -o "$out" ./cmd/mpx )
	elif [[ -n "${SUDO_USER:-}" ]] && sudo -u "$SUDO_USER" bash -lc 'command -v go' >/dev/null 2>&1; then
		note "root 的 PATH 里没有 go,借 $SUDO_USER 的编"
		sudo -u "$SUDO_USER" bash -lc "cd '$root' && go build -trimpath -o '$out' ./cmd/mpx"
	else
		return 1
	fi
}

# ---- 二进制 -----------------------------------------------------------------

BIN="$BIN_ARG"
if [[ -z "$BIN" ]]; then
	for candidate in "$SCRIPT_DIR/mpx" "./mpx" "/tmp/mpx"; do
		if [[ -f "$candidate" ]]; then
			BIN="$candidate"
			break
		fi
	done
fi

if [[ -n "$BIN" ]]; then
	[[ -f "$BIN" ]] || die "找不到要装的 mpx 二进制:$BIN"
	echo "==> 二进制 $BIN"
else
	# 手边没有,那就自己去找一个。
	if [[ -n "$FETCH" ]]; then
		if [[ "$VERSION" == latest ]]; then
			URL="$DOWNLOAD_BASE/latest/download/$ARCHIVE"
		else
			URL="$DOWNLOAD_BASE/download/$VERSION/$ARCHIVE"
		fi
		echo "==> 下载 $URL"
		if $FETCH "$URL" > "$TMP/$ARCHIVE" 2>/dev/null && tar -xzf "$TMP/$ARCHIVE" -C "$TMP" 2>/dev/null; then
			BIN="$(find "$TMP" -maxdepth 2 -type f -name mpx -print -quit)"
			[[ -n "$BIN" ]] || note "发布件里没有 mpx"
		else
			note "这个版本没有发布件,换下一条路"
		fi
	fi

	if [[ -z "$BIN" ]]; then
		root="$REPO_ROOT"
		if [[ -z "$root" ]]; then
			command -v git >/dev/null || die "手边没有二进制,也没有 git。把二进制放到 $SCRIPT_DIR/mpx,或者把路径当参数传进来。"
			echo "==> 本地没有源码,clone 一份 $MPX_REPO"
			if git clone --depth 1 --branch "$MPX_REF" "https://github.com/$MPX_REPO.git" "$TMP/src" >/dev/null 2>&1; then
				root="$TMP/src"
			else
				die "clone 不下来(仓库还没公开?)。在开发机上编一个传上来:GOOS=$OS GOARCH=$ARCH go build -trimpath -o /tmp/mpx ./cmd/mpx"
			fi
		fi
		echo "==> 用本机的 Go 编译(这要一会儿)"
		build_binary "$root" "$TMP/mpx-built" \
			|| die "编译失败。装一个 Go,或者把编好的二进制传到这台机器上再跑一次。"
		BIN="$TMP/mpx-built"
	fi
fi

# 先落到 .new 并试跑一次:更新时如果换上一个跑不起来的二进制(多半是交叉编译时
# GOOS/GOARCH 填错了),这台机器就没有控制面了,而数据库和节点都还在等它。
# 直接改名覆盖正在运行的文件也是可以的:老的 inode 会活到进程退出。
echo "==> 装到 /usr/local/bin/mpx"
install -m 0755 "$BIN" /usr/local/bin/mpx.new
if ! /usr/local/bin/mpx.new --help >/dev/null 2>&1; then
	rm -f /usr/local/bin/mpx.new
	die "这个二进制在本机跑不起来(--help 就失败了),已放弃替换。检查编译时的 GOOS/GOARCH。"
fi
mv -f /usr/local/bin/mpx.new /usr/local/bin/mpx

# ---- 用户与目录 -------------------------------------------------------------

echo "==> mpx 用户与目录"
if ! getent group mpx >/dev/null; then
	groupadd --system mpx
fi
NOLOGIN=/usr/sbin/nologin
[[ -x "$NOLOGIN" ]] || NOLOGIN=/sbin/nologin
if ! getent passwd mpx >/dev/null; then
	# home 必须是 /var/lib/mpx:控制面进程要去那里读 .ssh —— 能 SSH 到节点的是它,
	# 不是操作者手边那台笔记本。
	useradd --system --gid mpx --home-dir /var/lib/mpx --no-create-home --shell "$NOLOGIN" mpx
fi
install -d -m 0755 -o mpx -g mpx /var/lib/mpx
install -d -m 0755 -o root -g mpx /etc/mpx

# ---- 管理 token -------------------------------------------------------------

echo "==> 管理 token"
FRESH_TOKEN=
if [[ -f /etc/mpx/env ]]; then
	# 不重新生成:换了 token,所有已经登录的 CLI 都要重新登录一遍。
	echo "    /etc/mpx/env 已存在,不动它"
else
	FRESH_TOKEN="$(head -c 32 /dev/urandom | base64 | tr -d '\n=')"
	( umask 077; printf 'MPX_TOKEN=%s\n' "$FRESH_TOKEN" > /etc/mpx/env )
	echo "    生成了 /etc/mpx/env"
fi
chown mpx:mpx /etc/mpx/env
chmod 0600 /etc/mpx/env

# ---- 到节点去的 SSH 密钥 ----------------------------------------------------

echo "==> 到节点去的 SSH 密钥"
KEY=/var/lib/mpx/.ssh/id_ed25519
if [[ -f "$KEY" ]]; then
	echo "    已有 $KEY,不动它"
else
	install -d -m 0700 -o mpx -g mpx /var/lib/mpx/.ssh
	ssh-keygen -t ed25519 -N '' -C 'mpx 控制面' -f "$KEY" >/dev/null
	chown mpx:mpx "$KEY" "$KEY.pub"
	chmod 0600 "$KEY"
	chmod 0644 "$KEY.pub"
	echo "    生成了 $KEY"
fi

# ---- systemd 单元 -----------------------------------------------------------

echo "==> systemd 单元"
SRC="$SCRIPT_DIR/mpx.service"
if [[ ! -f "$SRC" ]]; then
	# `curl | sudo bash` 跑起来的时候脚本旁边什么都没有,去仓库里取一份。
	[[ -n "$FETCH" ]] || die "找不到单元文件 $SRC(它和这个脚本放在一起)"
	$FETCH "$RAW_BASE/deploy/mpx.service" > "$TMP/mpx.service" 2>/dev/null \
		|| die "取不到 systemd 单元文件。把 deploy/ 整个目录传上来再跑。"
	SRC="$TMP/mpx.service"
fi
UNIT=/etc/systemd/system/mpx.service
if [[ -f "$UNIT" ]] && ! cmp -s "$SRC" "$UNIT"; then
	# 单元文件跟着程序走,所以每次都以手上这份为准。但你改过的内容不能就这么没了。
	cp -f "$UNIT" "$UNIT.bak"
	echo "    $UNIT 与手上这份不同,原文件备份到 $UNIT.bak"
fi
install -m 0644 "$SRC" "$UNIT"
systemctl daemon-reload
systemctl enable mpx >/dev/null

# ---- 起服务并确认它真的在跑 -------------------------------------------------

echo "==> 重启 mpx"
systemctl restart mpx
sleep 1
if ! systemctl is-active --quiet mpx; then
	systemctl --no-pager -l status mpx || true
	journalctl -u mpx -n 20 --no-pager || true
	die "mpx 没起来,上面是它的状态和最近的日志"
fi

cat <<EOF

装好了。控制面在 127.0.0.1:8080,只监听回环 —— 公网要用它,前面得有个 TLS
反向代理,或者从本机拉一条隧道:

  ssh -N -L 8080:127.0.0.1:8080 root@<这台机器的地址>

然后在操作者机器上装好命令行(deploy/install-cli.sh,Windows 用
deploy/install-cli.ps1),再接上它:

  mpx login --server http://127.0.0.1:8080 --token '<管理 token>'

EOF

if [[ -n "$FRESH_TOKEN" ]]; then
	echo "这次生成的管理 token(也存好了,以后不用再记):"
	echo "  $FRESH_TOKEN"
else
	echo "管理 token 还是原来那个,取出来用:"
	echo "  sudo sed -n 's/^MPX_TOKEN=//p' /etc/mpx/env"
fi

cat <<EOF

接下来加节点,再在节点上把核心装上 —— 节点只需要 sshd 和 systemd,不用能连
GitHub,也不用装 xray:

  mpx node add hk --address <节点地址>
  mpx node install <节点 id>

控制面要 SSH 进节点,先把下面这行公钥追到每个节点的 authorized_keys 里
(默认以 root 登录;节点上用的是别的用户就填那个用户的):

  $(cat "$KEY.pub")

EOF
