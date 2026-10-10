package model

import "encoding/json"

// NodeCheckupReport combines measurements from one manual operator action.
// Network is a diagnostics.Report; it is raw JSON to keep the model layer independent.
type NodeCheckupReport struct {
	Summary   []string             `json:"summary"`
	Health    NodeStatus           `json:"health"`
	Drift     string               `json:"drift"`
	Network   json.RawMessage      `json:"network"`
	Entrances []EntranceDiagnostic `json:"entrances"`
}

type EntranceDiagnostic struct {
	InboundID int64              `json:"inbound_id"`
	HostID    int64              `json:"host_id"`
	Name      string             `json:"name"`
	Results   []CapabilityResult `json:"results"`
	Error     string             `json:"error,omitempty"`
}
