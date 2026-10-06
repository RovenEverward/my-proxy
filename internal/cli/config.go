package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config 是客户端自己记住的东西:控制面在哪、拿什么 token 说话。
//
// 配置里只放这两项:其余参数每次从 flag 或环境变量来,免得 CLI 也长出一套
// 与 .env / CI 变量重叠的配置。
type Config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

// configFile 返回客户端配置文件的路径。
func configFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("找不到用户配置目录: %w", err)
	}
	return filepath.Join(dir, "mpx", "config.json"), nil
}

// loadConfig 读配置文件。文件还不存在时返回零值,不算错误:首次使用没 login
// 是正常状态,不该变成一个需要处理的错误。
func loadConfig() (Config, error) {
	path, err := configFile()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("读配置文件 %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("配置文件 %s 不是合法 JSON: %w", path, err)
	}
	return c, nil
}

// saveConfig 写配置文件。里面有 token,所以目录 0700、文件 0600。
func saveConfig(c Config) error {
	path, err := configFile()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("建配置目录: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("写配置文件 %s: %w", path, err)
	}
	// WriteFile 不会收紧已存在文件的权限,这里显式来一次。
	return os.Chmod(path, 0o600)
}

// resolve 按 命令行 flag > 环境变量 > 配置文件 的优先级算出生效的 server 与 token。
//
// flag 以空值表示"没给":默认值本来就是空串,使用者也不会想把地址显式设成空。
func resolve() (server, token string, err error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", "", err
	}
	return firstNonEmpty(opts.server, os.Getenv("MPX_SERVER"), cfg.Server),
		firstNonEmpty(opts.token, os.Getenv("MPX_TOKEN"), cfg.Token),
		nil
}

// firstNonEmpty 返回第一个非空值。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// maskToken 只显示 token 的前 6 位:整串 token 出现在终端回滚或截图里,等于
// 把它泄露给了看到屏幕的人。
func maskToken(token string) string {
	if token == "" {
		return "(未设置)"
	}
	r := []rune(token)
	if len(r) <= 6 {
		return string(r)
	}
	return string(r[:6]) + "…"
}
