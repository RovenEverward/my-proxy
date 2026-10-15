package model

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Purpose is a named recommendation. It never grants inbound authorization.
type Purpose struct {
	Slug                 string            `json:"slug"`
	Name                 string            `json:"name"`
	Description          string            `json:"description"`
	Kind                 string            `json:"kind"` // daily, media, download, game
	Regions              []string          `json:"regions,omitempty"`
	Lines                []string          `json:"lines,omitempty"`
	Tags                 []string          `json:"tags,omitempty"`    // any matching purpose tag
	Domains              []string          `json:"domains,omitempty"` // domain suffixes routed to this group
	Strategy             string            `json:"strategy"`          // select or url-test
	TestURL              string            `json:"test_url"`
	Interval             int               `json:"interval"` // seconds
	RequiredCapabilities []string          `json:"required_capabilities,omitempty"`
	CapabilityRegion     string            `json:"capability_region,omitempty"`
	DailyPurpose         string            `json:"daily_purpose,omitempty"` // explicit fallback daily configuration
	Templates            map[string]string `json:"templates,omitempty"`
}

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
var purposeDomain = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)
var nodeLetter = regexp.MustCompile(`^[A-Z]{1,2}$`)
var purposeCountry = regexp.MustCompile(`^[A-Z]{2}$`)

func (p *Purpose) Normalize() {
	if p.Strategy == "" {
		p.Strategy = "url-test"
	}
	if p.TestURL == "" {
		p.TestURL = "https://www.gstatic.com/generate_204"
	}
	if p.Interval == 0 {
		p.Interval = 300
	}
	if p.Kind == "media" && len(p.Domains) == 0 {
		p.Domains = []string{"netflix.com", "nflxvideo.net", "youtube.com", "googlevideo.com", "disneyplus.com", "dssott.com", "spotify.com"}
	}
}
func (p Purpose) Validate() error {
	if !purposeSlug.MatchString(p.Slug) {
		return fmt.Errorf("用途 slug 必须是 1–48 位小写字母、数字或连字符，首位为字母")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 160 {
		return fmt.Errorf("用途名称不能为空且最多 160 字节")
	}
	switch p.Kind {
	case "daily", "media", "download", "game":
	default:
		return fmt.Errorf("kind 只能是 daily / media / download / game")
	}
	if p.DailyPurpose != "" && (p.Kind == "daily" || !purposeSlug.MatchString(p.DailyPurpose) || p.DailyPurpose == p.Slug) {
		return fmt.Errorf("daily_purpose 必须引用另一个日常用途 slug，日常用途不能再引用")
	}
	if p.Strategy != "select" && p.Strategy != "url-test" {
		return fmt.Errorf("strategy 只能是 select / url-test")
	}
	u, err := url.Parse(p.TestURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("test_url 必须是 HTTP(S) 地址")
	}
	if p.Interval < 30 || p.Interval > 86400 {
		return fmt.Errorf("interval 必须在 30–86400 秒之间")
	}
	if p.Kind != "daily" && len(p.Domains) == 0 {
		return fmt.Errorf("非日常用途必须配置 domains 域名后缀")
	}
	for _, d := range p.Domains {
		if !purposeDomain.MatchString(d) || !strings.Contains(d, ".") || strings.Contains(d, "..") {
			return fmt.Errorf("无效域名后缀 %q", d)
		}
	}
	if len(p.RequiredCapabilities) > 3 {
		return fmt.Errorf("required_capabilities 最多 3 项")
	}
	seen := map[string]bool{}
	for _, target := range p.RequiredCapabilities {
		if target != "egress" && target != "youtube" && target != "netflix" {
			return fmt.Errorf("未知能力目标 %q", target)
		}
		if seen[target] {
			return fmt.Errorf("重复能力目标 %q", target)
		}
		seen[target] = true
	}
	if p.CapabilityRegion != "" {
		if !purposeCountry.MatchString(p.CapabilityRegion) {
			return fmt.Errorf("capability_region 必须是两位大写国家代码")
		}
		if !seen["egress"] && !seen["netflix"] {
			return fmt.Errorf("capability_region 需要 egress 或 netflix 能力；youtube 不提供地区观测")
		}
	}
	if len(p.Regions)+len(p.Lines)+len(p.Tags)+len(p.Domains) > 256 {
		return fmt.Errorf("用途条件与域名合计最多 256 项")
	}
	return nil
}
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
func (p Purpose) Matches(m PurposeMetadata) bool {
	if len(p.Regions) > 0 && !containsPurpose(p.Regions, m.Region) {
		return false
	}
	if len(p.Lines) > 0 && !containsPurpose(p.Lines, m.Line) {
		return false
	}
	if len(p.Tags) == 0 {
		return true
	}
	for _, t := range m.Purposes {
		if containsPurpose(p.Tags, t) {
			return true
		}
	}
	return false
}
func containsPurpose(items []string, v string) bool {
	for _, x := range items {
		if x == v {
			return true
		}
	}
	return false
}
