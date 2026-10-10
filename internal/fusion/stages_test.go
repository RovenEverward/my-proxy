package fusion

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func stageFixture(status string, components, sections []any) json.RawMessage {
	return encode(struct {
		Schema     string `json:"schema_version"`
		Version    string `json:"ecs_version"`
		Status     string `json:"status"`
		Components []any  `json:"components"`
		Sections   []any  `json:"sections"`
		Text       string `json:"text"`
	}{"goecs.report/v1", Version, status, components, sections, "baseline evidence"})
}
func stageComponent(name, schema, status string, payload any) any {
	return map[string]any{"name": name, "schema_version": schema, "status": status, "payload": payload}
}

func TestMergeSpeedPreservesBaseline(t *testing.T) {
	core := stageComponent("unlocktests.media", "goecs.unlocktests/v1", "partial", map[string]any{"results": []any{map[string]any{"name": "Netflix", "status": "blocked"}}})
	raw := stageFixture("partial", []any{core}, []any{map[string]any{"name": "media", "enabled": true, "status": "partial"}, map[string]any{"name": "speed", "enabled": false, "status": "skipped"}})
	base := NewReport(Config{Speed: true})
	base.Status = "partial"
	base.Output = "baseline evidence"
	base.Data, _ = boundedData(raw)
	base.Finish(nil)
	for _, status := range []string{"done", "canceled", "timeout", "partial"} {
		t.Run(status, func(t *testing.T) {
			speed := NewReport(Config{Speed: true})
			speed.Status = status
			speed.Finish(nil)
			data := stageFixture("ok", []any{stageComponent("speed.registry", "goecs.speed/v1", "ok", map[string]any{"schema_version": "goecs.speed/v1", "benchmarks": []any{map[string]any{"download_mbps": 123.4}}}), stageComponent("unlocktests.media", "goecs.unlocktests/v1", "ok", "wrong replacement")}, []any{map[string]any{"name": "speed", "enabled": true, "status": "ok"}})
			if status != "done" {
				data = nil
				speed.Error = "deadline evidence"
			}
			got := MergeSpeed(base, raw, speed, data, []StageRecord{RecordStage("baseline", base, raw), RecordStage("speed", speed, data)})
			var root map[string]json.RawMessage
			if err := json.Unmarshal(got.Data, &root); err != nil {
				t.Fatal(err)
			}
			components := rawArray(root["components"])
			if len(components) < 1 || string(components[0]) != string(encode(core)) {
				t.Fatalf("baseline overwritten: %s", got.Data)
			}
			if strings.Contains(string(got.Data), "wrong replacement") {
				t.Fatal("non-speed evidence merged")
			}
			if status == "done" && len(components) != 2 {
				t.Fatalf("speed dropped: %s", got.Data)
			}
			if status != "done" && len(components) != 1 {
				t.Fatal("invented component")
			}
			if !strings.HasPrefix(got.Output, "baseline evidence") || !got.Speed || len(rawArray(root["adapter_stages"])) != 2 {
				t.Fatalf("missing provenance: %+v", got)
			}
			if status == "timeout" || status == "canceled" {
				if got.Status != status {
					t.Fatalf("lost cancellation %s", got.Status)
				}
			}
		})
	}
}

func TestMergeSpeedBoundsAndRejectsSchema(t *testing.T) {
	payload := map[string]any{"rows": strings.Repeat("中", 60000)}
	raw := stageFixture("ok", []any{stageComponent("basics", "goecs.basics/system-v1", "ok", payload)}, nil)
	base := NewReport(Config{Speed: true})
	base.Status = "done"
	base.Output = strings.Repeat("中", MaxOutputBytes/3)
	base.Finish(nil)
	speed := NewReport(Config{Speed: true})
	speed.Status = "done"
	speed.Finish(nil)
	data := stageFixture("ok", []any{stageComponent("speed.registry", "wrong/v1", "ok", "not official schema")}, nil)
	got := MergeSpeed(base, raw, speed, data, nil)
	if got.Status != "partial" || !got.Truncated || len(got.Data) > MaxDataBytes || len(got.Output) > MaxOutputBytes || strings.Contains(string(got.Data), "wrong/v1") {
		t.Fatalf("bounds/schema failure: %+v", got)
	}
}

func TestOfficialDataRejectsTrailingOrWrongVersion(t *testing.T) {
	raw := stageFixture("ok", nil, nil)
	pretty, _ := json.MarshalIndent(json.RawMessage(raw), "", "  ")
	if len(OfficialData("prefix\n"+string(pretty))) == 0 {
		t.Fatal("valid rejected")
	}
	for _, out := range []string{string(pretty) + "garbage", string(pretty[:len(pretty)-1]), strings.Replace(string(pretty), Version, "v999", 1)} {
		if OfficialData(out) != nil {
			t.Fatal("invalid accepted")
		}
	}
	r := NewReport(Config{})
	r.Error = strings.Repeat("secret-shaped external error ", 100)
	r.FinishedAt = time.Now()
	stage := RecordStage("baseline", r, raw)
	if len(stage.Error) > 512 || stage.OfficialStatus != "ok" {
		t.Fatal(stage)
	}
}

func TestMergeSpeedNeverEvictsRetainedBaseline(t *testing.T) {
	for _, size := range []int{126000, 130700} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			core := stageComponent("unlocktests.media", "goecs.unlocktests/media-v1", "partial", map[string]any{"rows": strings.Repeat("x", size), "counter": json.Number("9007199254740993")})
			raw := stageFixture("partial", []any{core}, nil)
			base := NewReport(Config{Speed: true})
			base.Status = "partial"
			base.Data, _ = boundedData(raw)
			base.Finish(nil)
			speed := NewReport(Config{Speed: true})
			speed.Status = "done"
			speed.Finish(nil)
			speedRaw := stageFixture("ok", []any{stageComponent("speed.registry", "goecs.speed/v1", "ok", map[string]any{"schema_version": "goecs.speed/v1", "huge": strings.Repeat("y", 200000)})}, []any{map[string]any{"name": "speed", "enabled": true, "status": "ok"}})
			got := MergeSpeed(base, raw, speed, speedRaw, []StageRecord{RecordStage("baseline", base, raw), RecordStage("speed", speed, speedRaw)})
			var before, after map[string]json.RawMessage
			_ = json.Unmarshal(base.Data, &before)
			_ = json.Unmarshal(got.Data, &after)
			left, right := rawArray(before["components"]), rawArray(after["components"])
			if len(right) == 0 || string(left[0]) != string(right[0]) || len(got.Data) > MaxDataBytes || !got.Truncated {
				t.Fatalf("evicted baseline (%d->%d bytes)", len(base.Data), len(got.Data))
			}
		})
	}
}

func TestMergeSpeedMissingOfficialSectionIsPartial(t *testing.T) {
	raw := stageFixture("ok", []any{stageComponent("basics", "goecs.basics/system-v1", "ok", "retained")}, nil)
	base := NewReport(Config{Speed: true})
	base.Status = "done"
	base.Data = raw
	base.Finish(nil)
	speed := NewReport(Config{Speed: true})
	speed.Status = "done"
	speed.Finish(nil)
	data := stageFixture("ok", []any{stageComponent("speed.registry", "goecs.speed/v1", "ok", map[string]any{"schema_version": "goecs.speed/v1"})}, nil)
	got := MergeSpeed(base, raw, speed, data, nil)
	if got.Status != "partial" || !strings.Contains(string(got.Data), `"adapter_generated":true`) {
		t.Fatalf("absent speedsection falselydone: %+v", got)
	}
}
