package model

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ExternalMetadata 是一个外部端点的清洗层记录:规范化后的地区、城市与字母。
// 原始上游名留在 source_snapshots 里不动;订阅名只从这里取(UC-17)。
// Kind 区分写入来源:"auto" 是快照刷新时按原名自动解析的;"graded" 是降级
// 探测或 regrade 写过的,快照回填只补 auto,不覆盖 graded 的结果。
type ExternalMetadata struct {
	SourceID  int64     `json:"source_id"`
	SourceKey string    `json:"source_key"`
	Region    string    `json:"region,omitempty"`
	City      string    `json:"city,omitempty"`
	Letter    string    `json:"letter,omitempty"`
	Kind      string    `json:"kind"` // auto / graded
	UpdatedAt time.Time `json:"updated_at"`
}

// AsLocation 把外部清洗记录换成节点的 LocationMetadata,方便共用 NodeDisplay
// 与分组逻辑。显示名规则两边一致:旗帜 + 城市(缺省国家中文名) + 字母。
func (m ExternalMetadata) AsLocation() LocationMetadata {
	return LocationMetadata{Region: m.Region, City: m.City, Letter: m.Letter}
}

var externalKinds = map[string]bool{"auto": true, "graded": true}

func (m ExternalMetadata) Validate() error {
	if m.SourceID <= 0 || m.SourceKey == "" || utf8.RuneCountInString(m.SourceKey) > 96 {
		return fmt.Errorf("外部端点缺少来源或指纹")
	}
	if !externalKinds[m.Kind] {
		return fmt.Errorf("外部清洗记录类型必须是 auto 或 graded")
	}
	return m.AsLocation().Validate()
}

// ExternalQuality 是一个外部端点最近一次降级探测的存档:判定 + 加权分 + 明细。
// 与 NodeQuality 同形态,身份换成 (source_id, source_key)。
type ExternalQuality struct {
	SourceID  int64           `json:"source_id"`
	SourceKey string          `json:"source_key"`
	CheckedAt time.Time       `json:"checked_at"`
	Verdict   string          `json:"verdict"` // ok / partial / untested
	Score     int             `json:"score"`
	Report    json.RawMessage `json:"report"`
}

// ExternalRemark 是外部端点在订阅里的显示名:
// 清洗层有地区时为「源名 · 旗帜 + 地区/城市 + 字母」;
// 没有清洗层或没地区时回退「源名 · 上游原名」。原名永不覆盖,可回溯。
func ExternalRemark(sourceName, rawRemark string, meta ExternalMetadata, hasMeta bool) string {
	if hasMeta {
		if d := NodeDisplay(meta.AsLocation()); d != "" {
			return sourceName + " · " + d
		}
	}
	return sourceName + " · " + rawRemark
}

