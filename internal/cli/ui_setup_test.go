package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/ui"
)

func TestSetupInitParamsDefaults(t *testing.T) {
	ip, err := setupInitParams(ui.SetupParams{Host: " 203.0.113.10 "})
	if err != nil {
		t.Fatalf("setupInitParams: %v", err)
	}
	if ip.Host != "203.0.113.10" {
		t.Errorf("Host = %q", ip.Host)
	}
	if ip.SSHUser != "root" {
		t.Errorf("SSHUser = %q", ip.SSHUser)
	}
	if ip.SSHPort != 22 {
		t.Errorf("SSHPort = %d", ip.SSHPort)
	}
	if ip.Addr != "0.0.0.0:8080" {
		t.Errorf("Addr = %q", ip.Addr)
	}
	if ip.APIPort != 8080 {
		t.Errorf("APIPort = %d", ip.APIPort)
	}
	if ip.Name != "default" {
		t.Errorf("Name = %q", ip.Name)
	}
	if ip.Username != "user1" {
		t.Errorf("Username = %q", ip.Username)
	}
	if ip.SNI != bootstrapSNI {
		t.Errorf("SNI = %q,want %q", ip.SNI, bootstrapSNI)
	}
	if ip.RealityDest != bootstrapRealityDest {
		t.Errorf("RealityDest = %q,want %q", ip.RealityDest, bootstrapRealityDest)
	}
}

func TestSetupInitParamsCustom(t *testing.T) {
	ip, err := setupInitParams(ui.SetupParams{
		Host:      "example.net",
		SSHUser:   "admin",
		SSHPort:   2222,
		Name:      "edge",
		Username:  "alice",
		Addr:      "127.0.0.1:9090",
		SNI:       "www.example.com",
		SubPrefix: "https://sub.example.net",
	})
	if err != nil {
		t.Fatalf("setupInitParams: %v", err)
	}
	if ip.APIPort != 9090 {
		t.Errorf("APIPort = %d", ip.APIPort)
	}
	if ip.SSHUser != "admin" || ip.SSHPort != 2222 {
		t.Errorf("SSH = %s:%d", ip.SSHUser, ip.SSHPort)
	}
	if ip.Name != "edge" || ip.Username != "alice" {
		t.Errorf("Name/Username = %q/%q", ip.Name, ip.Username)
	}
	if ip.SNI != "www.example.com" {
		t.Errorf("SNI = %q", ip.SNI)
	}
	if ip.SubPrefix != "https://sub.example.net" {
		t.Errorf("SubPrefix = %q", ip.SubPrefix)
	}
}

func TestSetupAPIPortError(t *testing.T) {
	_, err := setupInitParams(ui.SetupParams{Host: "h", Addr: "0.0.0.0"})
	if err == nil {
		t.Fatal("地址没有端口时应当报错")
	}
	if !strings.Contains(err.Error(), "监听地址要写成 地址:端口") {
		t.Errorf("错误文案 = %q", err.Error())
	}
}

