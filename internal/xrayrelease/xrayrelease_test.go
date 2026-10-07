package xrayrelease

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAssetName(t *testing.T) {
	cases := []struct {
		goos, goarch string
		want         string
	}{
		{"linux", "amd64", "Xray-linux-64.zip"},
		{"linux", "arm64", "Xray-linux-arm64-v8a.zip"},
		{"linux", "arm", "Xray-linux-arm32-v7a.zip"},
		{"linux", "386", "Xray-linux-32.zip"},
		{"darwin", "arm64", "Xray-macos-arm64-v8a.zip"},
	}
	for _, c := range cases {
		got, err := AssetName(c.goos, c.goarch)
		if err != nil {
			t.Errorf("AssetName(%s, %s): %v", c.goos, c.goarch, err)
			continue
		}
		if got != c.want {
			t.Errorf("AssetName(%s, %s) = %s, want %s", c.goos, c.goarch, got, c.want)
		}
	}

	// 认不出的平台必须报错而不是猜一个:核心装错架构,节点上是一启动就退,
	// 而报错里带着平台名,使用者一眼能看出是哪儿不支持。
	if _, err := AssetName("plan9", "mips"); err == nil {
		t.Fatal("plan9/mips 不该被当成能装的平台")
	}
}

// fakeGitHub 起一个假的 GitHub:给出 latest 的发布信息,并按要求发 zip 或报错。
type fakeGitHub struct {
	*httptest.Server
	asset    string
	zip      []byte
	download atomic.Int32 // 真的被下载了几次
	dlPaths  chan string  // 每次下载的路径
}

func newFakeGitHub(t *testing.T, asset string, zipBytes []byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{asset: asset, zip: zipBytes, dlPaths: make(chan string, 8)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v9.9.9",
				"assets": []map[string]string{
					{"name": f.asset, "browser_download_url": f.URL + "/dl/" + f.asset},
				},
			})
		case strings.HasPrefix(r.URL.Path, "/dl/"), strings.Contains(r.URL.Path, "/releases/download/"):
			f.download.Add(1)
			f.dlPaths <- r.URL.Path
			_, _ = w.Write(f.zip)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)

	oldAPI, oldDL := apiBase, downloadBase
	apiBase, downloadBase = f.URL, f.URL
	t.Cleanup(func() { apiBase, downloadBase = oldAPI, oldDL })
	return f
}

// makeZip 造一个内容像模像样的发布包。
func makeZip(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("造 zip: %v", err)
		}
		if _, err := io.WriteString(w, "内容:"+name); err != nil {
			t.Fatalf("写 zip: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("收尾 zip: %v", err)
	}
	return buf.Bytes()
}

// 取一次发布要把三个文件按正确的权限落地;同一个版本再取一次不该重新下载。
func TestFetchTakesLatestAndReusesCache(t *testing.T) {
	// 包里带上不该被取出来的文件,确认我们只挑要的那几个。
	gh := newFakeGitHub(t, "Xray-linux-64.zip",
		makeZip(t, fileXray, fileGeoIP, fileGeoSite, "LICENSE", "README.md"))

	dir := t.TempDir()
	req := Request{OS: "linux", Arch: "amd64", Dir: dir}

	first, err := Fetch(t.Context(), req)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if first.Version != "v9.9.9" {
		t.Fatalf("版本应当是 latest 拿到的 v9.9.9,实际 %q", first.Version)
	}
	for _, p := range []string{first.Xray, first.GeoIP, first.GeoSite} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("读 %s: %v", p, err)
		}
		if want := "内容:" + filepath.Base(p); string(got) != want {
			t.Errorf("%s 的内容是 %q,want %q", p, got, want)
		}
	}
	// 核心必须可执行:它在节点上是直接起进程的,-test 也靠它。
	st, err := os.Stat(first.Xray)
	if err != nil {
		t.Fatalf("看 %s: %v", first.Xray, err)
	}
	if st.Mode().Perm() != 0o755 {
		t.Errorf("核心的权限是 %v,want 0755", st.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(first.Xray), "LICENSE")); !os.IsNotExist(err) {
		t.Error("LICENSE 不该被解到缓存目录里")
	}

	// 第二次:同一个版本加平台,不该再下一遍。
	second, err := Fetch(t.Context(), req)
	if err != nil {
		t.Fatalf("第二次 Fetch: %v", err)
	}
	if second != first {
		t.Fatalf("第二次应当拿到同一批文件:\n got %+v\nwant %+v", second, first)
	}
	if n := gh.download.Load(); n != 1 {
		t.Fatalf("第二次不该重新下载,实际一共下载 %d 次", n)
	}
}

// 指定版本时不问 GitHub 接口:下载地址是能直接拼出来的,少一次请求,也不会被限流。
func TestFetchPinnedVersionDoesNotAskAPI(t *testing.T) {
	gh := newFakeGitHub(t, "Xray-linux-64.zip", makeZip(t, fileXray, fileGeoIP, fileGeoSite))
	// 接口一被打到就让测试失败:指定版本本来就不该问它。
	apiBase = gh.URL + "/不该被请求"

	// 版本号写成不带 v 的形式,落地的目录与下载地址都要补上 v。
	files, err := Fetch(t.Context(), Request{Version: "1.2.3", OS: "linux", Arch: "amd64", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if files.Version != "v1.2.3" {
		t.Fatalf("版本应当是 v1.2.3,实际 %q", files.Version)
	}
	got := <-gh.dlPaths
	want := "/XTLS/Xray-core/releases/download/v1.2.3/Xray-linux-64.zip"
	if got != want {
		t.Fatalf("下载路径是 %s,want %s", got, want)
	}
}

// 包不对的时候要报错,而且缓存目录里不能留下一个像是「已经取好了」的半成品。
func TestFetchLeavesNoHalfCacheBehind(t *testing.T) {
	// 少一份 geodata:装到节点上连 -test 都过不去,必须在这里就拦住。
	newFakeGitHub(t, "Xray-linux-64.zip", makeZip(t, fileXray, fileGeoIP))

	dir := t.TempDir()
	_, err := Fetch(t.Context(), Request{OS: "linux", Arch: "amd64", Dir: dir})
	if err == nil {
		t.Fatal("包里缺 geosite.dat 应当报错")
	}
	if !strings.Contains(err.Error(), fileGeoSite) {
		t.Fatalf("报错要指出缺的是哪个文件,实际: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读缓存目录: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("失败的这一次不该在缓存目录里留下东西,实际有 %v", names)
	}
}

// 认不出的平台要在碰网络之前就报错。
func TestFetchRejectsUnknownPlatform(t *testing.T) {
	dir := t.TempDir()
	if _, err := Fetch(context.Background(), Request{OS: "plan9", Arch: "mips", Dir: dir}); err == nil {
		t.Fatal("plan9/mips 应当报错")
	}
	if _, err := Fetch(context.Background(), Request{OS: "linux", Arch: "amd64"}); err == nil {
		t.Fatal("没给缓存目录应当报错")
	}
}
