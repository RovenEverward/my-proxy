// Package xrayrelease 取一份 xray 发行包,把节点上要用的文件摊到控制面的磁盘上。
//
// 它和 ssh 无关:下载、解压、缓存这几件事只跟网络和压缩包打交道,可以单独验证。
// 控制面只下载一次,之后把同一份文件推给每一台节点 —— 于是节点上不需要 curl、
// 不需要 unzip,也不需要能连上 GitHub,一台只开 ssh 的离线机器照样能装。
package xrayrelease

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// repo 是 xray 的上游仓库。
const repo = "XTLS/Xray-core"

// RecommendedVersion 是这个 MPX 版本推荐装的 xray 版本。
//
// 刻意钉死而不是追上游 latest:一次出的 MPX 发布对应的是一套已经一起验证过的
// 组合,节点上跑哪个版本应当由控制面说了算,而不是"上游昨天发了什么"。要换推荐
// 版本就改这个常量、跟着一次 MPX 发布走。
const RecommendedVersion = "v26.3.27"

// 发布包里只取这三个文件。包里还有 LICENSE 和 README,推到几百台节点上没用。
const (
	fileXray    = "xray"
	fileGeoIP   = "geoip.dat"
	fileGeoSite = "geosite.dat"
)

// apiBase 与 downloadBase 是包级变量,测试里换成本地服务的地址。
var (
	apiBase      = "https://api.github.com"
	downloadBase = "https://github.com"
)

// maxZip 是一次下载允许的最大字节数。上游的包不到 30MB,给一个宽松但有限的上限,
// 免得对端(或中间人)让我们把磁盘写满。
const maxZip = 200 << 20

var client = &http.Client{Timeout: 5 * time.Minute}

// Files 是一次发布里节点要用的文件,都已经落在这台机器的磁盘上。
//
// 给路径而不是内容:一个核心加两份数据接近 65MB,三个一起读进内存没有任何好处,
// 往节点推的时候本来就是一个一个过去的。
type Files struct {
	Version string // 形如 v26.3.27
	Xray    string
	GeoIP   string
	GeoSite string
}

// Request 说明要取哪一份发布、给哪个平台、缓存在哪。
type Request struct {
	Version string // 空表示最新版
	OS      string // 节点的操作系统,取值同 runtime.GOOS
	Arch    string // 节点的架构,取值同 runtime.GOARCH
	Dir     string // 缓存目录,不存在会被创建
}

// AssetName 给出某个平台对应的发布包名。
//
// 名字是上游发布流程定的,没有规律可循:amd64 的包叫 64,arm64 的叫 arm64-v8a,
// 所以只能列出来。认不出的平台直接报错 —— 核心装错架构,节点上是一启动就退。
func AssetName(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/386":
		return "Xray-linux-32.zip", nil
	case "linux/amd64":
		return "Xray-linux-64.zip", nil
	case "linux/arm":
		return "Xray-linux-arm32-v7a.zip", nil
	case "linux/arm64":
		return "Xray-linux-arm64-v8a.zip", nil
	case "linux/riscv64":
		return "Xray-linux-riscv64.zip", nil
	case "linux/s390x":
		return "Xray-linux-s390x.zip", nil
	case "linux/ppc64le":
		return "Xray-linux-ppc64le.zip", nil
	case "darwin/amd64":
		return "Xray-macos-64.zip", nil
	case "darwin/arm64":
		return "Xray-macos-arm64-v8a.zip", nil
	case "freebsd/amd64":
		return "Xray-freebsd-64.zip", nil
	case "freebsd/arm64":
		return "Xray-freebsd-arm64-v8a.zip", nil
	}
	return "", fmt.Errorf("没有 %s/%s 的 xray 发布包", goos, goarch)
}

