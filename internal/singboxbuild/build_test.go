package singboxbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchRejectsUnpinnedAndUnsupportedRequests(t *testing.T) {
	for _, req := range []Request{
		{Version: "latest", OS: "linux", Arch: "amd64", Dir: t.TempDir()},
		{Version: "../v1.14.3", OS: "linux", Arch: "amd64", Dir: t.TempDir()},
		{OS: "darwin", Arch: "arm64", Dir: t.TempDir()},
		{OS: "linux", Arch: "386", Dir: t.TempDir()},
		{OS: "linux", Arch: "amd64"},
	} {
		if _, err := Fetch(t.Context(), req); err == nil {
			t.Errorf("接受了错误构建请求: %+v", req)
		}
	}
}

func TestCacheRejectsNonOfficialMetadata(t *testing.T) {
	req := Request{Version: DefaultVersion, OS: "linux", Arch: "amd64"}
	dir := t.TempDir()
	bin := filepath.Join(dir, "sing-box")
	if err := os.WriteFile(bin, []byte("not a Go binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := checksum(bin)
	if err != nil {
		t.Fatal(err)
	}
	baseline := metadata{Source: SourceURL, Version: DefaultVersion, Revision: DefaultRevision, OS: req.OS, Arch: req.Arch, Tags: BuildTags, SHA256: sum}
	for name, mutate := range map[string]func(*metadata){
		"release download":                   func(m *metadata) { m.Source = "https://github.com/SagerNet/sing-box/releases" },
		"wrong pinned revision":              func(m *metadata) { m.Revision = strings.Repeat("0", 40) },
		"missing stats":                      func(m *metadata) { m.Tags = "with_quic" },
		"foreign platform":                   func(m *metadata) { m.Arch = "arm64" },
		"tampered binary":                    func(m *metadata) { m.SHA256 = strings.Repeat("0", 64) },
		"metadata alone cannot bless binary": func(m *metadata) {},
	} {
		t.Run(name, func(t *testing.T) {
			meta := baseline
			mutate(&meta)
			body, _ := json.Marshal(meta)
			if err := os.WriteFile(filepath.Join(dir, "metadata.json"), body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok := cached(dir, req); ok {
				t.Fatal("invalid cache was accepted")
			}
		})
	}
}

func TestBuildEnvOverridesHostTarget(t *testing.T) {
	t.Setenv("GOOS", "darwin")
	t.Setenv("GOARCH", "arm64")
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("GOFLAGS", "-tags=release")
	env := buildEnv(Request{OS: "linux", Arch: "amd64"})
	for key, want := range map[string]string{"GOOS": "linux", "GOARCH": "amd64", "CGO_ENABLED": "0", "GOFLAGS": "", "GOWORK": "off"} {
		count := 0
		for _, s := range env {
			if strings.HasPrefix(s, key+"=") {
				count++
				if s != key+"="+want {
					t.Errorf("%s", s)
				}
			}
		}
		if count != 1 {
			t.Errorf("%s had %d definitions", key, count)
		}
	}
}

// Opt-in official-source build verifies both supported executable formats and cache reuse.
func TestOfficialCrossBuild(t *testing.T) {
	dir := os.Getenv("MPX_SINGBOX_BUILD_TEST_CACHE")
	if dir == "" {
		t.Skip("set MPX_SINGBOX_BUILD_TEST_CACHE for real official-source cross-builds")
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			req := Request{OS: "linux", Arch: arch, Dir: dir}
			files, err := Fetch(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			if files.Version != DefaultVersion || files.Revision != DefaultRevision {
				t.Fatalf("unexpected build: %+v", files)
			}
			before, err := os.Stat(files.Binary)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Fetch(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(second.Binary)
			if err != nil {
				t.Fatal(err)
			}
			if second != files || !after.ModTime().Equal(before.ModTime()) {
				t.Fatal("verified cache did not preserve executable")
			}
			t.Logf("official %s linux/%s revision=%s binary=%s bytes=%d; cache hit", files.Version, arch, files.Revision, files.Binary, after.Size())
		})
	}
}
