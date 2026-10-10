package cli

import (
	"encoding/json"
	"github.com/RovenEverward/my-proxy/internal/model"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPurposeCLIConfigAndMetadata(t *testing.T) {
	var calls []string
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Error("missing auth")
		}
		if r.URL.Path == "/api/purposes" && r.Method == "POST" {
			var p model.Purpose
			if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
				t.Error(err)
			}
			if p.Slug != "media" || p.Strategy != "url-test" || p.RequiredCapabilities[0] != "netflix" || p.CapabilityRegion != "US" {
				t.Errorf("lost fields %+v", p)
			}
			w.WriteHeader(201)
		}
		w.Write([]byte(`{}`))
	})
	file := filepath.Join(t.TempDir(), "purpose.json")
	if err := os.WriteFile(file, []byte(`{"name":"Media","kind":"media","required_capabilities":["netflix"],"capability_region":"US"}`), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error { cmd := newPurposeCmd(); cmd.SetArgs(args); return cmd.Execute() }
	captureStdout(t, func() {
		if err := run("create", "media", "-f", file); err != nil {
			t.Fatal(err)
		}
		if err := run("set", "media", "-f", file); err != nil {
			t.Fatal(err)
		}
		if err := run("get", "media"); err != nil {
			t.Fatal(err)
		}
		if err := run("list"); err != nil {
			t.Fatal(err)
		}
		if err := run("del", "media"); err != nil {
			t.Fatal(err)
		}
	})
	metadata := filepath.Join(t.TempDir(), "meta.json")
	if err := os.WriteFile(metadata, []byte(`{"region":"US","line":"premium","purposes":["media"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	captureStdout(t, func() {
		if err := run("metadata", "set", "hosts", "12", "-f", metadata); err != nil {
			t.Fatal(err)
		}
		if err := run("metadata", "get", "nodes", "5"); err != nil {
			t.Fatal(err)
		}
	})
	if got := strings.Join(calls, "\n"); got != "POST /api/purposes\nPUT /api/purposes/media\nGET /api/purposes/media\nGET /api/purposes\nDELETE /api/purposes/media\nPUT /api/hosts/12/metadata\nGET /api/nodes/5/metadata" {
		t.Fatal(got)
	}
	for _, args := range [][]string{{"create", "x"}, {"preview", "media", "alice", "links"}, {"metadata", "get", "users", "1"}, {"metadata", "get", "nodes", "0"}} {
		if err := run(args...); err == nil {
			t.Fatal(args)
		}
	}
	invalid := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"name":"Media","kind":"media","required_capabilities":["fake"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run("create", "media", "-f", invalid); err == nil {
		t.Fatal("unknown capability accepted")
	}
}
func TestPurposeCLIPropagatesPreviewAndAPIErrors(t *testing.T) {
	fakeControlPlane(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/purposes/media/preview/alice/clash-meta" {
			w.Write([]byte("proxies: []\n"))
			return
		}
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"用途不存在"}`))
	})
	c := newPurposeCmd()
	c.SetArgs([]string{"preview", "media", "alice", "clash-meta"})
	out := captureStdout(t, func() {
		if err := c.Execute(); err != nil {
			t.Fatal(err)
		}
	})
	if out != "proxies: []\n" {
		t.Fatal(out)
	}
	c = newPurposeCmd()
	c.SetArgs([]string{"get", "missing"})
	if err := c.Execute(); err == nil || err.Error() != "用途不存在" {
		t.Fatal(err)
	}
}
