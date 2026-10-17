// 本文件负责「先把新版本取到暂存目录」:下载到 .part、按 SHA256SUMS 校验、
// 原子改名、写 staged.json;真正替换本机二进制仍由 install.go 决定。
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/release"
)

// Staged 是一个已经下载并通过校验、放在暂存目录里的发布件。
type Staged struct {
	Version    string    `json:"version"`
	Archive    string    `json:"archive"`
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
	VerifiedAt time.Time `json:"verified_at"`
}

// Stager 把发布件暂存到 Dir,供之后 Open 而不必现下现装。
//
// Dir 为空时用 os.UserCacheDir()/mpx/staged。Source、GOOS、GOARCH 的默认值与
// Fetch 一致:Source.Desktop 决定取桌面安装包还是 CLI 发布件。
type Stager struct {
	Dir    string
	Source Source
	GOOS   string
	GOARCH string
	// Now 是写 staged.json 时用的时钟,空表示 time.Now。
	Now func() time.Time
	// Rename 是原子改名的实现,空表示带重试的 os.Rename。做成字段是为了在测试里
	// 注入「前几次被占用」的行为。
	Rename func(oldpath, newpath string) error
	// Progress 在下载过程中报告已写字节与总字节;total 未知时为 0。
	Progress func(written, total int64)
}

func (s *Stager) goos() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

func (s *Stager) goarch() string {
	if s.GOARCH != "" {
		return s.GOARCH
	}
	return runtime.GOARCH
}

func (s *Stager) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// archiveName 取这一平台要下载的文件名,与 Fetch 的选择规则相同。
func (s *Stager) archiveName() string {
	if s.Source.Desktop {
		return DesktopArchiveName(s.goos(), s.goarch())
	}
	return ArchiveName(s.goos(), s.goarch())
}

// root 返回暂存根目录。
func (s *Stager) root() (string, error) {
	if s.Dir != "" {
		return s.Dir, nil
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("找不到用户缓存目录: %w", err)
	}
	return filepath.Join(dir, "mpx", "staged"), nil
}

