package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/backup"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/RovenEverward/my-proxy/internal/render"
	"github.com/RovenEverward/my-proxy/internal/store"
)

func awaitSource(t *testing.T, e *env, id int64) sourceView {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if !e.s.sources.Running(id) {
			v, err := e.st.GetSource(id)
			if err != nil {
				t.Fatal(err)
			}
			return e.s.viewSource(v)
		}
		select {
		case <-deadline.C:
			t.Fatal("source refresh did not finish")
		case <-tick.C:
		}
	}
}

func addTestSource(t *testing.T, e *env, url string) sourceView {
	t.Helper()
	code, data := e.api("POST", "/api/sources", map[string]any{"name": "airport", "url": url, "interval_seconds": 0})
	if code != 201 {
		t.Fatalf("create source: %d %s", code, data)
	}
	if strings.Contains(string(data), "secret-path") {
		t.Fatalf("URL exposed: %s", data)
	}
	v := decodeJSON[sourceView](t, data)
	return awaitSource(t, e, v.ID)
}

func TestSourcesMixedSubscriptionAndIsolation(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	n := e.createNode("managed", "managed.example.test")
	e.createInbound(n.ID, "vless", 443)
	alice, bob := e.createUser("alice"), e.createUser("bob")
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("MPX authorization sent to upstream")
		}
		if r.UserAgent() != "Clash.Meta" {
			t.Errorf("UA: %q", r.UserAgent())
		}
		if fail.Load() {
			http.Error(w, "expired secret response", 403)
			return
		}
		fmt.Fprint(w, "vless://11111111-1111-4111-8111-111111111111@first.example.test:443?security=tls&sni=first.example.test#HongKong\nvless://22222222-2222-4222-8222-222222222222@second.example.test:443?security=tls&sni=second.example.test#HongKong")
	}))
	defer upstream.Close()
	src := addTestSource(t, e, upstream.URL+"/secret-path")
	if src.NodeCount != 2 || src.LastError != "" {
		t.Fatalf("source not imported: %#v", src)
	}
	_, _, before := e.sub("/sub/"+alice.SubToken+"/sing-box", "")
	if strings.Contains(string(before), "first.example.test") {
		t.Fatal("default source disclosure")
	}
	if code, data := e.api("PUT", fmt.Sprintf("/api/sources/%d/grants/alice", src.ID), nil); code != 200 {
		t.Fatalf("grant: %d %s", code, data)
	}
	g := &model.ProxyGroup{Name: "mixed", Mode: model.GroupManual}
	if err := e.st.CreateProxyGroup(g); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.SetRouteFinal(0, model.GroupTarget(g.ID)); err != nil {
		t.Fatal(err)
	}
	e.s.invalidateSubCache()
	for _, format := range []string{"clash-meta", "sing-box", "links", "base64-links", "clash", "xray"} {
		code, _, body := e.sub("/sub/"+alice.SubToken+"/"+format, "")
		if code != 200 {
			t.Fatalf("%s: %d %s", format, code, body)
		}
		if format != "base64-links" && (!strings.Contains(string(body), "managed.example.test") || !strings.Contains(string(body), "first.example.test") || !strings.Contains(string(body), "second.example.test")) {
			t.Fatalf("%s missing endpoints: %s", format, body)
		}
	}
	code, _, body := e.sub("/sub/"+bob.SubToken+"/sing-box", "")
	if code != 200 || strings.Contains(string(body), "first.example.test") {
		t.Fatalf("bob source leakage: %s", body)
	}
	code, preview := e.api("GET", "/api/users/alice/subscription-preview?format=sing-box", nil)
	if code != 200 || !strings.Contains(string(preview), "airport") || strings.Contains(string(preview), "11111111-") {
		t.Fatalf("preview: %d %s", code, preview)
	}
	// Last good is retained on remote failure.
	fail.Store(true)
	if err := e.s.sources.StartRefresh(src.ID); err != nil {
		t.Fatal(err)
	}
	v := awaitSource(t, e, src.ID)
	if v.LastError == "" || v.NodeCount != 2 || v.InputFormat != src.InputFormat {
		t.Fatalf("last good lost: %#v", v)
	}
	_, _, body = e.sub("/sub/"+alice.SubToken+"/sing-box", "")
	if !strings.Contains(string(body), "second.example.test") {
		t.Fatal("failed refresh removed snapshot")
	}
	// A separate source filter survives old full-object group updates.
	if err := e.st.SetGroupSources(g.ID, []string{"managed"}); err != nil {
		t.Fatal(err)
	}
	if code, data := e.api("PUT", fmt.Sprintf("/api/proxy-groups/%d", g.ID), g); code != 200 {
		t.Fatalf("old group PUT: %d %s", code, data)
	}
	group, err := e.st.GetProxyGroup(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if code, data := e.api("PUT", "/api/users/alice", u); code != 200 {
		t.Fatalf("old user PUT: %d %s", code, data)
	}
	set, err := e.s.subscriptionEndpointsFor(u, model.FormatSingBox)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := e.s.routingPlan(u, set.Endpoints)
	if err != nil {
		t.Fatal(err)
	}
	for _, rg := range plan.Groups {
		if rg.ID == group.ID && len(rg.Members) != 1 {
			t.Fatalf("source filter changed: %v", rg.Members)
		}
	}
	if code, data := e.api("DELETE", fmt.Sprintf("/api/sources/%d/grants/alice", src.ID), nil); code != 200 {
		t.Fatalf("revoke: %d %s", code, data)
	}
	_, _, body = e.sub("/sub/"+alice.SubToken+"/sing-box", "")
	if strings.Contains(string(body), "first.example.test") {
		t.Fatal("revocation retained cached source")
	}
}

