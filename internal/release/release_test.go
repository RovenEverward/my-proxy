package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tempKey 造一把只在测试里用的临时密钥。
func tempKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.StdEncoding.EncodeToString(der)
}

func sumsFor(files map[string][]byte) []byte {
	var b strings.Builder
	for name, data := range files {
		h := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}
	return []byte(b.String())
}

func TestCheck(t *testing.T) {
	priv, pub := tempKey(t)
	_, other := tempKey(t)
	archive := []byte("tarball")
	sums := sumsFor(map[string][]byte{"mpx_linux_amd64.tar.gz": archive, "mpx_darwin_arm64.tar.gz": []byte("x")})
	sig := ed25519.Sign(priv, sums)

	if err := Check(sums, sig, []string{pub}, "mpx_linux_amd64.tar.gz", archive); err != nil {
		t.Fatalf("正常发布件应通过: %v", err)
	}
	if err := Check(sums, sig, []string{other, pub}, "mpx_linux_amd64.tar.gz", archive); err != nil {
		t.Fatalf("轮换期间任一把公钥能验过即可: %v", err)
	}
	cases := map[string]func() error{
		"发布件被改": func() error { return Check(sums, sig, []string{pub}, "mpx_linux_amd64.tar.gz", []byte("tarbalL")) },
		"SHA256SUMS 被改": func() error {
			s := append([]byte{}, sums...)
			s[0] ^= 1
			return Check(s, sig, []string{pub}, "mpx_linux_amd64.tar.gz", archive)
		},
		"别人签的":  func() error { return Check(sums, sig, []string{other}, "mpx_linux_amd64.tar.gz", archive) },
		"签名截断":  func() error { return Check(sums, sig[:10], []string{pub}, "mpx_linux_amd64.tar.gz", archive) },
		"清单里没有": func() error { return Check(sums, sig, []string{pub}, "mpx_windows_amd64.zip", archive) },
		"没配公钥":  func() error { return Verify(sums, sig, []string{}) },
	}
	for name, f := range cases {
		if f() == nil {
			t.Errorf("%s:应当拒绝", name)
		}
	}
}

// TestPlaceholderKeyIsNotTrusted:正式公钥配置之前,内置的公钥表是空的,任何签名都不算数。
func TestPlaceholderKeyIsNotTrusted(t *testing.T) {
	if len(PublicKeys) == 0 {
		if err := Verify([]byte("x"), make([]byte, 64), nil); err != ErrNoKey {
			t.Fatalf("占位状态应报 ErrNoKey,得到 %v", err)
		}
	}
	for _, k := range PublicKeys {
		if _, err := ParseKey(k); err != nil {
			t.Fatalf("PublicKeys 里有坏公钥: %v", err)
		}
	}
}

var block = regexp.MustCompile(`(?s)\n# >>> verify_release.*?# <<< verify_release\n`)
var scriptKey = regexp.MustCompile(`(?m)^MPX_RELEASE_PUBKEY="(.*)"$`)

