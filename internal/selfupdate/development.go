package selfupdate

import (
	"errors"

	"github.com/RovenEverward/my-proxy/internal/buildinfo"
)

// CheckLocalUpdate 必须先于下载、写状态和一键更新的远程步骤执行。
func CheckLocalUpdate(current string) error {
	if buildinfo.IsDevelopment(current) {
		return errors.New("开发版不通过发布件更新或回退，请运行 mpx-dev-update 重新编译")
	}
	return nil
}
