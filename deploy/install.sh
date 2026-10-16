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
#   sudo ./deploy/install.sh --addr 0.0.0.0:8080
#
# 也可以不下载仓库、直接一行装(脚本会自己去取二进制和 systemd 单元):
#   curl -fsSL https://raw.githubusercontent.com/RovenEverward/my-proxy/main/deploy/install.sh | sudo bash
#
# 默认只监听 127.0.0.1:8080(订阅和管理接口都不出这台机器)。要让客户端直接从公网
# 取订阅就传 --addr 0.0.0.0:8080。同一个端口上订阅走 HTTP、管理接口走 HTTPS
# (控制面首次启动时自己生成自签证书,CLI 按证书指纹认它),不需要域名和反向
# 代理。`mpx init` 就是按这个口径装的。
#
# 二进制按顺序找:参数给的那个 → GitHub Releases 的发布件 → 用本机 Go 从源码编。
# 不再自己去捡脚本旁边、当前目录或 /tmp 下的 mpx:那里常躺着上一次 init 传上来的旧
# 二进制,v0.4.0 就因此在一台机器上装回了一个十天前的版本。要装本地文件就显式传路径。

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

# >>> verify_release(install.sh 与 install-cli.sh 里逐字相同,internal/release 的测试会比对)
sha256_of() {
	if command -v sha256sum >/dev/null; then sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null; then shasum -a 256 "$1" | cut -d' ' -f1
	else return 1; fi
}
# verify_release FILE NAME:按同一发布里的 SHA256SUMS 核对已下载的发布件 FILE。
# 不做签名(决定见 docs/internal/update-rfc.md):防的是下载损坏、截断和错包,来源靠 HTTPS + GitHub。
# 早于 SHA256SUMS 的旧版本没有清单:提示后照常安装。
# 有清单却对不上,一律 die,不退回别的安装途径。
verify_release() {
	local file="$1" name="$2" base sums="$TMP/SHA256SUMS" want got
	if [[ "$VERSION" == latest ]]; then base="$DOWNLOAD_BASE/latest/download"; else base="$DOWNLOAD_BASE/download/$VERSION"; fi
	if ! $FETCH "$base/SHA256SUMS" > "$sums" 2>/dev/null; then
		note "这个版本发布时还没有 SHA256SUMS,没法校验,照常安装"
		return 0
	fi
	want="$(awk -v n="$name" '$2==n || $2=="*"n {print $1; exit}' "$sums")"
	[[ -n "$want" ]] || die "SHA256SUMS 里没有 $name,拒绝安装"
	got="$(sha256_of "$file")" || die "本机没有 sha256sum 或 shasum,没法校验发布件"
	[[ "$got" == "$want" ]] || die "$name 的 sha256 与 SHA256SUMS 不符,拒绝安装(下载损坏或被替换)"
	echo "==> sha256 ✓"
}
# <<< verify_release

# >>> install_release_layout(install.sh 的测试会把这一段原样拿出来,对着临时目录跑)
# 版本目录 + 符号链接布局:
#
#   $LOCAL_RELEASES/<版本>/mpx      每一版一个目录
#   $LOCAL_BIN/mpx -> 上面某一版    符号链接,用 rename 原子切换
#
# 路径都从 MPX_PREFIX 拼出来:默认空,就是真实的 /usr/local;测试把它设成临时目录,
# 就能在不碰 /usr 的情况下验证迁移、切换和清理。控制面的 root 侧执行器
# (internal/selfupdate)按同一套布局找二进制。
PREFIX="${MPX_PREFIX:-}"
LOCAL_BIN="$PREFIX/usr/local/bin"
LOCAL_RELEASES="$PREFIX/usr/local/lib/mpx/releases"

# version_field_of BIN:`BIN --version` 输出的最后一个字段(发布件形如「mpx version v0.6.0」)。
# 跑不起来或没有输出时给空串,由调用方决定退回什么名字。
version_field_of() {
	local out
	out="$("$1" --version 2>/dev/null)" || return 0
	printf '%s' "$out" | awk '{print $NF}'
}

