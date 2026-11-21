// Package ui 只留嵌入的页面资产给控制面用:管理台 SPA 由控制面在 /admin/
// 下伺服(internal/server/admin_page.go),这里提供 assets 子树、字体与国旗;
// 前端源码在 frontend/,浏览器测试在 e2e/。
//
// 曾经装在同一个包里的本机页面服务(mpx ui)已在 UC-26 退役:操作机不再
// 有代理层,浏览器直接访问控制面。
package ui

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed assets
var assets embed.FS

// EmbeddedSources 返回构建产物里记录的前端源码指纹。
// assets/build/UI-SOURCES 由 scripts/package-ui.mjs 在 ui:build 时写入,
// 第一行形如 "ui-src <sha256>"。要核对当前源码是否就是这份 UI 的来源,
// 用 scripts/ui-fingerprint.mjs 重新算一遍源码侧清单,比 sha256。
// 产物缺失或没有该文件时返回空字符串:对这类老构建无法判断新旧。
func EmbeddedSources() string {
	manifest, err := assets.ReadFile("assets/build/UI-SOURCES")
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(manifest), "\n")
	hash, found := strings.CutPrefix(line, "ui-src ")
	if !found {
		return ""
	}
	return hash
}

// Fonts 给控制面的订阅页用:同一份内置字体。
//
// 订阅页与管理台在同一份 assets 里取字体(后者走 /fonts/ 静态目录),字体
// 只该打包一次,所以订阅页也从这棵 embed 取,而不是各存一份。
func Fonts() fs.FS {
	sub, err := fs.Sub(assets, "assets/fonts")
	if err != nil {
		return emptyFS{}
	}
	return sub
}

// emptyFS 是各 fs 导出出错时的退化值:读什么都报不存在,而不是 panic。
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

// Admin 返回嵌进二进制的网页管理台资源:assets 整棵子树。
// 控制面把它挂在 /admin/ 下伺服(见 internal/server/admin_page.go)。
func Admin() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return emptyFS{}
	}
	return sub
}
