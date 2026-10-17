package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RestartEnv 是一键更新重启 mpx ui 时的交接方式:新进程的环境变量里带上
// 重启交接文件的路径,启动时读它回到同一个监听地址、认同一个会话。
const RestartEnv = "MPX_UI_RESTART"

// RestartState 是重启交接文件的内容。
//
// 会话在这套页面里是一个随机值(没有签名密钥),把它带上,新进程发的 cookie 校验
// 就与旧进程一致,浏览器里已打开的页面不用重新登录。
type RestartState struct {
	Addr    string `json:"addr"`
	Session string `json:"session"`
}

// writeRestartState 把交接状态写进一个 0600 的临时文件并返回路径。
//
// 会话值等于浏览器的登录凭据,文件权限必须只有本进程的用户能读;文件是一次性的,
// 新进程读完即删(见 ReadRestartState)。
func writeRestartState(addr, session string) (string, error) {
	f, err := os.CreateTemp("", "mpx-ui-restart-*.json")
	if err != nil {
		return "", fmt.Errorf("创建重启交接文件失败: %w", err)
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("收紧重启交接文件权限失败: %w", err)
	}
	if err := json.NewEncoder(f).Encode(RestartState{Addr: addr, Session: session}); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("写重启交接文件失败: %w", err)
	}
	return f.Name(), nil
}

// ReadRestartState 读回重启交接状态并删掉文件:它只在新进程启动时用一次,留下
// 等于把会话凭据落在磁盘上。
func ReadRestartState(path string) (RestartState, error) {
	var st RestartState
	b, err := os.ReadFile(path)
	if err != nil {
		return st, fmt.Errorf("读取重启交接文件失败: %w", err)
	}
	defer os.Remove(path)
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("重启交接文件不是合法 JSON: %w", err)
	}
	return st, nil
}

// restartEnviron 拼出新进程的环境:原样带上当前环境,替换掉可能残留的交接变量,
// 再指向这次的交接文件。
func restartEnviron(stateFile string) []string {
	prefix := RestartEnv + "="
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		env = append(env, e)
	}
	return append(env, prefix+stateFile)
}
