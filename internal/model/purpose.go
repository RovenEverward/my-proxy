package model

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// PurposeMetadata belongs to one node or host. Nonempty host fields override node fields.
type PurposeMetadata struct {
	Region   string   `json:"region"`
	Line     string   `json:"line"`
	Purposes []string `json:"purposes,omitempty"`
	// City 与 Letter 只用于节点,拼显示名:旗帜 + 城市(缺省为国家中文名) + 字母。
	City   string `json:"city,omitempty"`
	Letter string `json:"letter,omitempty"`
}

var purposeSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var nodeLetter = regexp.MustCompile(`^[A-Z]{1,2}$`)

func (m PurposeMetadata) Validate() error {
	if len(m.Region) > 80 || len(m.Line) > 80 || len(m.Purposes) > 64 {
		return fmt.Errorf("地区/线路最多 80 字节，用途标签最多 64 项")
	}
	if utf8.RuneCountInString(m.City) > 32 || strings.ContainsAny(m.City, "\t\r\n") {
		return fmt.Errorf("城市最多 32 个字符,不能含换行或制表符")
	}
	if m.Letter != "" && !nodeLetter.MatchString(m.Letter) {
		return fmt.Errorf("字母必须是 1–2 位大写字母,例如 A、B、AA")
	}
	for _, t := range m.Purposes {
		if !purposeSlug.MatchString(t) {
			return fmt.Errorf("无效用途标签 %q", t)
		}
	}
	return nil
}
func EffectivePurposeMetadata(node, host PurposeMetadata) PurposeMetadata {
	if host.Region != "" {
		node.Region = host.Region
	}
	if host.Line != "" {
		node.Line = host.Line
	}
	if len(host.Purposes) > 0 {
		node.Purposes = host.Purposes
	}
	return node
}
func containsPurpose(items []string, v string) bool {
	for _, x := range items {
		if x == v {
			return true
		}
	}
	return false
}
