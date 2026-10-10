package diagnostics

import (
	"context"
	"strconv"
	"strings"
)

// DefaultSpeedSites are independently operated, verified public data endpoints.
// LibreSpeed download sizes round up to provider MiB units and charge actual size.
func DefaultSpeedSites() []SpeedSite {
	return []SpeedSite{
		{Name: "Cloudflare", DownloadURL: "https://speed.cloudflare.com/__down?bytes={bytes}", UploadURL: "https://speed.cloudflare.com/__up", SourceURL: "https://github.com/cloudflare/speedtest"},
		{Name: "Clouvider Los Angeles", DownloadURL: "https://la.speedtest.clouvider.net/backend/garbage.php?ckSize={chunks}", UploadURL: "https://la.speedtest.clouvider.net/backend/empty.php", SourceURL: "https://librespeed.org/backend-servers/servers.php", DownloadUnitBytes: 1 << 20},
	}
}

func (e *engine) speed(ctx context.Context) []SpeedResult {
	results := make([]SpeedResult, 0, len(e.cfg.SpeedSites)*2)
	for _, s := range e.cfg.SpeedSites {
		for _, direction := range []string{"download", "upload"} {
			r := SpeedResult{Site: s.Name, SourceURL: s.SourceURL, Direction: direction}
			if !e.cfg.Speed {
				r.Status = Status{"skipped", "speed tests require explicit opt-in"}
				results = append(results, r)
				continue
			}
			if !e.tools["curl"] {
				r.Status = Status{"skipped", "curl not installed"}
				results = append(results, r)
				continue
			}
			method := "GET"
			u := strings.ReplaceAll(s.DownloadURL, "{bytes}", strconv.FormatInt(e.cfg.SpeedBytes, 10))
			capBytes := e.cfg.SpeedBytes
			if s.DownloadUnitBytes > 0 {
				chunks := (capBytes + s.DownloadUnitBytes - 1) / s.DownloadUnitBytes
				capBytes = chunks * s.DownloadUnitBytes
				u = strings.ReplaceAll(u, "{chunks}", strconv.FormatInt(chunks, 10))
			}
			var payload []byte
			if direction == "upload" {
				method = "POST"
				if s.UploadMethod != "" {
					method = s.UploadMethod
				}
				u = s.UploadURL
				capBytes = 1024
				payload = make([]byte, e.cfg.SpeedBytes)
			}
			h := e.request(ctx, u, method, payload, capBytes, "", false)
			r.Bytes = h.Download
			if direction == "upload" {
				r.Bytes = h.Upload
			}
			r.DurationMS = h.Seconds * 1000
			if h.Err != nil {
				r.Status = Status{"failed", safeError(h.Err)}
			} else if r.Bytes <= 0 || h.Seconds <= 0 {
				r.Status = Status{"failed", "no measurable payload or duration"}
			} else {
				r.Status = Status{Status: "ok"}
				r.Mbps = float64(r.Bytes) * 8 / h.Seconds / 1e6
			}
			results = append(results, r)
		}
	}
	return results
}
