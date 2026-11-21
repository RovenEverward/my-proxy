package ui

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// flagName 是 /flags/<xx>.svg 里允许的名字:两位小写 ISO 代码,可带一个地区后缀
// (如 gb-eng)。路径分隔符、点号等一律不接受,避免拿路径去翻 zip 里别的东西。
var flagName = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)?$`)

// flagAssets 把内嵌的 flags.zip 只解析一次,之后按名字取单个 SVG。
var flagAssets struct {
	once sync.Once
	z    *zip.Reader
	err  error
}

func flagsZip() (*zip.Reader, error) {
	flagAssets.once.Do(func() {
		b, err := assets.ReadFile("assets/flags.zip")
		if err != nil {
			flagAssets.err = err
			return
		}
		flagAssets.z, flagAssets.err = zip.NewReader(bytes.NewReader(b), int64(len(b)))
	})
	return flagAssets.z, flagAssets.err
}

// Flag 从内嵌 zip 里按两位代码取一面国旗 SVG;名字不合法或没有这面旗返回
// (nil, false)。控制面网页管理台(/admin)的节点列表要画同一批旗,但它不
// 经过 mpx ui 的处理器,所以把取旗这一步开放给它,两边仍共用同一个 zip。
func Flag(name string) (io.ReadCloser, bool) {
	if !flagName.MatchString(name) {
		return nil, false
	}
	z, err := flagsZip()
	if err != nil {
		return nil, false
	}
	f, err := z.Open(name + ".svg")
	if err != nil {
		return nil, false
	}
	return f, true
}

// serveFlag 从内嵌 zip 里按需读出一个国旗 SVG。它不碰控制面、也不看会话:页面
// 未登录时也可能要先画出登录页/向导里的旗帜。名字不合法与文件不存在都回 404。
func (s *Server) serveFlag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "国旗资源只能 GET")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/flags/")
	if !strings.HasSuffix(rest, ".svg") {
		http.NotFound(w, r)
		return
	}
	code := strings.TrimSuffix(rest, ".svg")
	if !flagName.MatchString(code) {
		http.NotFound(w, r)
		return
	}
	z, err := flagsZip()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "国旗资源读不出来")
		return
	}
	f, err := z.Open(code + ".svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if st, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, f)
}
