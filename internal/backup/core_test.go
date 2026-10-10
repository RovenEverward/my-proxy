package backup

import (
	"bytes"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/store"
	"os"
	"testing"
	"time"
)

func TestQUICCoreBackupPreservesAccountingBoundary(t *testing.T) {
	st, path := newPopulatedStore(t, t.TempDir())
	nodes, err := st.ListNodes()
	if err != nil {
		t.Fatal(err)
	}
	n := nodes[0]
	d := model.CoreDeployment{NodeID: n.ID, Core: model.CoreSingBox, APIPort: 11086, ConfigPath: "/etc/quic/config.json", ServiceName: "quic", DeployedHash: "applied"}
	if err := st.SetCoreDeployment(d); err != nil {
		t.Fatal(err)
	}
	in := model.Inbound{NodeID: n.ID, Protocol: model.ProtocolTUIC, Network: model.NetworkUDP, Security: model.SecurityTLS, Port: 9443, CertFile: "/cert.pem", KeyFile: "/key.pem", CongestionControl: "bbr"}
	if err := st.CreateInbound(&in); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, core := range []model.Core{model.CoreXray, model.CoreSingBox} {
		if err := st.ApplyCoreTrafficSample(model.TrafficSample{NodeID: n.ID, Username: "alice", Core: core, Generation: "old-process", SampledAt: now, Uplink: 100, Downlink: 200}, core); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ResetCoreTrafficBaselines(n.ID, model.CoreSingBox, now); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	m, _, err := Create(path, "", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if m.RowCounts["core_deployments"] != 1 {
		t.Fatalf("missing core metadata: %+v", m)
	}
	snap, _, err := Validate(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(snap) })
	restored, err := store.Open(snap)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetCoreDeployment(n.ID, model.CoreSingBox)
	if err != nil || got.APIPort != 11086 || got.DeployedHash != "applied" || got.ServiceName != "quic" {
		t.Fatalf("core restore %+v %v", got, err)
	}
	ins, err := restored.ListInbounds(n.ID)
	if err != nil || len(ins) != 1 || ins[0].CongestionControl != "bbr" || ins[0].Protocol != model.ProtocolTUIC {
		t.Fatalf("inbound restore %+v %v", ins, err)
	}
	// Restoring a planned-stop baseline must not subtract the old core counters.
	if err := restored.ApplyCoreTrafficSample(model.TrafficSample{NodeID: n.ID, Username: "alice", Core: model.CoreSingBox, Generation: "new-process", SampledAt: now, Uplink: 150, Downlink: 250}, model.CoreSingBox); err != nil {
		t.Fatal(err)
	}
	u, err := restored.GetUser("alice")
	if err != nil || u.UsedTraffic != 1000 || u.LifetimeUsedTraffic != 1000 {
		t.Fatalf("restored ledger %+v %v", u, err)
	}
	xr, err := restored.LatestTrafficSamples(n.ID)
	if err != nil || xr["alice"].Uplink != 100 {
		t.Fatalf("Xray baseline changed %+v %v", xr, err)
	}
}
