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

// CheckControlUpdate 与本机更新共用开发版判据，控制面改走工作区部署。
func CheckControlUpdate(current string) error {
	if CheckLocalUpdate(current) != nil {
		return errors.New("开发版控制面不通过发布件更新或回退，请运行 mpx-dev deploy 或 mpx-dev deploy --rollback")
	}
	return nil
}
