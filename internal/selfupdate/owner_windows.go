//go:build windows

package selfupdate

// matchOwner 在 Windows 上什么都不做:控制面只部署在 Linux 上。
func matchOwner(ref string, paths ...string) {}
