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