// remarkRegions 是上游节点名里常见写法到国家码的对照。订阅源名字风格杂,
// 旗帜 emoji 直接解码,文字部分按小写包含匹配;查不到返回空,等降级探测
// 从 IP 拿地区。只放代理场景常见写法,不做全世界地名库。
var remarkRegions = []struct {
	match  string
	region string
}{
	{"香港", "HK"}, {"hong kong", "HK"}, {"hongkong", "HK"},
	{"澳门", "MO"}, {"macau", "MO"}, {"macao", "MO"},
	{"台湾", "TW"}, {"taiwan", "TW"}, {"台北", "TW"},
	{"日本", "JP"}, {"japan", "JP"}, {"东京", "JP"}, {"tokyo", "JP"}, {"大阪", "JP"}, {"osaka", "JP"},
	{"韩国", "KR"}, {"korea", "KR"}, {"首尔", "KR"}, {"seoul", "KR"}, {"春川", "KR"},
	{"新加坡", "SG"}, {"singapore", "SG"}, {"狮城", "SG"},
	{"美国", "US"}, {"united states", "US"}, {"usa", "US"},
	{"洛杉矶", "US"}, {"los angeles", "US"}, {"圣何塞", "US"}, {"san jose", "US"},
	{"西雅图", "US"}, {"seattle", "US"}, {"芝加哥", "US"}, {"chicago", "US"},
	{"纽约", "US"}, {"new york", "US"}, {"硅谷", "US"}, {"达拉斯", "US"}, {"dallas", "US"},
	{"英国", "GB"}, {"london", "GB"}, {"伦敦", "GB"},
	{"德国", "DE"}, {"frankfurt", "DE"}, {"法兰克福", "DE"},
	{"法国", "FR"}, {"paris", "FR"}, {"巴黎", "FR"},
	{"加拿大", "CA"}, {"canada", "CA"}, {"多伦多", "CA"}, {"toronto", "CA"},
	{"澳大利亚", "AU"}, {"australia", "AU"}, {"悉尼", "AU"}, {"sydney", "AU"},
	{"俄罗斯", "RU"}, {"russia", "RU"}, {"莫斯科", "RU"}, {"moscow", "RU"},
	{"印度", "IN"}, {"india", "IN"}, {"孟买", "IN"}, {"mumbai", "IN"},
	{"菲律宾", "PH"}, {"manila", "PH"}, {"马尼拉", "PH"},
	{"越南", "VN"}, {"vietnam", "VN"},
	{"泰国", "TH"}, {"thailand", "TH"}, {"曼谷", "TH"}, {"bangkok", "TH"},
	{"马来西亚", "MY"}, {"malaysia", "MY"}, {"吉隆坡", "MY"},
	{"印度尼西亚", "ID"}, {"indonesia", "ID"}, {"印尼", "ID"}, {"雅加达", "ID"},
	{"阿联酋", "AE"}, {"dubai", "AE"}, {"迪拜", "AE"},
	{"土耳其", "TR"}, {"turkey", "TR"}, {"伊斯坦布尔", "TR"},
	{"荷兰", "NL"}, {"amsterdam", "NL"}, {"阿姆斯特丹", "NL"},
	{"巴西", "BR"}, {"brazil", "BR"}, {"圣保罗", "BR"},
	{"阿根廷", "AR"}, {"argentina", "AR"},
	{"墨西哥", "MX"}, {"mexico", "MX"},
	{"南非", "ZA"}, {"johannesburg", "ZA"},
	{"新西兰", "NZ"}, {"new zealand", "NZ"}, {"奥克兰", "NZ"},
	{"西班牙", "ES"}, {"spain", "ES"},
	{"意大利", "IT"}, {"italy", "IT"}, {"米兰", "IT"},
	{"瑞士", "CH"}, {"switzerland", "CH"}, {"苏黎世", "CH"},
	{"瑞典", "SE"}, {"sweden", "SE"}, {"斯德哥尔摩", "SE"},
	{"挪威", "NO"}, {"norway", "NO"},
	{"芬兰", "FI"}, {"finland", "FI"}, {"赫尔辛基", "FI"},
	{"波兰", "PL"}, {"poland", "PL"}, {"华沙", "PL"},
	{"爱尔兰", "IE"}, {"ireland", "IE"},
	{"乌克兰", "UA"}, {"ukraine", "UA"},
	{"哈萨克斯坦", "KZ"}, {"kazakhstan", "KZ"},
	{"巴基斯坦", "PK"}, {"pakistan", "PK"},
	{"柬埔寨", "KH"}, {"cambodia", "KH"},
	{"缅甸", "MM"}, {"myanmar", "MM"},
	{"孟加拉", "BD"}, {"bangladesh", "BD"},
	{"斯里兰卡", "LK"}, {"sri lanka", "LK"},
	{"尼泊尔", "NP"}, {"nepal", "NP"},
	{"蒙古", "MN"}, {"mongolia", "MN"},
	{"罗马尼亚", "RO"}, {"romania", "RO"},
	{"希腊", "GR"}, {"greece", "GR"},
	{"葡萄牙", "PT"}, {"portugal", "PT"},
	{"比利时", "BE"}, {"belgium", "BE"},
	{"奥地利", "AT"}, {"austria", "AT"},
	{"丹麦", "DK"}, {"denmark", "DK"},
	{"捷克", "CZ"}, {"czech", "CZ"},
	{"匈牙利", "HU"}, {"hungary", "HU"},
	{"保加利亚", "BG"}, {"bulgaria", "BG"},
	{"克罗地亚", "HR"}, {"croatia", "HR"},
	{"塞尔维亚", "RS"}, {"serbia", "RS"},
	{"哥伦比亚", "CO"}, {"colombia", "CO"},
	{"智利", "CL"}, {"chile", "CL"},
	{"秘鲁", "PE"}, {"peru", "PE"},
	{"巴拿马", "PA"}, {"panama", "PA"},
	{"伊朗", "IR"}, {"iran", "IR"},
	{"沙特", "SA"}, {"saudi", "SA"},
	{"埃及", "EG"}, {"egypt", "EG"},
	{"尼日利亚", "NG"}, {"nigeria", "NG"},
	{"以色列", "IL"}, {"israel", "IL"},
	{"肯尼亚", "KE"}, {"kenya", "KE"},
	{"摩洛哥", "MA"}, {"morocco", "MA"},
	{"中国", "CN"}, {"china", "CN"}, {"大陆", "CN"},
}

