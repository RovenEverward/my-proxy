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
	listener       *bufconn.Listener
	statsMu        sync.Mutex
	traffic        map[string]node.UserTraffic
	statsErr       error
	statsBlock     bool
	generation     string
	generations    []string
	beforeStop     func()
	beforeStart    func()
	inspectContext func(context.Context, string)
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
		if r.statsBlock {
			r.statsMu.Unlock()
			<-stream.Context().Done()
			return status.FromContextError(stream.Context().Err()).Err()
		}
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
func (r *coreTestRunner) RunWithInput(ctx context.Context, n model.Node, script string, input []byte) (string, error) {
	if r.inspectContext != nil {
		r.inspectContext(ctx, script)
	}
	return r.fakeRunner.RunWithInput(ctx, n, script, input)
}
func (r *coreTestRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	if r.inspectContext != nil {
		r.inspectContext(ctx, script)
	}
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

func TestSingBoxDeployFailureDoesNotBlockXray(t *testing.T) {
	c, st, r, n := coreFixture(t)
	alice := addUser(t, st, activeUser("alice"))
	if err := c.ReconcileUsers(context.Background(), n, time.Now()); err != nil {
		t.Fatal(err)
	}
	before, err := st.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	addInbound(t, st, n.ID, "vless-ws", 19232)
	alice.Enabled = false
	if err := st.UpdateUser(&alice); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.calls, r.stdin = nil, nil
	r.replies = append(r.replies, reply{"cp -f", ""}, reply{"-test -config", ""}, reply{"systemctl restart", ""})
	r.mu.Unlock()
	r.statsMu.Lock()
	r.statsErr = status.Error(codes.Unavailable, "sing-box final sample offline")
	r.statsMu.Unlock()
	var quicContext, xrayContext context.Context
	r.inspectContext = func(ctx context.Context, script string) {
		if strings.Contains(script, "check -c") {
			quicContext = ctx
		}
		if strings.Contains(script, "-test -config") {
			xrayContext = ctx
			if ctx.Err() != nil {
				t.Fatalf("healthy Xray deploy inherited QUIC cancellation: %v", ctx.Err())
			}
		}
	}
	if err := c.DeployNode(context.Background(), n, time.Now()); err == nil || !strings.Contains(err.Error(), "sing-box final sample offline") {
		t.Fatalf("QUIC failure must still be returned: %v", err)
	}
	if quicContext == nil || xrayContext == nil || quicContext == xrayContext {
		t.Fatal("deployment cores did not use independent contexts")
	}
	if !r.ran("-test -config") || !r.ran("systemctl restart") || r.ran("systemctl stop") || r.ran("systemctl start") {
		t.Fatalf("healthy Xray deployment blocked or failed QUIC restarted: %v", r.callList())
	}
	r.mu.Lock()
	var xrayConfig string
	for i, call := range r.calls {
		if strings.Contains(call, "umask 077") && strings.Contains(call, n.ConfigPathOrDefault()) {
			xrayConfig = r.stdin[i]
		}
	}
	r.mu.Unlock()
	if !json.Valid([]byte(xrayConfig)) || !strings.Contains(xrayConfig, `"vless"`) || strings.Contains(xrayConfig, `"hysteria2"`) {
		t.Fatalf("Xray configuration not written: %s", xrayConfig)
	}
	storedNode, err := st.GetNode(n.ID)
	if err != nil || storedNode.DeployedHash != hashOf([]byte(xrayConfig)) || storedNode.DeployedAt == nil {
		t.Fatalf("healthy Xray deployed hash not recorded: %+v, %v", storedNode, err)
	}
	after, err := st.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed QUIC deployment changed success record: before=%+v after=%+v err=%v", before, after, err)
	}
}

