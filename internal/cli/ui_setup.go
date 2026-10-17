package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/pin"
	"github.com/RovenEverward/my-proxy/internal/ui"
)

// setupRunner 把 mpx ui 的首次安装向导接到 runInitSteps 上:和 mpx init 走同一条路径。
type setupRunner struct{}

// discardProgress 是什么都不做的 initProgress:Check 只探路,不需要给人看进度。
type discardProgress struct{}

func (discardProgress) Step(string, ...any) {}
func (discardProgress) Note(string, ...any) {}
func (discardProgress) Stdout() io.Writer   { return io.Discard }
func (discardProgress) Stderr() io.Writer   { return io.Discard }

// Check:用表单给的密码把本机公钥装上,探一次路,把 os/arch/uid/fetch 交给页面。
//
// 这里不判 Linux/root:页面要根据 info 显示对应提示,runInitSteps 自己还会再判。
// Check 结束之后密码不再保留。
func (setupRunner) Check(ctx context.Context, p ui.SetupParams) (map[string]string, error) {
	ip, err := setupInitParams(p)
	if err != nil {
		return nil, err
	}
	defer func() { ip.Password = "" }()

	n := model.Node{
		Name:    "init",
		Address: ip.Host,
		SSHPort: ip.SSHPort,
		SSHUser: ip.SSHUser,
	}
	auth, err := initAuth(ctx, n, ip, discardProgress{})
	if err != nil {
		return nil, err
	}
	return probeRemote(ctx, n, auth)
}

// Run:公钥在 Check 时已经装好,这里不再需要密码,直接走 runInitSteps。
func (setupRunner) Run(ctx context.Context, p ui.SetupParams, prog ui.SetupProgress) (*ui.SetupResult, error) {
	ip, err := setupInitParams(p)
	if err != nil {
		return nil, err
	}
	ip.Password = ""

	res, err := runInitSteps(ctx, ip, newSetupProgress(prog))
	if err != nil {
		return nil, err
	}

	out := &ui.SetupResult{
		HostPort:    res.HostPort,
		Fingerprint: res.Fingerprint,
		Connected:   res.Connected,
		Username:    ip.Username,
	}
	if !res.Connected {
		return out, nil
	}

	cfg, _ := loadConfig()
	name, prof, ok := profileForServer(cfg, res.Server)
	if ok {
		out.Profile = name
		out.Token = prof.Token
	}
	if up, err := url.Parse(res.Server); err == nil {
		out.Upstream = up
	}
	// transportFor 需要配置里那份指纹(https 地址没有指纹会报错);按它的要求,
	// 指纹随 connectAndSave 一起落进了配置档。
	if tr, err := transportFor(res.Server); err == nil {
		out.Transport = tr
	}
	return out, nil
}

// profileForServer 找 Server 地址等于 server 的配置档,拿它的名字与 token。
func profileForServer(cfg Config, server string) (string, Profile, bool) {
	for name, p := range cfg.Profiles {
		if p.Server == server {
			return name, p, true
		}
	}
	return "", Profile{}, false
}

// profilesRunner 把页面的配置档列表/切换接到配置文件上:列出 cfg.Profiles,
// 切换时用 loadConfig / cfg.pick / transportFor 解析出转发要用的东西。
type profilesRunner struct{}

