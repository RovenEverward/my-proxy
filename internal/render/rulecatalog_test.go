package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

// 内置规则组目录是从两份默认模板生成的;任何一边改了 URL 或删了规则集,这里会报出来。
func TestCatalogMatchesDefaults(t *testing.T) {
	var clash struct {
		Providers map[string]struct {
			Behavior string `yaml:"behavior"`
			URL      string `yaml:"url"`
		} `yaml:"rule-providers"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(clashDefaultTemplate, &clash); err != nil {
		t.Fatal(err)
	}
	var sb struct {
		Route struct {
			RuleSet []struct{ Tag, URL string } `json:"rule_set"`
		} `json:"route"`
	}
	if err := json.Unmarshal(singboxDefaultTemplate, &sb); err != nil {
		t.Fatal(err)
	}
	sbURL := map[string]string{}
	for _, r := range sb.Route.RuleSet {
		sbURL[r.Tag] = r.URL
	}
	inCatalog := map[string]bool{}
	for _, rs := range model.RuleCatalog() {
		for _, c := range rs.Clash {
			p, ok := clash.Providers[c.Name]
			if !ok || p.URL != c.URL || p.Behavior != c.Behavior {
				t.Errorf("%s: Clash 规则集 %s 与默认模板不一致", rs.Key, c.Name)
			}
			inCatalog[c.Name] = true
		}
		for _, s := range rs.SingBox {
			if sbURL[s.Tag] != s.URL {
				t.Errorf("%s: sing-box 规则集 %s 与默认模板不一致", rs.Key, s.Tag)
			}
		}
	}
	for _, r := range clash.Rules {
		if f := strings.Split(r, ","); f[0] == "RULE-SET" && !inCatalog[f[1]] {
			t.Errorf("默认模板的规则集 %s 不在目录里", f[1])
		}
	}
}