# prune_releases KEEP...:只保留列出的版本目录,多余的删掉。调用方负责把「当前版」
# 和「上一版」都列上;一个不剩时把 releases 清空也是允许的。
prune_releases() {
	local d name keep=("$@") found
	for d in "$LOCAL_RELEASES"/*/; do
		[[ -d "$d" ]] || continue
		d="${d%/}"
		name="$(basename "$d")"
		found=
		for keep_name in "${keep[@]}"; do
			if [[ "$name" == "$keep_name" ]]; then found=1; break; fi
		done
		if [[ -n "$found" ]]; then continue; fi
		rm -rf "$d"
	done
}

# newest_release KEEP:按修改时间返回除 KEEP 外最近动过的一个版本目录名(可能为空)。
# 版本号目录名的排序和「谁更新」无关(手动回退、重复装同一版都会打乱它),所以
# 挑选上一版时看的是目录的修改时间。
newest_release() {
	local keep="$1" d newest= name
	for d in "$LOCAL_RELEASES"/*/; do
		[[ -d "$d" ]] || continue
		d="${d%/}"
		name="$(basename "$d")"
		[[ "$name" == "$keep" ]] && continue
		if [[ -z "$newest" || "$d" -nt "$newest" ]]; then newest="$d"; fi
	done
	if [[ -n "$newest" ]]; then
		basename "$newest"
	fi
}

# install_release_link BIN:把 BIN 装进 releases/<版本>/mpx,再把 $LOCAL_BIN/mpx 这个
# 符号链接原子地指过去。重复运行安全,同一版本会直接覆盖同名文件。
install_release_link() {
	local bin="$1" new_version v prev link="$LOCAL_BIN/mpx" old_version
	install -d -m 0755 "$LOCAL_BIN"

	# 先落到 .new 并试跑一次:更新时如果换上一个跑不起来的二进制(多半是交叉编译时
	# GOOS/GOARCH 填错了),这台机器就没有控制面了,而数据库和节点都还在等它。
	# 直接改名覆盖正在运行的文件也是可以的:老的 inode 会活到进程退出。
	install -m 0755 "$bin" "$LOCAL_BIN/mpx.new"
	if ! "$LOCAL_BIN/mpx.new" --help >/dev/null 2>&1; then
		rm -f "$LOCAL_BIN/mpx.new"
		die "这个二进制在本机跑不起来(--help 就失败了),已放弃替换。检查编译时的 GOOS/GOARCH。"
	fi
	# 说清楚装的是哪个版本。连 --version 都不认的是很老的 mpx,多半是捡错了文件。
	if ! new_version="$("$LOCAL_BIN/mpx.new" --version 2>/dev/null)"; then
		rm -f "$LOCAL_BIN/mpx.new"
		die "这个二进制不认 --version,是很老的 mpx($bin),已放弃替换。"
	fi
	echo "    版本:$new_version"
	if [[ "$VERSION" != latest && "$new_version" != *"$VERSION"* && -z "$BIN_ARG" ]]; then
		rm -f "$LOCAL_BIN/mpx.new"
		die "要装 $VERSION,拿到的却是「$new_version」,已放弃替换。"
	fi

	# 从输出的最后一个字段取版本号当目录名;取不到或不是 v 开头时用本地时间戳,
	# 让源码编出来的二进制也有个唯一的版本目录。
	v="$(printf '%s' "$new_version" | awk '{print $NF}')"
	case "$v" in
		v*) ;;
		*) v="local-$(date +%Y%m%d%H%M%S)" ;;
	esac

	# 先记下现在生效的是哪一版,切完要把它当上一版留下。老布局是普通文件
	# (以前直接 mv 上去的):把它按自己的版本搬进 releases 当上一版,这样回退时
	# 有得可用;读不出版本就叫 previous。
	prev=""
	if [[ -L "$link" ]]; then
		prev="$(basename "$(dirname "$(readlink "$link")")")"
	elif [[ -f "$link" ]]; then
		old_version="$(version_field_of "$link")"
		case "$old_version" in
			v*) prev="$old_version" ;;
			*) prev=previous ;;
		esac
		install -d -m 0755 "$LOCAL_RELEASES/$prev"
		mv -f "$link" "$LOCAL_RELEASES/$prev/mpx"
		echo "    $link 原是普通文件,按上一版 $prev 收进版本目录"
	fi

	install -d -m 0755 "$LOCAL_RELEASES/$v"
	mv -f "$LOCAL_BIN/mpx.new" "$LOCAL_RELEASES/$v/mpx"
	# 临时链接 + rename:切换是原子的,不会出现指向半个文件的窗口。
	ln -sfn "$LOCAL_RELEASES/$v/mpx" "$link.tmp"
	mv -Tf "$link.tmp" "$link"

	# 重复装同一版时上一版仍在链接后面,prev 会等于 v;这时按修改时间挑一个留。
	if [[ -z "$prev" || "$prev" == "$v" ]]; then
		prev="$(newest_release "$v")"
	fi
	prune_releases "$v" "$prev"
}
# <<< install_release_layout

