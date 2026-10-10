package fusion

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func compactFixture(t *testing.T) map[string]any {
	t.Helper()
	path := os.Getenv("MPX_FUSION_REPORT_FIXTURE")
	if path == "" {
		t.Skip("set MPX_FUSION_REPORT_FIXTURE for original upstream payload proof")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func compactRoundTrip(t *testing.T, original map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	bounded, _ := boundedData(body)
	if len(bounded) > MaxDataBytes {
		t.Fatalf("exceeded 128 KiB: %d", len(bounded))
	}
	var retained map[string]any
	if err := json.Unmarshal(bounded, &retained); err != nil {
		t.Fatal(err)
	}
	t.Logf("original=%d retained=%d components=%d", len(body), len(bounded), len(retained["components"].([]any)))
	return retained
}

func assertEssentialPayloads(t *testing.T, original, retained map[string]any) {
	t.Helper()
	before, after := original["components"].([]any), retained["components"].([]any)
	if len(before) != len(after) {
		t.Fatal("lost component envelopes")
	}
	for i, entry := range before {
		old, got := entry.(map[string]any), after[i].(map[string]any)
		for _, key := range []string{"name", "schema_version", "status", "reason"} {
			if !reflect.DeepEqual(old[key], got[key]) {
				t.Fatalf("component %d changed %s", i, key)
			}
		}
		oldPayload, _ := old["payload"].(map[string]any)
		gotPayload, _ := got["payload"].(map[string]any)
		switch old["name"] {
		case "unlocktests.media":
			if !reflect.DeepEqual(oldPayload["results"], gotPayload["results"]) {
				t.Fatal("lost or changed media results, including failed and IPv6 results")
			}
		case "security.evidence":
			oldAddresses, gotAddresses := oldPayload["addresses"].([]any), gotPayload["addresses"].([]any)
			for j := range oldAddresses {
				a, b := oldAddresses[j].(map[string]any), gotAddresses[j].(map[string]any)
				for _, key := range []string{"ip", "ip_type", "providers"} {
					if !reflect.DeepEqual(a[key], b[key]) {
						t.Fatalf("changed risk %s", key)
					}
				}
				ad, bd := a["dnsbl"].(map[string]any), b["dnsbl"].(map[string]any)
				for _, key := range []string{"total", "counts", "ip", "ip_type"} {
					if !reflect.DeepEqual(ad[key], bd[key]) {
						t.Fatalf("changed DNSBL %s", key)
					}
				}
			}
		case "backtrace.ip_bgp":
			if !reflect.DeepEqual(oldPayload["return_routes"], gotPayload["return_routes"]) {
				t.Fatal("changed return routes, classification, target_reached or hop RTT")
			}
		case "portchecker.email":
			for _, field := range []string{"fixed", "local", "mx", "outbound_smtp25"} {
				a, b := oldPayload[field].([]any), gotPayload[field].([]any)
				if len(a) != len(b) {
					t.Fatalf("lost %s email rows", field)
				}
				for j, original := range a {
					for key, value := range original.(map[string]any) {
						if key != "kind" && !reflect.DeepEqual(value, b[j].(map[string]any)[key]) {
							t.Fatalf("changed email %s row %d %s", field, j, key)
						}
					}
				}
			}
		case "speed.registry":
			if !sameJSON(oldPayload, gotPayload) {
				t.Fatalf("changed %s rows, errors or units", old["name"])
			}
		}
	}
}

func TestCompactOriginalPayloads(t *testing.T) {
	root := compactFixture(t)
	retained := compactRoundTrip(t, root)
	assertEssentialPayloads(t, root, retained)
	for _, entry := range retained["components"].([]any) {
		item := entry.(map[string]any)
		if item["payload_compacted"] == true && item["payload_omissions"] == nil {
			t.Fatal("compaction must expose omissions")
		}
	}
}

// The upstream fixture has seven modules. These additional modules are
// deterministic synthetic cases, not evidence of a nine-module production run.
func TestCompactNineModuleBudget(t *testing.T) {
	root := compactFixture(t)
	rows := make([]any, 200)
	for i := range rows {
		rows[i] = map[string]any{"target": map[string]any{"province_code": fmt.Sprint(i), "province_name": "北京市", "carrier": "ct", "host": "[redacted]", "ip_version": "ipv4"}, "attempts": 2, "failed": 1, "successful": 1, "loss_percent": 50, "mean": 174738600, "min": 166180801, "max": 183296400, "p50": 174738601, "p95": 182440620, "samples": []any{map[string]any{"attempt": 1, "duration": 174738600, "success": true}, map[string]any{"attempt": 2, "error": "timeout", "success": false}}}
	}
	benchmarks := make([]any, 60)
	for i := range benchmarks {
		benchmarks[i] = map[string]any{"id": fmt.Sprint(i), "name": "Shanghai", "source": "privatespeedtest", "status": "timeout", "error": "context deadline exceeded", "download_mbps": 1.5, "latency_ms": 200, "duration_ms": 1000}
	}
	root["components"] = append(root["components"].([]any), map[string]any{"name": "nt3.province_latency", "schema_version": "goecs.nt3/province-latency-v1", "status": "partial", "payload": rows}, map[string]any{"name": "speed.registry", "schema_version": "goecs.speed/v1", "status": "partial", "payload": map[string]any{"schema_version": "goecs.speed/v1", "benchmarks": benchmarks, "private_benchmarks": []any{}, "nodes": []any{}, "selected": []any{}}})
	retained := compactRoundTrip(t, root)
	// The original seven modules and speed must preserve all result fields.
	before := root["components"].([]any)
	after := retained["components"].([]any)
	for i := range before {
		old, got := before[i].(map[string]any), after[i].(map[string]any)
		if old["name"] == "nt3.province_latency" {
			gotRows, ok := got["payload"].([]any)
			if !ok || len(gotRows) != 200 {
				t.Fatal("lost province targets")
			}
			for j, original := range rows {
				for key, value := range original.(map[string]any) {
					if key != "samples" && !sameJSON(value, gotRows[j].(map[string]any)[key]) {
						t.Fatalf("province %d changed %s", j, key)
					}
				}
			}
			continue
		}
		assertEssentialPayloads(t, map[string]any{"components": []any{old}}, map[string]any{"components": []any{got}})
	}
}

func sameJSON(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func TestCompactSmallRawAndUnknownContract(t *testing.T) {
	root := map[string]any{"schema_version": "goecs.report/v1", "ecs_version": Version, "components": []any{map[string]any{"name": "unlocktests.media", "schema_version": "future/v2", "status": "error", "payload": map[string]any{"results": []any{map[string]any{"name": "Netflix", "status": "Error", "error": "full error"}}, "registry": map[string]any{"matched": []any{"keep me"}}}}}}
	if got := compactRoundTrip(t, root); !reflect.DeepEqual(got, root) {
		t.Fatal("small raw report must remain unchanged")
	}
	root["components"].([]any)[0].(map[string]any)["payload"].(map[string]any)["large"] = strings.Repeat("x", MaxDataBytes*2)
	got := compactRoundTrip(t, root)["components"].([]any)[0].(map[string]any)
	if got["name"] != "unlocktests.media" || got["status"] != "error" || got["payload_truncated"] != true || got["payload_omissions"] == nil {
		t.Fatal("unknown payload omission must retain truthful envelope and metadata")
	}
	omission := got["payload_omissions"].([]any)[0].(map[string]any)
	counts := omission["original_array_counts"].(map[string]any)
	if counts["results"] != float64(1) || counts["registry.matched"] != float64(1) || omission["retained_count"] != float64(0) {
		t.Fatal("unknown omission must report actual original array sizes")
	}
}

func TestCompactPreservesLongErrorsAndNames(t *testing.T) {
	errorText := strings.Repeat("完整错误", 200)
	name := strings.Repeat("ServiceName", 30)
	root := map[string]any{"schema_version": "goecs.report/v1", "ecs_version": Version, "components": []any{
		map[string]any{"name": "unlocktests.media", "schema_version": "goecs.unlocktests/media-v1", "status": "partial", "payload": map[string]any{"schema_version": "goecs.unlocktests/media-v1", "registry": map[string]any{"source_count": 400, "matched": []any{strings.Repeat("description", MaxDataBytes)}}, "results": []any{map[string]any{"name": name, "status": "DNSResolveFailed", "ip_version": "ipv6", "error": errorText}, map[string]any{"name": "Netflix", "status": "Yes", "ip_version": "ipv4", "region": "US"}}}},
	}}
	got := compactRoundTrip(t, root)
	assertEssentialPayloads(t, root, got)
	item := got["components"].([]any)[0].(map[string]any)
	omission := item["payload_omissions"].([]any)[0].(map[string]any)
	if omission["original_count"] != float64(1) || omission["retained_count"] != float64(0) {
		t.Fatal("registry omission must reflect removed entries")
	}
}
