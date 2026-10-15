package model

import "strings"

// cityZh 是常见 VPS 机房城市的英文名(ipwho.is 返回的写法)→ 中文名。
// 手工维护,中文名取自中文维基百科对应条目的标题(简体)。表里没有的城市
// 不做机器翻译,显示名退回国家中文名(docs/internal/node-naming-rfc.md §3)。
var cityZh = map[string]string{
	// 北美
	"los angeles":   "洛杉矶",
	"san jose":      "圣何塞",
	"santa clara":   "圣克拉拉",
	"fremont":       "弗里蒙特",
	"san francisco": "旧金山",
	"seattle":       "西雅图",
	"phoenix":       "菲尼克斯",
	"las vegas":     "拉斯维加斯",
	"dallas":        "达拉斯",
	"denver":        "丹佛",
	"chicago":       "芝加哥",
	"kansas city":   "堪萨斯城",
	"atlanta":       "亚特兰大",
	"miami":         "迈阿密",
	"ashburn":       "阿什本",
	"new york":      "纽约",
	"buffalo":       "布法罗",
	"toronto":       "多伦多",
	"montreal":      "蒙特利尔",
	"vancouver":     "温哥华",
	// 亚太
	"tokyo":            "东京",
	"osaka":            "大阪",
	"seoul":            "首尔",
	"chuncheon":        "春川",
	"singapore":        "新加坡",
	"hong kong":        "香港",
	"taipei":           "台北",
	"kuala lumpur":     "吉隆坡",
	"bangkok":          "曼谷",
	"ho chi minh city": "胡志明市",
	"manila":           "马尼拉",
	"jakarta":          "雅加达",
	"mumbai":           "孟买",
	"sydney":           "悉尼",
	"melbourne":        "墨尔本",
	// 欧洲、中东、其他
	"london":            "伦敦",
	"frankfurt":         "法兰克福",
	"frankfurt am main": "法兰克福",
	"nuremberg":         "纽伦堡",
	"falkenstein":       "法尔肯施泰因",
	"amsterdam":         "阿姆斯特丹",
	"paris":             "巴黎",
	"helsinki":          "赫尔辛基",
	"stockholm":         "斯德哥尔摩",
	"warsaw":            "华沙",
	"moscow":            "莫斯科",
	"istanbul":          "伊斯坦布尔",
	"dubai":             "迪拜",
	"são paulo":         "圣保罗",
	"sao paulo":         "圣保罗",
	"johannesburg":      "约翰内斯堡",
}

// CityZh 查城市中文名;查不到返回空串。
func CityZh(english string) string {
	return cityZh[strings.ToLower(strings.TrimSpace(english))]
}
