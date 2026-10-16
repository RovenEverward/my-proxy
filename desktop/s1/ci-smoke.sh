#!/usr/bin/env bash
# S1 桌面端冒烟:起控制面 + mpx ui,拿一次性登录链接,喂给 s1 截图。
#
# 由 .github/workflows/ci.yml 的 desktop-s1 job 调用,也可以本地跑。依赖:
#   - 仓库根已编出 ./mpxbin/mpx(以及同目录的 s1)
#   - curl、grep
#   - Linux 需要 WebKitGTK 4.1;有 xvfb-run 时脚本自己包一层虚拟屏
#
# 退出码就是 s1 的退出码:0 = S1_OK 且生成 png。
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
S1="$BIN_DIR/s1$EXE"
PNG="$ROOT/s1-$OS_NAME.png"

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
# 控制面与 mpx ui 用同一个 token;冒烟脚本自身不调控制面 API。
export MPX_TOKEN=ci-token

SERVE_LOG="$TMP/serve.log"
UI_LOG="$TMP/ui.log"
S1_LOG="$TMP/s1.log"
SERVE_PID=""
UI_PID=""

cleanup() {
  [ -n "$SERVE_PID" ] && kill "$SERVE_PID" 2>/dev/null
  [ -n "$UI_PID" ] && kill "$UI_PID" 2>/dev/null
  wait 2>/dev/null
  return 0
}
trap cleanup EXIT

if [ ! -x "$MPX" ]; then
  echo "找不到 $MPX,先在仓库根跑 go build -o mpxbin/mpx$EXE ./cmd/mpx" >&2
  exit 1
fi
if [ ! -x "$S1" ]; then
  echo "找不到 $S1,先在 desktop/s1 跑 go build -o ../../mpxbin/s1$EXE ." >&2
  exit 1
fi

# 1) 控制面。--auto-locate=false 不出网,自动识别另有 Go 测试。
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
  exit 1
fi

# 2) mpx ui。MPX_TOKEN 与控制面一致,登录链接从 stdout 里抓。
MPX_TOKEN=ci-token MPX_SERVER=http://127.0.0.1:18080 \
  "$MPX" ui --no-open --addr 127.0.0.1:0 >"$UI_LOG" 2>&1 &
UI_PID=$!

LINK=""
for _ in $(seq 1 300); do
  LINK="$(grep -oE 'http://[^ ]+\?k=[^ ]+' "$UI_LOG" 2>/dev/null | head -n 1 || true)"
  [ -n "$LINK" ] && break
  kill -0 "$UI_PID" 2>/dev/null || break
  sleep 0.1
done
if [ -z "$LINK" ]; then
  echo "没从 ui.log 里取到登录链接:" >&2
  cat "$UI_LOG" >&2
  exit 1
fi

# 3) s1。Linux 上要一块 X server:有 xvfb-run 就用它包一层,没有就直接跑
#    (本机有 DISPLAY 时)。其他平台用系统自带的窗口服务,不用包。
S1_PNG="$PNG"
if command -v cygpath >/dev/null 2>&1; then S1_PNG="$(cygpath -w "$PNG")"; fi
echo "运行: $S1 $LINK $S1_PNG"
if [ "$OS_NAME" = "linux" ] && command -v xvfb-run >/dev/null 2>&1; then
  xvfb-run -a -s "-screen 0 1280x800x24" "$S1" "$LINK" "$S1_PNG" >"$S1_LOG" 2>&1
else
  "$S1" "$LINK" "$S1_PNG" >"$S1_LOG" 2>&1
fi
S1_CODE=$?

# 4) 收尾。先 kill 再打印现场,失败时 artifact 里能看到全部日志。
cleanup
trap - EXIT

echo "===== s1 输出 ====="
cat "$S1_LOG" || true
echo "===== mpx ui 日志 ====="
cat "$UI_LOG" || true
echo "===== 控制面日志 ====="
cat "$SERVE_LOG" || true

# 日志也复制到仓库根,给 upload-artifact 用。
cp "$S1_LOG" "$ROOT/s1-$OS_NAME.log" 2>/dev/null || true
cp "$UI_LOG" "$ROOT/s1-$OS_NAME-ui.log" 2>/dev/null || true
cp "$SERVE_LOG" "$ROOT/s1-$OS_NAME-serve.log" 2>/dev/null || true

exit "$S1_CODE"
