package fusion

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// StageRecord describes an actual official invocation, not a measurement.
type StageRecord struct {
	Name           string    `json:"name"`
	Attempted      bool      `json:"attempted"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	DurationMS     float64   `json:"duration_ms"`
	Status         string    `json:"status"`
	OfficialStatus string    `json:"official_status,omitempty"`
	Error          string    `json:"error,omitempty"`
}

// OfficialData validates the full envelope before retaining stage provenance.
func OfficialData(out string) json.RawMessage {
	const anchor = "{\n  \"schema_version\": \"goecs.report/v1\""
	index := strings.LastIndex(out, anchor)
	if index < 0 {
		return nil
	}
	data := []byte(strings.TrimSpace(out[index:]))
	var header struct {
		Schema  string `json:"schema_version"`
		Version string `json:"ecs_version"`
	}
	if json.Unmarshal(data, &header) != nil || header.Schema != "goecs.report/v1" || header.Version != Version {
		return nil
	}
	return data
}

func RecordStage(name string, report Report, data json.RawMessage) StageRecord {
	var header struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(data, &header)
	return StageRecord{Name: name, Attempted: true, StartedAt: report.StartedAt, FinishedAt: report.FinishedAt, DurationMS: report.DurationMS, Status: report.Status, OfficialStatus: header.Status, Error: stageText(report.Error, 512)}
}

// MergeSpeed preserves previously bounded baseline components byte for byte.
// Only the official speed.registry component and speed section enter from the
// second invocation. NAT/identity work in that invocation cannot replace core
// evidence. Metadata marks the envelope as a composition of official runs.
func MergeSpeed(base Report, baseData json.RawMessage, speed Report, speedData json.RawMessage, stages []StageRecord) Report {
	var root, next map[string]json.RawMessage
	if len(base.Data) == 0 {
		base.Data, _ = boundedData(baseData)
	}
	protected := base.Data
	if json.Unmarshal(protected, &root) != nil || root == nil {
		base.Warnings = append(base.Warnings, "baseline structured evidence is absent; speed merge skipped")
		return base
	}
	_ = json.Unmarshal(speedData, &next)
	components := rawArray(root["components"])
	mergedComponent := false
	for _, component := range rawArray(next["components"]) {
		var identity struct {
			Name    string `json:"name"`
			Schema  string `json:"schema_version"`
			Payload struct {
				Schema string `json:"schema_version"`
			} `json:"payload"`
		}
		if json.Unmarshal(component, &identity) == nil && identity.Name == "speed.registry" && identity.Schema == "goecs.speed/v1" && identity.Payload.Schema == "goecs.speed/v1" {
			components = withoutName(components, "speed.registry")
			components = append(components, component)
			mergedComponent = true
			break
		}
	}
	sections := withoutName(rawArray(root["sections"]), "speed")
	var speedSection json.RawMessage
	for _, section := range rawArray(next["sections"]) {
		var identity struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		}
		if json.Unmarshal(section, &identity) == nil && identity.Name == "speed" && identity.Enabled {
			speedSection = section
			break
		}
	}
	officialSection := speedSection != nil
	if speedSection == nil || !mergedComponent {
		status := speed.Status
		if status == "done" || status == "failed" {
			status = "partial"
		}
		speedSection = encode(map[string]any{"name": "speed", "enabled": true, "status": status, "reason": "adapter: speed stage did not deliver an official speed component and section", "adapter_generated": true})
	}
	sections = append(sections, speedSection)
	root["components"] = encode(components)
	root["sections"] = encode(sections)
	root["adapter_composed"] = encode(true)
	root["adapter_stages"] = encode(stages)
	provenance := map[string]json.RawMessage{}
	for _, key := range []string{"data", "data_files", "dns"} {
		if value, ok := next[key]; ok {
			provenance[key] = value
		}
	}
	if len(provenance) > 0 {
		root["adapter_speed_sources"] = encode(provenance)
	}
	base.Speed = true
	if (speed.Status != "done" || !mergedComponent || !officialSection) && base.Status == "done" {
		base.Status = "partial"
	}
	if speed.Status == "timeout" || speed.Status == "canceled" {
		base.Status = speed.Status
	}
	root["status"] = encode(officialStatus(base.Status))
	root["started_at"] = encode(base.StartedAt)
	root["finished_at"] = encode(speed.FinishedAt)
	root["duration_ms"] = encode(float64(speed.FinishedAt.Sub(base.StartedAt)) / float64(time.Millisecond))
	summary := fmt.Sprintf("\n[mpx fusion speed stage] status=%s; official speed component delivered=%t", speed.Status, mergedComponent)
	if speed.Error != "" {
		summary += "; " + stageText(speed.Error, 512)
	}
	summary += "\n"
	prefix := base.Output
	if len(prefix)+len(summary) > MaxOutputBytes {
		prefix = utf8Prefix(prefix, max(0, MaxOutputBytes-len(summary)))
		base.Truncated = true
	}
	base.Output = prefix + summary
	root["text"] = encode(base.Output)
	data, mergedCut := fitComposedData(root, protected)
	base.Data = data
	base.Truncated = base.Truncated || speed.Truncated || mergedCut
	if mergedCut {
		base.Warnings = append(base.Warnings, "composed official report exceeds 128 KiB; omitted new speed evidence before baseline components")
	}
	if !officialSection {
		base.Warnings = append(base.Warnings, "speed stage delivered no enabled official speed section; baseline evidence retained")
	}
	if !mergedComponent {
		base.Warnings = append(base.Warnings, "speed stage delivered no official speed component; baseline evidence retained")
	}
	for _, warning := range speed.Warnings {
		if strings.HasPrefix(warning, "speed:") || strings.HasPrefix(warning, "speed.registry:") {
			base.Warnings = append(base.Warnings, warning)
		}
	}
	if speed.Error != "" {
		base.Error = stageText(speed.Error, 2048)
	}
	if base.Truncated && base.Status == "done" {
		base.Status = "partial"
		root["status"] = encode("partial")
		base.Data, _ = fitComposedData(root, protected)
	}
	var composed map[string]json.RawMessage
	_ = json.Unmarshal(base.Data, &composed)
	if len(composed["adapter_stages"]) == 0 {
		base.Warnings = append(base.Warnings, "speed stage metadata and new speed evidence could not fit; retained unchanged baseline JSON; see Output and overall status")
	}
	if len(base.Warnings) > 32 {
		base.Warnings = base.Warnings[:32]
	}
	return base
}

func officialStatus(status string) string {
	switch status {
	case "failed":
		return "error"
	case "done":
		return "ok"
	default:
		return status
	}
}

// fitComposedData only removes new speed payload/provenance or duplicate text.
// Previously retained baseline components never enter a second compaction pass.
func fitComposedData(root map[string]json.RawMessage, protected json.RawMessage) (json.RawMessage, bool) {
	body := encode(root)
	if len(body) <= MaxDataBytes {
		return body, false
	}
	components := rawArray(root["components"])
	for index, raw := range components {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil || string(item["name"]) != `"speed.registry"` {
			continue
		}
		delete(item, "payload")
		item["payload_truncated"] = encode(true)
		item["payload_omission_reason"] = encode("speed payload omitted to preserve baseline evidence within 128 KiB")
		components[index] = encode(item)
	}
	root["components"] = encode(components)
	root["adapter_truncated"] = encode(true)
	body = encode(root)
	if len(body) <= MaxDataBytes {
		return body, true
	}
	delete(root, "adapter_speed_sources")
	root["adapter_speed_sources_omitted"] = encode(true)
	body = encode(root)
	if len(body) <= MaxDataBytes {
		return body, true
	}
	delete(root, "text")
	body = encode(root)
	if len(body) <= MaxDataBytes {
		return body, true
	}
	return protected, true
}

func rawArray(data json.RawMessage) []json.RawMessage {
	var result []json.RawMessage
	_ = json.Unmarshal(data, &result)
	return result
}
func withoutName(items []json.RawMessage, name string) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		var identity struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(item, &identity) != nil || identity.Name != name {
			result = append(result, item)
		}
	}
	return result
}
func encode(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func stageText(text string, limit int) string {
	text, _ = CleanOutput(text)
	return utf8Prefix(text, limit)
}
func utf8Prefix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}
