package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// lastPutBody 返回针对某个用户名最后一次 PUT 的请求体(原始 JSON 对象)。
//
// 用 map[string]json.RawMessage 而不是 model.User:前者的键在不在,正是
// 「没带 plan_id」与「带了 null」的分界,反序列化成指针就把这一步丢了。
func lastPutBody(t *testing.T, f *fakeUserAPI, username string) map[string]json.RawMessage {
	t.Helper()
	var last []byte
	for _, r := range f.recorded() {
		if r.Method == http.MethodPut && r.Username == username {
			last = r.Body
		}
	}
	if last == nil {
		t.Fatalf("%s 没有发出 PUT 请求", username)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(last, &body); err != nil {
		t.Fatalf("PUT 请求体不是 JSON 对象: %v\n%s", err, last)
	}
	return body
}

// TestUserReadModifyWriteOmitsPlanID 断言所有"读回来整体回写"的子命令,在用户
// 没给 --plan 时请求体里根本不带 plan_id 键。
//
// plan_id 的语义是「不带 = 保持原套餐」,服务端据此区分三态。CLI 若把 GET 回来
// 的归属原样回传,并发下就会把别人刚改的套餐覆盖回去;UI 不带而 CLI 带,浏览器
// 对照测试也会失败。
func TestUserReadModifyWriteOmitsPlanID(t *testing.T) {
	planID := int64(7)
	cases := []struct {
		name string
		args []string
	}{
		{"renew", []string{"renew", "alice", "--days", "30"}},
		{"disable", []string{"disable", "alice"}},
		{"enable", []string{"enable", "alice"}},
		{"edit", []string{"edit", "alice", "--note", "改过"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeUserAPI(t)
			f.add(model.User{Username: "alice", Enabled: true, PlanID: &planID})

			runUser(t, tc.args...)

			body := lastPutBody(t, f, "alice")
			if raw, present := body["plan_id"]; present {
				t.Fatalf("没给 --plan 时不该带 plan_id,实际带的是 %s", raw)
			}
			got, ok := f.get("alice")
			if !ok {
				t.Fatal("alice 不见了")
			}
			if got.PlanID == nil || *got.PlanID != planID {
				t.Fatalf("套餐归属被改动了: %v", got.PlanID)
			}
		})
	}
}

// TestUserEditPlanFlagSendsExplicitPlanID 断言显式给 --plan 时仍然照发:
// 给名字发数字 id,给空串发 null(摘掉)。别为了"不带"把显式操作也吞掉。
func TestUserEditPlanFlagSendsExplicitPlanID(t *testing.T) {
	planID := int64(7)
	f := newFakeUserAPI(t)
	f.addPlan(model.Plan{ID: planID, Name: "全球"})
	f.add(model.User{Username: "alice", Enabled: true})

	runUser(t, "edit", "alice", "--plan", "全球")
	body := lastPutBody(t, f, "alice")
	raw, present := body["plan_id"]
	if !present {
		t.Fatal("--plan 全球 没带 plan_id")
	}
	if strings.TrimSpace(string(raw)) != "7" {
		t.Fatalf("plan_id = %s, want 7", raw)
	}

	// --plan "" 在本地就拒绝,不发请求。
	resetUserCLIFlags()
	if err := runRootErr(t, "user", "edit", "alice", "--plan", ""); err == nil {
		t.Fatal("--plan \"\" 应当被拒绝")
	}
	resetUserCLIFlags()
}