usage() {
	cat <<'EOF'
在控制面这台机器上安装或更新 mpx。

用法:
  sudo ./deploy/install.sh [mpx 二进制的路径]
  sudo ./deploy/install.sh --version v0.2.0
  sudo ./deploy/install.sh --addr 0.0.0.0:8080

默认监听 127.0.0.1:8080。--addr 换成别的地址就写进 systemd 单元;0.0.0.0:8080
会让订阅从公网可达;管理接口在同一端口上走 HTTPS(自签证书,按指纹认)。

二进制按顺序找:参数 → GitHub Releases → 本机 Go 编译。不会自动捡本机上现成的 mpx 文件。
EOF
	exit 0
}

# ---- 参数 -------------------------------------------------------------------

BIN_ARG=
VERSION=latest
ADDR=

while [[ $# -gt 0 ]]; do
	case "$1" in
		--version) VERSION="${2:-}"; shift 2 ;;
		--addr) ADDR="${2:-}"; shift 2 ;;
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
		if $FETCH "$URL" > "$TMP/$ARCHIVE" 2>/dev/null && verify_release "$TMP/$ARCHIVE" "$ARCHIVE" && tar --no-same-owner -xzf "$TMP/$ARCHIVE" -C "$TMP" 2>/dev/null; then
			BIN="$(find "$TMP" -maxdepth 2 -type f -name mpx -print -quit)"
			[[ -n "$BIN" ]] || note "发布件里没有 mpx"
		else
			note "这个版本没有发布件,换下一条路"
		fi
	fi

	if [[ -z "$BIN" ]]; then
		root="$REPO_ROOT"
		if [[ -z "$root" ]]; then
			command -v git >/dev/null || die "手边没有二进制,也没有 git。把编好的二进制路径当参数传进来。"
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

echo "==> 装到 $LOCAL_BIN/mpx"
# 布局与迁移都收在 install_release_link 里(见文件上半部分),这里只调用。
install_release_link "$BIN"

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
# 控制面(mpx 用户)要有地方放本机触发的升级请求:它只往这里写一个 request 文件,
# root 的 mpx-update.path 监听到就拉起 mpx-update.service。
install -d -m 0755 -o mpx -g mpx /var/lib/mpx/update
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

# fetch_unit NAME:优先用脚本旁边那份;`curl | sudo bash` 的时候旁边什么都没有,
# 去仓库里取一份。打印最终可用的路径。
fetch_unit() {
	local name="$1" src="$SCRIPT_DIR/$1"
	if [[ -f "$src" ]]; then
		printf '%s' "$src"
		return 0
	fi
	[[ -n "$FETCH" ]] || die "找不到单元文件 $src(它和这个脚本放在一起)"
	$FETCH "$RAW_BASE/deploy/$name" > "$TMP/$name" 2>/dev/null \
		|| die "取不到 $name。把 deploy/ 整个目录传上来再跑。"
	printf '%s' "$TMP/$name"
}

