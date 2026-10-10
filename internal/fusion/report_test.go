package fusion

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func fixture(status string) string {
	return "progress\n{\n  \"schema_version\": \"goecs.report/v1\",\n \"ecs_version\":\"v0.2.12\",\"status\":\"" + status + "\",\"text\":\"hardware\",\"sections\":[{\"name\":\"basic\",\"enabled\":true,\"status\":\"ok\"}],\"components\":[{\"name\":\"media\",\"status\":\"error\",\"reason\":\"blocked\"}]}"
}

func TestCaptureTruthAndEOF(t *testing.T) {
	r := NewReport(Config{})
	r.Capture(fixture("ok"))
	if r.Status != "partial" || len(r.Data) == 0 || len(r.Warnings) != 1 {
		t.Fatalf("lost failure: %+v", r)
	}
	for _, out := range []string{"raw evidence", fixture("ok") + " trailing", fixture("ok")[:90]} {
		r = NewReport(Config{})
		r.Capture(out)
		if r.Status != "partial" || r.Error == "" || len(r.Data) != 0 {
			t.Fatalf("accepted invalid data: %+v", r)
		}
	}
	r = NewReport(Config{})
	r.Capture(fixture("canceled"))
	if r.Status != "canceled" {
		t.Fatal(r.Status)
	}
}

func TestOutputAndPayloadBounds(t *testing.T) {
	text, truncated := CleanOutput("\x1b[31m" + strings.Repeat("汉", 30000) + "\x1b[0m")
	if !truncated || len(text) > MaxOutputBytes || !utf8.ValidString(text) || strings.Contains(text, "\x1b") {
		t.Fatal("invalid bounded UTF8")
	}
	parts := make([]any, 12)
	for i := range parts {
		parts[i] = map[string]any{"name": "media", "schema_version": "test/v1", "status": "partial", "payload": map[string]any{"samples": []any{strings.Repeat("汉", 20000), "last"}}}
	}
	body, _ := json.Marshal(map[string]any{"schema_version": "goecs.report/v1", "ecs_version": Version, "status": "partial", "text": strings.Repeat("text", 40000), "components": parts})
	data, cut := boundedData(body)
	var got struct {
		Components []map[string]any `json:"components"`
	}
	if !cut || len(data) > MaxDataBytes || json.Unmarshal(data, &got) != nil || len(got.Components) != 12 {
		t.Fatalf("lost bounded component evidence %d: %s", len(data), data)
	}
	for _, part := range got.Components {
		if part["status"] != "partial" {
			t.Fatal("changed component status")
		}
		if _, exists := part["payload"]; !exists && (part["payload_truncated"] != true || part["payload_omissions"] == nil) {
			t.Fatal("unmarked payload omission")
		}
	}
}

func TestStrictOfficialArchive(t *testing.T) {
	elf := make([]byte, 64)
	copy(elf, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	elf[18] = 62
	for _, name := range []string{"goecs", "../goecs", "sub/goecs"} {
		var buf bytes.Buffer
		w := zip.NewWriter(&buf)
		f, _ := w.Create(name)
		_, _ = f.Write(elf)
		_ = w.Close()
		a := Asset{Arch: "amd64", SHA256: checksum(buf.Bytes())}
		_, err := a.Executable(buf.Bytes())
		if (name == "goecs") != (err == nil) {
			t.Fatalf("%s: %v", name, err)
		}
		a.SHA256 = strings.Repeat("0", 64)
		if _, err = a.Executable(buf.Bytes()); err == nil {
			t.Fatal("accepted checksum mismatch")
		}
	}
	if _, err := OfficialAsset("386"); err == nil {
		t.Fatal("accepted unsupported architecture")
	}
	if _, err := (Asset{}).Executable(make([]byte, MaxArchiveBytes+1)); err == nil {
		t.Fatal("accepted oversized archive")
	}
}

func TestConfigTimeout(t *testing.T) {
	c, err := (Config{}).Normalized()
	if err != nil || c.TimeoutSeconds != 900 {
		t.Fatal(c, err)
	}
	for _, seconds := range []int{-1, 1, 1801} {
		if _, err := (Config{TimeoutSeconds: seconds}).Normalized(); err == nil {
			t.Fatal(seconds)
		}
	}
}

func TestOfficialReportFixture(t *testing.T) {
	path := os.Getenv("MPX_FUSION_REPORT_FIXTURE")
	if path == "" {
		t.Skip("set MPX_FUSION_REPORT_FIXTURE for full official report proof")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Components []map[string]any `json:"components"`
	}
	if err = json.Unmarshal(body, &source); err != nil {
		t.Fatal(err)
	}
	r := NewReport(Config{})
	r.Capture(string(body))
	var retained struct {
		Components []map[string]any `json:"components"`
	}
	if err = json.Unmarshal(r.Data, &retained); err != nil {
		t.Fatal(err)
	}
	if len(r.Data) > MaxDataBytes || len(source.Components) != len(retained.Components) {
		t.Fatalf("lost components: %d/%d bytes%d", len(retained.Components), len(source.Components), len(r.Data))
	}
	for i, original := range source.Components {
		for _, key := range []string{"name", "schema_version", "status"} {
			if original[key] != retained.Components[i][key] {
				t.Fatalf("component%d %s changed", i, key)
			}
		}
	}
	t.Logf("official report %d bytes retained %d bytes components%d truncated=%t status=%s", len(body), len(r.Data), len(retained.Components), r.Truncated, r.Status)
}