func TestSingBoxDeleteLastInboundDirectDeployNoXray(t *testing.T) {
	c, st, r, n := coreFixture(t)
	addUser(t, st, activeUser("alice"))
	if err := c.DeployNode(context.Background(), n, time.Now()); err != nil {
		t.Fatal(err)
	}
	inbounds, err := st.ListInbounds(n.ID)
	if err != nil || len(inbounds) != 1 {
		t.Fatalf("unexpected fixture inbounds: %v, %v", inbounds, err)
	}
	if err := st.DeleteInbound(inbounds[0].ID); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.calls, r.stdin = nil, nil
	r.mu.Unlock()
	if err := c.DeployNode(context.Background(), n, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, call := range r.callList() {
		if strings.Contains(call, "XRAY_BIN") || strings.Contains(call, n.ConfigPathOrDefault()) || strings.Contains(call, "'"+n.ServiceNameOrDefault()+"'") {
			t.Fatalf("empty QUIC-only deployment touched Xray: %s", call)
		}
	}
	if !r.ran("systemctl stop") || !r.ran("systemctl start") {
		t.Fatalf("last QUIC inbound not revoked: %v", r.callList())
	}
	raw := r.stdinFor("config.json.next")
	var cfg struct {
		Inbounds []json.RawMessage `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil || len(cfg.Inbounds) != 0 {
		t.Fatalf("last-inbound revocation configuration not empty: %s, %v", raw, err)
	}
	d, err := st.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil || d.DeployedHash != hashOf([]byte(raw)) {
		t.Fatalf("QUIC revocation hash not recorded: %+v, %v", d, err)
	}
	storedNode, err := st.GetNode(n.ID)
	if err != nil || storedNode.DeployedHash != "" {
		t.Fatalf("absent Xray acquired a deployment hash: %+v, %v", storedNode, err)
	}
}

func TestSingBoxDeleteLastInboundNoXrayQuery(t *testing.T) {
	c, st, r, n := coreFixture(t)
	addUser(t, st, activeUser("alice"))
	if err := c.ReconcileUsers(context.Background(), n, time.Now()); err != nil {
		t.Fatal(err)
	}
	inbounds, err := st.ListInbounds(n.ID)
	if err != nil || len(inbounds) != 1 {
		t.Fatalf("unexpected fixture inbounds: %v, %v", inbounds, err)
	}
	if err := st.DeleteInbound(inbounds[0].ID); err != nil {
		t.Fatal(err)
	}
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 4, Downlink: 6}})
	r.mu.Lock()
	r.calls, r.stdin = nil, nil
	r.mu.Unlock()
	task := NewTask(c, st, slog.New(slog.DiscardHandler), DefaultInterval)
	task.ticks = 1 // Keep this regression focused on accounting and revocation.
	task.Tick(context.Background())
	coreUsage(t, st, "alice", 10)
	if r.ran("statsquery") || r.ran("inbounduser") {
		t.Fatalf("QUIC-only revocation queried absent Xray: %v", r.callList())
	}
	if !r.ran("systemctl stop") || !r.ran("systemctl start") {
		t.Fatalf("last QUIC inbound not removed from running process: %v", r.callList())
	}
	raw := r.stdinFor("config.json.next")
	var cfg struct {
		Inbounds []json.RawMessage `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil || len(cfg.Inbounds) != 0 {
		t.Fatalf("revocation must deploy empty inbounds: %s, %v", raw, err)
	}
	d, err := st.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil || d.DeployedHash != hashOf([]byte(raw)) {
		t.Fatalf("empty configuration not recorded: %+v, %v", d, err)
	}
	if got := task.Snapshot(time.Now()).Backoffs; len(got) != 0 {
		t.Fatalf("healthy QUIC-only revocation caused backoff: %+v", got)
	}
}

func TestSingBoxMixedCoreContextsIndependent(t *testing.T) {
	c, st, r, n := coreFixture(t)
	c.sampleTimeout, c.reconcileTimeout = 30*time.Millisecond, 30*time.Millisecond
	addInbound(t, st, n.ID, "vless-ws", 19232)
	r.mu.Lock()
	r.replies = append(r.replies, reply{"statsquery", trafficReply()}, reply{"inbounduser", inbounduserReply()})
	r.mu.Unlock()
	r.statsMu.Lock()
	r.statsBlock = true
	r.statsMu.Unlock()
	var quicContext, xrayContext context.Context
	phase := "sample"
	r.inspectContext = func(ctx context.Context, script string) {
		if strings.Contains(script, "systemctl show -p InvocationID") && quicContext == nil {
			quicContext = ctx
		}
		if (phase == "sample" && strings.Contains(script, "statsquery")) || (phase == "reconcile" && strings.Contains(script, "inbounduser")) {
			xrayContext = ctx
			if ctx.Err() != nil {
				t.Fatalf("Xray inherited QUIC cancellation: %v", ctx.Err())
			}
		}
	}
	assertContexts := func() {
		t.Helper()
		if quicContext == nil || xrayContext == nil {
			t.Fatalf("both core contexts not observed in %s", phase)
		}
		if quicContext == xrayContext {
			t.Fatalf("%s cores share the same budget context", phase)
		}
		if quicContext.Err() != context.DeadlineExceeded || xrayContext.Err() != context.Canceled {
			t.Fatalf("QUIC deadline must expire without exhausting Xray: %v, %v", quicContext.Err(), xrayContext.Err())
		}
		if _, ok := xrayContext.Deadline(); !ok {
			t.Fatalf("Xray %s has no deadline", phase)
		}
	}
	if err := c.SampleTraffic(context.Background(), n, time.Now()); err == nil {
		t.Fatal("QUIC sampling failure not reported")
	}
	assertContexts()
	quicContext, xrayContext = nil, nil
	phase = "reconcile"
	if err := c.ReconcileUsers(context.Background(), n, time.Now()); err == nil {
		t.Fatal("QUIC deployment failure not reported")
	}
	assertContexts()
}

func TestSingBoxMixedCoreTickFailureIndependence(t *testing.T) {
	for _, failingCore := range []model.Core{model.CoreXray, model.CoreSingBox} {
		t.Run(string(failingCore), func(t *testing.T) {
			c, st, r, n := coreFixture(t)
			addInbound(t, st, n.ID, "vless-ws", 19232)
			alice := activeUser("alice")
			alice.DataLimit = 100
			addUser(t, st, alice)
			addUser(t, st, activeUser("bob"))
			r.mu.Lock()
			r.replies = append(r.replies, reply{"statsquery", trafficReply("alice", "uplink", 40, "alice", "downlink", 60)},
				reply{"inbounduser", inbounduserReply("alice", "bob")}, reply{"api adu", "Added 1 user(s)\n"}, reply{"api rmu", "Removed 1 user(s)\n"})
			if failingCore == model.CoreXray {
				// Both Xray sampling and reconciliation fail; QUIC must still revoke.
				r.failOn = "api "
			}
			r.mu.Unlock()
			if failingCore == model.CoreSingBox {
				r.statsMu.Lock()
				r.statsErr = status.Error(codes.Unavailable, "sing-box stats offline")
				r.statsMu.Unlock()
			}
			r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 40, Downlink: 60}})
			clk := newFakeClock()
			task := NewTask(c, st, slog.New(slog.DiscardHandler), DefaultInterval)
			task.now = clk.Now
			assertTick := func(round int, wantQUIC []string, xrayRemove string, xrayAdd bool) {
				t.Helper()
				task.Tick(context.Background())
				coreUsage(t, st, "alice", 100)
				snap := task.Snapshot(clk.Now())
				if len(snap.Backoffs) != 1 || snap.Backoffs[0].Failures != round {
					t.Fatalf("failure must be recorded once per tick: %+v", snap.Backoffs)
				}
				if failingCore == model.CoreXray {
					var cfg struct {
						Inbounds []struct {
							Users []struct {
								Name string `json:"name"`
							} `json:"users"`
						} `json:"inbounds"`
					}
					raw := r.stdinFor("config.json.next")
					if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
						t.Fatalf("healthy QUIC configuration missing: %v; calls=%v", err, r.callList())
					}
					if len(cfg.Inbounds) != 1 {
						t.Fatalf("unexpected configuration: %s", raw)
					}
					var got []string
					for _, u := range cfg.Inbounds[0].Users {
						got = append(got, u.Name)
					}
					if !reflect.DeepEqual(got, wantQUIC) || !r.ran("systemctl start") {
						t.Fatalf("Xray failure blocked QUIC users %v: %s; calls=%v", wantQUIC, raw, r.callList())
					}
					d, err := st.GetCoreDeployment(n.ID, model.CoreSingBox)
					if err != nil || d.DeployedHash != hashOf([]byte(raw)) {
						t.Fatalf("QUIC success not persisted: %+v, %v", d, err)
					}
				} else {
					if !r.ran("api rmu") || !r.ran(xrayRemove) {
						t.Fatalf("QUIC failure blocked Xray revocation: %v", r.callList())
					}
					if xrayAdd && (!r.ran("api adu") || !strings.Contains(r.stdinFor("api adu"), `"bob"`)) {
						t.Fatalf("QUIC failure blocked Xray renewal: %v", r.callList())
					}
					if r.ran("systemctl stop") || r.ran("systemctl start") {
						t.Fatalf("failed QUIC final sample restarted service: %v", r.callList())
					}
				}
			}
			assertTick(1, []string{"bob"}, "'alice'", false)
			for round := 2; round <= 3; round++ {
				bob, err := st.GetUser("bob")
				if err != nil {
					t.Fatal(err)
				}
				bob.Enabled = round == 3
				if err := st.UpdateUser(bob); err != nil {
					t.Fatal(err)
				}
				r.mu.Lock()
				r.calls, r.stdin = nil, nil
				if round == 3 {
					for i := range r.replies {
						if r.replies[i].keyword == "inbounduser" {
							r.replies[i].out = inbounduserReply("alice")
						}
					}
				}
				r.mu.Unlock()
				clk.advance(time.Second) // Still inside the node-wide backoff window.
				if round == 2 {
					assertTick(round, nil, "'bob'", false)
				} else {
					assertTick(round, []string{"bob"}, "'alice'", true)
				}
			}
		})
	}
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

