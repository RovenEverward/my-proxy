package model

import (
	"encoding/json"
	"time"
)

// DiagnosticRecord is the latest operator report at a specified measurement
// position. Local CLI reports remain local; the control plane retains at most
// one node-side and one control-side report per node.
type DiagnosticRecord struct {
	NodeID          int64           `json:"node_id"`
	MeasuredAddress string          `json:"measured_address,omitempty"`
	Source          string          `json:"source"`
	CheckedAt       time.Time       `json:"checked_at"`
	Report          json.RawMessage `json:"report"`
}
