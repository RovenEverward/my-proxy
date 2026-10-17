// Package selfupdate 负责把一只新的 mpx 发布件取到本机。
//
// 它只做"取"这件事:找到最新版本号、按平台挑归档、下载、按 SHA256SUMS 校验、
// 从归档里取出可执行文件。装到哪、怎么重启、旧文件怎么退回去,都不在这里 ——
// 换掉的正是控制面自己,决策必须比取件更谨慎。
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/release"
	"github.com/RovenEverward/my-proxy/internal/updatecheck"
)

// maxArchive 是单个下载件的字节上限。
//
// 200MB 远大于实际发布件(几十 MB 量级),只是挡住被指向一个大文件或响应不会
// 结束的情况,免得内存被吃光。
const maxArchive = 200 << 20

// maxSums 是 SHA256SUMS 的字节上限,清单本来只有几百字节。
const maxSums = 1 << 20

// ErrUpToDate 表示目标版本与当前版本相同,不需要更新。
var ErrUpToDate = errors.New("当前版本已经是最新的,无需更新")

// defaultClient 是没有注入 Client 时用的客户端。
var defaultClient = &http.Client{Timeout: 5 * time.Minute}

// Source 描述从哪儿取发布件。
type Source struct {
	// Repo 是 owner/name 形式的 GitHub 仓库。
	Repo string
	// APIBase 是 GitHub 接口的根,空表示 https://api.github.com。
	APIBase string
	// DownloadBase 是发布件下载站的根,空表示 https://github.com。
	DownloadBase string
	// Client 是发起请求用的客户端,空表示带超时的默认客户端。
	Client *http.Client
}

func (s Source) apiBase() string {
	if s.APIBase != "" {
		return strings.TrimRight(s.APIBase, "/")
	}
	return "https://api.github.com"
}

func (s Source) downloadBase() string {
	if s.DownloadBase != "" {
		return strings.TrimRight(s.DownloadBase, "/")
	}
	return "https://github.com"
}

func (s Source) client() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return defaultClient
}

