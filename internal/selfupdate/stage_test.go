package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// stageSrv 是暂存测试用的假发布站:按需要支持 Range、第一次下载中途断开,
// 并可随时换掉 SHA256SUMS。
type stageSrv struct {
	mu       sync.Mutex
	archive  []byte
	name     string
	sums     []byte
	rangeOK  bool
	dropOnce bool
	gets     int
	ranges   []string
}

func (s *stageSrv) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/SHA256SUMS"):
			_, _ = w.Write(s.sums)
		case strings.HasSuffix(r.URL.Path, "/"+s.name):
			s.gets++
			if rg := r.Header.Get("Range"); rg != "" {
				s.ranges = append(s.ranges, rg)
			}
			if !s.rangeOK || r.Header.Get("Range") == "" {
				w.Header().Set("Content-Length", strconv.Itoa(len(s.archive)))
				if s.dropOnce {
					s.dropOnce = false
					_, _ = w.Write(s.archive[:len(s.archive)/2])
					// 响应体比 Content-Length 短:连接被断开,客户端读到意外的 EOF。
					return
				}
				_, _ = w.Write(s.archive)
				return
			}
			// 支持 Range:解析 "bytes=N-",回 206。
			start, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-"))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(s.archive)-1, len(s.archive)))
			w.Header().Set("Content-Length", strconv.Itoa(len(s.archive)-start))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(s.archive[start:])
		default:
			http.NotFound(w, r)
		}
	}
}

// newStageRig 造一个归档和配套清单,返回服务、Stager 和归档名。
func newStageRig(t *testing.T) (*stageSrv, *Stager, string) {
	t.Helper()
	// 内容带随机字节,确保归档比「一半」大,断点续传有意义。
	payload := strings.Repeat("mpx-stage-test-payload-", 200)
	name := DesktopArchiveName("linux", "amd64")
	archive := tarGz(t, map[string]string{"mpx": payload, "README.md": "说明"})
	fs := &stageSrv{name: name, archive: archive}
	fs.sums = sumsFor(map[string][]byte{name: archive})
	srv := httptest.NewServer(fs.handler())
	t.Cleanup(srv.Close)
	st := &Stager{
		Dir:    t.TempDir(),
		Source: Source{Repo: testRepo, APIBase: srv.URL, DownloadBase: srv.URL, Client: srv.Client(), Desktop: true},
		GOOS:   "linux",
		GOARCH: "amd64",
	}
	return fs, st, name
}

func versionDir(t *testing.T, st *Stager, version string) string {
	t.Helper()
	dir, err := st.versionDir(version)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestStageEnsureAndOpen(t *testing.T) {
	_, st, name := newStageRig(t)
	const version = "v0.9.6"

	staged, err := st.Ensure(context.Background(), version)
	if err != nil {
		t.Fatalf("Ensure 失败: %v", err)
	}
	if staged.Version != version || staged.Archive != name || staged.SHA256 == "" || staged.Size == 0 {
		t.Fatalf("暂存记录不对: %+v", staged)
	}
	dir := versionDir(t, st, version)
	if _, err := os.Stat(filepath.Join(dir, name+".part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".part 应已改名为正式文件: %v", err)
	}
	var onDisk Staged
	data, err := os.ReadFile(filepath.Join(dir, "staged.json"))
	if err != nil {
		t.Fatalf("staged.json 没写出来: %v", err)
	}
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.SHA256 != staged.SHA256 || onDisk.Size != staged.Size {
		t.Fatalf("staged.json 内容不对: %+v", onDisk)
	}

	bin, err := st.Open(version)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if string(bin) != strings.Repeat("mpx-stage-test-payload-", 200) {
		t.Fatalf("解出的可执行文件不对: %q", bin)
	}

	// 第二次 Ensure 不必再下载。
	staged2, err := st.Ensure(context.Background(), version)
	if err != nil || staged2.SHA256 != staged.SHA256 {
		t.Fatalf("已有暂存件时 Ensure 应直接返回: %+v %v", staged2, err)
	}
}

// 第一次下载中途断开,留下 .part;第二次带 Range 续传并成功。
func TestStageResumePartial(t *testing.T) {
	fs, st, name := newStageRig(t)
	fs.mu.Lock()
	fs.rangeOK = true
	fs.dropOnce = true
	fs.mu.Unlock()
	const version = "v0.9.6"

	if _, err := st.Ensure(context.Background(), version); err == nil {
		t.Fatal("第一次下载应因连接断开而失败")
	}
	part := filepath.Join(versionDir(t, st, version), name+".part")
	fi, err := os.Stat(part)
	if err != nil {
		t.Fatalf(".part 应保留下来供续传: %v", err)
	}
	if fi.Size() == 0 || fi.Size() >= int64(len(fs.archive)) {
		t.Fatalf(".part 大小 %d 应介于 0 与完整归档 %d 之间", fi.Size(), len(fs.archive))
	}
	partial := fi.Size()

	staged, err := st.Ensure(context.Background(), version)
	if err != nil {
		t.Fatalf("续传应成功: %v", err)
	}
	if staged.Size != int64(len(fs.archive)) {
		t.Fatalf("续传后大小 = %d,想要 %d", staged.Size, len(fs.archive))
	}
	fs.mu.Lock()
	ranges := append([]string(nil), fs.ranges...)
	fs.mu.Unlock()
	want := "bytes=" + strconv.FormatInt(partial, 10) + "-"
	if len(ranges) != 1 || ranges[0] != want {
		t.Fatalf("续传 Range = %v,想要 [%s]", ranges, want)
	}

	bin, err := st.Open(version)
	if err != nil || string(bin) != strings.Repeat("mpx-stage-test-payload-", 200) {
		t.Fatalf("续传后的文件不能用: %v %q", err, bin)
	}
}

// 校验不过:删掉 .part、返回错误;清单改对后下一次 Ensure 重新完整下载。
func TestStageChecksumMismatchThenRedownload(t *testing.T) {
	fs, st, name := newStageRig(t)
	const version = "v0.9.6"
	fs.mu.Lock()
	fs.sums = []byte(strings.Repeat("0", 64) + "  " + name + "\n")
	fs.mu.Unlock()

	if _, err := st.Ensure(context.Background(), version); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("哈希不符应报错,得到: %v", err)
	}
	part := filepath.Join(versionDir(t, st, version), name+".part")
	if _, err := os.Stat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("校验不过应删掉 .part: %v", err)
	}

	fs.mu.Lock()
	fs.sums = sumsFor(map[string][]byte{name: fs.archive})
	before := fs.gets
	fs.mu.Unlock()
	staged, err := st.Ensure(context.Background(), version)
	if err != nil {
		t.Fatalf("清单改对后应重下成功: %v", err)
	}
	fs.mu.Lock()
	gets := fs.gets
	fs.mu.Unlock()
	if gets <= before {
		t.Fatalf("应重新下载归档,GET 次数没有增加: %d → %d", before, gets)
	}
	if staged.Size != int64(len(fs.archive)) {
		t.Fatalf("重下后的记录不对: %+v", staged)
	}
}

