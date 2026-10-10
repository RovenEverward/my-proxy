package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

const diagnosticReportLimit = 256 * 1024

func (s *Store) migrateDiagnostics() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS diagnostic_reports (
 node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
 source TEXT NOT NULL CHECK(source IN ('node','control')),
 measured_address TEXT NOT NULL DEFAULT '',
 checked_at INTEGER NOT NULL,
 report TEXT NOT NULL,
 PRIMARY KEY(node_id,source))`)
	return err
}

func (s *Store) SaveDiagnosticReport(record model.DiagnosticRecord) error {
	if record.NodeID <= 0 || (record.Source != "node" && record.Source != "control") || record.CheckedAt.IsZero() {
		return fmt.Errorf("综合体检报告缺少节点、测量位置或时间")
	}
	if len(record.Report) > diagnosticReportLimit || !json.Valid(record.Report) {
		return fmt.Errorf("综合体检报告无效或超过 256 KiB")
	}
	_, err := s.db.Exec(`INSERT INTO diagnostic_reports(node_id,source,measured_address,checked_at,report) VALUES(?,?,?,?,?)
 ON CONFLICT(node_id,source) DO UPDATE SET measured_address=excluded.measured_address,checked_at=excluded.checked_at,report=excluded.report`, record.NodeID, record.Source, record.MeasuredAddress, record.CheckedAt.Unix(), string(record.Report))
	return err
}

func (s *Store) GetDiagnosticReport(nodeID int64, source string) (*model.DiagnosticRecord, error) {
	var at int64
	var raw string
	record := &model.DiagnosticRecord{NodeID: nodeID, Source: source}
	err := s.db.QueryRow(`SELECT measured_address,checked_at,report FROM diagnostic_reports WHERE node_id=? AND source=?`, nodeID, source).Scan(&record.MeasuredAddress, &at, &raw)
	if err != nil {
		return nil, wrapNotFound(err, "综合体检报告")
	}
	record.CheckedAt = time.Unix(at, 0).UTC()
	record.Report = json.RawMessage(raw)
	return record, nil
}