func TestSingBoxDeletedUserCountersDoNotBlockReconcile(t *testing.T) {
	c, st, r, n := coreFixture(t)
	now := time.Now().UTC()
	addUser(t, st, activeUser("alice"))
	addUser(t, st, activeUser("bob"))
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 10, Downlink: 20}, "bob": {Uplink: 2, Downlink: 3}})
	if err := c.SampleTraffic(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 30)
	coreUsage(t, st, "bob", 5)
	if err := st.DeleteUser("alice"); err != nil {
		t.Fatal(err)
	}
	// The old process keeps Alice's counters until the revocation deployment stops it.
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 100, Downlink: 200}, "bob": {Uplink: 4, Downlink: 6}})
	if err := c.ReconcileUsers(context.Background(), n, now); err != nil {
		t.Fatalf("deleted user's retained counters blocked revocation: %v", err)
	}
	coreUsage(t, st, "bob", 10)
	if _, err := st.GetUser("alice"); err == nil {
		t.Fatal("deleted user was recreated")
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
		t.Fatalf("deleted user still in staged configuration: %s", raw)
	}
	if !r.ran("systemctl stop") || !r.ran("systemctl start") {
		t.Fatalf("revocation deployment did not restart service: %v", r.callList())
	}
	d, err := st.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if d.DeployedHash != hashOf([]byte(raw)) {
		t.Fatal("successful revocation configuration not recorded")
	}
}

