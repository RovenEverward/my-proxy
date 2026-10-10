//go:build windows

package diagnostics

import "os/exec"

// Local diagnostics depend on a POSIX shell; unsupported Windows installations
// fail explicitly. Remote node diagnostics remain available through Executor.
func configureProcess(cmd *exec.Cmd) {}
func cleanupProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
