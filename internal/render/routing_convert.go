package render

import (
	"fmt"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

// RoutingSeed 是把一份 Clash 模板转换成规则表的结果。Groups 的 ID 是从 1 开始的
// 临时编号,Rules 与 Final 里的 "group:<n>" 指向它们;写库时由调用方换成真实 id。
type RoutingSeed struct {
	Groups []model.ProxyGroup
	Rules  []model.RouteRule
	Final  string
}

// DefaultRoutingSeed 是内置 Clash 底稿对应的规则表(新库与没存过模板的控制面用它)。
func DefaultRoutingSeed() (*RoutingSeed, error) {
	seed, bad, err := ConvertClashTemplate(clashDefaultTemplate)
	if err != nil {
		return nil, err
	}
	if len(bad) > 0 {
		return nil, fmt.Errorf("内置底稿有无法转换的规则: %s", strings.Join(bad, "; "))
	}
	return seed, nil
}

type clashTplGroup struct {
	Name       string   `yaml:"name"`
	Type       string   `yaml:"type"`
	Proxies    []string `yaml:"proxies"`
	IncludeAll bool     `yaml:"include-all-proxies"`
	IncludeAny bool     `yaml:"include-all"`
	Filter     string   `yaml:"filter"`
	Use        []string `yaml:"use"`
	URL        string   `yaml:"url"`
	Interval   int      `yaml:"interval"`
}

// ConvertClashTemplate 把模板里的分流部分转换成规则表。转换不了的规则逐条列在
// bad 里;只要 bad 非空,调用方就不应采用这份结果(RFC:不静默丢规则)。
//
// 组的转换:url-test 等自动组、以及只靠 include-all-proxies 收全部节点的 select 组,
// 变成策略组;列了选项的 select 组沿第一个选项解析(和客户端的默认选择一致);
// 第一个选项是具体节点的组无法表达(规则不能固定到节点)。
func ConvertClashTemplate(tpl []byte) (*RoutingSeed, []string, error) {
	var t struct {
		Providers map[string]struct {
			URL string `yaml:"url"`
		} `yaml:"rule-providers"`
		Groups []clashTplGroup `yaml:"proxy-groups"`
		Rules  []string        `yaml:"rules"`
	}
	if err := yaml.Unmarshal(tpl, &t); err != nil {
		return nil, nil, fmt.Errorf("解析 Clash 模板: %w", err)
	}
	seed := &RoutingSeed{Final: model.TargetDirect}
	bad := []string{}
	byName := map[string]clashTplGroup{}
	for _, g := range t.Groups {
		byName[g.Name] = g
	}
	strategy := map[string]string{} // 模板组名 → 策略组去向
	memo := map[string]string{}
	var resolve func(name string, depth int) (string, error)
	resolve = func(name string, depth int) (string, error) {
		switch name {
		case "DIRECT", "PASS":
			return model.TargetDirect, nil
		case "REJECT", "REJECT-DROP":
			return model.TargetReject, nil
		}
		if v, ok := memo[name]; ok {
			return v, nil
		}
		if depth > 16 {
			return "", fmt.Errorf("组 %s 循环引用", name)
		}
		g, ok := byName[name]
		if !ok {
			return "", fmt.Errorf("%s 不是组(规则不能指向单个节点)", name)
		}
		if g.Filter != "" || len(g.Use) > 0 {
			return "", fmt.Errorf("组 %s 用了 filter/use,无法表达", name)
		}
		var out string
		switch {
		case g.Type == "select" && len(g.Proxies) > 0:
			v, err := resolve(g.Proxies[0], depth+1)
			if err != nil {
				return "", err
			}
			out = v
		case g.Type == "select" && (g.IncludeAll || g.IncludeAny):
			out = addSeedGroup(seed, strategy, g, model.GroupManual)
		case (g.Type == "url-test" || g.Type == "fallback" || g.Type == "load-balance") && (g.IncludeAll || g.IncludeAny) && len(g.Proxies) == 0:
			out = addSeedGroup(seed, strategy, g, model.GroupAuto)
		default:
			return "", fmt.Errorf("组 %s 的写法无法表达", name)
		}
		memo[name] = out
		return out, nil
	}
	// 先把所有组转换一遍,让「手动切换」这类没被规则直接引用的组也保留下来,顺序同模板。
	for _, g := range t.Groups {
		if g.Type != "select" || len(g.Proxies) == 0 {
			_, _ = resolve(g.Name, 0)
		}
	}

	type setRef struct {
		name   string
		target string
	}
	var run []setRef
	flush := func() {
		for i := 0; i < len(run); {
			best := ""
			bestLen := 0
			for _, rs := range model.RuleCatalog() {
				n := len(rs.Clash)
				if n <= bestLen || i+n > len(run) {
					continue
				}
				match := true
				for j, c := range rs.Clash {
					r := run[i+j]
					if r.name != c.Name || r.target != run[i].target || t.Providers[r.name].URL != c.URL {
						match = false
						break
					}
				}
				if match {
					best, bestLen = rs.Key, n
				}
			}
			if bestLen == 0 {
				bad = append(bad, "RULE-SET,"+run[i].name+" 不对应任何内置规则组")
				i++
				continue
			}
			seed.Rules = append(seed.Rules, model.RouteRule{Type: model.RuleSetRef, Value: best, Target: run[i].target, Enabled: true})
			i += bestLen
		}
		run = nil
	}
	for _, line := range t.Rules {
		f := strings.Split(line, ",")
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		kind := f[0]
		if kind == "MATCH" && len(f) >= 2 {
			flush()
			to, err := resolve(f[1], 0)
			if err != nil {
				bad = append(bad, line+": "+err.Error())
				continue
			}
			seed.Final = to
			continue
		}
		if len(f) < 3 {
			flush()
			bad = append(bad, line+": 无法解析")
			continue
		}
		to, err := resolve(f[2], 0)
		if err != nil {
			flush()
			bad = append(bad, line+": "+err.Error())
			continue
		}
		if kind == "RULE-SET" {
			if len(run) > 0 && run[len(run)-1].target != to {
				flush()
			}
			run = append(run, setRef{f[1], to})
			continue
		}
		flush()
		var typ model.RuleType
		switch kind {
		case "DOMAIN-SUFFIX":
			typ = model.RuleSuffix
		case "DOMAIN-KEYWORD":
			typ = model.RuleKeyword
		case "GEOIP":
			typ = model.RuleGeoIP
		case "IP-CIDR", "IP-CIDR6":
			typ = model.RuleCIDR
		default:
			bad = append(bad, line+": 不支持的规则类型 "+kind)
			continue
		}
		r := model.RouteRule{Type: typ, Value: f[1], Target: to, Enabled: true}
		r.Normalize()
		if err := r.Validate(); err != nil {
			bad = append(bad, line+": "+err.Error())
			continue
		}
		seed.Rules = append(seed.Rules, r)
	}
	flush()
	return seed, bad, nil
}

func addSeedGroup(seed *RoutingSeed, strategy map[string]string, g clashTplGroup, mode model.GroupMode) string {
	if t, ok := strategy[g.Name]; ok {
		return t
	}
	pg := model.ProxyGroup{ID: int64(len(seed.Groups) + 1), Name: g.Name, Mode: mode, TestURL: g.URL, Interval: g.Interval}
	pg.Normalize()
	seed.Groups = append(seed.Groups, pg)
	t := model.GroupTarget(pg.ID)
	strategy[g.Name] = t
	return t
}
