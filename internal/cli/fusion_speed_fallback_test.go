package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
	"github.com/RovenEverward/my-proxy/internal/fusion"
	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

func TestCombinedFusionIndependentSpeedSource(t *testing.T) {
	f, _ := json.Marshal(fusion.Report{Engine: "fusion", Version: fusion.Version, Status: "canceled", Speed: true, Error: "official fusion report status: canceled"})
	n, _ := json.Marshal(diagnostics.Report{Position: "node", Warnings: []string{"独立小样本测速"}})
	p, _ := json.Marshal(model.NodeCheckupReport{Fusion: f, Network: n})
	raw, _ := json.Marshal(model.DiagnosticRecord{NodeID: 1, Source: "node", CheckedAt: time.Now(), Report: p})
	cmd := &cobra.Command{}
	cmd.Flags().String("output", "table", "")
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := diagnosticRemoteOutput(cmd, raw); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, s := range []string{"独立小样本测速补充", "不属于融合怪三网测速", "canceled"} {
		if !strings.Contains(text, s) {
			t.Fatalf("missing %q: %s", s, text)
		}
	}
}
