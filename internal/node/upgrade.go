package node

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// UpgradeResult 是一次节点侧自升级的结果。
type UpgradeResult struct {
	From       string // 升级前节点上跑着的版本(不带 v)
	To         string // 目标版本,原样保留调用方给的写法
	RolledBack bool   // 是否因为失败回滚过
}

// UpgradeXray 让节点自己去 GitHub 拉一份指定版本的 xray 并原地替换,失败回滚。
//
// 和 InstallCore 的方向相反:那条路是控制面把 65MB 推下去,适合节点连不上外网、
// 或者一次装一批机器;这条路控制面只递 URL 和 sha256,下载、解包、替换全在节点
// 上发生,适合节点自己能连 GitHub、而控制面到节点这条链路窄或按流量计费的场景。
//
// 顺序刻意做得和 InstallCore 一样保守:新核心先落成 .new,拿节点上现有的配置验
// 一遍,通过了才备份旧核心并顶替。区别是这里多一层回滚:顶替后服务没起来就把
// .prev 换回去,节点不会停在一个跑不动的核心上。
//
// 调用方要先取好版本号、资产名和摘要(见 internal/xrayrelease 的 Latest、AssetName、
// Digest),这里只负责把给定的东西装上去。
func UpgradeXray(ctx context.Context, r Runner, n model.Node, version, assetURL, sha256 string) (UpgradeResult, error) {
	result := UpgradeResult{To: version}

	// a. 先问节点自己现在跑的是哪一版。已经是目标版本就什么都不做:重启一次是瞬断,
	//    能省就省。
	out, err := r.Run(ctx, n, shellQuote(XrayBinPath)+" version | head -1")
	if err != nil {
		return result, fmt.Errorf("读节点 %s 上 xray 的版本: %w", n.Name, err)
	}
	result.From = xrayVersionField(out)
	if sameVersion(result.From, version) {
		return result, nil
	}

	// b. 一条脚本把下载、校验、解包、落盘一次做完,中途失败不会碰到正在跑的核心。
	if _, err := r.Run(ctx, n, upgradeDownloadScript(assetURL, sha256)); err != nil {
		return result, fmt.Errorf("在节点 %s 上拉取 xray %s: %w", n.Name, version, err)
	}

	// c. 新核心通不过现有配置的校验就退回旧 geodata,核心一个字节没动、也不重启。
	if err := verifyNewCore(ctx, r, n, XrayBinPath+".new"); err != nil {
		_, _ = r.Run(ctx, n, cleanupNewScript())
		result.RolledBack = true
		return result, err
	}

	// d. 备份、顶替、重启、确认。任何一步失败都走 e。
	if err := activateXray(ctx, r, n, version); err != nil {
		_, _ = r.Run(ctx, n, rollbackScript())
		if rerr := Restart(ctx, r, n); rerr != nil {
			return result, fmt.Errorf("升级 xray 失败,回滚时重启服务也没成功(%v),已回滚到 %s: %w",
				rerr, result.From, err)
		}
		result.RolledBack = true
		return result, fmt.Errorf("升级 xray 失败,已回滚到 %s: %w", result.From, err)
	}
	return result, nil
}

// activateXray 是 d 步:备份旧核心、把 .new 顶上去、重启、确认真的在跑目标版本。
func activateXray(ctx context.Context, r Runner, n model.Node, version string) error {
	if _, err := r.Run(ctx, n, "cp -p "+shellQuote(XrayBinPath)+" "+shellQuote(XrayBinPath+".prev")); err != nil {
		return fmt.Errorf("备份当前核心: %w", err)
	}
	if _, err := r.Run(ctx, n, "mv -f "+shellQuote(XrayBinPath+".new")+" "+shellQuote(XrayBinPath)); err != nil {
		return fmt.Errorf("替换核心: %w", err)
	}
	if err := Restart(ctx, r, n); err != nil {
		return fmt.Errorf("重启 xray: %w", err)
	}
	if _, err := r.Run(ctx, n, upgradePollScript(n, version)); err != nil {
		return fmt.Errorf("重启后 xray 没能在 15 秒内起来并报出目标版本: %w", err)
	}
	return nil
}

// xrayVersionField 从 `xray version` 的第一行里取版本号。
//
// 原始输出是 "Xray 26.3.27 (Xray, Penetrates Everything.) ...",第二个字段才是
// 版本;取不到就回空串,调用方据此当成「认不出版本」而不是误判成已是最新。
func xrayVersionField(out string) string {
	line := out
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		line = out[:i]
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return strings.TrimPrefix(fields[1], "v")
}

