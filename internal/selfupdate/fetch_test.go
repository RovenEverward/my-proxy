package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
)

const testRepo = "owner/name"

// serve 造一个同时扮演 GitHub 接口与下载站的测试服务。
//
// sums 为 nil 时 SHA256SUMS 返回 404;archives 里没有的归档也返回 404。
func serve(t *testing.T, latest string, sums []byte, archives map[string][]byte) (*httptest.Server, Source) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/"+testRepo+"/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": latest})
		case r.URL.Path == "/repos/"+testRepo+"/releases/"+latest:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": latest})
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			if sums == nil {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(sums)
		case strings.Contains(r.URL.Path, "/releases/"):
			name := path.Base(r.URL.Path)
			data, ok := archives[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, Source{Repo: testRepo, APIBase: srv.URL, DownloadBase: srv.URL, Client: srv.Client()}
}

func sha256Hex(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func sumsFor(entries map[string][]byte) []byte {
	var b strings.Builder
	for name, data := range entries {
		fmt.Fprintf(&b, "%s  %s\n", sha256Hex(data), name)
	}
	return []byte(b.String())
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("写 tar 头: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("写 tar 内容: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("关闭 tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("关闭 gzip: %v", err)
	}
	return buf.Bytes()
}

func zipFile(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("写 zip 项: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("写 zip 内容: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip: %v", err)
	}
	return buf.Bytes()
}

func TestLatest(t *testing.T) {
	_, src := serve(t, "v0.3.0", nil, nil)
	got, err := src.Latest(context.Background())
	if err != nil {
		t.Fatalf("Latest 出错: %v", err)
	}
	if got != "v0.3.0" {
		t.Fatalf("Latest = %q,想要 v0.3.0", got)
	}
}

func TestFetchTarGz(t *testing.T) {
	const want = "linux 可执行文件的内容"
	name := ArchiveName("linux", "amd64")
	archive := tarGz(t, map[string]string{"README.md": "说明", "mpx": want})
	_, src := serve(t, "latest", sumsFor(map[string][]byte{name: archive}), map[string][]byte{name: archive})

	bin, verified, err := src.Fetch(context.Background(), "latest", "linux", "amd64")
	if err != nil {
		t.Fatalf("Fetch 出错: %v", err)
	}
	if !verified {
		t.Fatalf("verified = false,想要 true")
	}
	if string(bin) != want {
		t.Fatalf("解出的内容 = %q,想要 %q", bin, want)
	}
}

func TestFetchZip(t *testing.T) {
	const want = "windows 可执行文件的内容"
	name := ArchiveName("windows", "amd64")
	if name != "mpx_windows_amd64.zip" {
		t.Fatalf("ArchiveName(windows, amd64) = %q", name)
	}
	archive := zipFile(t, map[string]string{"mpx.exe": want, "README.md": "说明"})
	_, src := serve(t, "v0.4.0", sumsFor(map[string][]byte{name: archive}), map[string][]byte{name: archive})

	bin, verified, err := src.Fetch(context.Background(), "v0.4.0", "windows", "amd64")
	if err != nil {
		t.Fatalf("Fetch 出错: %v", err)
	}
	if !verified {
		t.Fatalf("verified = false,想要 true")
	}
	if string(bin) != want {
		t.Fatalf("解出的内容 = %q,想要 %q", bin, want)
	}
}

func TestFetchHashMismatch(t *testing.T) {
	name := ArchiveName("darwin", "arm64")
	archive := tarGz(t, map[string]string{"mpx": "真的内容"})
	// 清单里写一个错的哈希:长度合法但与归档不符。
	bad := []byte(strings.Repeat("0", 64) + "  " + name + "\n")
	_, src := serve(t, "latest", bad, map[string][]byte{name: archive})

	_, _, err := src.Fetch(context.Background(), "latest", "darwin", "arm64")
	if err == nil {
		t.Fatalf("哈希不符时应该报错")
	}
	if !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("错误里应说明 sha256 不符,得到: %v", err)
	}
}

func TestFetchSumsMissing(t *testing.T) {
	name := ArchiveName("linux", "arm64")
	archive := tarGz(t, map[string]string{"mpx": "旧版本的内容"})
	_, src := serve(t, "v0.1.0", nil, map[string][]byte{name: archive})

	bin, verified, err := src.Fetch(context.Background(), "v0.1.0", "linux", "arm64")
	if err != nil {
		t.Fatalf("缺 SHA256SUMS 应照常成功,却出错: %v", err)
	}
	if verified {
		t.Fatalf("没有清单时 verified 应为 false")
	}
	if string(bin) != "旧版本的内容" {
		t.Fatalf("解出的内容 = %q", bin)
	}
}

func TestFetchArchiveMissing(t *testing.T) {
	_, src := serve(t, "latest", []byte("x  mpx_linux_amd64.tar.gz\n"), nil)

	_, _, err := src.Fetch(context.Background(), "latest", "linux", "amd64")
	if err == nil {
		t.Fatalf("归档 404 时应该报错")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("错误里应带上 HTTP 404,得到: %v", err)
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name            string
		current         string
		target          string
		explicitVersion bool
		allowDowngrade  bool
		wantErr         error
		wantMsg         string
	}{
		{name: "版本未知", current: "", target: "v0.2.0", wantMsg: "install-cli.sh"},
		{name: "已是最新", current: "v0.2.0", target: "v0.2.0", wantErr: ErrUpToDate},
		{name: "正常升级", current: "v0.1.0", target: "v0.2.0"},
		{name: "降级被拦", current: "v0.2.0", target: "v0.1.0", wantMsg: "allow-downgrade"},
		{name: "显式允许降级", current: "v0.2.0", target: "v0.1.0", explicitVersion: true, allowDowngrade: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Decide(tc.current, tc.target, tc.explicitVersion, tc.allowDowngrade)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v,想要 %v", err, tc.wantErr)
				}
				return
			}
			if tc.wantMsg != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantMsg) {
					t.Fatalf("err = %v,想要包含 %q", err, tc.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("不该出错,得到: %v", err)
			}
		})
	}
}
