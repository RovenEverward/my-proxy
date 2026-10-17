// Package buildinfo 回答一个问题:这个二进制是哪一版。
//
// 版本号只能来自编译期,运行期查不出来。发布件是 CI 用 -ldflags -X 塞进去的;
// 而 `go build ./cmd/mpx` 和 `go install` 出来的二进制没有人塞 —— 对它们,版本
// 从 Go 自己记录的模块信息里读(git describe 出来的那个),实在读不到才承认不知道。
//
// 之所以不直接回落到 "(devel)" 之类的字符串:自动更新检查要拿这个版本去和新发布
// 比大小,一个假版本号会让它永远认为"有新版本"或者"已经是最新",两种都很难查。
// 宁可明确地说"不知道这是哪一版",也不要给一个错的。
package buildinfo

import (
	"runtime/debug"
	"strings"
	"sync"
)

// version 由发布流程注入:
//
//	go build -ldflags "-X github.com/RovenEverward/my-proxy/internal/buildinfo.version=v0.2.0"
//
// 留空表示没注入,这时去问 build info。
var version = ""

// desktop 由桌面版发布流程注入 "1":
//
//	go build -ldflags "-X github.com/RovenEverward/my-proxy/internal/buildinfo.desktop=1"
//
// 留空表示这是普通 CLI 构建。桌面构建仍然可以当 CLI 用,只是不带参数启动时
// 等价于 `mpx app`,自更新也取桌面归档。
var desktop = ""

// repo 是本项目的仓库,自动更新检查要拿它去问 GitHub。
const repo = "RovenEverward/my-proxy"

// Repo 返回本项目的 GitHub 仓库(owner/name)。
func Repo() string { return repo }

// Version 返回本二进制的版本,形如 v0.2.0。
//
// 拿不到时返回空串 —— 调用方要自己决定怎么呈现"不知道",这里不编。
func Version() string {
	if v := normalize(version); v != "" {
		return v
	}
	once.Do(func() { fromBuildInfo = readBuildInfo() })
	return fromBuildInfo
}

// Known 报告我们是否真的知道自己的版本。
func Known() bool { return Version() != "" }

// Desktop 报告这是不是桌面构建。发布流程用 -ldflags -X 注入 desktop=1;
// 从源码直接编出来的是普通 CLI 构建。
func Desktop() bool { return strings.TrimSpace(desktop) == "1" }

var (
	once          sync.Once
	fromBuildInfo string
)

// readBuildInfo 从 Go 记的模块信息里挖出版本。
//
// 两条线索,按可信度排:
//  1. 主模块自己的版本。`go install .../cmd/mpx@v0.2.0` 走的是这条,最准。
//  2. vcs.revision + vcs.modified,拼成 v0.0.0-<短哈希>。这是 `go build ./cmd/mpx`
//     在仓库里编出来的情况 —— 没打 tag 时拿不到干净的版本号,但哈希足够回答
//     "我手上这个是哪个提交",而且本地改过要标出来,否则排查时会对着一个和
//     提交对不上的二进制看半天。
func readBuildInfo() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	if v := normalize(info.Main.Version); v != "" {
		return v
	}

	var rev string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	v := "v0.0.0-" + rev
	if dirty {
		v += "-dirty"
	}
	return v
}

// normalize 把构建工具塞进来的占位值当成"没给"。
//
// Go 在模块信息里对"从源码目录直接 build"的模块写的是 (devel),对伪版本也会给
// 一个带 +dirty 之类的串;这些都不是可以拿去比大小的版本号。
func normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "(devel)" || v == "devel" {
		return ""
	}
	return v
}
