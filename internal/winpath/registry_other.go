//go:build !windows

package winpath

import "errors"

// Apply 只在 Windows 上有意义。
func Apply(op, dir string) (bool, string, error) {
	return false, "", errors.New("只在 Windows 上可用")
}
