package model

import "time"

// NodePending 表示这台节点上有还没应用(sync / install)的入站改动。
type NodePending struct {
	NodeID int64     `json:"node_id"`
	Since  time.Time `json:"since"`
}
