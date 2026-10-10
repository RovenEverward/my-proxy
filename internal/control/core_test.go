package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/node"
	"github.com/RovenEverward/my-proxy/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protowire"
)

// These tests integrate the controller, SQLite, and the public stats wire format.
// The SSH runner and remote service lifecycle are fake; this is not SSH runtime evidence.
type coreWire []byte
type coreWireCodec struct{}

func (coreWireCodec) Name() string { return "proto" }
func (coreWireCodec) Marshal(v any) ([]byte, error) {
	b, ok := v.(*coreWire)
	if !ok {
		return nil, fmt.Errorf("unexpected wire response %T", v)
	}
	return []byte(*b), nil
}
func (coreWireCodec) Unmarshal(b []byte, v any) error {
	out, ok := v.(*coreWire)
	if !ok {
		return fmt.Errorf("unexpected wire request %T", v)
	}
	*out = append((*out)[:0], b...)
	return nil
}

type coreTestRunner struct {
	*fakeRunner
	listener    *bufconn.Listener
	statsMu     sync.Mutex
	traffic     map[string]node.UserTraffic
	statsErr    error
	generation  string
	generations []string
	beforeStop  func()
	beforeStart func()
}

func newCoreTestRunner(t *testing.T) *coreTestRunner {
	t.Helper()
	r := &coreTestRunner{fakeRunner: &fakeRunner{replies: []reply{
		{"systemctl is-active", "active"}, {"check -c", ""}, {"systemctl stop", ""}, {"systemctl start", ""}, {"sleep 1", ""}, {"if test -f", ""}, {"umask 077", ""},
	}}, listener: bufconn.Listen(1024 * 1024), generation: strings.Repeat("1", 32), traffic: map[string]node.UserTraffic{}}
	server := grpc.NewServer(grpc.ForceServerCodec(coreWireCodec{}), grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		method, _ := grpc.MethodFromServerStream(stream)
		if method != "/v2ray.core.app.stats.command.StatsService/QueryStats" {
			return status.Error(codes.Unimplemented, method)
		}
		var req coreWire
		if err := stream.RecvMsg(&req); err != nil {
			return err
		}
		if len(req) != 0 {
			return status.Error(codes.InvalidArgument, "stats query must not reset counters")
		}
		r.statsMu.Lock()
		defer r.statsMu.Unlock()
		if r.statsErr != nil {
			return r.statsErr
		}
		var out coreWire
		record := func(name string, value int64) {
			b := protowire.AppendTag(nil, 1, protowire.BytesType)
			b = protowire.AppendString(b, name)
			b = protowire.AppendTag(b, 2, protowire.VarintType)
			b = protowire.AppendVarint(b, uint64(value))
			out = protowire.AppendTag(out, 1, protowire.BytesType)
			out = protowire.AppendBytes(out, b)
		}
		for name, v := range r.traffic {
			record("user>>>"+name+">>>traffic>>>uplink", v.Uplink)
			record("user>>>"+name+">>>traffic>>>downlink", v.Downlink)
		}
		record("inbound>>>quic>>>traffic>>>downlink", 999999)
		return stream.SendMsg(&out)
	}))
	go func() { _ = server.Serve(r.listener) }()
	t.Cleanup(func() { server.Stop(); _ = r.listener.Close() })
	return r
}
func (r *coreTestRunner) DialStats(ctx context.Context, _ model.Node, port int) (net.Conn, error) {
	if port != 10086 {
		return nil, fmt.Errorf("unexpected stats port %d", port)
	}
	return r.listener.DialContext(ctx)
}
func (r *coreTestRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	if strings.Contains(script, "systemctl show -p InvocationID --value") {
		r.statsMu.Lock()
		defer r.statsMu.Unlock()
		id := r.generation
		if len(r.generations) > 0 {
			id = r.generations[0]
			r.generations = r.generations[1:]
		}
		return id, nil
	}
	if strings.Contains(script, "systemctl stop") {
		r.statsMu.Lock()
		hook := r.beforeStop
		r.statsMu.Unlock()
		if hook != nil {
			hook()
		}
	}
	if strings.Contains(script, "systemctl start") {
		r.statsMu.Lock()
		hook := r.beforeStart
		r.statsMu.Unlock()
		if hook != nil {
			hook()
		}
	}
	out, err := r.fakeRunner.Run(ctx, n, script)
	if err == nil && strings.Contains(script, "systemctl start") {
		r.statsMu.Lock()
		r.generation = strings.Repeat("2", 32)
		r.traffic = map[string]node.UserTraffic{}
		r.statsMu.Unlock()
	}
	return out, err
}
func (r *coreTestRunner) setTraffic(values map[string]node.UserTraffic) {
	r.statsMu.Lock()
	defer r.statsMu.Unlock()
	r.traffic = values
}
func coreFixture(t *testing.T) (*Control, *store.Store, *coreTestRunner, model.Node) {
	t.Helper()
	r := newCoreTestRunner(t)
	c, st := newLoggingControl(t, r, slog.New(slog.DiscardHandler))
	n := addNode(t, st, "QUIC", "192.0.2.10")
	in := model.Inbound{NodeID: n.ID, Tag: "hy2", Protocol: model.ProtocolHysteria2, Port: 19231, Network: model.NetworkUDP, Security: model.SecurityTLS, CertFile: "/cert.pem", KeyFile: "/key.pem"}
	if err := st.CreateInbound(&in); err != nil {
		t.Fatal(err)
	}
	return c, st, r, n
}
func coreUsage(t *testing.T, st *store.Store, name string, want int64) {
	t.Helper()
	u, err := st.GetUser(name)
	if err != nil {
		t.Fatal(err)
	}
	if u.UsedTraffic != want || u.LifetimeUsedTraffic != want {
		t.Fatalf("%s use/lifetime=%d/%d, want %d", name, u.UsedTraffic, u.LifetimeUsedTraffic, want)
	}
}
func coreBaseline(t *testing.T, st *store.Store, n model.Node, core model.Core, name string) model.TrafficSample {
	t.Helper()
	values, err := st.LatestCoreTrafficSamples(n.ID, core)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := values[name]
	if !ok {
		t.Fatalf("missing %s baseline for %s", core, name)
	}
	return v
}

