package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/server"
	"github.com/RovenEverward/my-proxy/internal/store"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type cliCapabilityOps struct {
	st     *store.Store
	in     model.Inbound
	calls  int
	budget time.Duration
	config model.CapabilityConfig
}

func (*cliCapabilityOps) DeployNode(context.Context, model.Node, time.Time) error { return nil }
func (*cliCapabilityOps) CheckNode(context.Context, model.Node, time.Time) (model.NodeStatus, error) {
	return model.NodeStatus{}, nil
}
func (*cliCapabilityOps) InstallNode(context.Context, model.Node, string) (string, error) {
	return "", nil
}
func (*cliCapabilityOps) OnlineIPs(context.Context, model.Node) (map[string][]string, error) {
	return nil, nil
}
func (ops *cliCapabilityOps) ProbeCapabilities(ctx context.Context, n model.Node, cfg model.CapabilityConfig, now time.Time) ([]model.CapabilityResult, error) {
	ops.calls++
	ops.config = cfg
	if at, ok := ctx.Deadline(); ok {
		ops.budget = time.Until(at)
	}
	results := []model.CapabilityResult{}
	for _, target := range cfg.Targets {
		results = append(results, model.CapabilityResult{NodeID: n.ID, InboundID: cfg.InboundID, Target: target, Status: model.CapabilityAvailable, ObservedStatus: model.CapabilityAvailable, Consecutive: 2, Region: "JP", ExitIP: "203.0.113.8", Fingerprint: control.CapabilityFingerprint(n, ops.in, model.Host{}), CheckedAt: now, ExpiresAt: now.Add(3 * time.Hour), Location: "test node via proxy", Detector: "fixture/v1", Reason: "HTTP 200"})
	}
	return results, ops.st.SaveCapabilityResults(results)
}

// Reset shared Cobra command values before every execution, as a new CLI process
// would, while retaining the production command registration and parsing.
func runCapabilityRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	for _, cmd := range append([]*cobra.Command{rootCmd}, nodeCmd.Commands()...) {
		for _, flags := range []*pflag.FlagSet{cmd.Flags(), cmd.PersistentFlags()} {
			flags.VisitAll(func(f *pflag.Flag) {
				if _, ok := f.Value.(pflag.SliceValue); ok {
					// pflag 的切片值内部另记一个「已设置」标记,Replace 与 f.Changed
					// 都清不掉它;不换掉 Value,-count>1 时第二次 --targets 会追加到
					// 上一次的结果后面。按类型换一个全新的值。
					var defaults []string
					if values := strings.Trim(f.DefValue, "[]"); values != "" {
						defaults = strings.Split(values, ",")
					}
					fresh := pflag.NewFlagSet("reset", pflag.ContinueOnError)
					switch f.Value.Type() {
					case "stringSlice":
						fresh.StringSlice(f.Name, defaults, "")
					case "stringArray":
						fresh.StringArray(f.Name, defaults, "")
					default:
						t.Fatalf("cannot reset %s flag --%s", f.Value.Type(), f.Name)
					}
					f.Value = fresh.Lookup(f.Name).Value
				} else {
					_ = f.Value.Set(f.DefValue)
				}
				f.Changed = false
			})
		}
	}
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		opts.output = outputTable
		opts.server = ""
		opts.token = ""
	})
	err := rootCmd.Execute()
	return out.String(), err
}

