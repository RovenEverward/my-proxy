package cli

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func resetCoreCommandFlags(t *testing.T) {
	t.Helper()
	add, edit, core := inboundAddFlags, inboundEditFlags, nodeCoreFlags
	t.Cleanup(func() {
		inboundAddFlags, inboundEditFlags, nodeCoreFlags = add, edit, core
		rootCmd.SetArgs(nil)
	})
	for _, cmd := range []*cobra.Command{inboundAddCmd, inboundEditCmd, nodeCoreEditCmd} {
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		})
	}
}

func TestQUICInboundCLIAddDefaults(t *testing.T) {
	resetCoreCommandFlags(t)
	var got model.Inbound
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/inbounds" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		got.ID = 7
		_ = json.NewEncoder(w).Encode(got)
	})
	rootCmd.SetArgs([]string{"inbound", "add", "--node", "1", "--protocol", "tuic", "--port", "443", "--cert-file", "/cert.pem", "--key-file", "/key.pem", "--congestion-control", "new_reno"})
	if code := Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got.Network != model.NetworkUDP || got.Security != model.SecurityTLS || got.CongestionControl != "new_reno" {
		t.Fatalf("payload: %+v", got)
	}
}

func TestQUICInboundCLIEditDefaults(t *testing.T) {
	resetCoreCommandFlags(t)
	var got model.Inbound
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/inbounds/7" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(model.Inbound{ID: 7, NodeID: 1, Protocol: model.ProtocolTrojan, Network: model.NetworkTCP, Security: model.SecurityTLS, Port: 443, CertFile: "/cert.pem", KeyFile: "/key.pem"})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(got)
	})
	rootCmd.SetArgs([]string{"inbound", "edit", "7", "--protocol", "tuic", "--congestion-control", "bbr"})
	if code := Execute(); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got.Network != model.NetworkUDP || got.Security != model.SecurityTLS || got.CongestionControl != "bbr" {
		t.Fatalf("payload: %+v", got)
	}
}

func TestCoreCLIMetadata(t *testing.T) {
	resetCoreCommandFlags(t)
	var got model.CoreDeployment
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/nodes/1/cores/sing-box" {
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(model.CoreDeployment{NodeID: 1, Core: model.CoreSingBox, APIPort: 10086, ConfigPath: "/etc/mpx/sing-box/config.json", ServiceName: "mpx-sing-box"})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(got)
	})
	rootCmd.SetArgs([]string{"node", "core", "edit", "1", "sing-box", "--api-port", "11086"})
	if code := Execute(); code != 0 {
		t.Fatalf("edit exit %d", code)
	}
	if got.APIPort != 11086 || got.ConfigPath != "/etc/mpx/sing-box/config.json" || got.ServiceName != "mpx-sing-box" {
		t.Fatalf("metadata payload: %+v", got)
	}
}
