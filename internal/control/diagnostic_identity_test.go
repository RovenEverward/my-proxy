package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/diagnostics"
)

func TestDiagnoseControlAcceptsLongStoredNodeName(t *testing.T) {
	runner := &diagnosticRunner{}
	c, n := newDiagnosticControl(t, runner)
	n.Name = strings.Repeat("长", 40)
	if err := c.store.UpdateNode(&n); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	record, err := c.DiagnoseNode(ctx, n, diagnostics.Config{Position: "control"}, time.Now())
	if !errors.Is(err, context.Canceled) || record.MeasuredAddress != n.Address {
		t.Fatalf("normalization rejected legal node: %+v %v", record, err)
	}
	if runner.calls.Load() != 0 {
		t.Fatal("canceled run performed SSH")
	}
}
