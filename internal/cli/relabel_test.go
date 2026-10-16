package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestNodeRelabel(t *testing.T) {
	var mu sync.Mutex
	meta := map[int64]model.PurposeMetadata{
		3: {Region: "US", City: "洛杉矶", Letter: "A"}, // 已有显示名:跳过,占住 A
	}
	nodes := []model.Node{{ID: 1, Name: "la-1"}, {ID: 2, Name: "la-2"}, {ID: 3, Name: "la-old"}, {ID: 4, Name: "lab"}}
	var puts []int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var id int64
		switch {
		case r.URL.Path == "/api/nodes":
			var vs []model.NodeView
			for _, n := range nodes {
				vs = append(vs, model.NodeView{Node: n, Display: model.NodeDisplay(meta[n.ID])})
			}
			json.NewEncoder(w).Encode(vs)
		case strings.HasSuffix(r.URL.Path, "/locate"):
			fmt.Sscanf(r.URL.Path, "/api/nodes/%d/locate", &id)
			if id == 4 {
				w.WriteHeader(502)
				fmt.Fprint(w, `{"error":"ipwho.is 没给出结果"}`)
				return
			}
			fmt.Fprintf(w, `{"region":"US","city":"洛杉矶","city_en":"Los Angeles","ip":"1.2.3.%d","source":"ipwho.is","via":"control"}`, id)
		case strings.HasSuffix(r.URL.Path, "/metadata"):
			fmt.Sscanf(r.URL.Path, "/api/nodes/%d/metadata", &id)
			if r.Method == "PUT" {
				var m model.PurposeMetadata
				json.NewDecoder(r.Body).Decode(&m)
				meta[id] = m
				puts = append(puts, id)
			}
			json.NewEncoder(w).Encode(meta[id])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "t")
	t.Cleanup(func() { nodeRelabelFlags.all, nodeRelabelFlags.dryRun, nodeRelabelFlags.yes = false, false, false })

	out := captureStdout(t, func() { runRoot(t, "node", "relabel", "--all", "--dry-run") })
	for _, want := range []string{"🇺🇸 洛杉矶 B", "🇺🇸 洛杉矶 C", "已有显示名,跳过", "识别失败", "--dry-run:没有写入"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run 输出缺 %q:\n%s", want, out)
		}
	}
	if len(puts) != 0 {
		t.Fatalf("dry-run 不应写入: %v", puts)
	}
	nodeRelabelFlags.dryRun = false
	captureStdout(t, func() { runRoot(t, "node", "relabel", "--all", "--yes", "--dry-run=false") })
	if fmt.Sprint(puts) != "[1 2]" || meta[1].Letter != "B" || meta[2].Letter != "C" || meta[3].Letter != "A" {
		t.Fatalf("写入结果 puts=%v meta=%v", puts, meta)
	}
}

// fakeDoer 是最小的 doer:按路径记下调用,metadata 存在内存里。
type fakeDoer struct {
	mu    sync.Mutex
	calls []string
	meta  map[int64]model.PurposeMetadata
}

func (f *fakeDoer) do(method, path string, body any) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+path)
	var id int64
	fmt.Sscanf(path, "/api/nodes/%d/metadata", &id)
	if method == "GET" {
		return json.Marshal(f.meta[id])
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var m model.PurposeMetadata
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	f.meta[id] = m
	return json.Marshal(m)
}

func (f *fakeDoer) has(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.Contains(c, s) {
			return true
		}
	}
	return false
}

// apply 只写计划里的那份 metadata,绝不重新 locate;节点中途被别人改过就跳过。
func TestRelabelApplyNoLocateAndSkipsChanged(t *testing.T) {
	f := &fakeDoer{meta: map[int64]model.PurposeMetadata{
		1: {},                                      // 干净:按计划写
		2: {Region: "JP", City: "东京", Letter: "A"}, // 已被改过:跳过
	}}
	plans := []relabelPlanItem{
		{ID: 1, Name: "n1", Metadata: model.PurposeMetadata{Region: "US", City: "洛杉矶", Letter: "B"}},
		{ID: 2, Name: "n2", Metadata: model.PurposeMetadata{Region: "US", City: "洛杉矶", Letter: "C"}},
	}
	results, err := relabelApply(f, plans)
	if err != nil {
		t.Fatal(err)
	}
	if f.has("/locate") {
		t.Fatalf("apply 不应调 locate: %v", f.calls)
	}
	if len(results) != 2 {
		t.Fatalf("results=%+v", results)
	}
	if results[0].Skipped || results[0].Display != "🇺🇸 洛杉矶 B" {
		t.Fatalf("节点 1 应写入: %+v", results[0])
	}
	if !results[1].Skipped || results[1].Reason != "已被改过,跳过" {
		t.Fatalf("节点 2 应跳过: %+v", results[1])
	}
	if f.meta[2].Region != "JP" || f.meta[2].Letter != "A" {
		t.Fatalf("跳过时不能改节点 2: %+v", f.meta[2])
	}
	if f.meta[1].City != "洛杉矶" || f.meta[1].Letter != "B" {
		t.Fatalf("节点 1 未按计划写入: %+v", f.meta[1])
	}
}
