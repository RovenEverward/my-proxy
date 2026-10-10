package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestFusionCLIShowsRetainedStageAndOmittedEvidence(t *testing.T) {
	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	raw := []byte(`{"engine":"fusion","version":"v0.2.12","status":"timeout","truncated":true,"data":{"adapter_composed":true,"adapter_stages":[{"name":"baseline","status":"done","official_status":"ok","duration_ms":35000},{"name":"speed","status":"timeout","duration_ms":1000,"error":"deadline exceeded"}],"components":[{"name":"unlocktests.media","status":"ok","payload":{"results":[{"name":"ChatGPT","status":"Yes"}]},"payload_omissions":[{"field":"registry.matched","original_count":100,"retained_count":0}]},{"name":"speed.registry","status":"partial","payload_omission_reason":"speed payload exceeds remaining report budget"}]}}`)
	if err := printFusionReport(cmd, raw); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"状态: timeout", "按阶段保留官方运行结果", "阶段: baseline  状态: done", "阶段: speed  状态: timeout", "deadline exceeded", "registry.matched", "ChatGPT", "speed payload exceeds remaining report budget", "已压缩或截断"} {
		if !strings.Contains(output.String(), text) {
			t.Fatalf("missing %q in %s", text, output.String())
		}
	}
}
