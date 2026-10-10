// Package singboxbuild builds official sing-box sources on the control host.
// Release binaries omit with_v2ray_api, so they cannot supply per-user counters.
package singboxbuild

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

const (
	DefaultVersion  = "v1.14.3"
	DefaultRevision = "7054cac5124465ffdb145870d7c01885566711b1"
	SourceURL       = "https://github.com/SagerNet/sing-box.git"
	BuildTags       = "with_quic,with_v2ray_api"
)

// Request selects an official version tag and a Linux node platform.
// Dir is the control host's cache root. Empty Version uses the pinned default.
type Request struct{ Version, OS, Arch, Dir string }

// Files is a locally built executable ready to upload to the node.
type Files struct{ Version, Binary, Revision string }

var versionTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?$`)

// Compiling QUIC uses substantial memory; serialize cache misses and publication.
var buildMu sync.Mutex

// Fetch returns a verified cache entry or builds unmodified official sources.
// A build needs git and a Go toolchain satisfying the selected source's go.mod.
func Fetch(ctx context.Context, req Request) (Files, error) {
	if req.Version == "" {
		req.Version = DefaultVersion
	}
	if !versionTag.MatchString(req.Version) {
		return Files{}, fmt.Errorf("sing-box 版本必须是明确的官方 v* 标签: %q", req.Version)
	}
	if req.OS != "linux" || (req.Arch != "amd64" && req.Arch != "arm64") {
		return Files{}, fmt.Errorf("sing-box 源码构建只支持 linux/amd64 和 linux/arm64: %s/%s", req.OS, req.Arch)
	}
	if req.Dir == "" {
		return Files{}, fmt.Errorf("sing-box 构建缓存目录不能为空")
	}
	root, err := filepath.Abs(req.Dir)
	if err != nil {
		return Files{}, err
	}
	dir := filepath.Join(root, "sing-box", req.Version, req.OS+"-"+req.Arch)
	buildMu.Lock()
	defer buildMu.Unlock()
	if err := ctx.Err(); err != nil {
		return Files{}, err
	}
	if files, ok := cached(dir, req); ok {
		return files, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Files{}, err
	}
	work, err := os.MkdirTemp(dir, ".build-")
	if err != nil {
		return Files{}, err
	}
	// work is the absolute directory just created by MkdirTemp beneath dir.
	defer os.RemoveAll(work)
	src := filepath.Join(work, "source")
	if err := os.Mkdir(src, 0o700); err != nil {
		return Files{}, err
	}
	revision, err := checkout(ctx, src, req.Version)
	if err != nil {
		return Files{}, err
	}
	bin := filepath.Join(work, "sing-box")
	// -checklinkname=0 is required by the official release/LDFLAGS.
	ldflags := "-X github.com/sagernet/sing-box/constant.Version=" + strings.TrimPrefix(req.Version, "v") + " -X runtime.godebugDefault=multipathtcp=0,tlssha1=1 -checklinkname=0 -s -w -buildid="
	cmd := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-trimpath", "-buildvcs=true", "-tags", BuildTags, "-ldflags", ldflags, "-o", bin, "./cmd/sing-box")
	cmd.Dir = src
	cmd.Env = buildEnv(req)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Files{}, fmt.Errorf("从官方源码构建 sing-box %s %s/%s: %w: %s", req.Version, req.OS, req.Arch, err, strings.TrimSpace(string(out)))
	}
	meta := metadata{Source: SourceURL, Version: req.Version, Revision: revision, OS: req.OS, Arch: req.Arch, Tags: BuildTags}
	meta.SHA256, err = checksum(bin)
	if err != nil {
		return Files{}, err
	}
	if err := verifyBinary(bin, meta); err != nil {
		return Files{}, fmt.Errorf("sing-box 构建产物校验失败: %w", err)
	}
	if err := publish(dir, work, bin, meta); err != nil {
		return Files{}, err
	}
	return Files{Version: req.Version, Binary: filepath.Join(dir, "sing-box"), Revision: revision}, nil
}

func checkout(ctx context.Context, src, version string) (string, error) {
	commands := [][]string{
		{"init", "--quiet", src},
		{"-C", src, "remote", "add", "origin", SourceURL},
		{"-C", src, "fetch", "--quiet", "--depth=1", "origin", "refs/tags/" + version},
		{"-C", src, "checkout", "--quiet", "--detach", "FETCH_HEAD"},
	}
	for _, args := range commands {
		out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("取官方 sing-box %s 源码: %w: %s", version, err, strings.TrimSpace(string(out)))
		}
	}
	out, err := exec.CommandContext(ctx, "git", "-C", src, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	revision := strings.TrimSpace(string(out))
	if version == DefaultVersion && revision != DefaultRevision {
		return "", fmt.Errorf("官方 %s 提交不符合固定版本: 得到 %s,需要 %s", version, revision, DefaultRevision)
	}
	return revision, nil
}

func buildEnv(req Request) []string {
	var env []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		switch key {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOWORK":
			continue
		}
		env = append(env, item)
	}
	return append(env, "GOOS="+req.OS, "GOARCH="+req.Arch, "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off")
}
