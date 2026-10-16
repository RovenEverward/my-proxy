package ui

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeProfiles 是测试用的 ProfileSource:一份内存里的档表,Resolve 把档名映射到
// 一个本地假控制面。sources 记录每个档的控制面,便于断言切换后转发到了哪。
type fakeProfiles struct {
	infos  []ProfileInfo
	ups    map[string]*url.URL
	tokens map[string]string
}

func (f *fakeProfiles) List() ([]ProfileInfo, error) {
	out := make([]ProfileInfo, len(f.infos))
	copy(out, f.infos)
	return out, nil
}

func (f *fakeProfiles) Resolve(name string) (*url.URL, string, http.RoundTripper, error) {
	up, ok := f.ups[name]
	if !ok {
		return nil, "", nil, errors.New("没有名为 " + name + " 的配置档")
	}
	return up, f.tokens[name], nil, nil
}

// profileRig 起两个假控制面(a/b)与一个带 profiles 的 Server,返回已登录 cookie。
func profileRig(t *testing.T) (*Server, *http.Cookie, *seen, *seen) {
	t.Helper()
	mk := func(tag string) (*httptest.Server, *seen) {
		sn := &seen{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sn.mu.Lock()
			sn.reqs = append(sn.reqs, r.Clone(r.Context()))
			sn.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"who":"`+tag+`"}`)
		}))
		t.Cleanup(srv.Close)
		return srv, sn
	}
	cpA, seenA := mk("a")
	cpB, seenB := mk("b")
	upA, _ := url.Parse(cpA.URL)
	upB, _ := url.Parse(cpB.URL)
	fp := &fakeProfiles{
		infos: []ProfileInfo{
			{Name: "alpha", Server: cpA.URL, Current: true},
			{Name: "beta", Server: cpB.URL},
		},
		ups:    map[string]*url.URL{"alpha": upA, "beta": upB},
		tokens: map[string]string{"alpha": "token-a", "beta": "token-b"},
	}
	s, err := New("127.0.0.1:7480", Options{Upstream: upA, Token: "token-a", Profile: "alpha",
		Allow: []string{"GET /api"}, Profiles: fp})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/?k="+s.Key(), nil)
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	if c == nil {
		t.Fatal("没拿到会话 cookie")
	}
	return s, c, seenA, seenB
}

func TestProfilesMethodAndSession(t *testing.T) {
	s, c, _, _ := profileRig(t)
	// 只接受 POST:GET 405(有会话)。
	for _, p := range []string{"/local/profiles", "/local/profiles/use"} {
		if w := do(s, "GET", p, c); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s 应 405,得到 %d %s", p, w.Code, w.Body)
		}
	}
	// 无会话 401(在方法校验之前)。
	for _, p := range []string{"/local/profiles", "/local/profiles/use"} {
		if w := doJSON(s, "POST", p, nil, "{}"); w.Code != http.StatusUnauthorized {
			t.Errorf("无会话 POST %s 应 401,得到 %d", p, w.Code)
		}
	}
}

func TestProfilesNilIs404(t *testing.T) {
	s, _, _, c := rig(t)
	for _, p := range []string{"/local/profiles", "/local/profiles/use"} {
		if w := doJSON(s, http.MethodPost, p, c, "{}"); w.Code != http.StatusNotFound {
			t.Errorf("未启用 profiles 时 POST %s 应 404,得到 %d %s", p, w.Code, w.Body)
		}
	}
}

func TestProfilesListHidesSecrets(t *testing.T) {
	s, c, _, _ := profileRig(t)
	w := doJSON(s, http.MethodPost, "/local/profiles", c, "{}")
	if w.Code != http.StatusOK {
		t.Fatalf("列配置档应 200,得到 %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "token-a") || strings.Contains(w.Body.String(), "token-b") {
		t.Fatal("列表泄露了 token")
	}
	var list []ProfileInfo
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "alpha" || !list[0].Current || list[1].Current {
		t.Fatalf("列表不对: %s", w.Body)
	}
	if list[0].Server == "" {
		t.Fatalf("应有控制面地址: %s", w.Body)
	}
}

func TestProfilesUseSwitchesUpstream(t *testing.T) {
	s, c, seenA, seenB := profileRig(t)
	w := doJSON(s, http.MethodPost, "/local/profiles/use", c, `{"name":"beta"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("切换应 200,得到 %d %s", w.Code, w.Body)
	}
	// 切换后 /api 应转到新的 upstream,并带上新 token。
	if r := do(s, "GET", "/api/status", c); r.Code != 200 || !strings.Contains(r.Body.String(), `"who":"b"`) {
		t.Fatalf("切换后应转发到 b,得到 %d %s", r.Code, r.Body)
	}
	if seenB.count() != 1 {
		t.Fatalf("b 应收 1 条,实际 %d", seenB.count())
	}
	if seenA.count() != 0 {
		t.Fatalf("切换后不该再打 a,实际 %d", seenA.count())
	}
	if got := seenB.last().Header.Get("Authorization"); got != "Bearer token-b" {
		t.Fatalf("新 token 不对: %q", got)
	}
	// meta 的 profile 也跟着换。
	var m map[string]any
	wm := do(s, "GET", "/ui/meta", c)
	_ = json.Unmarshal(wm.Body.Bytes(), &m)
	if m["profile"] != "beta" || !strings.Contains(m["server"].(string), seenB.last().Host) {
		t.Fatalf("meta 未跟随切换: %s", wm.Body)
	}
	// 再列一次:当前档变成 beta。
	wl := doJSON(s, http.MethodPost, "/local/profiles", c, "{}")
	var list []ProfileInfo
	_ = json.Unmarshal(wl.Body.Bytes(), &list)
	if len(list) != 2 || list[1].Name != "beta" || !list[1].Current {
		t.Fatalf("切换后 current 应为 beta: %s", wl.Body)
	}
}

func TestProfilesUseBadName(t *testing.T) {
	s, c, _, _ := profileRig(t)
	if w := doJSON(s, http.MethodPost, "/local/profiles/use", c, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("空名字应 400,得到 %d %s", w.Code, w.Body)
	}
	if w := doJSON(s, http.MethodPost, "/local/profiles/use", c, `{"name":"nope"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("不存在的档应 400,得到 %d %s", w.Code, w.Body)
	}
}

// TestProfilesUseInSetupMode:没有 proxy 的首次安装模式也要能切到已有的档。
func TestProfilesUseInSetupMode(t *testing.T) {
	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer cp.Close()
	up, _ := url.Parse(cp.URL)
	fp := &fakeProfiles{infos: []ProfileInfo{{Name: "alpha", Server: cp.URL, Current: true}},
		ups: map[string]*url.URL{"alpha": up}, tokens: map[string]string{"alpha": "t"}}
	s, err := New("127.0.0.1:7480", Options{Profiles: fp, Allow: []string{"GET /api"}})
	if err != nil {
		t.Fatal(err)
	}
	w := do(s, "GET", "/?k="+s.Key(), nil)
	var c *http.Cookie
	for _, x := range w.Result().Cookies() {
		c = x
	}
	wl := doJSON(s, http.MethodPost, "/local/profiles", c, "{}")
	if wl.Code != http.StatusOK {
		t.Fatalf("setup 模式下列配置档应 200,得到 %d %s", wl.Code, wl.Body)
	}
	// 列出的 current 保留实现方的结论(进程里还没有 profile)。
	var list []ProfileInfo
	_ = json.Unmarshal(wl.Body.Bytes(), &list)
	if len(list) != 1 || !list[0].Current {
		t.Fatalf("setup 列表不对: %s", wl.Body)
	}
	wu := doJSON(s, http.MethodPost, "/local/profiles/use", c, `{"name":"alpha"}`)
	if wu.Code != http.StatusOK {
		t.Fatalf("setup 模式切换应 200,得到 %d %s", wu.Code, wu.Body)
	}
	if r := do(s, "GET", "/api/status", c); r.Code != 200 {
		t.Fatalf("切换后转发应 200,得到 %d %s", r.Code, r.Body)
	}
}
