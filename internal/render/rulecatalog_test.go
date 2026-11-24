package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"gopkg.in/yaml.v3"
)

// 默认模板里引用的每份规则集都必须与内置目录一致(URL / behavior / format),
// 且目录里每一项都应指向 MetaCubeX/meta-rules-dat 的 .mrs / .srs。
func TestCatalogMatchesDefaults(t *testing.T) {
	var clash struct {
		Providers map[string]struct {
			Behavior string `yaml:"behavior"`
			Format   string `yaml:"format"`
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
	clashCatalog := map[string]model.ClashRuleSet{}
	sbCatalog := map[string]model.SingBoxRuleSet{}
	for _, rs := range model.RuleCatalog() {
		for _, c := range rs.Clash {
			if !strings.HasSuffix(c.URL, ".mrs") {
				t.Errorf("%s: Clash 规则集 %s 应使用 .mrs: %s", rs.Key, c.Name, c.URL)
			}
			clashCatalog[c.Name] = c
		}
		for _, s := range rs.SingBox {
			if !strings.HasSuffix(s.URL, ".srs") {
				t.Errorf("%s: sing-box 规则集 %s 应使用 .srs: %s", rs.Key, s.Tag, s.URL)
			}
			sbCatalog[s.Tag] = s
		}
	}
	for name, p := range clash.Providers {
		c, ok := clashCatalog[name]
		if !ok {
			t.Errorf("默认 Clash 模板里的 provider %s 不在目录里", name)
			continue
		}
		if p.URL != c.URL || p.Behavior != c.Behavior || p.Format != "mrs" {
			t.Errorf("Clash provider %s 与目录不一致: tpl=%+v cat=%+v", name, p, c)
		}
	}
	for _, r := range clash.Rules {
		if f := strings.Split(r, ","); f[0] == "RULE-SET" {
			if _, ok := clashCatalog[f[1]]; !ok {
				t.Errorf("默认模板的规则集 %s 不在目录里", f[1])
			}
		}
	}
	for _, r := range sb.Route.RuleSet {
		s, ok := sbCatalog[r.Tag]
		if !ok {
			t.Errorf("默认 sing-box 模板里的 rule_set %s 不在目录里", r.Tag)
			continue
		}
		if r.URL != s.URL {
			t.Errorf("sing-box rule_set %s URL 不一致: tpl=%s cat=%s", r.Tag, r.URL, s.URL)
		}
	}
}
