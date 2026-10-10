package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

func TestDiagnosticReportsBoundedAndCascade(t *testing.T) {
	s := newTestStore(t)
	n := newTestNode(t, s, "diagnostics")
	at := time.Now().UTC().Truncate(time.Second)
	record := model.DiagnosticRecord{NodeID: n.ID, Source: "node", CheckedAt: at, Report: json.RawMessage(`{"location":"node","latency":[]}`)}
	for i := 0; i < 4; i++ {
		record.CheckedAt = at.Add(time.Duration(i) * time.Minute)
		if err := s.SaveDiagnosticReport(record); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetDiagnosticReport(n.ID, "node")
	if err != nil || !got.CheckedAt.Equal(record.CheckedAt) || string(got.Report) != string(record.Report) {
		t.Fatalf("latest: %+v %v", got, err)
	}
	record.Source = "control"
	if err := s.SaveDiagnosticReport(record); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT count(*) FROM diagnostic_reports WHERE node_id=?`, n.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("bounded count: %d %v", count, err)
	}
	for _, change := range []func(*model.DiagnosticRecord){func(r *model.DiagnosticRecord) { r.Source = "local" }, func(r *model.DiagnosticRecord) { r.NodeID = 0 }, func(r *model.DiagnosticRecord) { r.CheckedAt = time.Time{} }, func(r *model.DiagnosticRecord) { r.Report = json.RawMessage(`{"unfinished"`) }, func(r *model.DiagnosticRecord) {
		r.Report = json.RawMessage(`"` + strings.Repeat("x", diagnosticReportLimit) + `"`)
	}} {
		bad := record
		change(&bad)
		if s.SaveDiagnosticReport(bad) == nil {
			t.Fatal("accepted invalid report")
		}
	}
	if err := s.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDiagnosticReport(n.ID, "node"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cascade: %v", err)
	}
	if _, err := s.GetDiagnosticReport(n.ID, "control"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cascade: %v", err)
	}
}
