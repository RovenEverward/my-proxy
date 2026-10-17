package cli

import (
	"net/http"
	"strings"

	"encoding/json"
	"errors"
	"fmt"
	"github.com/RovenEverward/my-proxy/internal/pin"
	"os"
	"path/filepath"
	"sort"
)

// Profile 是一套控制面地址 + token。
type Profile struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	// Fingerprint 是控制面自签证书的公钥指纹(SHA256:...)。https 地址必须有它:
	// 控制面没有 CA 签的证书,CLI 只认这个指纹,做法同 SSH 的 known_hosts。
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Config 是客户端自己记住的东西。
//
// 一份配置里能放多个 profile,current 记住"现在对谁说话"。之所以不是单槽位:
// 这台机器上同时存在真实控制面和临时控制面是常态(跑测试、试命令、连另一套
// 环境),单槽位下换过去再换回来只能靠重新登录一次 —— 而 token 只在服务器上,
// 重新登录意味着要去机器上把 token 翻出来。多槽位让"切回去"变成一次本地操作。
//
// 字段都带 omitempty:只用了单 profile 的人,文件里不会多出 profiles/current。
type Config struct {
	Current  string             `json:"current,omitempty"`
	Profiles map[string]Profile `json:"profiles,omitempty"`

	// Server / Token 是单 profile 时代的字段,读取时兼容,保存时不再写。
	// 迁移在 loadConfig 里一次做完,内部只认 Current + Profiles。
	Server string `json:"server,omitempty"`
	Token  string `json:"token,omitempty"`
}

// defaultProfile 是所有地方都没点名时用的 profile 名。
//
// 用 "default" 而不是把 profile 设成可选:可选会让"当前是哪个"有两种表达
// (有名字 / 没名字),每个读它的地方都要判两次。
const defaultProfile = "default"

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
//
// 读到旧格式(单份 server/token、没有 profiles)时就地迁移成 default profile,
// 但**不写回文件** —— 读操作不该改用户的文件;真正落盘等下一次 login。
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
	return c.migrated(), nil
}

// migrated 把旧格式的单份配置折算成 default profile。
func (c Config) migrated() Config {
	if len(c.Profiles) == 0 && (c.Server != "" || c.Token != "") {
		c.Profiles = map[string]Profile{defaultProfile: {Server: c.Server, Token: c.Token}}
		c.Current = defaultProfile
	}
	// 有 profiles 但没记 current(手写的文件)时兜一个,免得解析不出该用哪个。
	if c.Current == "" && len(c.Profiles) > 0 {
		c.Current = defaultProfile
	}
	// 旧字段不再有含义,清掉免得又写回文件。
	c.Server, c.Token = "", ""
	return c
}

// saveConfig 写配置文件。里面有 token,所以目录 0700、文件 0600。
//
// 写的是"同目录临时文件 + rename":rename 在同一个文件系统里是原子的,观察者
// 要么看到旧文件、要么看到新文件,不会看到半个。直接写目标文件的话,写到一半
// 失败会留下半个 JSON,下次任何一条命令都在 loadConfig 里报"不是合法 JSON",
// 连 login 自己都跑不起来 —— 那是把可恢复的小事变成必须手改文件的事故。
func saveConfig(c Config) error {
	path, err := configFile()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("建配置目录: %w", err)
	}
	c = c.migrated()
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// 临时文件必须和目标同目录:跨文件系统的 rename 不是原子的,会退化成
	// 复制 + 删除,中途断电还是半个文件。
	f, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("建临时配置文件: %w", err)
	}
	tmp := f.Name()
	// 任何一步失败都别留垃圾。
	defer func() {
		if tmp != "" {
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("设临时配置文件权限: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("写临时配置文件: %w", err)
	}
	// fsync 之后再 rename:否则 rename 虽然原子,内容却可能还在页缓存里,
	// 断电后拿到一个新名字的空文件。
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("同步临时配置文件: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭临时配置文件: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换配置文件 %s: %w", path, err)
	}
	tmp = "" // 成功了,别在 defer 里删掉刚换上去的文件
	return nil
}

// resolve 按 命令行 flag > 环境变量 > 配置文件 的优先级算出生效的 server 与 token。
//
// flag 以空值表示"没给":默认值本来就是空串,使用者也不会想把地址显式设成空。
func resolve() (server, token string, err error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", "", err
	}
	p, err := cfg.pick(firstNonEmpty(opts.profile, os.Getenv("MPX_PROFILE")))
	if err != nil {
		return "", "", err
	}
	return firstNonEmpty(opts.server, os.Getenv("MPX_SERVER"), p.Server),
		firstNonEmpty(opts.token, os.Getenv("MPX_TOKEN"), p.Token),
		nil
}

// resolveFingerprint 找出连 server 时该认的证书指纹:--fp > MPX_FINGERPRINT >
// 生效配置里记的那个。配置里的指纹只在地址也来自(或等于)这个控制面时才
// 用 —— --server 临时指到别处时沿用旧指纹,只会得到一个莫名其妙的"指纹不符"。
func resolveFingerprint(server string) (string, error) {
	fp := firstNonEmpty(opts.fingerprint, os.Getenv("MPX_FINGERPRINT"))
	if fp == "" {
		cfg, err := loadConfig()
		if err != nil {
			return "", err
		}
		p, err := cfg.pick(firstNonEmpty(opts.profile, os.Getenv("MPX_PROFILE")))
		if err != nil {
			return "", err
		}
		if strings.TrimSuffix(p.Server, "/") == strings.TrimSuffix(server, "/") {
			fp = p.Fingerprint
		}
	}
	if fp == "" {
		return "", nil
	}
	return pin.Normalize(fp)
}

// transportFor 造一个连 server 用的 HTTP 传输:https 地址必须带指纹并只认它,
// http 地址用默认传输。
func transportFor(server string) (*http.Transport, error) {
	fp, err := resolveFingerprint(server)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(strings.ToLower(server), "https://") && fp == "" {
		return nil, errors.New("控制面是 https 地址,但没有证书指纹:用 mpx connect 接入(会记下指纹),或加 --fp")
	}
	if !strings.HasPrefix(strings.ToLower(server), "https://") {
		fp = ""
	}
	return pin.Transport(fp), nil
}

// pick 挑出生效的 profile。name 为空表示"用 current"。
func (c Config) pick(name string) (Profile, error) {
	if name == "" {
		name = c.Current
	}
	if name == "" {
		return Profile{}, nil // 还没 login 过,交给上层报"没有控制面地址"
	}
	p, ok := c.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("没有名为 %q 的控制面,%s", name, c.profileHint())
	}
	return p, nil
}

// profileHint 列出有哪些控制面,给报错用。一个都没有时提示去 login。
func (c Config) profileHint() string {
	names := c.names()
	if len(names) == 0 {
		return "先跑 mpx connect <控制面地址>"
	}
	return "现有:" + joinComma(names)
}

// names 返回排好序的 profile 名。map 的遍历顺序是随机的,用它直接输出会让
// 同一份配置每次打印出来的顺序都不一样。
func (c Config) names() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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

// joinComma 用顿号连起来,和仓里其它地方列名字的写法一致。
func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
