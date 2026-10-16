package node

import (
	"errors"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func upgradeNode() model.Node {
	return model.Node{Name: "hk-1", Address: "203.0.113.10", ServiceName: "mpx-xray"}
}

// 已经是目标版本就什么都不做:重启一次是瞬断,能省就省,更不该白下一遍包。
func TestUpgradeXrayAlreadyAtTargetDoesNothing(t *testing.T) {
	f := &fakeRunner{fn: func(script string) (string, error) {
		if strings.Contains(script, " version") {
			return "Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0\n", nil
		}
		return "", nil
	}}

	res, err := UpgradeXray(t.Context(), f, upgradeNode(), "v26.3.27",
		"https://example.com/Xray-linux-64.zip", strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("已是目标版本不该报错: %v", err)
	}
	if res.From != "26.3.27" || res.To != "v26.3.27" || res.RolledBack {
		t.Fatalf("结果不对: %+v", res)
	}
	if f.count("curl") != 0 || f.count("systemctl restart") != 0 {
		t.Fatalf("已是目标版本不该下载或重启,实际:%v", f.calls)
	}
}

// 成功路径:备份旧核心、顶替 .new、重启、轮询确认在跑目标版本。
func TestUpgradeXraySuccess(t *testing.T) {
	f := &fakeRunner{fn: func(script string) (string, error) {
		switch {
		case strings.Contains(script, "-test -config"):
			return "", nil
		case strings.Contains(script, "curl -fsSL"):
			return "", nil
		case strings.Contains(script, "is-active"):
			return "", nil
		case strings.Contains(script, " version"):
			return "Xray 26.3.26 (Xray, Penetrates Everything.)\n", nil
		}
		return "", nil
	}}

	res, err := UpgradeXray(t.Context(), f, upgradeNode(), "v26.3.27",
		"https://example.com/Xray-linux-64.zip", strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("升级: %v", err)
	}
	if res.From != "26.3.26" || res.To != "v26.3.27" || res.RolledBack {
		t.Fatalf("结果不对: %+v", res)
	}
	if got := f.count("systemctl restart"); got != 1 {
		t.Fatalf("成功升级应当只重启一次,实际 %d 次:%v", got, f.calls)
	}
	all := strings.Join(f.calls, "\n")
	if !strings.Contains(all, "cp -p '"+XrayBinPath+"' '"+XrayBinPath+".prev'") {
		t.Errorf("顶替前应当备份旧核心:%v", f.calls)
	}
	if !strings.Contains(all, "mv -f '"+XrayBinPath+".new' '"+XrayBinPath+"'") {
		t.Errorf("应当把 .new 顶上去:%v", f.calls)
	}
	if !strings.Contains(all, "curl -fsSL --retry 2 -o x.zip 'https://example.com/Xray-linux-64.zip'") {
		t.Errorf("应当让节点自己下载指定的资产:%v", f.calls)
	}
}

// 下载或 sha256 校验失败时,现有核心和 geodata 一个字节都不该动,也不重启。
func TestUpgradeXrayDownloadFailureLeavesFilesAlone(t *testing.T) {
	f := &fakeRunner{fn: func(script string) (string, error) {
		switch {
		case strings.Contains(script, "curl -fsSL"):
			return "curl: (22) 404", errors.New("exit status 3")
		case strings.Contains(script, " version"):
			return "Xray 26.3.26 (Xray, Penetrates Everything.)\n", nil
		}
		return "", nil
	}}

	res, err := UpgradeXray(t.Context(), f, upgradeNode(), "v26.3.27",
		"https://example.com/Xray-linux-64.zip", strings.Repeat("ab", 32))
	if err == nil {
		t.Fatal("下载/校验失败应当报错")
	}
	if res.RolledBack {
		t.Error("还没碰到现有文件,不该说回滚过")
	}
	if f.count("systemctl restart") != 0 {
		t.Fatalf("失败不该重启,实际:%v", f.calls)
	}
	if f.count("mv -f '"+XrayBinPath+".prev'") != 0 {
		t.Fatalf("失败不该动旧核心,实际:%v", f.calls)
	}
}

// 新核心通不过现有配置校验:删掉 .new、退回 geodata,不重启、也不动旧核心。
func TestUpgradeXrayRollsBackWhenNewCoreFailsConfigCheck(t *testing.T) {
	f := &fakeRunner{fn: func(script string) (string, error) {
		switch {
		case strings.Contains(script, "-test -config"):
			return "failed to parse config", errors.New("exit status 23")
		case strings.Contains(script, "curl -fsSL"):
			return "", nil
		case strings.Contains(script, " version"):
			return "Xray 26.3.26 (Xray, Penetrates Everything.)\n", nil
		}
		return "", nil
	}}

	res, err := UpgradeXray(t.Context(), f, upgradeNode(), "v26.3.27",
		"https://example.com/Xray-linux-64.zip", strings.Repeat("ab", 32))
	if err == nil {
		t.Fatal("新核心校验失败应当报错")
	}
	if !res.RolledBack {
		t.Error("校验失败应当标记回滚")
	}
	if f.count("systemctl restart") != 0 {
		t.Fatalf("校验失败不该重启,实际:%v", f.calls)
	}
	all := strings.Join(f.calls, "\n")
	if !strings.Contains(all, "rm -f '"+XrayBinPath+".new'") {
		t.Errorf("应当删掉没被采纳的新核心:%v", f.calls)
	}
	if !strings.Contains(all, "geoip.dat.prev") {
		t.Errorf("应当退回 geodata 备份:%v", f.calls)
	}
}

// 重启后服务没 active:把旧核心换回去、退回 geodata、再重启,并报「已回滚」。
func TestUpgradeXrayRollsBackWhenServiceNotActive(t *testing.T) {
	f := &fakeRunner{fn: func(script string) (string, error) {
		switch {
		case strings.Contains(script, "-test -config"):
			return "", nil
		case strings.Contains(script, "curl -fsSL"):
			return "", nil
		case strings.Contains(script, "is-active"):
			return "activating\n", errors.New("exit status 1")
		case strings.Contains(script, " version"):
			return "Xray 26.3.26 (Xray, Penetrates Everything.)\n", nil
		}
		return "", nil
	}}

	res, err := UpgradeXray(t.Context(), f, upgradeNode(), "v26.3.27",
		"https://example.com/Xray-linux-64.zip", strings.Repeat("ab", 32))
	if err == nil {
		t.Fatal("服务没起来应当报错")
	}
	if !res.RolledBack {
		t.Error("服务没起来应当标记回滚")
	}
	if !strings.Contains(err.Error(), "已回滚到 26.3.26") {
		t.Errorf("报错应当说清回到哪一版,实际: %v", err)
	}
	if got := f.count("systemctl restart"); got != 2 {
		t.Fatalf("应当重启两次(升级、回滚),实际 %d 次:%v", got, f.calls)
	}
	all := strings.Join(f.calls, "\n")
	if !strings.Contains(all, "mv -f '"+XrayBinPath+".prev' '"+XrayBinPath+"'") {
		t.Errorf("回滚应当把旧核心换回去:%v", f.calls)
	}
	if !strings.Contains(all, "geosite.dat.prev") {
		t.Errorf("回滚应当退回 geodata 备份:%v", f.calls)
	}
}
