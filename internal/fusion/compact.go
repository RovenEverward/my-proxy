package fusion

import (
	"encoding/json"
	"sort"
	"strconv"
)

// compactPayload only removes verbose evidence with a known pinned contract.
// Measurements, errors, target identities, and units keep their upstream values.
func compactPayload(item map[string]any) bool {
	if !knownPayload(item) {
		return false
	}
	payload, _ := item["payload"].(map[string]any)
	switch item["name"] {
	case "unlocktests.media":
		if registry, ok := payload["registry"].(map[string]any); ok {
			omitField(item, registry, "matched", "registry.matched")
		}
	case "security.evidence":
		addresses, _ := payload["addresses"].([]any)
		for i, entry := range addresses {
			address, _ := entry.(map[string]any)
			if dnsbl, ok := address["dnsbl"].(map[string]any); ok {
				omitField(item, dnsbl, "probes", fmtPath("addresses", i)+".dnsbl.probes")
			}
		}
	case "portchecker.email":
		// The containing array already supplies the row kind. Keep unexpected
		// kinds unchanged so schema extensions are not silently normalized.
		for _, group := range []struct{ field, kind string }{{"fixed", "fixed_protocol"}, {"local", "local_listen"}, {"mx", "mx_smtp25"}, {"outbound_smtp25", "outbound_smtp25"}} {
			rows, _ := payload[group.field].([]any)
			count := 0
			for _, entry := range rows {
				row, _ := entry.(map[string]any)
				if row["kind"] == group.kind {
					delete(row, "kind")
					count++
				}
			}
			if count > 0 {
				omissions, _ := item["payload_omissions"].([]any)
				item["payload_omissions"] = append(omissions, map[string]any{"field": group.field + "[].kind", "original_count": count, "retained_count": 0, "value": group.kind, "reason": "kind identified by containing array"})
				item["payload_compacted"] = true
				item["payload_truncated"] = true
			}
		}
	case "backtrace.ip_bgp":
		omitField(item, payload, "asn_registry", "asn_registry")
		reports, _ := payload["reports"].([]any)
		for i, entry := range reports {
			report, _ := entry.(map[string]any)
			omitField(item, report, "rdap", fmtPath("reports", i)+".rdap")
			if relationships, ok := report["relationships"].(map[string]any); ok {
				for _, field := range []string{"peers", "ixps"} {
					omitField(item, relationships, field, fmtPath("reports", i)+".relationships."+field)
				}
			}
		}
	case "nt3.province_latency":
		rows, _ := item["payload"].([]any)
		count, bytes, affected := 0, 0, 0
		for _, entry := range rows {
			row, _ := entry.(map[string]any)
			if samples, exists := row["samples"]; exists {
				body, _ := json.Marshal(samples)
				bytes += len(body)
				if samples, ok := samples.([]any); ok {
					count += len(samples)
				}
				affected++
				delete(row, "samples")
			}
		}
		if affected > 0 {
			item["payload_omissions"] = []any{map[string]any{"field": "payload[].samples", "original_bytes": bytes, "retained_bytes": 0, "original_count": count, "retained_count": 0, "affected_rows": affected}}
			item["payload_compacted"] = true
			item["payload_truncated"] = true
		}
	}
	return item["payload_compacted"] == true
}

func knownPayload(item map[string]any) bool {
	contracts := map[string]string{
		"basics": "goecs.system/v1", "unlocktests.media": "goecs.unlocktests/media-v1",
		"security.evidence": "goecs.security/v1", "portchecker.email": "goecs.portchecker/mail-v1",
		"backtrace.ip_bgp": "goecs.backtrace/v2", "nt3.province_latency": "goecs.nt3/province-latency-v1",
		"ping.icmp": "goecs.ping/icmp-v1", "gostun.nat": "goecs.stun/v1", "speed.registry": "goecs.speed/v1",
	}
	name, _ := item["name"].(string)
	schema, exists := contracts[name]
	if !exists || item["schema_version"] != schema {
		return false
	}
	// Array payloads have no embedded schema; object payloads must agree if present.
	if payload, ok := item["payload"].(map[string]any); ok {
		if name == "portchecker.email" {
			schema = "portchecker.mail/v1"
		}
		if embedded, exists := payload["schema_version"]; exists && embedded != schema {
			return false
		}
	}
	return true
}

func fmtPath(field string, index int) string {
	return field + "[" + strconv.Itoa(index) + "]"
}

func omitField(item, object map[string]any, key, path string) {
	value, exists := object[key]
	if !exists {
		return
	}
	body, _ := json.Marshal(value)
	omission := map[string]any{"field": path, "original_bytes": len(body), "retained_bytes": 0}
	if rows, ok := value.([]any); ok {
		omission["original_count"] = len(rows)
		omission["retained_count"] = 0
	} else if object, ok := value.(map[string]any); ok {
		counts := make(map[string]int)
		collectArrayCounts(object, "", counts)
		if len(counts) > 0 {
			omission["original_array_counts"] = counts
			omission["retained_count"] = 0
		}
	}
	omissions, _ := item["payload_omissions"].([]any)
	item["payload_omissions"] = append(omissions, omission)
	item["payload_compacted"] = true
	item["payload_truncated"] = true
	delete(object, key)
}

func collectArrayCounts(object map[string]any, prefix string, counts map[string]int) {
	for field, value := range object {
		path := prefix + field
		switch value := value.(type) {
		case []any:
			counts[path] = len(value)
		case map[string]any:
			collectArrayCounts(value, path+".", counts)
		}
	}
}

// fitPayloads is the exceptional overflow path after semantic compaction.
// Omit whole payloads instead of breaking names, errors or classification text.
// Unknown contracts are omitted first, then the largest remaining payloads.
func fitPayloads(root map[string]any, components []any) json.RawMessage {
	type candidate struct {
		item  map[string]any
		known bool
		bytes int
	}
	items := make([]candidate, 0, len(components))
	for _, entry := range components {
		if item, ok := entry.(map[string]any); ok {
			body, _ := json.Marshal(item["payload"])
			items = append(items, candidate{item: item, known: knownPayload(item), bytes: len(body)})
		}
	}
	// Stable order makes equal-size overflow deterministic without selecting by status.
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].known != items[j].known {
			return !items[i].known
		}
		return items[i].bytes > items[j].bytes
	})
	body, _ := json.Marshal(root)
	for _, candidate := range items {
		item := candidate.item
		if len(body) <= MaxDataBytes {
			return body
		}
		if _, exists := item["payload"]; exists {
			omitField(item, item, "payload", "payload")
			item["payload_omission_reason"] = "compacted report exceeds 128 KiB; entire payload omitted"
			body, _ = json.Marshal(root)
		}
	}
	return body
}
