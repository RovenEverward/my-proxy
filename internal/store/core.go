package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// GetCoreDeployment 返回生效的核心部署信息。Xray 始终从 Node 原字段读取。
func (s *Store) GetCoreDeployment(nodeID int64, core model.Core) (*model.CoreDeployment, error) {
	if !core.Valid() {
		return nil, fmt.Errorf("不支持的核心: %s", core)
	}
	n, err := s.GetNode(nodeID)
	if err != nil {
		return nil, err
	}
	if core == model.CoreXray {
		return &model.CoreDeployment{NodeID: nodeID, Core: core, APIPort: n.APIPortOrDefault(), ConfigPath: n.ConfigPathOrDefault(), ServiceName: n.ServiceNameOrDefault(), DeployedHash: n.DeployedHash, DeployedAt: n.DeployedAt}, nil
	}
	d := model.CoreDeployment{NodeID: nodeID, Core: core}
	var at sql.NullInt64
	err = s.db.QueryRow(`SELECT api_port, config_path, service_name, deployed_hash, deployed_at FROM core_deployments WHERE node_id=? AND core=?`, nodeID, core).Scan(&d.APIPort, &d.ConfigPath, &d.ServiceName, &d.DeployedHash, &at)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	d.APIPort = d.APIPortOrDefault()
	d.ConfigPath = d.ConfigPathOrDefault()
	d.ServiceName = d.ServiceNameOrDefault()
	if at.Valid {
		t := time.Unix(at.Int64, 0).UTC()
		d.DeployedAt = &t
	}
	return &d, nil
}

// SetCoreDeployment 整体覆盖部署信息。传入下发记录由控制面维护。
func (s *Store) SetCoreDeployment(d model.CoreDeployment) error {
	if !d.Core.Valid() {
		return fmt.Errorf("不支持的核心: %s", d.Core)
	}
	var at any
	if d.DeployedAt != nil {
		at = normalizeTime(*d.DeployedAt).Unix()
	}
	if d.Core == model.CoreXray {
		res, err := s.db.Exec(`UPDATE nodes SET api_port=?,config_path=?,service_name=?,deployed_hash=?,deployed_at=? WHERE id=?`, d.APIPort, d.ConfigPath, d.ServiceName, d.DeployedHash, at, d.NodeID)
		if err != nil {
			return err
		}
		return affectedOrNotFound(res, fmt.Sprintf("节点 %d", d.NodeID))
	}
	_, err := s.db.Exec(`INSERT INTO core_deployments (node_id,core,api_port,config_path,service_name,deployed_hash,deployed_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT(node_id,core) DO UPDATE SET api_port=excluded.api_port,config_path=excluded.config_path,service_name=excluded.service_name,deployed_hash=excluded.deployed_hash,deployed_at=excluded.deployed_at`, d.NodeID, d.Core, d.APIPort, d.ConfigPath, d.ServiceName, d.DeployedHash, at)
	return err
}

// SetCoreDeployed 只更新一次成功下发的基准,不覆盖核心的监听和配置位置。
func (s *Store) SetCoreDeployed(nodeID int64, core model.Core, hash string, now time.Time) error {
	if !core.Valid() {
		return fmt.Errorf("不支持的核心: %s", core)
	}
	if core == model.CoreXray {
		res, err := s.db.Exec(`UPDATE nodes SET deployed_hash=?,deployed_at=? WHERE id=?`, hash, normalizeTime(now).Unix(), nodeID)
		if err != nil {
			return err
		}
		return affectedOrNotFound(res, fmt.Sprintf("节点 %d", nodeID))
	}
	_, err := s.db.Exec(`INSERT INTO core_deployments(node_id,core,deployed_hash,deployed_at) VALUES (?,?,?,?) ON CONFLICT(node_id,core) DO UPDATE SET deployed_hash=excluded.deployed_hash,deployed_at=excluded.deployed_at`, nodeID, core, hash, normalizeTime(now).Unix())
	return err
}
