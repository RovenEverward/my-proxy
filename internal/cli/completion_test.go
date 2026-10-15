package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

func TestCompletion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/nodes":
			_ = json.NewEncoder(w).Encode([]model.NodeView{{Node: model.Node{ID: 1, Name: "la-1"}}, {Node: model.Node{ID: 5, Name: "jp-1"}}})
		case "/api/inbounds":
			_ = json.NewEncoder(w).Encode([]model.Inbound{{ID: 10, NodeID: 1, Tag: "reality", Protocol: model.ProtocolVLESS, Port: 443}})
		case "/api/users":
			_ = json.NewEncoder(w).Encode(userPage{Users: []userRow{{Username: "alice"}, {Username: "bob"}}})
		}
	}))
	defer srv.Close()
	t.Setenv("MPX_SERVER", srv.URL)
	t.Setenv("MPX_TOKEN", "t")
	wireCompletion(rootCmd)

	complete := func(args ...string) string {
		var out strings.Builder
		rootCmd.SetOut(&out)
		rootCmd.SetArgs(append([]string{cobra.ShellCompRequestCmd}, args...))
		t.Cleanup(func() { rootCmd.SetArgs(nil); rootCmd.SetOut(nil) })
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	for _, c := range []struct {
		args       []string
		want, deny string
	}{
		{[]string{"node", "online", "l"}, "la-1\tid 1", "jp-1"},
		{[]string{"node", "sync", ""}, "jp-1", ""},
		{[]string{"node", "core", "show", "la-1", "s"}, "sing-box", "xray"},
		{[]string{"inbound", "show", "LA"}, "la-1/reality\tvless :443 · id 10", ""},
		{[]string{"host", "list", "--inbound", ""}, "la-1/reality", ""},
		{[]string{"inbound", "list", "--node", "j"}, "jp-1", "la-1"},
		{[]string{"user", "rm", "alice", "b"}, "bob", "alice"},
	} {
		got := complete(c.args...)
		if !strings.Contains(got, c.want) || (c.deny != "" && strings.Contains(got, c.deny)) {
			t.Errorf("%v: 得到 %q", c.args, got)
		}
	}

	// 控制面不通:静默不补,也不报错。
	t.Setenv("MPX_SERVER", "http://127.0.0.1:1")
	if got := complete("node", "online", ""); strings.Contains(got, "la-1") {
		t.Errorf("控制面不通时不该有候选: %q", got)
	}
}
