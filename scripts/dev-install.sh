#!/bin/bash
# 安装工作区启动器；每次运行检查源码，mpx-dev-update 强制重编，不拉取或切换分支。
set -euo pipefail
repo_dir="$(cd -- "$(dirname -- "$0")/.." && pwd)"
bin_dir="$HOME/.local/bin"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) echo '请先把 ~/.local/bin 加入 PATH。' >&2; exit 1 ;;
esac
if [[ ! -d "$bin_dir" || -d "$bin_dir/mpx-dev" || -d "$bin_dir/mpx-dev-update" ]]; then
  echo '安装目录不存在，或目标命令是目录。' >&2
  exit 1
fi
python3 "$repo_dir/scripts/dev-run.py" --rebuild-only
build_dir="$(mktemp -d "$bin_dir/.mpx-dev-launcher.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT
printf '#!/bin/bash\nexec python3 %q "$@"\n' "$repo_dir/scripts/dev-run.py" > "$build_dir/mpx-dev"
printf '#!/bin/bash\nexec python3 %q --rebuild-only\n' "$repo_dir/scripts/dev-run.py" > "$build_dir/mpx-dev-update"
chmod 755 "$build_dir/mpx-dev" "$build_dir/mpx-dev-update"
mv -f "$build_dir/mpx-dev" "$bin_dir/mpx-dev"
mv -f "$build_dir/mpx-dev-update" "$bin_dir/mpx-dev-update"
echo '已安装工作区启动器 mpx-dev；每次启动自动检查，mpx-dev-update 强制重编。'
