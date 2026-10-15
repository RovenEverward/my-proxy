package model

import (
	"strings"
	"unicode"
)

// LabelKey 把节点名或展示名规范成用于匹配的形式:去掉国旗(区域指示符号)、
// 空白和 `·`,再转小写。「🇺🇸 洛杉矶 A」「洛杉矶a」「洛杉矶 A」得到同一个 key。
//
// CLI 的对象解析与服务端拼展示名用同一个函数,两边对「同一个名字」的理解才一致。
// 设计见 docs/internal/node-naming-rfc.md §8。
func LabelKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 0x1F1E6 && r <= 0x1F1FF: // 区域指示符号,两个拼成一面国旗
		case unicode.IsSpace(r), r == '·', r == '\uFE0F':
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
