package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocateNode(t *testing.T) {
	geo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1.2.3.4":
			fmt.Fprint(w, `{"ip":"1.2.3.4","success":true,"country_code":"US","city":"Los Angeles"}`)
		case "/5.6.7.8":
			fmt.Fprint(w, `{"ip":"5.6.7.8","success":true,"country_code":"DE","city":"Gunzenhausen"}`)
		default:
			fmt.Fprint(w, `{"success":false,"message":"reserved range"}`)
		}
	}))
	defer geo.Close()
	old := geoBaseURL
	geoBaseURL = geo.URL + "/"
	defer func() { geoBaseURL = old }()

	e := newEnv(t)
	cases := []struct {
		addr, display, city string
		code                int
	}{
		{"1.2.3.4", "🇺🇸 洛杉矶", "洛杉矶", 200},
		{"5.6.7.8", "🇩🇪 德国", "", 200}, // 城市不在对照表里:只给国家
		{"10.0.0.1", "", "", 502},
		{"la.example.com", "", "", 502}, // 域名走节点探测;测试环境没有执行器

	}
	for i, c := range cases {
		n := e.createNode(fmt.Sprintf("n%d", i), c.addr)
		code, raw := e.api("POST", fmt.Sprintf("/api/nodes/%d/locate", n.ID), nil)
		if code != c.code {
			t.Fatalf("%s: code %d %s", c.addr, code, raw)
		}
		if code != 200 {
			continue
		}
		var sg LocateSuggestion
		json.Unmarshal(raw, &sg)
		if sg.Display != c.display || sg.City != c.city || sg.Via != "control" {
			t.Fatalf("%s: %+v", c.addr, sg)
		}
		// 只是建议:不写入元数据。
		_, mraw := e.api("GET", fmt.Sprintf("/api/nodes/%d/metadata", n.ID), nil)
		var m struct{ Region string }
		json.Unmarshal(mraw, &m)
		if m.Region != "" {
			t.Fatalf("locate 不应写入: %s", mraw)
		}
	}
}
