package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// TestUserCSVExportIncludesPlanName 断言导出的 CSV 有「套餐」列并写套餐名。
func TestUserCSVExportIncludesPlanName(t *testing.T) {
	planID := int64(5)
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/users":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"users": []model.User{
					{ID: 1, Username: "alice", Enabled: true, PlanID: &planID},
					{ID: 2, Username: "bob", Enabled: true},
				},
				"total": 2, "limit": 1000, "offset": 0,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans":
			_ = json.NewEncoder(w).Encode([]model.Plan{{ID: 5, Name: "全球"}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"没实现"}`))
		}
	})

	opts.output = outputTable
	out := captureStdout(t, func() { runRoot(t, "user", "export") })

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("导出内容 = %q", out)
	}
	header := lines[0]
	if !strings.Contains(header, csvColumnPlan) {
		t.Fatalf("CSV 表头没有「%s」列: %q", csvColumnPlan, header)
	}
	// 找 alice 那一行,套餐列应当是「全球」。
	for _, line := range lines[1:] {
		if strings.HasPrefix(line, "alice,") {
			if !strings.Contains(line, "全球") {
				t.Fatalf("alice 的套餐列没写套餐名: %q", line)
			}
			return
		}
	}
	t.Fatalf("导出里没有 alice: %q", out)
}

// TestUserCSVImportPlanColumn 断言导入时「套餐」列解析成 plan_id;空列不改归属。
func TestUserCSVImportPlanColumn(t *testing.T) {
	var posts []model.User
	var rawPosts []map[string]json.RawMessage
	handler := func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans":
			_ = json.NewEncoder(w).Encode([]model.Plan{{ID: 5, Name: "全球"}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/users/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"用户不存在"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/users":
			var u model.User
			_ = json.Unmarshal(body, &u)
			posts = append(posts, u)
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(body, &raw)
			rawPosts = append(rawPosts, raw)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"没实现"}`))
		}
	}
	c := testClient(t, handler)

	csvText := strings.Join([]string{
		strings.Join(userCSVHeader, ","),
		"bob,1GB,2027-01-01,启用,none,全部,全球,备注一",
		"carol,2GB,2027-02-01,启用,none,全部,,备注二",
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Failed != 0 || rep.Added != 2 {
		t.Fatalf("导入结果 = %+v, want 2 新增 0 失败", rep)
	}
	if len(posts) != 2 {
		t.Fatalf("POST 次数 = %d, want 2", len(posts))
	}
	if posts[0].PlanID == nil || *posts[0].PlanID != 5 {
		t.Fatalf("bob 的 plan_id = %v, want 5", posts[0].PlanID)
	}
	if posts[1].PlanID != nil {
		t.Fatalf("空套餐列不该带 plan_id: %v", posts[1].PlanID)
	}
	// 空单元格要的是「键根本不出现」(不带 = 保持原套餐),而不是 `"plan_id":null`
	// (那会摘掉别人刚挂上的套餐)。
	if _, present := rawPosts[0]["plan_id"]; !present {
		t.Fatalf("有套餐名时 POST 体里应当有 plan_id 键: %v", rawPosts[0])
	}
	if raw, present := rawPosts[1]["plan_id"]; present {
		t.Fatalf("空套餐列不该出现 plan_id 键,实际是 %s", raw)
	}
}

// TestUserCSVImportUnknownPlanFailsRow 断言套餐名不存在时该行失败,其余行照常。
func TestUserCSVImportUnknownPlanFailsRow(t *testing.T) {
	alive := 0
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/plans":
			_ = json.NewEncoder(w).Encode([]model.Plan{{ID: 5, Name: "全球"}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/users/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"用户不存在"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/users":
			alive++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{}`))
		}
	}
	c := testClient(t, handler)
	csvText := strings.Join([]string{
		strings.Join(userCSVHeader, ","),
		"bob,1GB,2027-01-01,启用,none,全部,不存在的套餐,备注一",
		"carol,2GB,2027-02-01,启用,none,全部,全球,备注二",
	}, "\n") + "\n"

	rep, err := importUsersCSV(c, strings.NewReader(csvText))
	if err != nil {
		t.Fatalf("importUsersCSV: %v", err)
	}
	if rep.Failed != 1 || rep.Added != 1 {
		t.Fatalf("导入结果 = %+v, want 1 失败 1 新增", rep)
	}
	if !strings.Contains(rep.Errors[0].Error, "套餐") {
		t.Fatalf("错误信息没提到套餐: %q", rep.Errors[0].Error)
	}
	if alive != 1 {
		t.Fatalf("只有 carol 该被写进去, got %d", alive)
	}
}
