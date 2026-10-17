package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// TestSubPullRecordedAndBodyUnchanged:拉一次订阅后 /api/sub-pulls 有记录,
// 再拉一次正文与第一次逐字节相同。
func TestSubPullRecordedAndBodyUnchanged(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("hk", "node.example.com")
	in := e.createInbound(n.ID, "vless-ws", 8443)
	e.createHost(in.ID, "香港 ws tls", "node.example.com")
	user := e.createUser("alice")

	path := "/sub/" + user.SubToken + "/links"
	code, hdr1, body1 := e.sub(path, "v2rayng/1.8.0")
	if code != http.StatusOK {
		t.Fatalf("第一次订阅: %d %s", code, body1)
	}
	// 记录拉取不能动正文:它必须和直接渲染出来的那一份逐字节相同。
	full, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := e.s.renderSubscription(full, model.FormatLinks)
	if err != nil {
		t.Fatal(err)
	}
	if string(body1) != string(rendered) {
		t.Fatalf("订阅正文被拉取记录影响:\n got=%s\nwant=%s", body1, rendered)
	}
	code, data := e.api("GET", "/api/sub-pulls", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/sub-pulls: %d %s", code, data)
	}
	pulls := decodeJSON[[]model.SubPull](t, data)
	if len(pulls) != 1 {
		t.Fatalf("拉取记录 = %+v, want 1 条", pulls)
	}
	p := pulls[0]
	if p.Username != "alice" || p.Format != string(model.FormatLinks) || p.Client != "v2rayng" {
		t.Fatalf("拉取记录 = %+v", p)
	}
	if p.FirstPulledAt.IsZero() || !p.FirstPulledAt.Equal(p.LastPulledAt) {
		t.Fatalf("第一次拉取的 first/last 应相同: %+v", p)
	}

	code, hdr2, body2 := e.sub(path, "v2rayng/1.8.0")
	if code != http.StatusOK {
		t.Fatalf("第二次订阅: %d %s", code, body2)
	}
	if string(body1) != string(body2) {
		t.Fatalf("两次订阅正文不一致:\nfirst=%s\nsecond=%s", body1, body2)
	}
	if hdr1.Get("ETag") != hdr2.Get("ETag") || hdr1.Get("Subscription-Userinfo") != hdr2.Get("Subscription-Userinfo") {
		t.Fatalf("两次订阅的头不一致: %v vs %v", hdr1, hdr2)
	}

	// 304 同样算一次拉取,且不能改响应体之外的既有语义。
	req, err := http.NewRequest("GET", e.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", hdr1.Get("ETag"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("带 If-None-Match 应 304,得到 %d", resp.StatusCode)
	}
}

// TestSubPullThrottledWithinMinute:60 秒内同一用户的第二次拉取不改 last_at。
func TestSubPullThrottledWithinMinute(t *testing.T) {
	e := newEnv(t)
	e.createUser("alice")
	u, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	e.s.subPulls.now = func() time.Time { return now }
	e.s.subPulls.every = time.Minute

	path := "/sub/" + u.SubToken + "/links"
	if code, _, body := e.sub(path, "v2rayng/1.8"); code != http.StatusOK {
		t.Fatalf("第一次订阅: %d %s", code, body)
	}
	pulls, err := e.st.ListSubPulls()
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 1 || !pulls[0].LastPulledAt.Equal(now) {
		t.Fatalf("第一次拉取记录 = %+v", pulls)
	}

	// 同一分钟内换格式、换客户端再拉:内存节流挡住写库,last_at 不变。
	now = now.Add(30 * time.Second)
	if code, _, body := e.sub("/sub/"+u.SubToken+"/clash", "clash-verge/2.0"); code != http.StatusOK {
		t.Fatalf("第二次订阅: %d %s", code, body)
	}
	pulls, err = e.st.ListSubPulls()
	if err != nil {
		t.Fatal(err)
	}
	if !pulls[0].LastPulledAt.Equal(now.Add(-30 * time.Second)) {
		t.Fatalf("60 秒内第二次拉取不应改 last_at: %+v", pulls[0])
	}
	if pulls[0].Format != string(model.FormatLinks) || pulls[0].Client != "v2rayng" {
		t.Fatalf("节流期间的第二次拉取不应改 format/client: %+v", pulls[0])
	}

	// 过了一分钟再拉:更新 last_at / format / client,first_at 不动。
	now = now.Add(time.Minute)
	if code, _, body := e.sub("/sub/"+u.SubToken+"/clash", "clash-verge/2.0"); code != http.StatusOK {
		t.Fatalf("第三次订阅: %d %s", code, body)
	}
	pulls, err = e.st.ListSubPulls()
	if err != nil {
		t.Fatal(err)
	}
	if !pulls[0].LastPulledAt.Equal(now) || pulls[0].FirstPulledAt.Equal(now) {
		t.Fatalf("过了节流窗口后 last_at 应更新、first_at 应保留: %+v", pulls[0])
	}
	if pulls[0].Format != string(model.FormatClash) || pulls[0].Client != "clash-verge" {
		t.Fatalf("节流窗口后的更新 = %+v", pulls[0])
	}
}

// TestRevokeSubClearsPull:换 token 后记录清空,新链接的第一次拉取重新落库。
func TestRevokeSubClearsPull(t *testing.T) {
	e := newEnv(t)
	user := e.createUser("alice")

	path := "/sub/" + user.SubToken + "/links"
	if code, _, body := e.sub(path, "v2rayng/1.8"); code != http.StatusOK {
		t.Fatalf("订阅: %d %s", code, body)
	}
	pulls, err := e.st.ListSubPulls()
	if err != nil || len(pulls) != 1 {
		t.Fatalf("拉取记录 = %+v, err = %v", pulls, err)
	}

	code, data := e.api("POST", "/api/users/alice/revoke-sub", nil)
	if code != http.StatusOK {
		t.Fatalf("revoke-sub: %d %s", code, data)
	}
	view := decodeJSON[userView](t, data)
	if view.SubToken == "" || view.SubToken == user.SubToken {
		t.Fatalf("换 token 后 SubToken = %q", view.SubToken)
	}

	code, data = e.api("GET", "/api/sub-pulls", nil)
	if code != http.StatusOK {
		t.Fatalf("GET /api/sub-pulls: %d", code)
	}
	if strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("revoke 后记录应为空数组,得到 %s", data)
	}

	// 新链接立刻拉一次:内存节流也随 revoke 一起清掉,应该马上有记录。
	if code, _, body := e.sub("/sub/"+view.SubToken+"/links", "sing-box/1.10"); code != http.StatusOK {
		t.Fatalf("新链接订阅: %d %s", code, body)
	}
	pulls, err = e.st.ListSubPulls()
	if err != nil || len(pulls) != 1 {
		t.Fatalf("新链接的第一次拉取记录 = %+v, err = %v", pulls, err)
	}
	if pulls[0].Client != "sing-box" {
		t.Fatalf("新链接的 client = %q", pulls[0].Client)
	}
}

func TestSubPullClientParsing(t *testing.T) {
	cases := map[string]string{
		"":                               "unknown",
		"   ":                            "unknown",
		"v2rayng/1.8.0":                  "v2rayng",
		"sing-box 1.10":                  "sing-box",
		"clash-verge/v2.0.0 (Windows)":   "clash-verge",
		strings.Repeat("a", 70) + "/1.0": strings.Repeat("a", 64),
	}
	for ua, want := range cases {
		if got := subPullClient(ua); got != want {
			t.Errorf("subPullClient(%q) = %q, want %q", ua, got, want)
		}
	}
}
