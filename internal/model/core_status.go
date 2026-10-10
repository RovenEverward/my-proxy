package model

// CoreStatus records an independent core observation. StatsAvailable means a
// real per-user API query succeeded, including a valid empty counter response.
type CoreStatus struct {
	Core           Core   `json:"core"`
	Active         bool   `json:"active"`
	Version        string `json:"version"`
	ObservedHash   string `json:"observed_hash"`
	StatsAvailable bool   `json:"stats_available"`
	Error          string `json:"error,omitempty"`
}
