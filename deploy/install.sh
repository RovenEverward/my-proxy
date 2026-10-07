#!/usr/bin/env bash
#
# 在控制面这台机器上安装或更新 mpx。装和更新是同一条命令。
#
# 每次都是「换二进制 → 补齐用户和目录 → 备好 token 与 SSH 密钥 → 写单元 → 重启」,
# 已经存在的东西(token、数据库、密钥)一律不动,所以重复跑是安全的。
#
# 这个脚本只管控制面。节点上装 xray 是 `mpx node install` 的事(由控制面自己 SSH
# 过去做),操作者本机的 CLI 则是 `go build -o ~/.local/bin/mpx ./cmd/mpx`。
#
# 用法:
#   sudo ./install.sh [mpx 二进制的路径]
#
# 不给路径时按顺序找:脚本旁边的 mpx、当前目录的 mpx、/tmp/mpx。
# 在开发机上交叉编译一个:
#   GOOS=linux GOARCH=amd64 go build -trimpath -o /tmp/mpx ./cmd/mpx

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

die() {
	printf '错误:%s\n' "$1" >&2
	exit 1
}

[[ ${EUID} -eq 0 ]] || die "要 root 才能装服务:sudo $0 $*"

# ---- 二进制 -----------------------------------------------------------------

BIN="${1:-}"
if [[ -z "$BIN" ]]; then
	for candidate in "$SCRIPT_DIR/mpx" "./mpx" "/tmp/mpx"; do
		if [[ -f "$candidate" ]]; then
			BIN="$candidate"
			break
		fi
	done
fi
[[ -n "$BIN" && -f "$BIN" ]] || die "找不到要装的 mpx 二进制。把它放到 $SCRIPT_DIR/mpx,或者把路径当第一个参数传进来。"

echo "==> 二进制 $BIN → /usr/local/bin/mpx"
# 先落到 .new 并试跑一次:更新时如果换上一个跑不起来的二进制(多半是交叉编译时
# GOOS/GOARCH 填错了),这台机器就没有控制面了,而数据库和节点都还在等它。
# 直接改名覆盖正在运行的文件也是可以的:老的 inode 会活到进程退出。
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
[[ -f "$SRC" ]] || die "找不到单元文件 $SRC(它和这个脚本放在一起)"
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

然后在操作者机器上接上它:

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