// 已暂存文件被篡改:Verify 失败并删掉整个版本目录,Open 也不会给出内容。
func TestStageVerifyTampered(t *testing.T) {
	_, st, name := newStageRig(t)
	const version = "v0.9.6"
	staged, err := st.Ensure(context.Background(), version)
	if err != nil {
		t.Fatal(err)
	}
	dir := versionDir(t, st, version)
	if err := os.WriteFile(filepath.Join(dir, name), []byte("篡改过的内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Verify(staged); err == nil {
		t.Fatal("篡改后 Verify 应失败")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Verify 失败应删掉整个版本目录: %v", err)
	}
	if _, err := st.Open(version); err == nil {
		t.Fatal("暂存件被删后 Open 应失败")
	}
}

// Prune 只留下指定版本,其余版本目录(包括没下完的 .part)都删掉。
func TestStagePrune(t *testing.T) {
	_, st, _ := newStageRig(t)
	root, err := st.root()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v0.9.4", "v0.9.5", "v0.9.6"} {
		dir := filepath.Join(root, v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "archive.part"), []byte("半截"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Prune("v0.9.6"); err != nil {
		t.Fatalf("Prune 失败: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "v0.9.6" {
		t.Fatalf("Prune 后应只剩 v0.9.6,得到 %v", entries)
	}
}

// rename 被占用时重试:注入的 Rename 前两次失败、第三次成功,Ensure 照常完成。
func TestStageRenameRetries(t *testing.T) {
	_, st, _ := newStageRig(t)
	var attempts int
	st.Rename = func(oldpath, newpath string) error {
		if strings.HasSuffix(oldpath, ".part") {
			attempts++
			if attempts <= 2 {
				return errors.New("文件被占用")
			}
		}
		return os.Rename(oldpath, newpath)
	}
	if _, err := st.Ensure(context.Background(), "v0.9.6"); err != nil {
		t.Fatalf("Rename 重试后应成功: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("正式暂存件的改名应尝试 3 次(两次失败一次成功),实际 %d", attempts)
	}
}

// retryRename 本身:重试到成功;一直失败时返回最后一次错误。
func TestRetryRename(t *testing.T) {
	var sleeps int
	sleep := func(time.Duration) { sleeps++ }

	var n int
	rename := func(string, string) error {
		n++
		if n < 3 {
			return errors.New("占用")
		}
		return nil
	}
	if err := retryRename(rename, "a", "b", 5, time.Millisecond, sleep); err != nil {
		t.Fatalf("第三次应成功: %v", err)
	}
	if n != 3 || sleeps != 2 {
		t.Fatalf("调用 %d 次、睡了 %d 次,想要 3 次与 2 次", n, sleeps)
	}

	boom := errors.New("一直被占用")
	always := func(string, string) error { return boom }
	if err := retryRename(always, "a", "b", 4, 0, sleep); !errors.Is(err, boom) {
		t.Fatalf("一直失败应返回原错误,得到 %v", err)
	}
}