// Fetch 取一份 xray 发布,解开并缓存在 req.Dir 下。
//
// 同一个版本加平台取过第二次就不再下载:装一台新节点、或者换核心重装,都只是从
// 本地往节点推。这是让「节点不需要能连外网」成立的那一步。
func Fetch(ctx context.Context, req Request) (Files, error) {
	if req.Dir == "" {
		return Files{}, errors.New("没有给缓存目录")
	}
	asset, err := AssetName(req.OS, req.Arch)
	if err != nil {
		return Files{}, err
	}

	version := normalize(req.Version)
	var url string
	if version == "" {
		if version, url, err = resolveLatest(ctx, asset); err != nil {
			return Files{}, err
		}
	} else {
		url = releaseURL(version, asset)
	}

	files := Files{
		Version: version,
		Xray:    filepath.Join(req.Dir, version+"_"+req.OS+"_"+req.Arch, fileXray),
		GeoIP:   filepath.Join(req.Dir, version+"_"+req.OS+"_"+req.Arch, fileGeoIP),
		GeoSite: filepath.Join(req.Dir, version+"_"+req.OS+"_"+req.Arch, fileGeoSite),
	}
	if ready(files) {
		return files, nil
	}

	if err := os.MkdirAll(req.Dir, 0o755); err != nil {
		return Files{}, fmt.Errorf("建缓存目录 %s: %w", req.Dir, err)
	}
	if err := fetchInto(ctx, url, filepath.Dir(files.Xray)); err != nil {
		return Files{}, err
	}
	if !ready(files) {
		return Files{}, fmt.Errorf("发布包 %s 里没有 %s/%s/%s", url, fileXray, fileGeoIP, fileGeoSite)
	}
	return files, nil
}

// resolveLatest 问 GitHub 最新版是哪个,顺带拿到该平台资产的下载地址。
//
// 只有「不指定版本」才需要问接口:指定了版本的话下载地址能直接拼出来,少一次
// 请求,也不会被接口的限流挡住。
//
// 接口不可用(未登录限额 60 次/小时,共享出口常被用光,返回 403/429)时退回不看
// 接口的办法:从下载站 /releases/latest 的 302 Location 取版本号,再用 releaseURL
// 把该平台资产的地址拼出来。接口和退回都失败才报错。
func resolveLatest(ctx context.Context, asset string) (version, url string, err error) {
	resp, err := get(ctx, apiBase+"/repos/"+repo+"/releases/latest")
	if err != nil {
		tag, fbErr := latestFromRedirect(ctx)
		if fbErr != nil {
			return "", "", fmt.Errorf("%w;退回 %s 也失败: %v", err, latestURL(), fbErr)
		}
		return tag, releaseURL(tag, asset), nil
	}
	defer resp.Body.Close()

	var body struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return "", "", fmt.Errorf("读 GitHub 的发布信息: %w", err)
	}
	if body.TagName == "" {
		return "", "", errors.New("GitHub 没有给出最新版的版本号")
	}
	for _, a := range body.Assets {
		if a.Name == asset {
			return body.TagName, a.URL, nil
		}
	}
	return "", "", fmt.Errorf("发布 %s 里没有 %s", body.TagName, asset)
}

// Latest 问 GitHub xray 仓库最新发布是哪个版本,返回上游的标签形式(v26.3.27)。
//
// 只回答「最新是哪个版本」:要下载哪个平台的包由调用方用 AssetName 决定,下载
// 地址用 releaseURL 拼。升级流程往往要先知道版本号才能算资产名和摘要地址,所以
// 它比 resolveLatest 窄 —— 后者还要在资产列表里挑出该平台那一个。
//
// 和 resolveLatest 一样,接口被限流时退回下载站的重定向。
func Latest(ctx context.Context) (string, error) {
	resp, err := get(ctx, apiBase+"/repos/"+repo+"/releases/latest")
	if err != nil {
		tag, fbErr := latestFromRedirect(ctx)
		if fbErr != nil {
			return "", fmt.Errorf("%w;退回 %s 也失败: %v", err, latestURL(), fbErr)
		}
		return tag, nil
	}
	defer resp.Body.Close()

	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("读 GitHub 的发布信息: %w", err)
	}
	if body.TagName == "" {
		return "", errors.New("GitHub 没有给出最新版的版本号")
	}
	return body.TagName, nil
}

// latestURL 是不走接口时问最新版用的地址。
func latestURL() string {
	return downloadBase + "/" + repo + "/releases/latest"
}

// latestFromRedirect 不看接口,靠下载站 /releases/latest 的 302 取版本号。
func latestFromRedirect(ctx context.Context) (string, error) {
	u := latestURL()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "mpx")

	resp, err := noRedirect().Do(req)
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
func noRedirect() *http.Client {
	c := *client
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

// Digest 取某个发布资产的官方摘要(.dgst),返回小写的 sha256。
//
// 摘要和资产是同一个 release 下的两个文件,内容是一行 "SHA2-256= <hex>"。升级时
// 先取摘要、再让节点解包前自己核对 —— 一个下坏的核心顶替上去会让整台节点断流。
// 摘要里没有这一行就报错,不猜、不放行。
func Digest(ctx context.Context, version, asset string) (string, error) {
	resp, err := get(ctx, releaseURL(version, asset)+".dgst")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读 %s 的摘要: %w", asset, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "SHA2-256=")
		if !ok {
			continue
		}
		sum := strings.ToLower(strings.TrimSpace(rest))
		if !isSHA256Hex(sum) {
			return "", fmt.Errorf("%s 的 SHA2-256 不是 64 位十六进制: %q", asset, sum)
		}
		return sum, nil
	}
	return "", fmt.Errorf("%s 的摘要里没有 SHA2-256 行", asset)
}