func TestSourceRefreshConfigurationRaceAndScheduler(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	started, release := make(chan struct{}), make(chan struct{})
	var oldCalls, newCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			if oldCalls.Add(1) == 1 {
				close(started)
			}
			<-release
			fmt.Fprint(w, "vless://11111111-1111-4111-8111-111111111111@old.example.test:443#old")
			return
		}
		newCalls.Add(1)
		fmt.Fprint(w, "vless://22222222-2222-4222-8222-222222222222@new.example.test:443#new")
	}))
	defer upstream.Close()
	src := &model.SubscriptionSource{Name: "race", URL: upstream.URL + "/old", Enabled: true, IntervalSeconds: 1}
	if err := e.st.CreateSource(src); err != nil {
		t.Fatal(err)
	}
	if err := e.s.sources.StartRefresh(src.ID); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := e.s.sources.StartRefresh(src.ID); err != nil {
		t.Fatal(err)
	}
	src.URL = upstream.URL + "/new"
	if err := e.st.UpdateSource(src); err != nil {
		t.Fatal(err)
	}
	close(release)
	v := awaitSource(t, e, src.ID)
	if v.NodeCount != 1 || v.LastError != "" || oldCalls.Load() != 1 || newCalls.Load() != 1 {
		t.Fatalf("race: %#v, %d/%d", v, oldCalls.Load(), newCalls.Load())
	}
	stored, err := e.st.GetSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Snapshot), "old.example.test") || !strings.Contains(string(stored.Snapshot), "new.example.test") {
		t.Fatal("stale snapshot committed")
	}
	e.s.sources.RefreshDue(time.Now().Add(2 * time.Second))
	awaitSource(t, e, src.ID)
	if newCalls.Load() != 2 {
		t.Fatal("due source did not refresh")
	}
	src, err = e.st.GetSource(src.ID)
	if err != nil {
		t.Fatal(err)
	}
	src.Enabled = false
	if err := e.st.UpdateSource(src); err != nil {
		t.Fatal(err)
	}
	e.s.sources.RefreshDue(time.Now().Add(time.Hour))
	if e.s.sources.Running(src.ID) {
		t.Fatal("disabled source scheduled")
	}
}