func TestSingBoxQuotaReconcileAndUnchangedConfig(t *testing.T) {
	c, st, r, n := coreFixture(t)
	now := time.Now().UTC()
	alice := activeUser("alice")
	alice.DataLimit = 100
	addUser(t, st, alice)
	addUser(t, st, activeUser("bob"))
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 40, Downlink: 60}, "bob": {Uplink: 2, Downlink: 3}})
	if err := c.SampleTraffic(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 100)
	coreUsage(t, st, "bob", 5)
	if err := c.ReconcileUsers(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	raw := r.stdinFor("config.json.next")
	var cfg struct {
		Inbounds []struct {
			Users []struct {
				Name string `json:"name"`
			} `json:"users"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inbounds) != 1 || len(cfg.Inbounds[0].Users) != 1 || cfg.Inbounds[0].Users[0].Name != "bob" {
		t.Fatalf("quota user not removed: %s", raw)
	}
	d, err := st.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if d.DeployedHash != hashOf([]byte(raw)) || d.DeployedAt == nil {
		t.Fatal("successful deployment not recorded")
	}
	calls := r.callList()
	if err := c.ReconcileUsers(context.Background(), n, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, r.callList()) {
		t.Fatalf("unchanged config touched service: %v", r.callList())
	}
	if r.ran("xray") {
		t.Fatal("QUIC-only reconcile called Xray")
	}
}

func TestSingBoxAndXrayTrafficBaselinesIndependent(t *testing.T) {
	c, st, r, n := coreFixture(t)
	now := time.Now().UTC()
	addUser(t, st, activeUser("alice"))
	addInbound(t, st, n.ID, "vless", 19232)
	r.replies = append(r.replies, reply{"statsquery", trafficReply("alice", "uplink", int64(100), "alice", "downlink", int64(200))})
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 10, Downlink: 20}})
	if err := c.SampleTraffic(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 330)
	r.replies[len(r.replies)-1].out = trafficReply("alice", "uplink", int64(105), "alice", "downlink", int64(210))
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 15, Downlink: 25}})
	if err := c.SampleTraffic(context.Background(), n, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 355)
	xr := coreBaseline(t, st, n, model.CoreXray, "alice")
	sb := coreBaseline(t, st, n, model.CoreSingBox, "alice")
	if xr.Uplink != 105 || xr.Downlink != 210 || sb.Uplink != 15 || sb.Downlink != 25 || sb.Generation != strings.Repeat("1", 32) {
		t.Fatalf("cross-core baseline collision: xray=%+v singbox=%+v", xr, sb)
	}
	// New invocation counters can exceed the previous counters: generation is decisive.
	r.statsMu.Lock()
	r.generation = strings.Repeat("3", 32)
	r.statsMu.Unlock()
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 50, Downlink: 60}})
	if err := c.SampleTraffic(context.Background(), n, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 465)
}

func TestSingBoxFinalSampleAndResetPersistBeforeStart(t *testing.T) {
	c, st, r, n := coreFixture(t)
	now := time.Now().UTC()
	addUser(t, st, activeUser("alice"))
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 10, Downlink: 20}})
	if err := c.SampleTraffic(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	// A pre-existing Xray baseline must survive the sing-box stop boundary.
	if err := st.RecordTrafficSample(model.TrafficSample{Username: "alice", NodeID: n.ID, SampledAt: now, Uplink: 1000, Downlink: 2000}); err != nil {
		t.Fatal(err)
	}
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 15, Downlink: 27}})
	r.beforeStop = func() {
		coreUsage(t, st, "alice", 42)
		v := coreBaseline(t, st, n, model.CoreSingBox, "alice")
		if v.Uplink != 15 || v.Downlink != 27 {
			t.Fatalf("stop before final sample persisted: %+v", v)
		}
	}
	r.beforeStart = func() {
		v := coreBaseline(t, st, n, model.CoreSingBox, "alice")
		if v.Uplink != 0 || v.Downlink != 0 {
			t.Fatalf("start before baseline reset persisted: %+v", v)
		}
	}
	if err := c.ReconcileUsers(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	zero := coreBaseline(t, st, n, model.CoreSingBox, "alice")
	if zero.Uplink != 0 || zero.Downlink != 0 {
		t.Fatalf("reset not persisted: %+v", zero)
	}
	xr := coreBaseline(t, st, n, model.CoreXray, "alice")
	if xr.Uplink != 1000 || xr.Downlink != 2000 {
		t.Fatal("reset touched Xray baseline")
	}
	history, err := st.ListCoreTrafficSamples("alice", n.ID, now.Add(-time.Hour), now.Add(time.Hour), model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[1].Uplink != 15 || history[1].Downlink != 27 || history[2].Uplink != 0 || history[2].Downlink != 0 {
		t.Fatalf("final/reset history lost: %+v", history)
	}
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 3, Downlink: 4}})
	if err := c.SampleTraffic(context.Background(), n, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 49)
}

func TestSingBoxDeploymentFailureKeepsHashAndBaselines(t *testing.T) {
	for _, failure := range []string{"stats", "generation-race", "start"} {
		t.Run(failure, func(t *testing.T) {
			c, st, r, n := coreFixture(t)
			now := time.Now().UTC()
			addUser(t, st, activeUser("alice"))
			r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 10, Downlink: 20}})
			if err := c.SampleTraffic(context.Background(), n, now); err != nil {
				t.Fatal(err)
			}
			const oldHash = "prior-successful-config"
			if err := st.SetCoreDeployed(n.ID, model.CoreSingBox, oldHash, now.Add(-time.Hour)); err != nil {
				t.Fatal(err)
			}
			r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 15, Downlink: 25}})
			switch failure {
			case "stats":
				r.statsMu.Lock()
				r.statsErr = status.Error(codes.Unavailable, "stats unavailable")
				r.statsMu.Unlock()
			case "generation-race":
				r.statsMu.Lock()
				r.generations = []string{strings.Repeat("1", 32), strings.Repeat("2", 32)}
				r.statsMu.Unlock()
			case "start":
				r.failOn = "systemctl start"
			}
			err := c.ReconcileUsers(context.Background(), n, now)
			if err == nil {
				t.Fatal("expected deploy failure")
			}
			d, getErr := st.GetCoreDeployment(n.ID, model.CoreSingBox)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if d.DeployedHash != oldHash || !d.DeployedAt.Equal(now.Add(-time.Hour).Truncate(time.Second)) {
				t.Fatalf("failed deploy falsely recorded: %+v", d)
			}
			baseline := coreBaseline(t, st, n, model.CoreSingBox, "alice")
			if failure != "start" {
				if !strings.Contains(err.Error(), "final sing-box sample failed") {
					t.Fatal(err)
				}
				if r.ran("systemctl stop") || r.ran("systemctl start") || r.ran("if test -f") {
					t.Fatalf("failed sample applied config: %v", r.callList())
				}
				coreUsage(t, st, "alice", 30)
				if baseline.Uplink != 10 || baseline.Downlink != 20 {
					t.Fatalf("failed query modified baseline: %+v", baseline)
				}
			} else {
				coreUsage(t, st, "alice", 40)
				if baseline.Uplink != 0 || baseline.Downlink != 0 {
					t.Fatal("stopped process baseline was not reset")
				}
				if !r.ran("systemctl stop") {
					t.Fatal("failed start did not leave service stopped")
				}
			}
		})
	}
}

func TestSingBoxGenerationRaceDoesNotApplySamples(t *testing.T) {
	c, st, r, n := coreFixture(t)
	now := time.Now().UTC()
	addUser(t, st, activeUser("alice"))
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 10, Downlink: 20}})
	r.generations = []string{strings.Repeat("1", 32), strings.Repeat("2", 32)}
	if err := c.SampleTraffic(context.Background(), n, now); err == nil || !strings.Contains(err.Error(), "restarted during traffic query") {
		t.Fatalf("race accepted: %v", err)
	}
	coreUsage(t, st, "alice", 0)
	samples, err := st.LatestCoreTrafficSamples(n.ID, model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 0 {
		t.Fatal("raced sample persisted")
	}
	// A stable retry must account the complete current process counters.
	if err := c.SampleTraffic(context.Background(), n, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 30)
}

var _ node.StatsDialer = (*coreTestRunner)(nil)
var _ node.Runner = (*coreTestRunner)(nil)