// isSHA256Hex 报告 s 是不是 64 位小写十六进制。
func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') {
			continue
		}
		return false
	}
	return true
}

// fetchInto 把发布包取到 dest 的旁边解开,最后整体改名成 dest。
//
// 先落临时目录再改名,是为了让缓存目录里出现的每一个版本都是完整的:解到一半
// 断线留下的半份如果被当成「已缓存」,下次装到节点上的核心就是坏的。
func fetchInto(ctx context.Context, url, dest string) error {
	tmp, err := os.MkdirTemp(filepath.Dir(dest), filepath.Base(dest)+".part-")
	if err != nil {
		return fmt.Errorf("建临时目录: %w", err)
	}
	defer os.RemoveAll(tmp)

	zipPath := filepath.Join(tmp, "release.zip")
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	if err := save(resp, zipPath); err != nil {
		return fmt.Errorf("下载 %s: %w", url, err)
	}

	stage := filepath.Join(tmp, "files")
	if err := extract(zipPath, stage); err != nil {
		return err
	}
	if err := os.Remove(zipPath); err != nil {
		return err
	}
	if err := os.Rename(stage, dest); err != nil {
		return fmt.Errorf("把解出来的文件放到 %s: %w", dest, err)
	}
	return nil
}

// extract 把发布包里节点要用的三个文件取出来。
func extract(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("读发布包: %w", err)
	}
	defer r.Close()

	// 取文件的权限写死,不看包里的:核心必须可执行,而 OpenFile 的 mode 会被
	// umask 削掉,所以还要显式 Chmod 一次。
	want := map[string]os.FileMode{fileXray: 0o755, fileGeoIP: 0o644, fileGeoSite: 0o644}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("建 %s: %w", dest, err)
	}
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		// 只看文件名不看目录:上游把文件放在包根下,但哪天挪进子目录了我们也不该瞎。
		name := filepath.Base(f.Name)
		mode, ok := want[name]
		if !ok {
			continue
		}
		if err := writeEntry(f, filepath.Join(dest, name), mode); err != nil {
			return err
		}
		delete(want, name)
	}

	var missing []string
	for name := range want {
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		return fmt.Errorf("发布包里缺少 %s", strings.Join(missing, "、"))
	}
	return nil
}

func writeEntry(f *zip.File, path string, mode os.FileMode) error {
	src, err := f.Open()
	if err != nil {
		return fmt.Errorf("读 %s: %w", f.Name, err)
	}
	defer src.Close()

	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		return fmt.Errorf("设 %s 的权限: %w", path, err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return fmt.Errorf("解出 %s: %w", f.Name, err)
	}
	return out.Close()
}

// get 发一次 GET。GitHub 的接口要求带 User-Agent,不带会直接 403。
func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mpx")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("请求 %s: HTTP %d", url, resp.StatusCode)
	}
	return resp, nil
}

// save 把响应体写到 path 上。先写 path.tmp 再改名,断线不会留下半份文件。
func save(resp *http.Response, path string) error {
	defer resp.Body.Close()
	out, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(resp.Body, maxZip+1))
	if err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if n > maxZip {
		return fmt.Errorf("发布包超过 %d 字节", int64(maxZip))
	}
	return os.Rename(path+".tmp", path)
}

// ready 报告缓存里是不是已经有完整的一份了。三个文件缺一个都算没有。
func ready(f Files) bool {
	for _, p := range []string{f.Xray, f.GeoIP, f.GeoSite} {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return false
		}
	}
	return true
}

// normalize 把使用者写的版本号补成上游的标签形式(26.3.27 → v26.3.27)。
func normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "latest") {
		return ""
	}
	if strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

func releaseURL(version, asset string) string {
	return downloadBase + "/" + repo + "/releases/download/" + version + "/" + asset
}

// DownloadURL 是对外暴露的 releaseURL:控制面要把地址递给节点,让节点自己去
// 下载同一份资产,所以这个拼法不能再只留在包里。version 与 asset 都原样使用,
// 不在这里补 v —— 调用方拿到的版本号本来就是上游的标签写法。
func DownloadURL(version, asset string) string { return releaseURL(version, asset) }