func TestSourceRefreshReplacesCredentialsAndRemovedNodes(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	u := e.createUser("alice")
	var body atomic.Value
	oldUUID := "11111111-1111-4111-8111-111111111111"
	newUUID := "22222222-2222-4222-8222-222222222222"
	body.Store("vless://" + oldUUID + "@rotate.example.test:443#rotate\nvless://" + oldUUID + "@removed.example.test:443#removed")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() != "Clash.Meta" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected upstream request credentials or UA")
		}
		fmt.Fprint(w, body.Load().(string))
	}))
	defer up.Close()
	src := addTestSource(t, e, up.URL)
	if code, data := e.api("PUT", fmt.Sprintf("/api/sources/%d/grants/alice", src.ID), nil); code != 200 {
		t.Fatalf("grant: %d %s", code, data)
	}
	code, _, first := e.sub("/sub/"+u.SubToken+"/links", "")
	if code != 200 || !strings.Contains(string(first), oldUUID) || !strings.Contains(string(first), "removed.example.test") {
		t.Fatal("initial snapshot missing")
	}
	body.Store("vless://" + newUUID + "@rotate.example.test:443#rotate")
	if err := e.s.sources.StartRefresh(src.ID); err != nil {
		t.Fatal(err)
	}
	current := awaitSource(t, e, src.ID)
	if current.NodeCount != 1 || current.LastError != "" {
		t.Fatal("new snapshot not committed")
	}
	code, _, second := e.sub("/sub/"+u.SubToken+"/links", "")
	if code != 200 || !strings.Contains(string(second), newUUID) || strings.Contains(string(second), oldUUID) || strings.Contains(string(second), "removed.example.test") {
		t.Fatalf("stale credentials or removed nodes remain: %d %s", code, second)
	}
}

func TestSourceInvalidBodiesKeepSnapshotAndFormat(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	var body atomic.Value
	body.Store("vless://11111111-1111-4111-8111-111111111111@good.example.test:443#good")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body.Load().(string)) }))
	defer up.Close()
	src := addTestSource(t, e, up.URL)
	for _, bad := range []string{"<html>please login</html>", "proxies: []", "proxies: [{name: unsupported, type: ssr, password: do-not-echo}]"} {
		body.Store(bad)
		if err := e.s.sources.StartRefresh(src.ID); err != nil {
			t.Fatal(err)
		}
		current := awaitSource(t, e, src.ID)
		if current.NodeCount != 1 || current.LastError == "" {
			t.Fatalf("invalid response replaced snapshot: %#v", current)
		}
		if strings.Contains(current.LastError, "do-not-echo") {
			t.Fatal("credentials exposed in error")
		}
	}
	body.Store("<html>login again</html>")
	if err := e.s.sources.StartRefresh(src.ID); err != nil {
		t.Fatal(err)
	}
	current := awaitSource(t, e, src.ID)
	if !strings.Contains(current.LastError, "无法识别") {
		t.Fatalf("unrecognized format diagnosis: %s", current.LastError)
	}
}

