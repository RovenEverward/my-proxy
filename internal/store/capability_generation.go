package store

import "github.com/RovenEverward/my-proxy/internal/model"

// CapabilityGeneration detects config edits even if their contents are unchanged.
func (s *Store) CapabilityGeneration(inboundID, hostID int64) (int64, error) {
	var generation int64
	err := s.db.QueryRow(`SELECT COALESCE((SELECT generation FROM capability_generations WHERE inbound_id=? AND host_id=?),0)`, inboundID, hostID).Scan(&generation)
	return generation, err
}

// SaveCapabilityResultsIfCurrent prevents an in-flight probe from reviving evidence
// after the operator disables or edits its configuration.
func (s *Store) SaveCapabilityResultsIfCurrent(results []model.CapabilityResult, generation int64) (bool, error) {
	if len(results) == 0 {
		return true, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var current int64
	if err := tx.QueryRow(`SELECT COALESCE((SELECT generation FROM capability_generations WHERE inbound_id=? AND host_id=?),0)`, results[0].InboundID, results[0].HostID).Scan(&current); err != nil {
		return false, err
	}
	if current != generation {
		return false, nil
	}
	if err := saveCapabilityResults(tx, results); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