func readScript(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestScriptsCarrySameKeys:两个安装脚本里的校验函数逐字相同,公钥与 PublicKeys 一致。
// 公钥这个事实有三处副本(curl | bash 跑的脚本没法引用仓库里的文件),靠这条测试绑住。
func TestScriptsCarrySameKeys(t *testing.T) {
	a, b := block.FindString(readScript(t, "install.sh")), block.FindString(readScript(t, "install-cli.sh"))
	if a == "" || a != b {
		t.Fatal("install.sh 与 install-cli.sh 的 verify_release 段不一致(或找不到)")
	}
	m := scriptKey.FindStringSubmatch(a)
	if m == nil {
		t.Fatal("脚本里找不到 MPX_RELEASE_PUBKEY")
	}
	switch {
	case m[1] == "" && len(PublicKeys) == 0:
	case m[1] != "" && len(PublicKeys) > 0 && m[1] == PublicKeys[0]:
	default:
		t.Fatalf("脚本公钥 %q 与 PublicKeys[0] 不一致", m[1])
	}
}

// TestScriptVerify 把脚本里那段 verify_release 原样拿出来跑:签名用 openssl 生成,
// 和 release.yml 用的是同一条命令,同时证明 openssl 的签名格式与 Go 的 ed25519 互通。
func TestScriptVerify(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("没有 openssl")
	}
	if out, _ := exec.Command("openssl", "version").Output(); !strings.HasPrefix(string(out), "OpenSSL 3") {
		t.Skipf("要 OpenSSL 3: %s", out)
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "k.pem")
	run(t, dir, "openssl genpkey -algorithm ed25519 -out "+key)
	pub := strings.TrimSpace(run(t, dir, "openssl pkey -in "+key+" -pubout | sed '1d;$d' | tr -d '\\n'"))

	fn := scriptKey.ReplaceAllString(block.FindString(readScript(t, "install-cli.sh")), `MPX_RELEASE_PUBKEY="`+pub+`"`)
	nokey := block.FindString(readScript(t, "install-cli.sh"))

	// 发布目录:releases/download/v1/{archive,SHA256SUMS,SHA256SUMS.sig},另有一个没有清单的旧版本 v0。
	rel := filepath.Join(dir, "releases")
	mk := func(ver string, signed bool) {
		d := filepath.Join(rel, "download", ver)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "a.tar.gz"), []byte("archive-"+ver), 0o644)
		if signed {
			run(t, d, "sha256sum a.tar.gz > SHA256SUMS && openssl pkeyutl -sign -inkey "+key+" -rawin -in SHA256SUMS -out SHA256SUMS.sig")
		}
	}
	mk("v0", false)
	mk("v1", true)

	// Go 侧也要能验 openssl 签出来的东西。
	sums, _ := os.ReadFile(filepath.Join(rel, "download/v1/SHA256SUMS"))
	sig, _ := os.ReadFile(filepath.Join(rel, "download/v1/SHA256SUMS.sig"))
	if err := Check(sums, sig, []string{pub}, "a.tar.gz", []byte("archive-v1")); err != nil {
		t.Fatalf("Go 验不过 openssl 的签名: %v", err)
	}

	harness := func(f, ver, file, env string) (string, error) {
		sh := `set -euo pipefail
die() { printf '错误:%s\n' "$1" >&2; exit 1; }
note() { printf '  %s\n' "$1"; }
FETCH="curl -fsSL"; DOWNLOAD_BASE="file://` + rel + `"; VERSION="` + ver + `"; TMP="$(mktemp -d)"
` + f + `
verify_release "` + file + `" a.tar.gz && echo PASSED`
		c := exec.Command("bash", "-c", sh)
		c.Env = append(os.Environ(), env)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	good := filepath.Join(rel, "download/v1/a.tar.gz")
	bad := filepath.Join(dir, "bad.tar.gz")
	os.WriteFile(bad, []byte("archive-v2"), 0o644)
	old := filepath.Join(rel, "download/v0/a.tar.gz")

	cases := []struct {
		name, f, ver, file, env, want string
		ok                            bool
	}{
		{"签名与哈希都对", fn, "v1", good, "", "签名 ✓", true},
		{"发布件被换", fn, "v1", bad, "", "sha256 与 SHA256SUMS 不符", false},
		{"旧版本没有清单:提示后放行", fn, "v0", old, "", "早于签名发布", true},
		{"旧版本 + 严格模式:拒绝", fn, "v0", old, "MPX_REQUIRE_SIGNATURE=1", "MPX_REQUIRE_SIGNATURE=1", false},
		{"占位公钥:只验哈希", nokey, "v1", good, "", "还没配置签名公钥", true},
		{"占位公钥 + 严格模式:拒绝", nokey, "v1", good, "MPX_REQUIRE_SIGNATURE=1", "还没配置签名公钥", false},
	}
	for _, c := range cases {
		out, err := harness(c.f, c.ver, c.file, c.env)
		if (err == nil) != c.ok || !strings.Contains(out, c.want) {
			t.Errorf("%s:err=%v 输出=%s", c.name, err, out)
		}
	}

	// 签名被改 / 签名缺失:都要拒绝。
	v1 := filepath.Join(rel, "download/v1")
	orig, _ := os.ReadFile(filepath.Join(v1, "SHA256SUMS.sig"))
	tampered := append([]byte{}, orig...)
	tampered[0] ^= 1
	os.WriteFile(filepath.Join(v1, "SHA256SUMS.sig"), tampered, 0o644)
	if out, err := harness(fn, "v1", good, ""); err == nil || !strings.Contains(out, "签名不对") {
		t.Errorf("签名被改:err=%v 输出=%s", err, out)
	}
	os.Remove(filepath.Join(v1, "SHA256SUMS.sig"))
	if out, err := harness(fn, "v1", good, ""); err == nil || !strings.Contains(out, "没有 SHA256SUMS.sig") {
		t.Errorf("签名缺失:err=%v 输出=%s", err, out)
	}
}

func run(t *testing.T, dir, sh string) string {
	t.Helper()
	c := exec.Command("bash", "-c", sh)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", sh, err, out)
	}
	return string(out)
}