// Latest 问最新发布是哪一版,返回 tag_name。
//
// 接口不可用时不认输:未登录的 GitHub 接口每小时只有 60 次,共享出口很容易被
// 用光,403/429 一出来,本机自更新和 mpx ui 的一键更新都会卡在这一步。这时退回
// 不看接口的办法 —— 直接请求下载站的 /releases/latest,从它 302 的 Location 头
// 里读出版本号。接口和退回都失败才报错,错误里两种都提到。
func (s Source) Latest(ctx context.Context) (string, error) {
	u := s.apiBase() + "/repos/" + s.Repo + "/releases/latest"
	data, code, err := s.get(ctx, u, 1<<20)
	if err == nil && code == http.StatusOK {
		var body struct {
			TagName string `json:"tag_name"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			return "", fmt.Errorf("解析 GitHub 的发布信息: %w", err)
		}
		if body.TagName == "" {
			return "", fmt.Errorf("GitHub 没有给出最新版的版本号")
		}
		return body.TagName, nil
	}

	// 只有「接口这条路断了」才退回:200 但内容读不出来是另一回事,不该被掩盖。
	apiErr := err
	if apiErr == nil {
		apiErr = fmt.Errorf("请求 GitHub 最新发布: HTTP %d", code)
	}
	fallback := s.downloadBase() + "/" + s.Repo + "/releases/latest"
	tag, fbErr := s.latestFromRedirect(ctx)
	if fbErr != nil {
		return "", fmt.Errorf("%w;退回 %s 也失败: %v", apiErr, fallback, fbErr)
	}
	return tag, nil
}

// latestFromRedirect 不看接口,靠下载站 /releases/latest 的 302 取版本号。
func (s Source) latestFromRedirect(ctx context.Context) (string, error) {
	u := s.downloadBase() + "/" + s.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "mpx")

	resp, err := s.noRedirect().Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 %s: %w", u, err)
	}
	defer resp.Body.Close()

	tag, err := tagFromLocation(resp)
	if err != nil {
		return "", fmt.Errorf("从 %s 的重定向取版本: %w", u, err)
	}
	return tag, nil
}

// noRedirect 返回一个不跟随重定向的客户端副本,保留原客户端的 Transport。
//
// 退回办法要的正是那个 302 本身:跟下去就变成发布页面,Location 也看不到了。
func (s Source) noRedirect() *http.Client {
	c := *s.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &c
}

// tagFromLocation 从重定向的 Location 头里取最后一段作为版本号。
//
// GitHub 的 Location 形如 .../releases/tag/v0.8.0,最后一段就是 tag_name。
func tagFromLocation(resp *http.Response) (string, error) {
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("HTTP %d 没有 Location 头", resp.StatusCode)
	}
	u, err := resp.Request.URL.Parse(loc)
	if err != nil {
		return "", fmt.Errorf("解析 Location %q: %w", loc, err)
	}
	tag := path.Base(strings.TrimRight(u.Path, "/"))
	if tag == "" || tag == "." || tag == "/" {
		return "", fmt.Errorf("Location %q 里没有版本号", loc)
	}
	return tag, nil
}

// ArchiveName 给出某个平台上发布件的文件名。
//
// 命名与 deploy/install-cli.sh 保持一致:unix 是 tar.gz,windows 单独是 zip,
// 因为 zip 里的可执行文件是 mpx.exe。
func ArchiveName(goos, goarch string) string {
	if goos == "windows" {
		return "mpx_" + goos + "_" + goarch + ".zip"
	}
	return "mpx_" + goos + "_" + goarch + ".tar.gz"
}

// Fetch 取回 version 这一版在 goos/goarch 上的 mpx 可执行文件。
//
// version 为空或 "latest" 表示最新发布。verified 报告是否用 SHA256SUMS 校验过:
// 早于清单的旧发布会缺 SHA256SUMS,这时按 install-cli.sh 的做法只警告、继续,
// 并把 verified 置为 false 交给调用方决定要不要接受。
func (s Source) Fetch(ctx context.Context, version, goos, goarch string) (bin []byte, verified bool, err error) {
	name := ArchiveName(goos, goarch)
	base := s.releaseBase(version)

	sums, code, err := s.get(ctx, base+"/SHA256SUMS", maxSums)
	if err != nil {
		return nil, false, err
	}
	switch code {
	case http.StatusOK:
		verified = true
	case http.StatusNotFound:
		// 旧发布没有清单,照常安装,只是没法校验。
		verified = false
	default:
		return nil, false, fmt.Errorf("下载 SHA256SUMS: HTTP %d", code)
	}

	archive, code, err := s.get(ctx, base+"/"+name, maxArchive)
	if err != nil {
		return nil, false, err
	}
	if code != http.StatusOK {
		return nil, false, fmt.Errorf("下载 %s: HTTP %d", name, code)
	}

	if verified {
		if err := release.Check(sums, name, archive); err != nil {
			return nil, false, err
		}
	}

	bin, err = extract(archive)
	if err != nil {
		return nil, verified, err
	}
	return bin, verified, nil
}

// Decide 判断要不要从 current 换到 target,不换时给出原因。
//
// current 为空说明这只二进制是源码直接编的,没有版本可比,也不能拿它的"最新"
// 去覆盖一个来路不明的文件 —— 直接让人改用安装脚本。
func Decide(current, target string, explicitVersion, allowDowngrade bool) error {
	if current == "" {
		return errors.New("本机版本未知(源码编译),请用 install-cli.sh 安装")
	}
	if target == current {
		return ErrUpToDate
	}
	// 确认是降级才拦:版本号比不出大小时(伪版本、带后缀)不拦,免得正常升级
	// 被一个认不出来的版本号挡住。
	if updatecheck.Ahead(target, current) {
		if !(explicitVersion && allowDowngrade) {
			return fmt.Errorf("目标版本 %s 比当前版本 %s 旧,降级请用 --version 加 --allow-downgrade", target, current)
		}
	}
	return nil
}

// releaseBase 给出某一版的下载目录。
func (s Source) releaseBase(version string) string {
	ver := strings.TrimSpace(version)
	if ver == "" || ver == "latest" {
		return s.downloadBase() + "/" + s.Repo + "/releases/latest/download"
	}
	return s.downloadBase() + "/" + s.Repo + "/releases/download/" + url.PathEscape(ver)
}

// get 发起一次 GET,返回响应体、HTTP 状态和网络/读取错误。
//
// 非 200 不算错误,由调用方按状态码决定(比如 SHA256SUMS 的 404 要特殊处理)。
func (s Source) get(ctx context.Context, u string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	// GitHub 接口不带 User-Agent 会直接 403。
	req.Header.Set("User-Agent", "mpx")

	resp, err := s.client().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("请求 %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("读取 %s: %w", u, err)
	}
	if int64(len(data)) > limit {
		return nil, resp.StatusCode, fmt.Errorf("%s 超过 %d 字节上限", u, limit)
	}
	return data, resp.StatusCode, nil
}

// extract 从归档里取出 mpx 可执行文件。
func extract(data []byte) ([]byte, error) {
	// 按魔数判断而不是按平台:zip 以 PK\x03\x04 开头,其余当 tar.gz。
	if len(data) >= 4 && bytes.Equal(data[:4], []byte("PK\x03\x04")) {
		return extractZip(data)
	}
	return extractTarGz(data)
}

// isMPX 报告归档里的这个名字是不是 mpx 可执行文件。
func isMPX(name string) bool {
	// zip 在 windows 上可能用反斜杠分隔。
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	return base == "mpx" || base == "mpx.exe"
}

func extractTarGz(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("解压归档: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("解包 tar.gz: %w", err)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		if !isMPX(h.Name) {
			continue
		}
		return readLimited(tr, "tar.gz")
	}
	return nil, errors.New("归档里没有 mpx 可执行文件")
}

func extractZip(data []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("解包 zip: %w", err)
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !isMPX(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("读取 zip 里的 %s: %w", f.Name, err)
		}
		b, err := readLimited(rc, "zip")
		rc.Close()
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, errors.New("归档里没有 mpx 可执行文件")
}

func readLimited(r io.Reader, kind string) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxArchive+1))
	if err != nil {
		return nil, fmt.Errorf("读取 %s 里的可执行文件: %w", kind, err)
	}
	if int64(len(b)) > maxArchive {
		return nil, fmt.Errorf("%s 里的可执行文件超过 %d 字节上限", kind, maxArchive)
	}
	return b, nil
}
