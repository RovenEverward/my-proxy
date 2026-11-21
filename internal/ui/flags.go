package ui

import (
	"archive/zip"
	"bytes"
	"io"
	"regexp"
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
// (nil, false)。控制面网页管理台(/admin)的节点列表要画这批旗,处理器在
// internal/server/admin_page.go,取旗这一步在这里,两边共用同一个 zip。
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


