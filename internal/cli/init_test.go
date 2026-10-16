package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
)

// fakeRemote 是 initRemote 的测试替身:只认 init 会发的那几条脚本,把它们的
// 返回值调成用例想要的样子,并把每次调用记下来。
type fakeRemote struct {
	calls []string

	osName      string
	arch        string
	uid         string
	fetch       string
	bootstrapOK bool
	fp          string
	setPWErr    error
	connectErr  error
}

func (f *fakeRemote) record(script string) { f.calls = append(f.calls, script) }

func (f *fakeRemote) EnsureKey(keyPath string) (string, error) {
	f.calls = append(f.calls, "EnsureKey "+keyPath)
	return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKEFA fake@test", nil
}

func (f *fakeRemote) Bootstrap(ctx context.Context, n model.Node, keyPath, pubkey, password string) error {
	f.calls = append(f.calls, "Bootstrap "+keyPath)
	return nil
}

func (f *fakeRemote) Run(ctx context.Context, n model.Node, a node.Auth, script string) (string, error) {
	f.record(script)
	switch {
	case strings.Contains(script, "uname -s"):
		return fmt.Sprintf("os=%s\narch=%s\nuid=%s\nfetch=%s\n",
			f.osName, f.arch, f.uid, f.fetch), nil
	case strings.Contains(script, "bootstrap --help"):
		if f.bootstrapOK {
			return "MPX_BOOTSTRAP_OK\n", nil
		}
		return "MPX_BOOTSTRAP_MISSING\n", nil
	case strings.Contains(script, "tls-fingerprint"):
		return f.fp, nil
	}
	return "", nil
}

func (f *fakeRemote) RunIO(ctx context.Context, n model.Node, a node.Auth, script string, stdout, stderr io.Writer) error {
	f.record(script)
	fmt.Fprint(stdout, "fake install line\n")
	return nil
}

func (f *fakeRemote) RunWithInput(ctx context.Context, n model.Node, a node.Auth, script string, stdin []byte) (string, error) {
	f.record(script)
	if f.setPWErr != nil {
		return "fake set password failed", f.setPWErr
	}
	return "", nil
}

func (f *fakeRemote) PutFile(ctx context.Context, n model.Node, a node.Auth, p string, content []byte) error {
	f.calls = append(f.calls, "PutFile "+p)
	return nil
}

// runInitCapture 用管道临时接管 os.Stdout/os.Stderr,跑一次 runInit,把两边
// 的输出和 error 一起交回来。所有被 init 直接摸的全局量都在这里摆好,并在
// 测试结束时还原。
func runInitCapture(t *testing.T, setup func()) (stdout, stderr string, err error) {
	t.Helper()

	oldStdout, oldStderr := os.Stdout, os.Stderr
	oldSSH, oldConnect := initSSH, initConnect
	oldFlags := initFlags
	t.Cleanup(func() {
		os.Stdout, os.Stderr = oldStdout, oldStderr
		initSSH, initConnect = oldSSH, oldConnect
		initFlags = oldFlags
	})

	// 先把 flag 摆回默认值,免得用例之间互相污染。
	initFlags = struct {
		host          string
		sshUser       string
		sshPort       int
		sshPassword   bool
		identity      string
		binary        string
		installScript string
		addr          string
		apiPort       int
		name          string
		username      string
		sni           string
		realityDest   string
		remark        string
		subPrefix     string
		password      string
	}{
		sshUser:     "root",
		sshPort:     22,
		addr:        "0.0.0.0:8080",
		name:        "default",
		username:    "user1",
		sni:         bootstrapSNI,
		realityDest: bootstrapRealityDest,
	}

	// 安装脚本指向本机临时文件,免得 uploadInstallScript 走到网络上。
	dir := t.TempDir()
	script := filepath.Join(dir, "install.sh")
	if werr := os.WriteFile(script, []byte("#!/bin/sh\n# fake\n"), 0o755); werr != nil {
		t.Fatalf("写临时安装脚本: %v", werr)
	}
	initFlags.installScript = script

	fake := &fakeRemote{osName: "Linux", arch: "x86_64", uid: "0", fetch: "/usr/bin/curl"}
	initSSH = fake
	initConnect = func(server, fp, password, name string) (*connectResult, error) {
		if fake.connectErr != nil {
			return nil, fake.connectErr
		}
		return &connectResult{DeviceID: 1, Name: name, Token: "fake-token"}, nil
	}

	setup()

	rOut, wOut, werr := os.Pipe()
	if werr != nil {
		t.Fatalf("建 stdout 管道: %v", werr)
	}
	rErr, wErr, werr := os.Pipe()
	if werr != nil {
		t.Fatalf("建 stderr 管道: %v", werr)
	}
	os.Stdout, os.Stderr = wOut, wErr

	type res struct{ b []byte }
	outCh := make(chan res, 1)
	errCh := make(chan res, 1)
	go func() { b, _ := io.ReadAll(rOut); outCh <- res{b} }()
	go func() { b, _ := io.ReadAll(rErr); errCh <- res{b} }()

	runErr := runInit()

	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout, os.Stderr = oldStdout, oldStderr

	stdout = strings.ReplaceAll(string((<-outCh).b), dir, "<TMPDIR>")
	stderr = strings.ReplaceAll(string((<-errCh).b), dir, "<TMPDIR>")
	return stdout, stderr, runErr
}

