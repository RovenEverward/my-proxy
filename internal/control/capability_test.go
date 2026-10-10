package control

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestCapabilityProbeAuthorizationAndIdentity(t *testing.T) {
	r := &fakeRunner{}
	c, st := newTestControl(t, r)
	n := addNode(t, st, "proxy", "127.0.0.1")
	in := addInbound(t, st, n.ID, "vless", 12345)
	cfg := model.CapabilityConfig{InboundID: in.ID, Targets: []string{"egress"}}
	results, err := c.ProbeCapabilities(context.Background(), n, cfg, time.Now())
	if err != nil || len(results) != 1 || results[0].Status != model.CapabilityUnknown || len(r.callList()) != 0 {
		t.Fatalf("probe without authorized active user: %+v %v", results, err)
	}
	other := addNode(t, st, "other", "127.0.0.2")
	if c.ValidateCapabilityEndpoint(other, cfg) == nil {
		t.Fatal("foreign node accepted")
	}
	h := model.Host{InboundID: in.ID, Address: "cdn.example"}
	if err := st.CreateHost(&h); err != nil {
		t.Fatal(err)
	}
	if c.ValidateCapabilityEndpoint(n, cfg) == nil {
		t.Fatal("default exposure accepted with advertised hosts")
	}
	cfg.HostID = h.ID
	if err := c.ValidateCapabilityEndpoint(n, cfg); err != nil {
		t.Fatal(err)
	}
	addUser(t, st, model.User{Username: "expired", Key: "expired-key", Enabled: true, ExpireAt: timePtr(time.Now().Add(-time.Hour))})
	cfg.Targets = []string{"youtube"}
	// Config edit clears old evidence and does not authorize inactive credentials.
	if err := st.SaveCapabilityConfig(cfg); err != nil {
		t.Fatal(err)
	}
	results, err = c.ProbeCapabilities(context.Background(), n, cfg, time.Now())
	if err != nil || len(results) != 1 || len(r.callList()) != 0 {
		t.Fatalf("inactive credential probe: %+v %v", results, err)
	}
}

func timePtr(v time.Time) *time.Time { return &v }

type blockingCapabilityRunner struct {
	started chan struct{}
	once    sync.Once
}

func (r *blockingCapabilityRunner) Run(ctx context.Context, n model.Node, script string) (string, error) {
	return "", nil
}
func (r *blockingCapabilityRunner) RunWithInput(ctx context.Context, n model.Node, script string, stdin []byte) (string, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return "", ctx.Err()
}

func TestCapabilitySlowProbeDoesNotBlockControlAndBoundsConcurrency(t *testing.T) {
	r := &blockingCapabilityRunner{started: make(chan struct{})}
	c, st := newLoggingControl(t, r, slog.New(slog.DiscardHandler))
	n := addNode(t, st, "proxy", "127.0.0.1")
	in := addInbound(t, st, n.ID, "vless", 12345)
	addUser(t, st, activeUser("active"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.ProbeCapabilities(ctx, n, model.CapabilityConfig{InboundID: in.ID, Targets: []string{"egress"}}, time.Now())
		done <- err
	}()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("probe didn't start")
	}
	if _, err := c.ProbeCapabilities(context.Background(), n, model.CapabilityConfig{InboundID: in.ID}, time.Now()); err == nil {
		cancel()
		t.Fatal("concurrent probe accepted")
	}
	if _, err := st.ListUsers(); err != nil {
		cancel()
		t.Fatal("slow network held database")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe failed to stop")
	}
	results, err := st.ListCapabilityResults(n.ID)
	if err != nil || len(results) != 1 || results[0].Status != model.CapabilityUnknown {
		t.Fatalf("transport failure lost observation: %+v %v", results, err)
	}
	if _, err := c.ProbeCapabilities(context.Background(), n, model.CapabilityConfig{InboundID: in.ID}, time.Now()); err == nil {
		t.Fatal("immediate repeated manual probe accepted")
	}
}
