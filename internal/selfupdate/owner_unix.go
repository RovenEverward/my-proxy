//go:build !windows

package selfupdate

import (
	"os"
	"syscall"
)

// matchOwner 把 paths 的属主改成和 ref 一样(存在的才改,尽力而为)。
//
// root 侧执行器写出的 state.json、打开库时可能新建的 -wal/-shm 默认属于 root,
// 而控制面以 mpx 用户运行:不改回来,控制面就读不了升级状态、写不了自己的库。
func matchOwner(ref string, paths ...string) {
	fi, err := os.Stat(ref)
	if err != nil {
		return
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); err == nil {
			_ = os.Lchown(p, int(st.Uid), int(st.Gid))
		}
	}
}
