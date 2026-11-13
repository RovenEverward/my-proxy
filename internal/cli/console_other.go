//go:build !windows

package cli

// attachParentConsole 在 Windows 之外没有意义,留空。
func attachParentConsole() {}
