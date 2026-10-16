package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
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
