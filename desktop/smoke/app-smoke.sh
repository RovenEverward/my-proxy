#!/usr/bin/env bash
# 桌面程序冒烟:用 desktop 构建的 mpx 跑两次 `mpx app`,每次都要自己截图、打印
# APP_SMOKE_OK <title> 并退出 0。
#
#   ① 没有任何配置:应当进入首次安装向导,截图 app-<os>-setup.png;
#   ② 起了本地控制面并给出 MPX_TOKEN / MPX_SERVER:应当进入主界面,截图
#      app-<os>-main.png。
#
# 由 .github/workflows/ci.yml 的 desktop-smoke job 调用,也可以本地跑。依赖:
#   - 仓库根已编出 ./mpxbin/mpx,且带 -X .../buildinfo.desktop=1
#   - curl、grep
#   - Linux 需要 WebKitGTK 4.1;有 xvfb-run 时脚本自己包一层虚拟屏
#
# 退出码:0 = 两次都成功;1 = 任一失败。
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BIN_DIR="$ROOT/mpxbin"

# GitHub 会设 RUNNER_OS(Linux/macOS/Windows);本地退回 uname。
case "${RUNNER_OS:-}" in
  Linux) OS_NAME=linux ;;
  macOS) OS_NAME=macos ;;
  Windows) OS_NAME=windows ;;
  *)
    case "$(uname -s)" in
      Darwin) OS_NAME=macos ;;
      MINGW* | MSYS* | CYGWIN*) OS_NAME=windows ;;
      *) OS_NAME=linux ;;
    esac
    ;;
esac
case "$OS_NAME" in
  windows) EXE=".exe" ;;
  *) EXE="" ;;
esac

MPX="$BIN_DIR/mpx$EXE"
SETUP_PNG="$ROOT/app-$OS_NAME-setup.png"
MAIN_PNG="$ROOT/app-$OS_NAME-main.png"

# 临时 HOME / 配置目录,别碰开发者自己的配置。Git Bash 的 mktemp 给的是 MSYS
# 路径(/tmp/...),原生 Windows 程序不认,过一层 cygpath。
TMP="$(mktemp -d)"
if command -v cygpath >/dev/null 2>&1; then
  NATIVE_TMP="$(cygpath -w "$TMP")"
else
  NATIVE_TMP="$TMP"
fi
export HOME="$NATIVE_TMP"
export USERPROFILE="$NATIVE_TMP"
export XDG_CONFIG_HOME="$NATIVE_TMP"
# Windows 上 os.UserConfigDir 读的是 %AppData%,不是 HOME/USERPROFILE。也指到
# 临时目录,否则第一次「无配置」可能读到 runner 上残留的配置,直接进主界面。
if [ "$OS_NAME" = "windows" ]; then
  export APPDATA="$NATIVE_TMP/AppData/Roaming"
  mkdir -p "$APPDATA"
fi

SERVE_LOG="$TMP/serve.log"
SETUP_LOG="$TMP/setup.log"
MAIN_LOG="$TMP/main.log"
SERVE_PID=""
FAIL=0

cleanup() {
  [ -n "$SERVE_PID" ] && kill "$SERVE_PID" 2>/dev/null
  wait 2>/dev/null
  return 0
}
trap cleanup EXIT

if [ ! -x "$MPX" ]; then
  echo "找不到 $MPX,先在仓库根跑: go build -ldflags '-X github.com/RovenEverward/my-proxy/internal/buildinfo.desktop=1' -o mpxbin/mpx$EXE ./cmd/mpx" >&2
  exit 1
fi

# 原生程序(Windows 上的 mpx.exe)只认 Windows 路径。
native_path() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1"
  else
    printf '%s\n' "$1"
  fi
}

# run_app <截图路径> <日志路径> [额外环境变量...]
# Linux 上套 xvfb-run;其他平台用系统自带的窗口服务。
run_app() {
  local png log
  png="$(native_path "$1")"
  log="$2"
  shift 2
  echo "运行: MPX_APP_SMOKE=$png $MPX app"
  if [ "$OS_NAME" = "linux" ] && command -v xvfb-run >/dev/null 2>&1; then
    env "$@" MPX_APP_SMOKE="$png" xvfb-run -a -s "-screen 0 1280x800x24" "$MPX" app >"$log" 2>&1
  else
    env "$@" MPX_APP_SMOKE="$png" "$MPX" app >"$log" 2>&1
  fi
}

# check_result <名字> <退出码> <日志> <截图>
check_result() {
  local name="$1" code="$2" log="$3" png="$4" ok=1
  echo "===== $name 输出(退出码 $code) ====="
  cat "$log" || true
  if [ "$code" != "0" ]; then
    echo "$name:退出码 $code,期望 0" >&2
    ok=0
  fi
  if ! grep -q 'APP_SMOKE_OK' "$log" 2>/dev/null; then
    echo "$name:输出里没有 APP_SMOKE_OK" >&2
    ok=0
  fi
  if [ ! -s "$png" ]; then
    echo "$name:没有生成截图 $png" >&2
    ok=0
  fi
  [ "$ok" = "1" ] || FAIL=1
  return 0
}

# 1) 无配置:应当是首次安装向导。清掉可能从外部带进来的控制面设置。
unset MPX_SERVER MPX_TOKEN
run_app "$SETUP_PNG" "$SETUP_LOG"
SETUP_CODE=$?
check_result "安装向导冒烟" "$SETUP_CODE" "$SETUP_LOG" "$SETUP_PNG"

# 2) 本地控制面 + 主界面。--auto-locate=false 不出网。
"$MPX" serve --addr 127.0.0.1:18080 --db "$NATIVE_TMP/mpx.db" --auto-locate=false \
  >"$SERVE_LOG" 2>&1 &
SERVE_PID=$!

status_ok=0
for _ in $(seq 1 300); do
  code="$(curl -s -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer ci-token" http://127.0.0.1:18080/api/status || true)"
  if [ "$code" = "200" ]; then status_ok=1; break; fi
  kill -0 "$SERVE_PID" 2>/dev/null || break   # 控制面提前退出就别再等
  sleep 0.1
done
if [ "$status_ok" != "1" ]; then
  echo "控制面 30 秒内没有就绪:" >&2
  cat "$SERVE_LOG" >&2
  FAIL=1
else
  run_app "$MAIN_PNG" "$MAIN_LOG" \
    MPX_TOKEN=ci-token MPX_SERVER=http://127.0.0.1:18080
  MAIN_CODE=$?
  check_result "主界面冒烟" "$MAIN_CODE" "$MAIN_LOG" "$MAIN_PNG"
fi

# 3) 收尾。先 kill 再打印现场,失败时 artifact 里能看到全部日志。
cleanup
trap - EXIT

echo "===== 控制面日志 ====="
cat "$SERVE_LOG" || true

# 日志也复制到仓库根,给 upload-artifact 用。
cp "$SETUP_LOG" "$ROOT/app-$OS_NAME-setup.log" 2>/dev/null || true
cp "$MAIN_LOG" "$ROOT/app-$OS_NAME-main.log" 2>/dev/null || true
cp "$SERVE_LOG" "$ROOT/app-$OS_NAME-serve.log" 2>/dev/null || true

exit "$FAIL"
