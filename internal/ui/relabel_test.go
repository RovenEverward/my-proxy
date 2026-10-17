package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// fakeRelabel 记录 ui 转交给 runner 的请求,并回一份固定的对照表。
type fakeRelabel struct {
	mu      sync.Mutex
	plans   []RelabelRequest
	applied [][]RelabelPlanItem
}

func (f *fakeRelabel) Plan(_ context.Context, _ *url.URL, _ string, _ http.RoundTripper, req RelabelRequest) ([]RelabelRow, []RelabelPlanItem, error) {
	f.mu.Lock()
	f.plans = append(f.plans, req)
	f.mu.Unlock()
	return []RelabelRow{{ID: 1, Name: "n1", Old: "n1", New: "🇺🇸 洛杉矶 A", Note: "ipwho.is 1.2.3.4"}},
		[]RelabelPlanItem{{ID: 1, Name: "n1", Metadata: model.LocationMetadata{Region: "US", City: "洛杉矶", Letter: "A"}}}, nil
}

func (f *fakeRelabel) Apply(_ context.Context, _ *url.URL, _ string, _ http.RoundTripper, plans []RelabelPlanItem) ([]RelabelResult, error) {
	f.mu.Lock()
	f.applied = append(f.applied, plans)
	f.mu.Unlock()
	return []RelabelResult{{ID: 1, Name: "n1", Display: "🇺🇸 洛杉矶 A"}}, nil
}

func TestRelabelOnlyPost(t *testing.T) {
	s, _, _, c := rig(t)
	s.opts.Relabel = &fakeRelabel{}
	o := []string{"Origin", "http://127.0.0.1:7480", "Content-Type", "application/json"}
	for _, p := range []string{"/local/relabel/plan", "/local/relabel/apply"} {
		if w := do(s, "GET", p, c, o...); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s 应 405,得到 %d", p, w.Code)
		}
	}
}

func TestRelabelNeedsSession(t *testing.T) {
	s, _, sn, _ := rig(t)
	s.opts.Relabel = &fakeRelabel{}
	for _, p := range []string{"/local/relabel/plan", "/local/relabel/apply"} {
		if w := do(s, "POST", p, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("无会话 POST %s 应 401,得到 %d", p, w.Code)
		}
	}
	if sn.count() != 0 {
		t.Fatal("未登录的 relabel 请求不能到控制面")
	}
}

func TestRelabelNilRunnerIs404(t *testing.T) {
	s, _, _, c := rig(t)
	o := []string{"Origin", "http://127.0.0.1:7480"}
	if w := do(s, "POST", "/local/relabel/plan", c, o...); w.Code != http.StatusNotFound {
		t.Fatalf("没有 runner 应 404,得到 %d", w.Code)
	}
}

// postJSON 发一个带 Origin 与 JSON body 的写请求。
func postJSON(s *Server, c *http.Cookie, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://127.0.0.1:7480"+path, strings.NewReader(body))
	r.AddCookie(c)
	r.Header.Set("Origin", "http://127.0.0.1:7480")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestRelabelPlanThenApply(t *testing.T) {
	f := &fakeRelabel{}
	s, _, _, c := rig(t)
	s.opts.Relabel = f

	w := postJSON(s, c, "/local/relabel/plan", `{"all":true}`)
	if w.Code != 200 {
		t.Fatalf("plan 应 200,得到 %d %s", w.Code, w.Body)
	}
	var plan struct {
		Rows  []RelabelRow      `json:"rows"`
		Plans []RelabelPlanItem `json:"plans"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Rows) != 1 || len(plan.Plans) != 1 {
		t.Fatalf("plan 响应: %s", w.Body)
	}
	if !f.plans[0].All {
		t.Fatalf("plan 请求应带 all:true,得到 %+v", f.plans[0])
	}

	// 页面把 plan 返回的 plans 原样回传:apply 写的就是这份 metadata,不再自己算。
	body, _ := json.Marshal(map[string]any{"plans": plan.Plans})
	w = postJSON(s, c, "/local/relabel/apply", string(body))
	if w.Code != 200 {
		t.Fatalf("apply 应 200,得到 %d %s", w.Code, w.Body)
	}
	if len(f.applied) != 1 || len(f.applied[0]) != 1 || f.applied[0][0].Metadata.City != "洛杉矶" {
		t.Fatalf("apply 收到的 plans 不对: %+v", f.applied)
	}
	if len(f.plans) != 1 {
		t.Fatal("apply 不应该再调 plan")
	}
}
