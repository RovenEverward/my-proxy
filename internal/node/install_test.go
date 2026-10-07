package node

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 节点上那三个文件和控制面这份逐字节相同时不再重推。
//
// 「重装同一版」是这条命令最常见的用法(装第二台节点、换核心重装),而一个核心加
// 两份 geodata 接近 65MB —— 每次都全推一遍等于每次白等一次传输。
func TestInstallCoreSkipsFilesAlreadyOnNode(t *testing.T) {
	core, hashes := fakeCore(t)
	f := hashRunner(hashes)

	if err := InstallCore(t.Context(), f, installNode(), core); err != nil {
		t.Fatalf("装核心: %v", err)
	}
	for _, path := range []string{XrayBinPath + ".new", geodataPath("geoip.dat"), geodataPath("geosite.dat")} {
		if pushedTo(f.calls, path) {
			t.Errorf("节点上已经有一份一样的 %s,不该再推:%v", path, f.calls)
		}
	}
	// 文件不变不代表什么都不做:单元文件可能还是旧的,自启也得设。
	if !pushedTo(f.calls, unitPath()) {
		t.Errorf("单元文件该照写,实际:%v", f.calls)
	}
	if f.count("systemctl enable") == 0 {
		t.Errorf("该设开机自启,实际:%v", f.calls)
	}
}

// 只有核心变了:geodata 一个字节都不该动,而新核心仍要走完「先校验再顶替」。
func TestInstallCorePushesOnlyWhatChanged(t *testing.T) {
	core, hashes := fakeCore(t)
	hashes[XrayBinPath] = strings.Repeat("0", 64) // 节点上那份核心是别的版本
	f := hashRunner(hashes)

	if err := InstallCore(t.Context(), f, installNode(), core); err != nil {
		t.Fatalf("装核心: %v", err)
	}
	if !pushedTo(f.calls, XrayBinPath+".new") {
		t.Errorf("核心变了就该推新的,实际:%v", f.calls)
	}
	for _, path := range []string{geodataPath("geoip.dat"), geodataPath("geosite.dat")} {
		if pushedTo(f.calls, path) {
			t.Errorf("geodata 没变,不该推 %s:%v", path, f.calls)
		}
	}
	verify := indexContaining(f.calls, XrayBinPath+".new' -test -config")
	replace := indexContaining(f.calls, "mv -f '"+XrayBinPath+".new'")
	if verify < 0 || replace < 0 {
		t.Fatalf("换了核心就必须先校验再顶替,实际:%v", f.calls)
	}
	if verify > replace {
		t.Errorf("必须先校验再顶替(%d > %d):%v", verify, replace, f.calls)
	}
}

// 对不出 sha256 时退回「全推一遍」。
//
// 三种都算对不出来:节点上没装 sha256sum/shasum、文件不在、这次 ssh 就失败了。
// 少推一个文件只是多等一会儿,推错一个装不起来 —— 两者不对称,所以拿不准就推。
func TestInstallCorePushesEverythingWhenHashesAreUnknown(t *testing.T) {
	for name, reply := range map[string]func(string) (string, error){
		"节点上没有算 sha256 的工具": func(string) (string, error) { return "", nil },
		"查 sha256 这一步就失败了":  func(string) (string, error) { return "", errors.New("ssh 断了") },
		"回来的东西读不懂":          func(string) (string, error) { return "这不是哈希\n", nil },
	} {
		t.Run(name, func(t *testing.T) {
			core, _ := fakeCore(t)
			f := &fakeRunner{fn: func(script string) (string, error) {
				if strings.Contains(script, "sha256sum") {
					return reply(script)
				}
				return "", nil
			}}

			if err := InstallCore(t.Context(), f, installNode(), core); err != nil {
				t.Fatalf("装核心: %v", err)
			}
			for _, path := range []string{XrayBinPath + ".new", geodataPath("geoip.dat"), geodataPath("geosite.dat")} {
				if !pushedTo(f.calls, path) {
					t.Errorf("算不出哈希就该推 %s,实际:%v", path, f.calls)
				}
			}
		})
	}
}

func installNode() model.Node {
	return model.Node{Name: "hk-1", Address: "203.0.113.10"}
}

func geodataPath(name string) string {
	return filepath.Dir(XrayBinPath) + "/" + name
}

func unitPath() string {
	n := installNode()
	return "/etc/systemd/system/" + n.ServiceNameOrDefault() + ".service"
}

// fakeCore 在临时目录里造一套「发布包」,并给出节点上已经躺着同一套时该回的哈希表。
func fakeCore(t *testing.T) (Core, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatalf("写 %s: %v", name, err)
		}
		return p
	}
	core := Core{
		Version: "v26.3.27",
		Xray:    write("xray", "假的核心"),
		GeoIP:   write("geoip.dat", "假的 geoip"),
		GeoSite: write("geosite.dat", "假的 geosite"),
	}

	hashes := make(map[string]string, 3)
	for _, f := range []coreFile{
		{core.GeoIP, geodataPath("geoip.dat")},
		{core.GeoSite, geodataPath("geosite.dat")},
		{core.Xray, XrayBinPath},
	} {
		sum, err := fileSHA256(f.src)
		if err != nil {
			t.Fatalf("算 %s 的 sha256: %v", f.src, err)
		}
		hashes[f.dest] = sum
	}
	return core, hashes
}

// hashRunner 只回答「对 sha256」那次查询,其余脚本一律空回。
func hashRunner(hashes map[string]string) *fakeRunner {
	return &fakeRunner{fn: func(script string) (string, error) {
		if !strings.Contains(script, "sha256sum") {
			return "", nil
		}
		var b strings.Builder
		for dest, sum := range hashes {
			b.WriteString(dest + " " + sum + "\n")
		}
		return b.String(), nil
	}}
}

// pushedTo 报告有没有把内容推到某个路径上。PutFile 的脚本里带着目标路径,内容走 stdin。
func pushedTo(calls []string, path string) bool {
	return indexOfPath(calls, path) >= 0
}

func indexContaining(calls []string, sub string) int {
	for i, c := range calls {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

func indexOfPath(calls []string, path string) int {
	for i, c := range calls {
		if strings.Contains(c, "umask 077") && strings.Contains(c, path) {
			return i
		}
	}
	return -1
}