func TestCapabilityCLIActualServer(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mpx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	n := model.Node{Name: "test", Address: "node.example.com"}
	if err := st.CreateNode(&n); err != nil {
		t.Fatal(err)
	}
	in := model.Inbound{NodeID: n.ID, Tag: "probe", Protocol: model.ProtocolVLESS, Port: 443, Network: model.NetworkTCP, Security: model.SecurityNone}
	if err := st.CreateInbound(&in); err != nil {
		t.Fatal(err)
	}
	u := model.User{Username: "reader", Enabled: true, Key: "credential-secret", SubToken: "subscription-secret"}
	if err := st.CreateUser(&u); err != nil {
		t.Fatal(err)
	}
	ops := &cliCapabilityOps{st: st, in: in}
	srv := server.New(st, "test-token", slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetNodeOps(ops)
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	t.Setenv("MPX_SERVER", httpSrv.URL)
	t.Setenv("MPX_TOKEN", "test-token")
	id := fmt.Sprint(n.ID)
	inID := fmt.Sprint(in.ID)
	out, err := runCapabilityRoot(t, "node", "probe", id, "--inbound", inID, "--targets", "egress,youtube,netflix")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"available", "JP", "203.0.113.8", "HTTP 200", "test node via proxy", "fixture/v1", "检查时间", "到期时间", "原始观测"} {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q: %s", s, out)
		}
	}
	if ops.calls != 1 || ops.config.Enabled || ops.budget < 119*time.Second {
		t.Fatalf("ops=%+v", ops)
	}
	configs, _ := st.ListCapabilityConfigs(n.ID)
	if len(configs) != 0 {
		t.Fatal("manual probe enabled or saved scheduled configuration")
	}
	out, err = runCapabilityRoot(t, "node", "capabilities", id, "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Configs []model.CapabilityConfig `json:"configs"`
		Results []model.CapabilityResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Results) != 3 || view.Results[0].Status != model.CapabilityAvailable {
		t.Fatalf("view=%+v", view)
	}
	if strings.Contains(out, u.Key) || strings.Contains(out, u.SubToken) {
		t.Fatal("credentials disclosed")
	}
	summary := captureStdout(t, func() { _, err = runCapabilityRoot(t, "node", "show", id) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "youtube:available") {
		t.Fatalf("show missing capability summary: %s", summary)
	}
	out, err = runCapabilityRoot(t, "node", "capability-set", id, "--inbound", inID, "--enabled", "--interval", "1h", "--ttl", "3h", "--targets", "youtube,netflix")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "已保存") {
		t.Fatal(out)
	}
	configs, _ = st.ListCapabilityConfigs(n.ID)
	if len(configs) != 1 || !configs[0].Enabled || configs[0].IntervalSeconds != 3600 || configs[0].TTLSeconds != 10800 {
		t.Fatalf("configs=%+v", configs)
	}
	if ops.calls != 1 {
		t.Fatal("capability-set performed probe")
	}
	out, err = runCapabilityRoot(t, "node", "capability-set", id, "--inbound", inID, "--enabled=false", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg model.CapabilityConfig
	if err := json.Unmarshal([]byte(out), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Enabled {
		t.Fatal("disable failed")
	}
}

func TestCapabilityCLIValidationBeforeRequest(t *testing.T) {
	t.Setenv("MPX_SERVER", "http://127.0.0.1:1")
	t.Setenv("MPX_TOKEN", "test")
	for _, tc := range []struct {
		args  []string
		match string
	}{
		{[]string{"node", "probe", "1"}, "入站 ID"},
		{[]string{"node", "probe", "1", "--inbound", "2", "--targets", "hulu"}, "检测目标未知"},
		{[]string{"node", "probe", "1", "--inbound", "2", "--targets", "egress,egress"}, "重复"},
		{[]string{"node", "capability-set", "1", "--inbound", "2", "--interval", "4m"}, "300–86400"},
		{[]string{"node", "capability-set", "1", "--inbound", "2", "--interval", "25h"}, "300–86400"},
		{[]string{"node", "capability-set", "1", "--inbound", "2", "--ttl", "30m"}, "有效期"},
		{[]string{"node", "capability-set", "1", "--inbound", "2", "--ttl", "169h"}, "有效期"},
		{[]string{"node", "capability-set", "1", "--inbound", "2", "--interval", "1.5s"}, "整秒"},
		{[]string{"node", "capability-set", "1", "--inbound", "2", "--interval", "nonsense"}, "invalid duration"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			_, err := runCapabilityRoot(t, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.match) {
				t.Fatalf("got %v want %s", err, tc.match)
			}
		})
	}
}