// versionDir 把版本号映射到暂存子目录,并挡住能跑到暂存根目录外面的名字。
func (s *Stager) versionDir(version string) (string, error) {
	root, err := s.root()
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(version)
	if v == "" || v == "." || v == ".." || strings.ContainsAny(v, `/\`) {
		return "", fmt.Errorf("版本号 %q 不能用作暂存目录", version)
	}
	return filepath.Join(root, v), nil
}

// rename 做一次带重试的原子改名。Rename 是单次尝试的实现,测试可注入「前几次
// 被占用」;为空时用 os.Rename。
func (s *Stager) rename(oldpath, newpath string) error {
	rename := s.Rename
	if rename == nil {
		rename = os.Rename
	}
	return retryRename(rename, oldpath, newpath, 6, 100*time.Millisecond, time.Sleep)
}

// retryRename 在改名被占用(Windows 上杀软、索引器常见)时重试。
//
// 做成接受 rename 函数的纯函数,测试不用真的占用文件就能覆盖重试次数与最终失败。
func retryRename(rename func(string, string) error, oldpath, newpath string, attempts int, delay time.Duration, sleep func(time.Duration)) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = rename(oldpath, newpath); err == nil {
			return nil
		}
		if i < attempts-1 && sleep != nil {
			sleep(delay)
		}
	}
	return err
}

// readStaged 读取某个版本目录里的 staged.json;不存在或读不出来都按还没有暂存处理。
func readStaged(dir string) (Staged, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "staged.json"))
	if err != nil {
		return Staged{}, false
	}
	var st Staged
	if err := json.Unmarshal(data, &st); err != nil || st.Version == "" || st.Archive == "" || st.SHA256 == "" {
		return Staged{}, false
	}
	return st, true
}

// writeStaged 先写临时文件再改名,避免读到写了一半的 JSON。
func (s *Stager) writeStaged(dir string, st Staged) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.CreateTemp(dir, "staged.json.tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := s.rename(tmp, filepath.Join(dir, "staged.json")); err != nil {
		return err
	}
	return nil
}

// Ensure 保证 version 这一版已经完整地放在暂存目录里并通过校验。
//
// 已有且校验通过时直接返回;有 .part 时用 Range 接着下;下完按 SHA256SUMS
// 核对,通过才把 .part 改名成正式文件并写 staged.json。校验不通过删掉 .part,
// 返回错误交给调用方决定何时重试。
func (s *Stager) Ensure(ctx context.Context, version string) (Staged, error) {
	version = strings.TrimSpace(version)
	dir, err := s.versionDir(version)
	if err != nil {
		return Staged{}, err
	}

	if st, ok := readStaged(dir); ok {
		if st.Version == version {
			if verr := s.Verify(st); verr == nil {
				return st, nil
			} else if _, statErr := os.Stat(dir); statErr != nil {
				// Verify 认为暂存件不可信,已经删掉整个版本目录;下面重下。
			} else {
				// 只是这次拿不到校验清单之类的暂时问题,保留暂存件等下一轮。
				return Staged{}, verr
			}
		}
	}

	name := s.archiveName()
	base := s.Source.releaseBase(version)
	sums, code, err := s.Source.get(ctx, base+"/SHA256SUMS", maxSums)
	if err != nil {
		return Staged{}, err
	}
	switch code {
	case http.StatusOK:
	case http.StatusNotFound:
		return Staged{}, fmt.Errorf("这个发布没有 SHA256SUMS,不能预下载(校验通过才允许暂存)")
	default:
		return Staged{}, fmt.Errorf("下载 SHA256SUMS: HTTP %d", code)
	}
	want, err := release.Lookup(sums, name)
	if err != nil {
		return Staged{}, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Staged{}, fmt.Errorf("创建暂存目录 %s: %w", dir, err)
	}
	part := filepath.Join(dir, name+".part")
	size, err := s.download(ctx, version, name, part)
	if err != nil {
		return Staged{}, err
	}
	got, err := fileSHA256(part)
	if err != nil {
		return Staged{}, err
	}
	if got != want {
		_ = os.Remove(part)
		return Staged{}, fmt.Errorf("%s 的 sha256 与 SHA256SUMS 不符", name)
	}

	st := Staged{Version: version, Archive: name, SHA256: got, Size: size, VerifiedAt: s.now()}
	if err := s.rename(part, filepath.Join(dir, name)); err != nil {
		return Staged{}, fmt.Errorf("把下载件改成正式暂存件: %w", err)
	}
	if err := s.writeStaged(dir, st); err != nil {
		return Staged{}, fmt.Errorf("写 staged.json: %w", err)
	}
	return st, nil
}

// download 把归档写到 part,已有内容时发 Range 续传;返回最终文件字节数。
//
// 用 O_APPEND 追加并在结束前 fsync。服务端不认 Range、直接回 200 时从头写,
// 不会把新旧两份内容拼在一起。
func (s *Stager) download(ctx context.Context, version, name, part string) (int64, error) {
	archiveURL := s.Source.releaseBase(version) + "/" + name
	var offset int64
	if fi, err := os.Stat(part); err == nil {
		offset = fi.Size()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("检查断点文件 %s: %w", part, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "mpx")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := s.Source.client().Do(req)
	if err != nil {
		return 0, fmt.Errorf("请求 %s: %w", archiveURL, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// 服务端没接续传:从头写,offset 归零。
		offset = 0
	case http.StatusPartialContent:
		// 校验 Content-Range 的起点,防止服务端从别处开始发。
		if start, _, ok := parseContentRange(resp.Header.Get("Content-Range")); ok && start != offset {
			return 0, fmt.Errorf("服务端从第 %d 字节续传,期望第 %d 字节", start, offset)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// 断点文件已经不小于整个文件:交给哈希校验判断完整性。
		if fi, statErr := os.Stat(part); statErr == nil {
			return fi.Size(), nil
		}
		return 0, fmt.Errorf("下载 %s: HTTP %d", name, resp.StatusCode)
	default:
		return 0, fmt.Errorf("下载 %s: HTTP %d", name, resp.StatusCode)
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if resp.StatusCode == http.StatusOK {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return 0, fmt.Errorf("打开断点文件 %s: %w", part, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()

	total := int64(0)
	if resp.StatusCode == http.StatusPartialContent {
		if _, end, ok := parseContentRange(resp.Header.Get("Content-Range")); ok && end > 0 {
			total = end + 1
		}
	} else if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	if total == 0 && resp.ContentLength > 0 {
		total = offset + resp.ContentLength
	}

	written := offset
	lastReport := time.Time{}
	report := func(force bool) {
		if s.Progress == nil {
			return
		}
		if !force && time.Since(lastReport) < 250*time.Millisecond {
			return
		}
		lastReport = time.Now()
		s.Progress(written, total)
	}
	report(true)

	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return 0, fmt.Errorf("写入 %s: %w", part, werr)
			}
			written += int64(n)
			if written > maxArchive {
				return 0, fmt.Errorf("%s 超过 %d 字节上限", name, maxArchive)
			}
			report(false)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return 0, fmt.Errorf("读取 %s: %w", name, rerr)
		}
	}
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("落盘 %s: %w", part, err)
	}
	if err := f.Close(); err != nil {
		return 0, fmt.Errorf("关闭 %s: %w", part, err)
	}
	closed = true
	report(true)
	return written, nil
}

// parseContentRange 解析 "bytes 100-199/2000",返回起点、终点与是否成功。
func parseContentRange(v string) (start, end int64, ok bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "bytes ") {
		return 0, 0, false
	}
	v = strings.TrimPrefix(v, "bytes ")
	rangePart, _, _ := strings.Cut(v, "/")
	a, b, found := strings.Cut(rangePart, "-")
	if !found {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(a, "%d", &start); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(b, "%d", &end); err != nil {
		return 0, 0, false
	}
	return start, end, true
}

// fileSHA256 流式计算文件的 sha256,不把整包读进内存。
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Verify 再算一次暂存件的 sha256,与 staged.json 和 SHA256SUMS 对照。
//
// 哈希对不上(暂存文件被改过)时删掉整个版本目录;只是这次拿不到 SHA256SUMS
// 时不删,留给调度下一轮再试。
func (s *Stager) Verify(st Staged) error {
	return s.verify(context.Background(), st)
}

func (s *Stager) verify(ctx context.Context, st Staged) error {
	dir, err := s.versionDir(st.Version)
	if err != nil {
		return err
	}
	discard := func(err error) error {
		_ = os.RemoveAll(dir)
		return err
	}
	if st.Archive != s.archiveName() {
		return discard(fmt.Errorf("暂存件 %s 不是这个平台的发布件(期望 %s)", st.Archive, s.archiveName()))
	}
	path := filepath.Join(dir, st.Archive)
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return discard(fmt.Errorf("暂存件 %s 不见了", path))
		}
		return err
	}
	if st.Size > 0 && fi.Size() != st.Size {
		return discard(fmt.Errorf("暂存件 %s 的大小 %d 与记录 %d 不符", path, fi.Size(), st.Size))
	}
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if got != st.SHA256 {
		return discard(fmt.Errorf("暂存件 %s 的 sha256 与 staged.json 不符", st.Archive))
	}

	sums, code, err := s.Source.get(ctx, s.Source.releaseBase(st.Version)+"/SHA256SUMS", maxSums)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("校验暂存件时下载 SHA256SUMS: HTTP %d", code)
	}
	want, err := release.Lookup(sums, st.Archive)
	if err != nil {
		return discard(err)
	}
	if want != got || want != st.SHA256 {
		return discard(fmt.Errorf("%s 的 sha256 与 SHA256SUMS 不符", st.Archive))
	}
	return nil
}

// Prune 只留下 keep 这一版,其余版本目录(包括没下完的 .part)全部删掉。
func (s *Stager) Prune(keep string) error {
	root, err := s.root()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	keep = strings.TrimSpace(keep)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Open 先 Verify 暂存件,再从归档里解出 mpx 可执行文件。
func (s *Stager) Open(version string) ([]byte, error) {
	version = strings.TrimSpace(version)
	dir, err := s.versionDir(version)
	if err != nil {
		return nil, err
	}
	st, ok := readStaged(dir)
	if !ok {
		return nil, fmt.Errorf("没有 %s 的暂存发布件", version)
	}
	if st.Version != version {
		return nil, fmt.Errorf("暂存目录里的版本是 %s,不是 %s", st.Version, version)
	}
	if err := s.Verify(st); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, st.Archive))
	if err != nil {
		return nil, fmt.Errorf("读取暂存件: %w", err)
	}
	return extract(data)
}