# install_unit NAME [ADDR]:装一份单元。ADDR 为空就原样装;非空时把文件里现有的
# --addr 换成 ADDR。
#
# 替换后的单元写到 $TMP,不改仓库里那份 —— 从源码目录跑这个脚本是常规用法,
# 就地 sed 会把工作区改脏,下次 git status 里多出一处莫名其妙的改动。
# 单元文件跟着程序走,所以每次都以手上这份为准;但你改过的内容不能就这么没了,
# 有差异就先备份。
install_unit() {
	local name="$1" addr="${2:-}" src unit
	src="$(fetch_unit "$name")"
	unit="/etc/systemd/system/$name"
	if [[ -n "$addr" ]]; then
		grep -q -- '--addr ' "$src" || die "$src 里没有 --addr,没法替换监听地址"
		sed "s|--addr [^ ]*|--addr $addr|" "$src" > "$TMP/$name.final"
		src="$TMP/$name.final"
	fi
	if [[ -f "$unit" ]] && ! cmp -s "$src" "$unit"; then
		cp -f "$unit" "$unit.bak"
		echo "    $unit 与手上这份不同,原文件备份到 $unit.bak"
	fi
	install -m 0644 "$src" "$unit"
}

# --addr 只在换了监听地址时才动单元:默认那份是仓库里的原文,直接装。
if [[ -n "$ADDR" ]]; then
	[[ "$ADDR" == *:* ]] || die "--addr 要写成 地址:端口(比如 0.0.0.0:8080)"
fi
install_unit mpx.service "${ADDR:-}"

# 控制面升级用的两个单元:path 监视请求文件,service 由它拉起,以 root 执行换二进制。
# health check 只从本机连控制面,所以 service 里的 --addr 固定成回环;控制面监听的
# 是 0.0.0.0:P 时,这里就是 127.0.0.1:P。
UPDATE_ADDR=""
if [[ -n "$ADDR" ]]; then
	UPDATE_ADDR="127.0.0.1:${ADDR##*:}"
fi
install_unit mpx-update.service "$UPDATE_ADDR"
install_unit mpx-update.path

systemctl daemon-reload
systemctl enable mpx >/dev/null
# --now 会立刻把 path 单元挂上:控制面刚装好,本机就能用 mpx update apply control 了。
systemctl enable --now mpx-update.path >/dev/null

# ---- 起服务并确认它真的在跑 -------------------------------------------------

echo "==> 重启 mpx"
systemctl restart mpx
sleep 1
if ! systemctl is-active --quiet mpx; then
	systemctl --no-pager -l status mpx || true
	journalctl -u mpx -n 20 --no-pager || true
	die "mpx 没起来,上面是它的状态和最近的日志"
fi

if [[ -n "$ADDR" ]]; then
	BOUND="$ADDR"
else
	BOUND=127.0.0.1:8080
fi

cat <<EOF

装好了。控制面在 $BOUND。

EOF

FP="$(cat /var/lib/mpx/tls-fingerprint 2>/dev/null || true)"
SHOWN=127.0.0.1:8080
[[ -n "$ADDR" ]] && SHOWN="<这台机器的地址>:${BOUND##*:}"

if [[ -z "$ADDR" ]]; then
	cat <<EOF
它只监听回环 —— 要从别的机器管它,拉一条隧道:

  ssh -N -L 8080:127.0.0.1:8080 root@<这台机器的地址>

EOF
fi

cat <<EOF
同一个端口上:订阅走 HTTP,管理接口走 HTTPS(控制面的自签证书,指纹
${FP:-<稍后 mpx status 里看>})。

用 mpx init 装的,init 会接着设设备密码并把你的电脑接上去。其他设备装好命令行
(deploy/install-cli.sh,Windows 用 deploy/install-cli.ps1)后:

  mpx connect $SHOWN --fp '${FP:-<证书指纹>}'

不是经 mpx init、而是直接跑本脚本装的,设备密码还没设,先在这台机器上设一个:

  sudo sh -c 'MPX_TOKEN="\$(sed -n "s/^MPX_TOKEN=//p" /etc/mpx/env)" mpx --server http://127.0.0.1:${BOUND##*:} device password'

EOF

cat <<EOF

接下来加节点,再在节点上把核心装上 —— 节点只需要 sshd 和 systemd,不用能连
GitHub,也不用装 xray:

  mpx node add hk --address <节点地址>
  mpx node install <节点 id>

控制面要 SSH 进节点,先把下面这行公钥追到每个节点的 authorized_keys 里
(默认以 root 登录;节点上用的是别的用户就填那个用户的):

  $(cat "$KEY.pub")

EOF
