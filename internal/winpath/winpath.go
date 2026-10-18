// Package winpath 增删 Windows 用户 PATH(HKCU\Environment\Path)里的一个目录。
//
// 为什么放在 Go 里:之前 setup.exe 用 NSIS 自己读改写 PATH,而 NSIS 的字符串缓冲
// 区是编译 NSIS 时定死的(Ubuntu 的 nsis 包是 1024 字符,脚本里 !define 改不了)。
// PATH 一长,ReadRegStr 就读失败返回空串,脚本把空串当成「原来没有 PATH」,写回
// 去只剩安装目录 —— 用户的 PATH 被清空。这里没有长度上限,而且:
//   - 读不出来就什么都不写(宁可让用户手动加);
//   - 原值原样保留:不展开 %VAR%,不改 REG_EXPAND_SZ 类型,不动别的条目(连空条目
//     和顺序都不动),只在末尾追加或只摘掉我们那一项;
//   - 写之前先备份原值,写完再读回来核对。
package winpath

import "strings"

// Add 在 cur 末尾追加 dir;已经有(大小写不敏感,忽略结尾的 \ 和两侧引号)就不动。
// 原有内容逐字保留,新串一定以原串开头。
func Add(cur, dir string) (string, bool) {
	if Contains(cur, dir) {
		return cur, false
	}
	if cur == "" {
		return dir, true
	}
	if strings.HasSuffix(cur, ";") {
		return cur + dir, true
	}
	return cur + ";" + dir, true
}

// Remove 摘掉 cur 里所有等于 dir 的条目,别的条目(包括空条目)和顺序原样保留。
func Remove(cur, dir string) (string, bool) {
	parts := strings.Split(cur, ";")
	kept := parts[:0:0]
	for _, p := range parts {
		if Same(p, dir) {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == len(parts) {
		return cur, false
	}
	return strings.Join(kept, ";"), true
}

// Contains 报告 cur 里有没有等于 dir 的条目。
func Contains(cur, dir string) bool {
	for _, p := range strings.Split(cur, ";") {
		if Same(p, dir) {
			return true
		}
	}
	return false
}

// Same 按 Windows 的习惯比较两个目录:不分大小写,忽略两侧空白、引号和结尾的 \ 或 /。
// 带 %VAR% 的条目不展开,只按原文比较。
func Same(a, b string) bool {
	na, nb := norm(a), norm(b)
	return na != "" && strings.EqualFold(na, nb)
}

func norm(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	s = strings.TrimRight(s, `\/`)
	return s
}