// remarkCodes 是机场名里常见的 ISO 短码写法,按 token 边界匹配(“jp-01”
// 里的 jp 算,“happy”里的 pp 不算)。表外码(如 gb 比 uk 少见)也收,
// 反正这里只认已知合法国家码。
var remarkCodes = map[string]string{
	"us": "US", "usa": "US", "uk": "GB", "gb": "GB", "hk": "HK", "mo": "MO",
	"tw": "TW", "jp": "JP", "kr": "KR", "sg": "SG", "cn": "CN", "ca": "CA",
	"au": "AU", "nz": "NZ", "de": "DE", "fr": "FR", "nl": "NL", "it": "IT",
	"es": "ES", "ch": "CH", "se": "SE", "no": "NO", "fi": "FI", "dk": "DK",
	"pl": "PL", "ie": "IE", "ua": "UA", "ru": "RU", "in": "IN", "ph": "PH",
	"vn": "VN", "th": "TH", "my": "MY", "id": "ID", "ae": "AE", "tr": "TR",
	"br": "BR", "ar": "AR", "mx": "MX", "za": "ZA", "eg": "EG", "ng": "NG",
	"il": "IL", "ir": "IR", "sa": "SA", "pk": "PK", "kz": "KZ", "ro": "RO",
	"gr": "GR", "pt": "PT", "be": "BE", "at": "AT", "cz": "CZ", "hu": "HU",
	"bg": "BG", "hr": "HR", "rs": "RS", "co": "CO", "cl": "CL", "pe": "PE",
	"pa": "PA", "ke": "KE", "ma": "MA", "kh": "KH", "mm": "MM", "bd": "BD",
	"lk": "LK", "np": "NP", "mn": "MN",
}

// RegionFromRemark 从上游节点名里解析地区:先解旗帜 emoji,再按 token
// 匹配 ISO 短码,最后按写法表做子串匹配。都没有返回空串。
func RegionFromRemark(remark string) string {
	if r := flagEmojiRegion(remark); r != "" {
		return r
	}
	lower := strings.ToLower(remark)
	for _, tok := range remarkTokens(lower) {
		if r, ok := remarkCodes[tok]; ok {
			return r
		}
	}
	for _, e := range remarkRegions {
		if strings.Contains(lower, strings.ToLower(e.match)) {
			return e.region
		}
	}
	return ""
}

// remarkTokens 把名字切成字母数字 token:分隔符是任何非字母数字。
func remarkTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}

// flagEmojiRegion 把名字里的旗帜 emoji 解成两位国家码;没有或不是合法
// 双字母时返回空。
func flagEmojiRegion(s string) string {
	var pair []rune
	for _, r := range s {
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			pair = append(pair, r-0x1F1E6)
			if len(pair) == 2 {
				return string([]rune{'A' + pair[0], 'A' + pair[1]})
			}
			continue
		}
		pair = pair[:0]
	}
	return ""
}

// ExternalGroup 是外部端点的分组键:同一来源同一地区同一城市内字母不重复。
// 比节点的 LetterGroup 多一层来源隔离——两个来源可以各有「日本 A」。
func ExternalGroup(sourceID int64, m LocationMetadata) string {
	return fmt.Sprintf("s:%d\x00%s", sourceID, LetterGroup(m))
}

// AssignExternalLetters 给端点分配清洗记录(地区 + 字母)。
// keys 按调用方给的顺序处理(刷新回填传 source_key 排序后的新键,保证可复现);
// 已存在的记录把字母占住:graded 的记录原样返回不改动,auto 的记录在地区
// 还空着时允许用新解析出的地区补一次(上游名字可能改过)。没有地区的端点
// 不生成记录——没地区拼不出显示名,留着行只会和「回退原名」撞语义。
func AssignExternalLetters(sourceID int64, keys []string, regions map[string]string, existing map[string]ExternalMetadata) map[string]ExternalMetadata {
	used := map[string]map[string]bool{}
	for _, m := range existing {
		if m.Letter == "" {
			continue
		}
		g := ExternalGroup(sourceID, m.AsLocation())
		if used[g] == nil {
			used[g] = map[string]bool{}
		}
		used[g][m.Letter] = true
	}
	out := map[string]ExternalMetadata{}
	// 同地区新端点继承该来源已有记录的最多见城市:上游名通常不写城市,
	// 而清洗展示用「城市」优先;同 (region, "") 和 (region, 东京) 会落进
	// 两个字母组,名字撞重复后缀。回填时借已排好的城市保持同组。
	cityByRegion := map[string]string{}
	cityVotes := map[string]map[string]int{}
	for _, m := range existing {
		if m.Region == "" || m.City == "" {
			continue
		}
		if cityVotes[m.Region] == nil {
			cityVotes[m.Region] = map[string]int{}
		}
		cityVotes[m.Region][m.City]++
		if cityVotes[m.Region][m.City] > cityVotes[m.Region][cityByRegion[m.Region]] {
			cityByRegion[m.Region] = m.City
		}
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	for _, key := range sorted {
		m, ok := existing[key]
		if ok && m.Kind == "graded" {
			out[key] = m
			continue
		}
		if !ok {
			m = ExternalMetadata{SourceID: sourceID, SourceKey: key, Kind: "auto"}
		}
		if m.Region == "" {
			m.Region = regions[key]
		}
		if m.Region == "" {
			continue // 没地区就不生成记录
		}
		if m.City == "" {
			m.City = cityByRegion[m.Region]
		}
		g := ExternalGroup(sourceID, m.AsLocation())
		if used[g] == nil {
			used[g] = map[string]bool{}
		}
		if m.Letter == "" {
			m.Letter = NextLetter(used[g])
			if m.Letter == "" {
				continue // 字母池耗尽,保持无记录回退原名
			}
		}
		used[g][m.Letter] = true
		out[key] = m
	}
	return out
}