// sameVersion 比较两个版本号,忽略前导 v 和空白。
func sameVersion(a, b string) bool {
	a = strings.TrimPrefix(strings.TrimSpace(a), "v")
	b = strings.TrimPrefix(strings.TrimSpace(b), "v")
	return a != "" && a == b
}

// upgradeDownloadScript 是 b 步那条脚本:下载、sha256 校验、解包、落盘。
//
// 校验不过 exit 3,没有解包工具 exit 4 —— 都用不同的退出码,调用方(和看日志的
// 人)一眼能分出是包坏了还是机器上缺东西。geodata 先各自备份成 .prev 再覆盖,
// 回滚时才有东西可恢复。
func upgradeDownloadScript(assetURL, sha256 string) string {
	dir := path.Dir(XrayBinPath)
	try := "cp -f xray " + shellQuote(XrayBinPath+".new") + "\nchmod 755 " + shellQuote(XrayBinPath+".new") + "\n"
	try += upgradeGeodataScript("geoip.dat", path.Join(dir, "geoip.dat"))
	try += upgradeGeodataScript("geosite.dat", path.Join(dir, "geosite.dat"))

	return fmt.Sprintf(`set -e
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cd "$tmp"
curl -fsSL --retry 2 -o x.zip %s
if command -v sha256sum >/dev/null 2>&1; then
  printf '%%s  %%s\n' %s x.zip | sha256sum -c - >/dev/null 2>&1 || exit 3
elif command -v shasum >/dev/null 2>&1; then
  printf '%%s  %%s\n' %s x.zip | shasum -a 256 -c - >/dev/null 2>&1 || exit 3
else
  exit 3
fi
if command -v unzip >/dev/null 2>&1; then
  unzip -o -q x.zip
elif command -v python3 >/dev/null 2>&1; then
  python3 -m zipfile -e x.zip .
else
  exit 4
fi
[ -f xray ] || exit 5
%s`, shellQuote(assetURL), shellQuote(sha256), shellQuote(sha256), try)
}

// upgradeGeodataScript 把解出来的 src 覆盖到 dest,覆盖前先备份 dest.prev。
func upgradeGeodataScript(src, dest string) string {
	return fmt.Sprintf(`if [ -f %s ]; then
  if [ -f %s ]; then cp -p %s %s; fi
  cp -f %s %s
fi
`, shellQuote(src), shellQuote(dest), shellQuote(dest), shellQuote(dest+".prev"), shellQuote(src), shellQuote(dest))
}

// restoreGeodataScript 把两份 geodata 的 .prev 放回去;没有备份的机器上静默跳过。
func restoreGeodataScript() string {
	dir := path.Dir(XrayBinPath)
	return "mv -f " + shellQuote(path.Join(dir, "geoip.dat")+".prev") + " " + shellQuote(path.Join(dir, "geoip.dat")) + " 2>/dev/null || true; " +
		"mv -f " + shellQuote(path.Join(dir, "geosite.dat")+".prev") + " " + shellQuote(path.Join(dir, "geosite.dat")) + " 2>/dev/null || true"
}

// cleanupNewScript 是 c 步失败后的收尾:删掉没被采纳的新核心,退回旧 geodata。
func cleanupNewScript() string {
	return "rm -f " + shellQuote(XrayBinPath+".new") + "; " + restoreGeodataScript()
}

// rollbackScript 是 e 步的回滚:旧核心换回原位,geodata 回到升级前,再由调用方重启。
func rollbackScript() string {
	return "mv -f " + shellQuote(XrayBinPath+".prev") + " " + shellQuote(XrayBinPath) + "; " + restoreGeodataScript()
}

// upgradePollScript 在节点上原地轮询最多 15 秒,确认服务 active 且报的是目标版本。
//
// 放在节点上是一个循环而不是 15 次 ssh:Type=simple 的服务在 systemctl 返回时只是
// 刚 fork 出来,进程随后退出要过一瞬才反映到状态;与其隔着网络问十几遍,不如让
// 节点自己看,控制面只等一个结论。
func upgradePollScript(n model.Node, version string) string {
	bare := strings.TrimPrefix(strings.TrimSpace(version), "v")
	return fmt.Sprintf(`i=0
while [ "$i" -lt 15 ]; do
  if [ "$(systemctl is-active %s || true)" = active ] && %s version 2>/dev/null | head -1 | grep -Fq -- %s; then
    exit 0
  fi
  i=$((i+1))
  sleep 1
done
exit 1
`, shellQuote(n.ServiceNameOrDefault()), shellQuote(XrayBinPath), shellQuote(bare))
}
