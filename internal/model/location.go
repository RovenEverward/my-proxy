package model

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// LocationMetadata belongs to one node or host. Nonempty host fields override node fields.
//
// v0.7.0 删掉了 line 与 purposes:老客户端写进来的这两个字段解不出来,直接忽略;
// 库里残留的 JSON 也一样,读出时自然丢弃。现在只认 region / city / letter。
type LocationMetadata struct {
	Region string `json:"region"`
	// City 与 Letter 只用于节点,拼显示名:旗帜 + 城市(缺省为国家中文名) + 字母。
	City   string `json:"city,omitempty"`
	Letter string `json:"letter,omitempty"`
}

var tagSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var nodeLetter = regexp.MustCompile(`^[A-Z]{1,2}$`)

func (m LocationMetadata) Validate() error {
	if len(m.Region) > 80 {
		return fmt.Errorf("地区最多 80 字节")
	}
	if utf8.RuneCountInString(m.City) > 32 || strings.ContainsAny(m.City, "\t\r\n") {
		return fmt.Errorf("城市最多 32 个字符,不能含换行或制表符")
	}
	if m.Letter != "" && !nodeLetter.MatchString(m.Letter) {
		return fmt.Errorf("字母必须是 1–2 位大写字母,例如 A、B、AA")
	}
	return nil
}
func EffectiveLocationMetadata(node, host LocationMetadata) LocationMetadata {
	if host.Region != "" {
		node.Region = host.Region
	}
	return node
}
func containsString(items []string, v string) bool {
	for _, x := range items {
		if x == v {
			return true
		}
	}
	return false
}
