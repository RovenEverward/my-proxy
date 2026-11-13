package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// node retry:给节点 id 时只对退避重试端点发一条 POST,不必先拉节点列表。
func TestNodeRetryPostsBackoffEndpoint(t *testing.T) {
	var method, path string
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"node": "la-1", "retried": true})
	})
	runRoot(t, "node", "retry", "3")
	if method != http.MethodPost || path != "/api/nodes/3/backoff/retry" {
		t.Fatalf("该打到退避重试端点: %s %s", method, path)
	}
}

// node retry:控制面回错时命令要把原因带出来,而不是吞成一句"失败"。
func TestNodeRetryPropagatesError(t *testing.T) {
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "ssh: connect timeout"})
	})
	err := runRootErr(t, "node", "retry", "3")
	if err == nil || !strings.Contains(err.Error(), "connect timeout") {
		t.Fatalf("控制面的原因该原样回给使用者: %v", err)
	}
}