// goldenOf 把一次运行的 stdout/stderr/err/calls 拼成可比较的一段文本。
func goldenOf(stdout, stderr string, err error, fake *fakeRemote) string {
	var b strings.Builder
	b.WriteString("stdout:\n")
	b.WriteString(stdout)
	b.WriteString("stderr:\n")
	b.WriteString(stderr)
	fmt.Fprintf(&b, "err: %v\n", err)
	b.WriteString("calls:\n")
	for _, c := range fake.calls {
		b.WriteString(c)
		b.WriteString("\n---\n")
	}
	return b.String()
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "init-"+name+".golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("建 testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("写 golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 golden %s: %v(先跑 UPDATE_GOLDEN=1 go test ./internal/cli/)", path, err)
	}
	if got != string(want) {
		t.Errorf("golden 不符 %s:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// validFP 是 pin.Normalize 认得的指纹:43 个 base64 字符 = 32 字节。
var validFP = "SHA256:" + strings.Repeat("A", 43)

func setDevicePassword(t *testing.T, pw string) {
	t.Helper()
	if pw == "" {
		t.Setenv("MPX_DEVICE_PASSWORD", "")
		os.Unsetenv("MPX_DEVICE_PASSWORD")
		return
	}
	t.Setenv("MPX_DEVICE_PASSWORD", pw)
}

func TestInitRemoteGolden(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("golden 里的安装脚本路径用 Unix 分隔符,Windows 上对不上")
	}
	const devicePW = "device-password-1234"

	cases := []struct {
		name  string
		setup func(t *testing.T, fake *fakeRemote) func()
	}{
		{
			name: "success",
			setup: func(t *testing.T, fake *fakeRemote) func() {
				setDevicePassword(t, devicePW)
				fake.fp = validFP
				fake.bootstrapOK = true
				return func() {
					initFlags.host = "203.0.113.10"
					initFlags.password = "vps-secret"
				}
			},
		},
		{
			name: "uid-not-root",
			setup: func(t *testing.T, fake *fakeRemote) func() {
				setDevicePassword(t, "")
				fake.uid = "1000"
				return func() { initFlags.host = "203.0.113.10" }
			},
		},
		{
			name: "os-darwin",
			setup: func(t *testing.T, fake *fakeRemote) func() {
				setDevicePassword(t, "")
				fake.osName = "Darwin"
				fake.arch = "arm64"
				return func() { initFlags.host = "203.0.113.10" }
			},
		},
		{
			name: "bootstrap-missing",
			setup: func(t *testing.T, fake *fakeRemote) func() {
				setDevicePassword(t, devicePW)
				fake.bootstrapOK = false
				return func() { initFlags.host = "203.0.113.10" }
			},
		},
		{
			name: "fingerprint-empty",
			setup: func(t *testing.T, fake *fakeRemote) func() {
				setDevicePassword(t, devicePW)
				fake.fp = ""
				fake.bootstrapOK = true
				return func() { initFlags.host = "203.0.113.10" }
			},
		},
		{
			name: "no-device-password",
			setup: func(t *testing.T, fake *fakeRemote) func() {
				setDevicePassword(t, "")
				fake.fp = validFP
				fake.bootstrapOK = true
				return func() { initFlags.host = "203.0.113.10" }
			},
		},
		{
			name: "set-device-password-fails",
			setup: func(t *testing.T, fake *fakeRemote) func() {
				setDevicePassword(t, devicePW)
				fake.fp = validFP
				fake.bootstrapOK = true
				fake.setPWErr = fmt.Errorf("boom")
				return func() { initFlags.host = "203.0.113.10" }
			},
		},
		{
			name: "connect-fails",
			setup: func(t *testing.T, fake *fakeRemote) func() {
				setDevicePassword(t, devicePW)
				fake.fp = validFP
				fake.bootstrapOK = true
				fake.connectErr = fmt.Errorf("dial: connection refused")
				return func() { initFlags.host = "203.0.113.10" }
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var fake *fakeRemote
			stdout, stderr, err := runInitCapture(t, func() {
				fake = initSSH.(*fakeRemote)
				apply := tc.setup(t, fake)
				apply()
			})
			checkGolden(t, tc.name, goldenOf(stdout, stderr, err, fake))
		})
	}
}
