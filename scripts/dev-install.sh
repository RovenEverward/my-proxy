#!/bin/bash
# 从当前工作区安装开发版；再次运行 mpx-dev-update 即可重编，不拉取或切换分支。
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

cd "$repo_dir"
module_path="$(go list -m)"
revision="$(git rev-parse --short=12 HEAD)"
state=clean
if [[ -n "$(git status --porcelain)" ]]; then state=dirty; fi
version="dev-$revision-$state"
build_dir="$(mktemp -d "$bin_dir/.mpx-dev-build.XXXXXX")"
trap 'rm -rf -- "$build_dir"' EXIT

go build -trimpath \
  -ldflags "-X $module_path/internal/buildinfo.version=$version" \
  -o "$build_dir/mpx-dev" ./cmd/mpx
printf '#!/bin/bash\nexec %q\n' "$repo_dir/scripts/dev-install.sh" > "$build_dir/mpx-dev-update"
chmod 755 "$build_dir/mpx-dev" "$build_dir/mpx-dev-update"
mv -f "$build_dir/mpx-dev" "$bin_dir/mpx-dev"
mv -f "$build_dir/mpx-dev-update" "$bin_dir/mpx-dev-update"
echo "已安装 mpx-dev ${version}；重新编译：mpx-dev-update"
