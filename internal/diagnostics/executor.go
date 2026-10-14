package diagnostics

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// LocalExecutor invokes the installed POSIX shell with separately bounded output.
// Commands inherit environment but diagnostic curl ignores ambient proxy settings.
type LocalExecutor struct{}

var ErrOutputLimit = errors.New("diagnostic command output exceeds 65536 bytes")

type limitedOutput struct {
	mu       sync.Mutex
	b        bytes.Buffer
	exceeded bool
	cancel   context.CancelFunc
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	left := MaxOutputBytes - w.b.Len()
	if len(p) > left {
		w.b.Write(p[:left])
		w.exceeded = true
		if w.cancel != nil {
			w.cancel()
		}
		return left, ErrOutputLimit
	}
	return w.b.Write(p)
}
func (LocalExecutor) Exec(ctx context.Context, script string, stdin []byte) (string, error) {
	if len(script) > 32<<10 || len(stdin) > 8<<20 {
		return "", fmt.Errorf("diagnostic script or stdin exceeds limit")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "sh", "-c", script)
	configureProcess(cmd)
	cmd.Stdin = bytes.NewReader(stdin)
	out := &limitedOutput{cancel: cancel}
	stderr := &limitedOutput{cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.WaitDelay = 200 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return "", err
	}
	err := cmd.Wait()
	cleanupProcess(cmd)
	if out.exceeded || stderr.exceeded {
		err = ErrOutputLimit
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return out.b.String(), err
}

// boundedScript protects remote execution too: each tool gets a wall-clock
// watchdog and traps clean its child and watchdog on exit/disconnection.
// Scripts use no persistent files outside a trap-cleaned private mktemp directory.
//
// The top-level shell owns that directory: it points TMPDIR at it and removes
// it after waiting for the child. Leaving removal to the child's own EXIT trap
// lost the directory whenever a signal hit the child before or during that
// trap (it then exited 124 without removing it). Cleanup ignores further
// signals so a second HUP/INT/TERM cannot cut it short. The watchdog is
// stopped with SIGKILL: a TERM landing after it forks its sleep but before it
// enters wait is only acted on when the sleep ends, which held fast commands
// for the whole watchdog period. Its orphaned sleep has no output and exits on
// its own (locally the process-group kill reaps it at once).
func boundedScript(command string, seconds int) string {
	return fmt.Sprintf(`set -f
exec 3<&0
p=''; w=''; t=''
cleanup() { trap '' HUP INT TERM; [ -z "$p" ] || { kill "$p" 2>/dev/null || :; wait "$p" 2>/dev/null || :; }; [ -z "$w" ] || { kill -9 "$w" 2>/dev/null || :; wait "$w" 2>/dev/null || :; }; [ -z "$t" ] || rm -rf -- "$t"; }
trap cleanup EXIT
trap 'exit 124' HUP INT TERM
t=$(mktemp -d) || exit 1
TMPDIR=$t
export TMPDIR
run_command() {
%s
}
run_command <&3 &
p=$!
watchdog() {
 sp=''
 trap '[ -z "$sp" ] || kill "$sp" 2>/dev/null || :' EXIT
 trap 'exit 0' HUP INT TERM
 sleep %d &
 sp=$!
 wait "$sp"
 sp=''
 kill "$p" 2>/dev/null || :
}
watchdog >/dev/null 2>&1 &
w=$!
wait "$p"
s=$?
p=''
exit "$s"
`, command, seconds)
}
