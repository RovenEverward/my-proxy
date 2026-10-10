package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
)

type fakeCapabilityOps struct {
	fakeNodeOps
	calls  int
	config model.CapabilityConfig
	budget time.Duration
	err    error
}

func (f *fakeCapabilityOps) ProbeCapabilities(ctx context.Context, n model.Node, cfg model.CapabilityConfig, now time.Time) ([]model.CapabilityResult, error) {
	f.calls++
	f.config = cfg
	if deadline, ok := ctx.Deadline(); ok {
		f.budget = time.Until(deadline)
	}
	return []model.CapabilityResult{{NodeID: n.ID, InboundID: cfg.InboundID, HostID: cfg.HostID, Target: cfg.Targets[0], Status: model.CapabilityUnknown, ObservedStatus: model.CapabilityAvailable, CheckedAt: now, ExpiresAt: now.Add(time.Hour), Location: "node", Detector: "fake", Reason: "pending second observation"}}, f.err
}
func capabilityPath(id int64) string { return fmt.Sprintf("/api/nodes/%d/capabilities", id) }

func TestCapabilityAPIAuthAndAssociation(t *testing.T) {
	ops := &fakeCapabilityOps{}
	e := newEnvWith(t, ops)
	n := e.createNode("test", "node.example.com")
	in := e.createInbound(n.ID, "a", 443)
	other := e.createNode("other", "other.example.com")
	foreign := e.createInbound(other.ID, "b", 443)
	h := e.createHost(in.ID, "explicit", "cdn.example.com")
	foreignH := e.createHost(foreign.ID, "foreign", "foreign.example.com")
	cfg := model.CapabilityConfig{InboundID: in.ID, HostID: h.ID, Targets: []string{"youtube"}}
	for _, method := range []string{"GET", "PUT", "POST"} {
		path := capabilityPath(n.ID)
		if method == "POST" {
			path += "/probe"
		}
		code, _ := e.request(method, path, cfg, nil)
		if code != http.StatusUnauthorized {
			t.Errorf("%s auth=%d", method, code)
		}
	}
	for _, tc := range []struct {
		name string
		cfg  model.CapabilityConfig
		code int
	}{
		{"foreign inbound", model.CapabilityConfig{InboundID: foreign.ID}, 400},
		{"foreign host", model.CapabilityConfig{InboundID: in.ID, HostID: foreignH.ID}, 400},
		{"default hidden", model.CapabilityConfig{InboundID: in.ID}, 400},
		{"missing inbound", model.CapabilityConfig{InboundID: 99999}, 404},
		{"missing host", model.CapabilityConfig{InboundID: in.ID, HostID: 99999}, 404},
		{"unknown target", model.CapabilityConfig{InboundID: in.ID, HostID: h.ID, Targets: []string{"hulu"}}, 400},
		{"bad duration", model.CapabilityConfig{InboundID: in.ID, HostID: h.ID, IntervalSeconds: 1}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []string{"PUT", "POST"} {
				path := capabilityPath(n.ID)
				if method == "POST" {
					path += "/probe"
				}
				code, data := e.api(method, path, tc.cfg)
				if code != tc.code {
					t.Fatalf("%s got %d %s", method, code, data)
				}
			}
		})
	}
	if ops.calls != 0 {
		t.Fatal("invalid request invoked detector")
	}
	code, data := e.api("PUT", capabilityPath(n.ID), cfg)
	if code != 200 {
		t.Fatalf("save %d %s", code, data)
	}
	configs, err := e.st.ListCapabilityConfigs(n.ID)
	if err != nil || len(configs) != 1 || configs[0].Enabled {
		t.Fatalf("configs=%+v err=%v", configs, err)
	}
	code, data = e.api("POST", capabilityPath(n.ID)+"/probe", cfg)
	if code != 200 {
		t.Fatalf("probe %d %s", code, data)
	}
	if ops.calls != 1 || ops.config.Enabled || ops.budget > 2*time.Minute || ops.budget < 119*time.Second {
		t.Fatalf("ops=%+v", ops)
	}
	code, data = e.api("GET", capabilityPath(n.ID), nil)
	if code != 200 {
		t.Fatalf("read %d %s", code, data)
	}
	view := decodeJSON[capabilityView](t, data)
	if len(view.Configs) != 1 || len(view.Results) != 0 {
		t.Fatalf("view=%+v", view)
	}
	code, _ = e.api("GET", capabilityPath(99999), nil)
	if code != 404 {
		t.Fatalf("missing node=%d", code)
	}
}

func TestCapabilityOptionalOpsAndError(t *testing.T) {
	e := newEnvWith(t, &fakeNodeOps{})
	n := e.createNode("test", "node.example.com")
	in := e.createInbound(n.ID, "a", 443)
	cfg := model.CapabilityConfig{InboundID: in.ID}
	code, _ := e.api("POST", capabilityPath(n.ID)+"/probe", cfg)
	if code != 501 {
		t.Fatalf("no optional ops=%d", code)
	}
	e.s.SetNodeOps(&fakeCapabilityOps{err: errors.New("detector failed")})
	code, data := e.api("POST", capabilityPath(n.ID)+"/probe", cfg)
	if code != 502 || !strings.Contains(string(data), "detector failed") {
		t.Fatalf("error %d %s", code, data)
	}
	configs, _ := e.st.ListCapabilityConfigs(n.ID)
	if len(configs) != 0 {
		t.Fatal("manual probe implicitly saved config")
	}
}

