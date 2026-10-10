package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestCapabilityPageAuthorizationAndExpiry(t *testing.T) {
	e := newEnv(t)
	user, path := e.createSubReadyUser("alice")
	nodes, _ := e.st.ListNodes()
	ins, _ := e.st.ListInbounds(nodes[0].ID)
	hosts, _ := e.st.ListHosts(ins[0].ID)
	now := time.Now().UTC()
	r := model.CapabilityResult{NodeID: nodes[0].ID, InboundID: ins[0].ID, HostID: hosts[0].ID, Target: "netflix", Status: model.CapabilityAvailable, ObservedStatus: model.CapabilityAvailable, Consecutive: 2, Region: "JP", ExitIP: "192.0.2.100", Location: "指定入口代理出口", Detector: "test", Fingerprint: control.CapabilityFingerprint(nodes[0], ins[0], hosts[0]), CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := e.st.SaveCapabilityResults([]model.CapabilityResult{r}); err != nil {
		t.Fatal(err)
	}
	code, body := e.request("GET", path, nil, browserHeaders())
	if code != http.StatusOK {
		t.Fatalf("page %d %s", code, body)
	}
	for _, want := range []string{"节点能力", "netflix", "JP", "192.0.2.100", "检测时间", "指定入口代理出口", "不能保证登录播放"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing %s", want)
		}
	}
	r.ExpiresAt = now.Add(-time.Second)
	if err := e.st.SaveCapabilityResults([]model.CapabilityResult{r}); err != nil {
		t.Fatal(err)
	}
	_, body = e.request("GET", path, nil, browserHeaders())
	if !strings.Contains(string(body), "netflix · 未知") || !strings.Contains(string(body), "已过期") {
		t.Fatalf("expiry not visible: %s", body)
	}
	u, _ := e.st.GetUser(user.Username)
	u.InboundIDs = []int64{999999}
	if err := e.st.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	_, body = e.request("GET", path, nil, browserHeaders())
	if strings.Contains(string(body), "192.0.2.100") {
		t.Fatal("unauthorized capability disclosed")
	}
}
