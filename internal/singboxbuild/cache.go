package singboxbuild

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type metadata struct {
	Source   string `json:"source"`
	Version  string `json:"version"`
	Revision string `json:"revision"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Tags     string `json:"tags"`
	SHA256   string `json:"sha256"`
}

func cached(dir string, req Request) (Files, bool) {
	body, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return Files{}, false
	}
	var meta metadata
	if json.Unmarshal(body, &meta) != nil || meta.Source != SourceURL || meta.Version != req.Version || meta.OS != req.OS || meta.Arch != req.Arch || meta.Tags != BuildTags {
		return Files{}, false
	}
	if len(meta.Revision) != 40 || len(meta.SHA256) != 64 {
		return Files{}, false
	}
	if req.Version == DefaultVersion && meta.Revision != DefaultRevision {
		return Files{}, false
	}
	bin := filepath.Join(dir, "sing-box")
	sum, err := checksum(bin)
	if err != nil || sum != meta.SHA256 || verifyBinary(bin, meta) != nil {
		return Files{}, false
	}
	return Files{Version: req.Version, Binary: bin, Revision: meta.Revision}, true
}

func verifyBinary(bin string, meta metadata) error {
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		return err
	}
	if info.Path != "github.com/sagernet/sing-box/cmd/sing-box" || info.Main.Path != "github.com/sagernet/sing-box" {
		return fmt.Errorf("不是官方 sing-box 模块")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	for key, want := range map[string]string{"GOOS": meta.OS, "GOARCH": meta.Arch, "CGO_ENABLED": "0", "-tags": BuildTags, "vcs.revision": meta.Revision, "vcs.modified": "false"} {
		if settings[key] != want {
			return fmt.Errorf("sing-box 构建参数 %s=%q,需要 %q", key, settings[key], want)
		}
	}
	return nil
}

func checksum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func publish(dir, work, bin string, meta metadata) error {
	body, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	marker := filepath.Join(work, "metadata.json")
	if err := os.WriteFile(marker, append(body, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(bin, filepath.Join(dir, "sing-box")); err != nil {
		return err
	}
	// Publish metadata last. Interrupted writes never leave a matching cache entry.
	return os.Rename(marker, filepath.Join(dir, "metadata.json"))
}
