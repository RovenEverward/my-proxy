package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/RovenEverward/my-proxy/internal/model"
)

var capabilitySchema = []string{
	`CREATE TABLE IF NOT EXISTS capability_configs (
 inbound_id INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
 host_id INTEGER NOT NULL DEFAULT 0, body TEXT NOT NULL,
 PRIMARY KEY(inbound_id,host_id))`,
	`CREATE TABLE IF NOT EXISTS capability_results (
 inbound_id INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
 host_id INTEGER NOT NULL DEFAULT 0, target TEXT NOT NULL, body TEXT NOT NULL,
 PRIMARY KEY(inbound_id,host_id,target))`,
	`CREATE TABLE IF NOT EXISTS capability_generations (
 inbound_id INTEGER NOT NULL REFERENCES inbounds(id) ON DELETE CASCADE,
 host_id INTEGER NOT NULL DEFAULT 0, generation INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(inbound_id,host_id))`,
	`CREATE TRIGGER IF NOT EXISTS capability_host_delete AFTER DELETE ON hosts BEGIN
 DELETE FROM capability_configs WHERE host_id=OLD.id;
 DELETE FROM capability_generations WHERE host_id=OLD.id;
 DELETE FROM capability_results WHERE host_id=OLD.id;
 END`,
}

func (s *Store) migrateCapabilities() error {
	for _, ddl := range capabilitySchema {
		if _, err := s.db.Exec(ddl); err != nil {
			return fmt.Errorf("创建能力检测表: %w", err)
		}
	}
	return nil
}

func (s *Store) SaveCapabilityConfig(c model.CapabilityConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if _, err := s.GetInbound(c.InboundID); err != nil {
		return err
	}
	if c.HostID != 0 {
		h, err := s.GetHost(c.HostID)
		if err != nil {
			return err
		}
		if h.InboundID != c.InboundID {
			return fmt.Errorf("入口不属于所选入站")
		}
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO capability_configs(inbound_id,host_id,body) VALUES(?,?,?) ON CONFLICT(inbound_id,host_id) DO UPDATE SET body=excluded.body`, c.InboundID, c.HostID, string(body)); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO capability_generations(inbound_id,host_id,generation) VALUES(?,?,1) ON CONFLICT(inbound_id,host_id) DO UPDATE SET generation=generation+1`, c.InboundID, c.HostID); err != nil {
		return err
	}
	// Configuration edits revoke the previous evidence, including disabling probes.
	if _, err = tx.Exec(`DELETE FROM capability_results WHERE inbound_id=? AND host_id=?`, c.InboundID, c.HostID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListCapabilityConfigs(nodeID int64) ([]model.CapabilityConfig, error) {
	rows, err := s.db.Query(`SELECT c.body,COALESCE(g.generation,0) FROM capability_configs c JOIN inbounds i ON i.id=c.inbound_id LEFT JOIN capability_generations g ON g.inbound_id=c.inbound_id AND g.host_id=c.host_id WHERE (?=0 OR i.node_id=?) ORDER BY c.inbound_id,c.host_id`, nodeID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CapabilityConfig{}
	for rows.Next() {
		var raw string
		var generation int64
		if err := rows.Scan(&raw, &generation); err != nil {
			return nil, err
		}
		var c model.CapabilityConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, err
		}
		c.Generation = generation
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SaveCapabilityResults(results []model.CapabilityResult) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveCapabilityResults(tx, results); err != nil {
		return err
	}
	return tx.Commit()
}

func saveCapabilityResults(tx *sql.Tx, results []model.CapabilityResult) error {
	for _, r := range results {
		if !model.ValidCapabilityTarget(r.Target) {
			return fmt.Errorf("未知检测目标: %s", r.Target)
		}
		body, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO capability_results(inbound_id,host_id,target,body) VALUES(?,?,?,?) ON CONFLICT(inbound_id,host_id,target) DO UPDATE SET body=excluded.body`, r.InboundID, r.HostID, r.Target, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListCapabilityResults(nodeID int64) ([]model.CapabilityResult, error) {
	rows, err := s.db.Query(`SELECT c.body FROM capability_results c JOIN inbounds i ON i.id=c.inbound_id WHERE (?=0 OR i.node_id=?) ORDER BY c.inbound_id,c.host_id,c.target`, nodeID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.CapabilityResult{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var r model.CapabilityResult
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