// List:返回配置档的名字与地址,current 先按配置文件里的 current 打勾。指纹是要
// 给人核对的信息(等价 mpx status 里的「证书指纹」),一并带上;token 绝不进返回值。
func (profilesRunner) List() ([]ui.ProfileInfo, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	out := make([]ui.ProfileInfo, 0, len(cfg.Profiles))
	for name, p := range cfg.Profiles {
		out = append(out, ui.ProfileInfo{Name: name, Server: p.Server, Current: name == cfg.Current,
			Fingerprint: p.Fingerprint})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Resolve:把档名解析成转发用的 upstream/token/transport。做法同 SetupRunner:
// 地址用 url.Parse,传输用 transportFor(https 需要配置里那份指纹)。
func (profilesRunner) Resolve(name string) (*url.URL, string, http.RoundTripper, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, "", nil, err
	}
	p, err := cfg.pick(name)
	if err != nil {
		return nil, "", nil, err
	}
	if p.Server == "" {
		return nil, "", nil, fmt.Errorf("配置档 %s 没有控制面地址", name)
	}
	up, err := url.Parse(p.Server)
	if err != nil || (up.Scheme != "http" && up.Scheme != "https") || up.Host == "" {
		return nil, "", nil, fmt.Errorf("控制面地址 %q 不是 http(s) URL", p.Server)
	}
	// 该档自己存了指纹就只认它:transportFor 取指纹看的是 --profile/env/current,
	// 切换到的档未必是它们,用错指纹会连错或连不上。没存指纹(http 地址)才退回 transportFor。
	var tr http.RoundTripper
	if p.Fingerprint != "" {
		tr = pin.Transport(p.Fingerprint)
	} else if tr, err = transportFor(p.Server); err != nil {
		return nil, "", nil, err
	}
	return up, p.Token, tr, nil
}

// setupInitParams 把页面表单补成一份 initParams:缺省值跟 mpx init 的 flag 默认一致。
func setupInitParams(p ui.SetupParams) (*initParams, error) {
	host := strings.TrimSpace(p.Host)

	sshUser := strings.TrimSpace(p.SSHUser)
	if sshUser == "" {
		sshUser = "root"
	}
	sshPort := p.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}

	addr := strings.TrimSpace(p.Addr)
	if addr == "" {
		addr = "0.0.0.0:8080"
	}
	apiPort, err := setupAPIPort(addr)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(p.Name)
	if name == "" {
		name = "default"
	}
	username := strings.TrimSpace(p.Username)
	if username == "" {
		username = "user1"
	}
	sni := strings.TrimSpace(p.SNI)
	if sni == "" {
		sni = bootstrapSNI
	}

	return &initParams{
		FromUI:         true,
		Host:           host,
		SSHUser:        sshUser,
		SSHPort:        sshPort,
		Password:       p.Password,
		Identity:       strings.TrimSpace(p.Identity),
		Addr:           addr,
		APIPort:        apiPort,
		Name:           name,
		Username:       username,
		SNI:            sni,
		RealityDest:    bootstrapRealityDest,
		SubPrefix:      strings.TrimSpace(p.SubPrefix),
		DevicePassword: p.DevicePassword,
	}, nil
}

// setupAPIPort 从监听地址里取端口。地址写坏了就报「监听地址要写成 地址:端口」。
func setupAPIPort(addr string) (int, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("监听地址要写成 地址:端口: %w", err)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("监听地址要写成 地址:端口: 端口不合法 %q", port)
	}
	return p, nil
}

// setupProgress 把 initProgress 的进度转成页面的 Step/Line。
type setupProgress struct {
	p   ui.SetupProgress
	out *lineWriter
	err *lineWriter
}

func newSetupProgress(p ui.SetupProgress) *setupProgress {
	return &setupProgress{p: p, out: &lineWriter{emit: p.Line}, err: &lineWriter{emit: p.Line}}
}

func (sp *setupProgress) Step(format string, a ...any) { sp.p.Step(fmt.Sprintf(format, a...)) }
func (sp *setupProgress) Note(format string, a ...any) { sp.p.Line("    " + fmt.Sprintf(format, a...)) }
func (sp *setupProgress) Stdout() io.Writer            { return sp.out }
func (sp *setupProgress) Stderr() io.Writer            { return sp.err }

// lineWriter 按行切分写入的内容:未完的半行留在缓冲里,等下一次写补上。
type lineWriter struct {
	emit func(string)
	buf  []byte
}

func (w *lineWriter) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		w.emit(line)
	}
	return len(b), nil
}

// Flush 把末尾没有换行的那半行也交出去。
func (w *lineWriter) Flush() {
	if len(w.buf) == 0 {
		return
	}
	line := strings.TrimSuffix(string(w.buf), "\r")
	w.buf = nil
	w.emit(line)
}
