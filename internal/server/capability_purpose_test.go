package server

import (
	"github.com/RovenEverward/my-proxy/internal/control"
	"github.com/RovenEverward/my-proxy/internal/model"
	"strings"
	"testing"
	"time"
)

func TestPurposeCapabilityLiveRemovalRecoveryAndAuthorization(t *testing.T) {
	e := newEnv(t)
	n := e.createNode("media", "media.example.com")
	in := e.createInbound(n.ID, "media", 443)
	h := e.createHost(in.ID, "Media", "media.example.com")
	u := e.createUser("alice")
	e.updateUser(u.Username, func(v *model.User) { v.InboundIDs = []int64{in.ID} })
	e.s.SetPurposeFilter(e.s.filterPurposeCapabilities)
	p := model.Purpose{Slug: "media", Name: "流媒体", Kind: "media", Domains: []string{"netflix.com"}, RequiredCapabilities: []string{"netflix"}, CapabilityRegion: "JP"}
	if code, b := e.api("POST", "/api/purposes", p); code != 201 {
		t.Fatalf("create: %d %s", code, b)
	}
	node, _ := e.st.GetNode(n.ID)
	inbound, _ := e.st.GetInbound(in.ID)
	host, _ := e.st.GetHost(h.ID)
	now := time.Now()
	r := model.CapabilityResult{NodeID: n.ID, InboundID: in.ID, HostID: h.ID, Target: "netflix", Status: model.CapabilityAvailable, ObservedStatus: model.CapabilityAvailable, Consecutive: 2, Region: "JP", Fingerprint: control.CapabilityFingerprint(*node, *inbound, *host), CheckedAt: now, ExpiresAt: now.Add(time.Hour)}
	save := func() {
		t.Helper()
		if err := e.st.SaveCapabilityResults([]model.CapabilityResult{r}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/sub/" + u.SubToken + "/clash-meta?purpose=media"
	get := func(want int) {
		t.Helper()
		code, _, b := e.sub(path, "")
		if code != want {
			t.Fatalf("want %d got %d %s", want, code, b)
		}
		if want == 200 && !strings.Contains(string(b), "media.example.com") {
			t.Fatal("candidate missing")
		}
	}
	get(422)
	save()
	get(200)
	r.Status = model.CapabilityUnknown
	save()
	get(422)
	r.Status = model.CapabilityAvailable
	r.ExpiresAt = now.Add(-time.Second)
	save()
	get(422)
	r.ExpiresAt = now.Add(time.Hour)
	r.Region = "US"
	save()
	get(422)
	r.Region = "JP"
	save()
	get(200)
	e.updateUser(u.Username, func(v *model.User) { v.InboundIDs = []int64{999999} })
	get(422)
	e.updateUser(u.Username, func(v *model.User) { v.InboundIDs = []int64{in.ID} })
	get(200)
	host.Address = "*.example.com"
	if err := e.st.UpdateHost(host); err != nil {
		t.Fatal(err)
	}
	r.Fingerprint = control.CapabilityFingerprint(*node, *inbound, *host)
	save()
	get(422)
}