func TestCapabilityReadInvalidatesEvidence(t *testing.T) {
	for _, kind := range []string{"valid", "expired", "fingerprint", "previous-detector", "disabled", "expired-user", "limited-user", "unauthorized", "draining", "default-replaced", "host-changed", "inbound-changed"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			n := e.createNode("test", "node.example.com")
			in := e.createInbound(n.ID, "a", 443)
			e.createUser("reader")
			now := time.Now()
			result := model.CapabilityResult{NodeID: n.ID, InboundID: in.ID, Target: "youtube", Status: model.CapabilityAvailable, ObservedStatus: model.CapabilityAvailable, Consecutive: 2, Fingerprint: control.CapabilityFingerprint(n, in, model.Host{}), CheckedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), Reason: "HTTP 200", Location: "node", Detector: "fake"}
			if kind == "host-changed" {
				h := e.createHost(in.ID, "host", "original.example.com")
				result.HostID = h.ID
				result.Fingerprint = control.CapabilityFingerprint(n, in, h)
				h.Address = "changed.example.com"
				code, data := e.api("PUT", pathf("/api/hosts/%d", h.ID), h)
				if code != 200 {
					t.Fatalf("host edit %d %s", code, data)
				}
			}
			if kind == "inbound-changed" {
				in.WSPath = "/changed"
				code, data := e.api("PUT", pathf("/api/inbounds/%d", in.ID), in)
				if code != 200 {
					t.Fatalf("inbound edit %d %s", code, data)
				}
			}
			if kind == "expired" {
				result.ExpiresAt = now.Add(-time.Second)
			}
			if kind == "fingerprint" {
				result.Fingerprint = "old"
			}
			if kind == "previous-detector" {
				// Evidence generated before the detector identity joined the fingerprint
				// must become unknown even when the public endpoint is unchanged.
				body, err := json.Marshal(struct {
					Node    model.Node
					Inbound model.Inbound
					Host    model.Host
				}{n, in, model.Host{}})
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(body)
				result.Fingerprint = hex.EncodeToString(sum[:])
			}
			if err := e.st.SaveCapabilityResults([]model.CapabilityResult{result}); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "disabled":
				e.updateUser("reader", func(u *model.User) { u.Enabled = false })
			case "expired-user":
				e.updateUser("reader", func(u *model.User) { at := now.Add(-time.Hour); u.ExpireAt = &at })
			case "limited-user":
				e.updateUser("reader", func(u *model.User) { u.DataLimit = 1; u.UsedTraffic = 1 })
			case "unauthorized":
				other := e.createInbound(n.ID, "other", 444)
				e.updateUser("reader", func(u *model.User) { u.InboundIDs = []int64{other.ID} })
			case "draining":
				code, data := e.api("POST", pathf("/api/nodes/%d/drain", n.ID), nil)
				if code != 200 {
					t.Fatalf("drain %d %s", code, data)
				}
			case "default-replaced":
				e.createHost(in.ID, "new", "new.example.com")
			}
			code, data := e.api("GET", capabilityPath(n.ID), nil)
			if code != 200 {
				t.Fatalf("get %d %s", code, data)
			}
			view := decodeJSON[capabilityView](t, data)
			if len(view.Results) != 1 {
				t.Fatalf("view=%+v", view)
			}
			want := model.CapabilityUnknown
			if kind == "valid" {
				want = model.CapabilityAvailable
			}
			got := view.Results[0]
			if got.Status != want || got.ObservedStatus != model.CapabilityAvailable || !strings.Contains(got.Reason, "HTTP 200") {
				t.Fatalf("got=%+v want=%s", got, want)
			}
			persisted, err := e.st.ListCapabilityResults(n.ID)
			if err != nil || persisted[0].Status != model.CapabilityAvailable {
				t.Fatalf("read mutated store %+v %v", persisted, err)
			}
			for _, path := range []string{pathf("/api/nodes/%d", n.ID), "/api/nodes"} {
				code, data = e.api("GET", path, nil)
				if code != 200 {
					t.Fatalf("node summary %d %s", code, data)
				}
				var node model.NodeView
				if path == "/api/nodes" {
					node = decodeJSON[[]model.NodeView](t, data)[0]
				} else {
					node = decodeJSON[model.NodeView](t, data)
				}
				if len(node.Capabilities) != 1 || node.Capabilities[0].Status != want {
					t.Fatalf("summary=%+v", node.Capabilities)
				}
			}
			if strings.Contains(string(data), "sub_token") || strings.Contains(string(data), "uuid") {
				t.Fatal("capability response disclosed user credentials")
			}
		})
	}
}
