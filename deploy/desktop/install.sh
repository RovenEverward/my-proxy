#!/usr/bin/env bash
# MPX 桌面版的一键安装脚本(给没有 .deb 的发行版用)。
#
#   ./install.sh             装到当前用户:二进制、桌面入口、图标
#   ./install.sh --uninstall 删掉装进去的东西
#
# 只动当前用户自己的目录,不需要 root:
#   ~/.local/opt/mpx/mpx
#   ~/.local/share/applications/mpx.desktop
#   ~/.local/share/icons/hicolor/256x256/apps/mpx.png
#
# 运行依赖是系统的 GTK 3 与 WebKitGTK 4.1(程序启动时缺库会给出提示);
# 包不签名。
set -eu

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PREFIX="${HOME}/.local"
OPT_DIR="${PREFIX}/opt/mpx"
BIN="${OPT_DIR}/mpx"
APPS_DIR="${PREFIX}/share/applications"
DESKTOP="${APPS_DIR}/mpx.desktop"
ICON_DIR="${PREFIX}/share/icons/hicolor/256x256/apps"
ICON="${ICON_DIR}/mpx.png"

die() {
  echo "install.sh: $*" >&2
  exit 1
}

refresh_caches() {
  # 有就刷新,没有也不算错:桌面环境下次登录会自己扫。
  if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database "$APPS_DIR" >/dev/null 2>&1 || true
  fi
  if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -f -t "${PREFIX}/share/icons/hicolor" >/dev/null 2>&1 || true
  fi
}

if [ "${1:-}" = "--uninstall" ]; then
  rm -f "$BIN" "$DESKTOP" "$ICON"
  rmdir "$OPT_DIR" 2>/dev/null || true
  refresh_caches
  echo "已卸载 MPX 桌面版。配置与 token 仍在 ~/.config/mpx/,没有动。"
  exit 0
fi

case "${1:-}" in
  "" | --install) ;;
  *)
    echo "用法: $0 [--install|--uninstall]" >&2
    exit 2
    ;;
esac

[ -f "$SELF_DIR/mpx" ] || die "找不到 $SELF_DIR/mpx(要在解开的 MPX/ 目录里运行)"
[ -f "$SELF_DIR/mpx.desktop" ] || die "找不到 $SELF_DIR/mpx.desktop"

mkdir -p "$OPT_DIR" "$APPS_DIR" "$ICON_DIR"
install -m 0755 "$SELF_DIR/mpx" "$BIN"
install -m 0644 "$SELF_DIR/mpx.png" "$ICON"

# 模板里的 Exec 是系统级安装的 /opt/mpx/mpx;这里换成用户目录里的那份。
sed "s|^Exec=.*|Exec=${BIN} app|" "$SELF_DIR/mpx.desktop" >"$DESKTOP"
chmod 0644 "$DESKTOP"

refresh_caches

echo "已安装 MPX 桌面版:"
echo "  程序  $BIN"
echo "  入口  $DESKTOP"
echo "  图标  $ICON"
echo "从应用菜单打开 MPX,或直接运行: $BIN app"
echo "卸载: $0 --uninstall"