func TestSourceBackupRestoreAndCorruptSnapshotIsolation(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	n := e.createNode("managed", "managed.example.test")
	e.createInbound(n.ID, "in", 443)
	uview := e.createUser("alice")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "vless://11111111-1111-4111-8111-111111111111@restored.example.test:443#restored")
	}))
	defer up.Close()
	src := addTestSource(t, e, up.URL+"/secret-backup")
	if err := e.st.SetSourceGrant(src.ID, "alice", true); err != nil {
		t.Fatal(err)
	}
	g := &model.ProxyGroup{Name: "restore-group"}
	if err := e.st.CreateProxyGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetGroupSources(g.ID, []string{model.SourceOrigin(src.ID)}); err != nil {
		t.Fatal(err)
	}
	code, archive := e.api("GET", "/api/backup", nil)
	if code != 200 {
		t.Fatalf("backup: %d", code)
	}
	path, manifest, err := backup.Validate(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if manifest.RowCounts["subscription_sources"] != 1 || manifest.RowCounts["user_source_grants"] != 1 {
		t.Fatal("source relationships missing from archive")
	}
	restored, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	server := New(restored, testToken, e.s.logger, path)
	defer server.sources.Close()
	hs := httptest.NewServer(server.Handler())
	defer hs.Close()
	resp, err := http.Get(hs.URL + "/sub/" + uview.SubToken + "/links")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != 200 || !strings.Contains(string(raw), "restored.example.test") || !strings.Contains(string(raw), "managed.example.test") {
		t.Fatalf("restored subscription: %d %s %v", resp.StatusCode, raw, err)
	}
	filters, err := restored.GetGroupSources(g.ID)
	if err != nil || len(filters) != 1 || filters[0] != model.SourceOrigin(src.ID) {
		t.Fatal("group sources not restored")
	}
	snapshot, err := restored.GetSource(src.ID)
	if err != nil || snapshot.URL != up.URL+"/secret-backup" {
		t.Fatal("upstream URL not restored")
	}
	// A damaged external snapshot must not break unrelated managed endpoints.
	if err := restored.CompleteSourceRefresh(src.ID, snapshot.Revision, time.Now(), []byte("invalid-json"), nil, ""); err != nil {
		t.Fatal(err)
	}
	user, err := restored.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	set, err := server.subscriptionEndpointsFor(user, model.FormatLinks)
	if err != nil || len(set.Endpoints) != 1 || len(set.Issues) != 1 || set.Endpoints[0].Address != "managed.example.test" {
		t.Fatalf("bad source killed managed subscription: %v %#v", err, set)
	}
}

func TestSourceCacheRejectsOldRender(t *testing.T) {
	var cache subCache
	old := subCacheKey{generation: cache.version()}
	cache.invalidate()
	cache.put(old, &subCacheEntry{body: []byte("revoked source")})
	if cache.get(old) != nil {
		t.Fatal("stale render repopulated invalidated cache")
	}
	current := subCacheKey{generation: cache.version()}
	cache.put(current, &subCacheEntry{body: []byte("current")})
	if cache.get(current) == nil {
		t.Fatal("current render missing")
	}
}

func TestSourceInactiveUsersAndDeletedReferences(t *testing.T) {
	e := newEnv(t)
	t.Cleanup(e.s.sources.Close)
	u := e.createUser("alice")
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "vless://11111111-1111-4111-8111-111111111111@external.example.test:443#ext")
	}))
	defer up.Close()
	src := addTestSource(t, e, up.URL)
	if err := e.st.SetSourceGrant(src.ID, "alice", true); err != nil {
		t.Fatal(err)
	}
	user, err := e.st.GetUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	user.Enabled = false
	if err := e.st.UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	e.s.invalidateSubCache()
	_, _, body := e.sub("/sub/"+u.SubToken+"/sing-box", "")
	if strings.Contains(string(body), "external.example.test") || !strings.Contains(string(body), "订阅已停用") {
		t.Fatalf("inactive subscription: %s", body)
	}
	g := &model.ProxyGroup{Name: "external"}
	if err := e.st.CreateProxyGroup(g); err != nil {
		t.Fatal(err)
	}
	if err := e.st.SetGroupSources(g.ID, []string{model.SourceOrigin(src.ID)}); err != nil {
		t.Fatal(err)
	}
	if code, data := e.api("DELETE", fmt.Sprintf("/api/sources/%d", src.ID), nil); code != 204 {
		t.Fatalf("delete: %d %s", code, data)
	}
	eps := []render.Endpoint{{NodeID: 1, InboundID: 1, Remark: "managed"}}
	p, err := e.s.routingPlan(user, eps)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Groups[0].Members) != 0 {
		t.Fatal("deleted source widened selection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := e.s.sources.Refresh(ctx, src.ID); err == nil {
		t.Fatal("deleted source refreshed")
	}
}
