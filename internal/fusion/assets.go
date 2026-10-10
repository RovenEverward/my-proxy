package fusion

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MaxArchiveBytes = 24 << 20
const MaxExecutableBytes = 48 << 20

type Asset struct{ Arch, URL, SHA256 string }

func OfficialAsset(arch string) (Asset, error) {
	var sum string
	switch arch {
	case "amd64":
		sum = "71fe7c474d093db623e85fbc48c73e158fc4ad7d2da70ee18156fed478c422e0"
	case "arm64":
		sum = "84fbe2e974fb65e55cfbd2f3af464298086a17e598b6c33e0e2a0cc75c372290"
	default:
		return Asset{}, fmt.Errorf("unsupported fusion Linux architecture %q", arch)
	}
	return Asset{Arch: arch, URL: "https://github.com/oneclickvirt/ecs/releases/download/" + Version + "/goecs_linux_" + arch + ".zip", SHA256: sum}, nil
}

func checksum(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }

// Executable returns only the single regular official goecs archive member.
// No archive paths are ever written to disk, and all sizes are checked before allocation.
func (a Asset) Executable(body []byte) ([]byte, error) {
	if len(body) > MaxArchiveBytes {
		return nil, fmt.Errorf("fusion archive exceeds %d bytes", MaxArchiveBytes)
	}
	if checksum(body) != a.SHA256 {
		return nil, fmt.Errorf("fusion archive SHA256 mismatch")
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("fusion ZIP: %w", err)
	}
	if len(reader.File) != 1 {
		return nil, fmt.Errorf("fusion ZIP must contain exactly one executable")
	}
	file := reader.File[0]
	if file.Name != "goecs" || !file.Mode().IsRegular() || file.UncompressedSize64 == 0 || file.UncompressedSize64 > MaxExecutableBytes {
		return nil, fmt.Errorf("unsafe fusion ZIP member %q", file.Name)
	}
	stream, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	exe, err := io.ReadAll(io.LimitReader(stream, MaxExecutableBytes+1))
	if err != nil {
		return nil, err
	}
	if len(exe) > MaxExecutableBytes || uint64(len(exe)) != file.UncompressedSize64 {
		return nil, fmt.Errorf("fusion executable size mismatch")
	}
	if len(exe) < 20 || !bytes.Equal(exe[:4], []byte{0x7f, 'E', 'L', 'F'}) || exe[4] != 2 || exe[5] != 1 {
		return nil, fmt.Errorf("fusion executable is not a 64-bit little-endian ELF")
	}
	expected := byte(62)
	if a.Arch == "arm64" {
		expected = 183
	}
	if exe[18] != expected || exe[19] != 0 {
		return nil, fmt.Errorf("fusion executable architecture mismatch")
	}
	return exe, nil
}

// LoadExecutable caches the verified ZIP, never a downloaded shell/source installer.
func LoadExecutable(ctx context.Context, arch, cacheDir string) ([]byte, string, error) {
	asset, err := OfficialAsset(arch)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(cacheDir) == "" {
		return nil, "", fmt.Errorf("fusion cache directory is required")
	}
	if err = os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, "", err
	}
	cache := filepath.Join(cacheDir, "goecs-"+Version+"-linux-"+arch+".zip")
	body, err := readBounded(cache)
	if err == nil {
		exe, decodeErr := asset.Executable(body)
		if decodeErr == nil {
			return exe, checksum(exe), nil
		}
	}
	body, err = download(ctx, asset.URL)
	if err != nil {
		return nil, "", err
	}
	exe, err := asset.Executable(body)
	if err != nil {
		return nil, "", err
	}
	tmp, err := os.CreateTemp(cacheDir, ".goecs-*.zip")
	if err != nil {
		return nil, "", err
	}
	// CreateTemp guarantees this cleanup target is inside the requested cache directory.
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(body); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, "", err
	}
	if err = os.Rename(tmp.Name(), cache); err != nil {
		return nil, "", err
	}
	return exe, checksum(exe), nil
}

func readBounded(name string) ([]byte, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaxArchiveBytes {
		return nil, fmt.Errorf("invalid fusion cache file")
	}
	return io.ReadAll(io.LimitReader(file, MaxArchiveBytes+1))
}

func download(ctx context.Context, url string) ([]byte, error) {
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" {
			return fmt.Errorf("unsafe fusion download redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fusion download: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxArchiveBytes {
		return nil, fmt.Errorf("fusion download exceeds %d bytes", MaxArchiveBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxArchiveBytes {
		return nil, fmt.Errorf("fusion download exceeds %d bytes", MaxArchiveBytes)
	}
	return body, nil
}
