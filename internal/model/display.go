package model

import (
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

var zhRegions = display.Regions(language.SimplifiedChinese)

// zhShort 是 CLDR 的短名(alt="short"),x/text 不提供,这里补上全称太长的几个。
var zhShort = map[string]string{"HK": "香港", "MO": "澳门"}

// RegionFlag 把两位国家码转成旗帜 emoji;不是两位字母时返回空串。
func RegionFlag(region string) string {
	r := strings.ToUpper(strings.TrimSpace(region))
	if len(r) != 2 || r[0] < 'A' || r[0] > 'Z' || r[1] < 'A' || r[1] > 'Z' {
		return ""
	}
	return string([]rune{0x1F1E6 + rune(r[0]-'A'), 0x1F1E6 + rune(r[1]-'A')})
}

// RegionNameZh 返回国家/地区的简体中文名(CLDR 数据);认不出时原样返回。
func RegionNameZh(region string) string {
	r := strings.TrimSpace(region)
	if RegionFlag(r) == "" {
		return r
	}
	if n, ok := zhShort[strings.ToUpper(r)]; ok {
		return n
	}
	reg, err := language.ParseRegion(r)
	if err != nil {
		return r
	}
	if n := zhRegions.Name(reg); n != "" {
		return n
	}
	return r
}

// NodeDisplay 拼节点显示名:旗帜 + 城市(缺省为国家中文名) + 字母。没有地区时返回空串。
func NodeDisplay(m PurposeMetadata) string {
	region := strings.TrimSpace(m.Region)
	if region == "" {
		return ""
	}
	place := strings.TrimSpace(m.City)
	if place == "" {
		place = RegionNameZh(region)
	}
	parts := []string{}
	if f := RegionFlag(region); f != "" {
		parts = append(parts, f)
	}
	parts = append(parts, place)
	if m.Letter != "" {
		parts = append(parts, m.Letter)
	}
	return strings.Join(parts, " ")
}

// LetterGroup 是分配字母的分组键:同一地区同一城市内字母不重复。
func LetterGroup(m PurposeMetadata) string {
	return strings.ToUpper(strings.TrimSpace(m.Region)) + "\x00" + strings.TrimSpace(m.City)
}

// LetterAt 返回第 i 个字母(0 起):A…Z,然后 AA、AB…ZZ。
func LetterAt(i int) string {
	if i < 26 {
		return string(rune('A' + i))
	}
	i -= 26
	return string([]rune{rune('A' + i/26), rune('A' + i%26)})
}

// NextLetter 返回 used 里没有的最小字母;删掉节点不会让别人重排。
func NextLetter(used map[string]bool) string {
	for i := 0; i < 26+26*26; i++ {
		if l := LetterAt(i); !used[l] {
			return l
		}
	}
	return ""
}