func TestSingBoxFinalSampleQuotaRestagesConfig(t *testing.T) {
	c, st, r, n := coreFixture(t)
	now := time.Now().UTC()
	alice := activeUser("alice")
	alice.DataLimit = 100
	addUser(t, st, alice)
	addUser(t, st, activeUser("bob"))
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 20, Downlink: 30}})
	if err := c.SampleTraffic(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 50)
	// Alice is active when the first file is staged, but limited by the final sample.
	r.setTraffic(map[string]node.UserTraffic{"alice": {Uplink: 40, Downlink: 60}})
	if err := c.ReconcileUsers(context.Background(), n, now); err != nil {
		t.Fatal(err)
	}
	coreUsage(t, st, "alice", 100)
	r.mu.Lock()
	var stages []string
	checks, lastCheck, stop := 0, -1, -1
	for i, call := range r.calls {
		if strings.Contains(call, "umask 077") && strings.Contains(call, "config.json.next") {
			stages = append(stages, r.stdin[i])
		}
		if strings.Contains(call, "check -c") {
			checks++
			lastCheck = i
		}
		if strings.Contains(call, "systemctl stop") && stop < 0 {
			stop = i
		}
	}
	r.mu.Unlock()
	if len(stages) != 2 || checks != 2 || stop <= lastCheck {
		t.Fatalf("fresh config must be staged and checked before stop: %v", r.callList())
	}
	userNames := func(raw string) []string {
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
		if len(cfg.Inbounds) != 1 {
			t.Fatalf("unexpected inbound count: %s", raw)
		}
		var names []string
		for _, user := range cfg.Inbounds[0].Users {
			names = append(names, user.Name)
		}
		return names
	}
	if !reflect.DeepEqual(userNames(stages[0]), []string{"alice", "bob"}) || !reflect.DeepEqual(userNames(stages[1]), []string{"bob"}) {
		t.Fatalf("final quota not reflected in restaged users: %v", stages)
	}
	d, err := st.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil {
		t.Fatal(err)
	}
	if d.DeployedHash != hashOf([]byte(stages[1])) || d.DeployedHash == hashOf([]byte(stages[0])) {
		t.Fatal("deployment hash does not reflect final configuration")
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