func TestLineWriterSplitsLines(t *testing.T) {
	var got []string
	w := &lineWriter{emit: func(s string) { got = append(got, s) }}

	if _, err := w.Write([]byte("part")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := w.Write([]byte("ial\nsecond\nth")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := w.Write([]byte("ird")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	w.Flush()

	want := []string{"partial", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("收到 %d 行 %q,want %q", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 行 = %q,want %q", i, got[i], want[i])
		}
	}
}

// fakeSetupProgress 收集向导收到的步骤与输出行。
type fakeSetupProgress struct {
	steps []string
	lines []string
}

func (f *fakeSetupProgress) Step(title string) { f.steps = append(f.steps, title) }
func (f *fakeSetupProgress) Line(text string)  { f.lines = append(f.lines, text) }

func hasStep(steps []string, sub string) bool {
	for _, s := range steps {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func hasLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// setupTestEnv 把配置目录指到临时目录,并换上假的远端与接入实现。
func setupTestEnv(t *testing.T, fake *fakeRemote, token string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("MPX_DEVICE_PASSWORD", "device-password-1234")

	oldSSH, oldConnect := initSSH, initConnect
	t.Cleanup(func() { initSSH, initConnect = oldSSH, oldConnect })
	initSSH = fake
	initConnect = func(server, fp, password, name string) (*connectResult, error) {
		if _, err := saveProfile(Profile{Server: server, Token: token, Fingerprint: fp}); err != nil {
			return nil, err
		}
		return &connectResult{DeviceID: 1, Name: name, Token: token}, nil
	}
}

func TestSetupRunnerCheck(t *testing.T) {
	fake := &fakeRemote{osName: "Linux", arch: "x86_64", uid: "0", fetch: "/usr/bin/curl"}
	setupTestEnv(t, fake, "tok-check")

	info, err := setupRunner{}.Check(context.Background(), ui.SetupParams{
		Host:     "203.0.113.10",
		Password: "vps-secret",
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if info["uid"] != "0" {
		t.Errorf("uid = %q,want 0", info["uid"])
	}
}

func TestSetupRunnerRun(t *testing.T) {
	fake := &fakeRemote{
		osName:      "Linux",
		arch:        "x86_64",
		uid:         "0",
		fetch:       "/usr/bin/curl",
		bootstrapOK: true,
		fp:          validFP,
	}
	setupTestEnv(t, fake, "tok-run")

	prog := &fakeSetupProgress{}
	res, err := setupRunner{}.Run(context.Background(), ui.SetupParams{
		Host:           "203.0.113.10",
		DevicePassword: "device-password-1234",
	}, prog)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !hasStep(prog.steps, "看清这台机器") {
		t.Errorf("步骤里没有「看清这台机器」: %q", prog.steps)
	}
	if !hasStep(prog.steps, "在 VPS 上装控制面") {
		t.Errorf("步骤里没有「在 VPS 上装控制面」: %q", prog.steps)
	}
	if !hasLine(prog.lines, "fake install line") {
		t.Errorf("输出里没有 fake install line: %q", prog.lines)
	}
	if !res.Connected {
		t.Error("Connected = false")
	}
	if res.Upstream == nil || res.Upstream.Host == "" {
		t.Errorf("Upstream = %v", res.Upstream)
	}
	if res.Token != "tok-run" {
		t.Errorf("Token = %q,want tok-run", res.Token)
	}
}

func TestProfilesRunnerListAndResolve(t *testing.T) {
	path := useTempConfig(t)
	writeRaw(t, path, `{"current":"prod","profiles":{
		"prod":{"server":"https://cp.example.net","token":"prod-token","fingerprint":"`+validFP+`"},
		"dev":{"server":"http://127.0.0.1:9001","token":"dev-token"}
	}}`)

	list, err := profilesRunner{}.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 || list[0].Name != "dev" || list[1].Name != "prod" {
		t.Fatalf("列表应按名字排序: %+v", list)
	}
	if list[1].Current != true || list[0].Current {
		t.Fatalf("current 应标在 prod: %+v", list)
	}
	if list[1].Server != "https://cp.example.net" {
		t.Fatalf("地址不对: %+v", list[1])
	}

	// Resolve 不应把 token/指纹暴露给调用方之外的任何输出;这里只验证转发要素。
	up, token, tr, err := profilesRunner{}.Resolve("dev")
	if err != nil {
		t.Fatalf("Resolve dev: %v", err)
	}
	if up.String() != "http://127.0.0.1:9001" || token != "dev-token" || tr == nil {
		t.Fatalf("dev 解析不对: %v %q %v", up, token, tr)
	}
	if _, _, _, err := (profilesRunner{}).Resolve("nope"); err == nil {
		t.Fatal("不存在的档应报错")
	}
}
