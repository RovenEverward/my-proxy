#!/usr/bin/env python3
"""本机开发入口：按工作区指纹编译，保留上次可用文件，接管旧开发窗口。"""
import fcntl
import hashlib
import json
import os
from pathlib import Path
import signal
import shlex
import subprocess
import sys
import tempfile
import time

REPO = Path(__file__).resolve().parent.parent
STATE = Path.home() / ".local/share/mpx-dev"
BINARY = STATE / "mpx-dev"
STAMP = STATE / "build.json"
APP = STATE / "app.json"


def git(*args):
    return subprocess.check_output(["git", "-C", str(REPO), *args])


def fingerprint():
    head = git("rev-parse", "HEAD").strip()
    diff = git("diff", "--binary", "--no-ext-diff", "--no-textconv", "HEAD", "--")
    untracked = git("ls-files", "--others", "--exclude-standard", "-z")
    digest = hashlib.sha256(head + b"\0" + diff)
    for name in untracked.split(b"\0"):
        if not name:
            continue
        path = REPO / os.fsdecode(name)
        digest.update(b"\0" + name + b"\0")
        digest.update(os.fsencode(os.readlink(path)) if path.is_symlink() else path.read_bytes())
    state = "dirty" if diff or untracked else "clean"
    return {"fingerprint": digest.hexdigest(), "version": f"dev-{head.decode()[:12]}-{state}"}


def read_json(path):
    if not path.exists():
        return {}
    return json.loads(path.read_text())


def write_json(path, value):
    pending = path.with_suffix(".new")
    pending.write_text(json.dumps(value))
    pending.replace(path)


def compile_binary(wanted):
    module = next(line.split()[1] for line in (REPO / "go.mod").read_text().splitlines() if line.startswith("module "))
    print("mpx-dev：正在编译 " + wanted["version"], file=sys.stderr)
    with tempfile.TemporaryDirectory(prefix="build-", dir=STATE) as folder:
        target = Path(folder) / "mpx-dev"
        subprocess.run(["go", "build", "-trimpath", "-ldflags",
                        f"-X {module}/internal/buildinfo.version={wanted['version']}",
                        "-o", str(target), "./cmd/mpx"], cwd=REPO, check=True)
        if fingerprint() != wanted:
            raise RuntimeError("编译期间工作区发生变化，请重新运行。")
        target.replace(BINARY)
        write_json(STAMP, wanted)


def is_app(args):
    # 当前 CLI 的全局参数都带值；帮助和版本参数不应关闭已有窗口。
    values = {"--fp", "--server", "--token", "--profile", "--output", "-o"}
    index = 0
    while index < len(args):
        arg = args[index]
        if arg == "--":
            return index + 1 < len(args) and args[index + 1] == "app"
        if arg in values:
            index += 2
        elif any(arg.startswith(flag + "=") for flag in values) or (arg.startswith("-o") and len(arg) > 2):
            index += 1
        else:
            return arg == "app"
    return False


def app_processes():
    # 只接受已知开发版程序路径，且真实子命令是 app。配置和其他命令均不碰。
    executables = [str(BINARY), str(Path.home() / ".local/bin/mpx-dev")]
    listing = subprocess.check_output(["ps", "-axo", "pid=,command="], text=True)
    found = []
    for line in listing.splitlines():
        fields = line.strip().split(None, 1)
        if len(fields) != 2:
            continue
        pid, command = int(fields[0]), fields[1]
        if pid == os.getpid():
            continue
        suffix = next((command[len(exe) + 1:] for exe in executables if command.startswith(exe + " ")), None)
        if suffix is not None and is_app(shlex.split(suffix)):
            # comm 是内核报告的可执行文件名，避免仅凭命令行误杀同名脚本。
            if sys.platform == "linux":
                try:
                    executable = os.readlink(f"/proc/{pid}/exe").removesuffix(" (deleted)")
                except FileNotFoundError:
                    continue
            else:
                executable = subprocess.run(["ps", "-p", str(pid), "-o", "comm="], capture_output=True, text=True).stdout.strip()
            if executable in executables:
                found.append(pid)
    return found


def take_over_app(stamp):
    recorded = read_json(APP)
    for pid in app_processes():
        if recorded.get("pid") == pid and recorded.get("fingerprint") == stamp.get("fingerprint"):
            print("mpx-dev：相同源码版本的开发窗口已在运行，请使用现有窗口。", file=sys.stderr)
            return False
        print("mpx-dev：正在关闭旧开发窗口，随后启动当前版本。", file=sys.stderr)
        try:
            os.kill(pid, signal.SIGTERM)
        except ProcessLookupError:
            continue
        deadline = time.monotonic() + 3
        while pid in app_processes():
            if time.monotonic() >= deadline:
                raise RuntimeError("旧开发窗口尚未退出，请关闭后重新运行 mpx-dev app。")
            time.sleep(0.05)
    write_json(APP, {"pid": os.getpid(), "fingerprint": stamp.get("fingerprint")})
    return True


def main():
    args = sys.argv[1:]
    force = args == ["--rebuild-only"]
    STATE.mkdir(parents=True, exist_ok=True)
    # Python 文件描述符默认不跨 exec 继承，桌面运行期间不会占住编译锁。
    with (STATE / "build.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        try:
            wanted = fingerprint()
            if force or not BINARY.exists() or read_json(STAMP).get("fingerprint") != wanted["fingerprint"]:
                compile_binary(wanted)
        except (OSError, subprocess.CalledProcessError, RuntimeError, ValueError) as error:
            print(f"mpx-dev：编译失败：{error}", file=sys.stderr)
            if force or not BINARY.exists():
                return 1
            print("mpx-dev：用的是旧版本，继续启动上次编译成功的程序。", file=sys.stderr)
        if force:
            print("已编译 mpx-dev " + read_json(STAMP)["version"])
            return 0
        if is_app(args) and not take_over_app(read_json(STAMP)):
            return 0
        os.execv(BINARY, [str(BINARY), *args])


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError) as error:
        print(f"mpx-dev：{error}", file=sys.stderr)
        sys.exit(1)
