package model

import "time"

// UserActivity:某人在某台节点上最后一次有流量的时间。
type UserActivity struct {
	Username     string    `json:"username"`
	NodeID       int64     `json:"node_id"`
	LastActiveAt time.Time `json:"last_active_at"`
}

// OnlineSnapshot:后台每轮从 xray 读到的在线名单(用户名 → 来源 IP),只放内存。
type OnlineSnapshot struct {
	NodeID int64               `json:"node_id"`
	At     time.Time           `json:"at"`
	Users  map[string][]string `json:"users"`
}

// ActivitySegment:一段连续使用(相邻有流量的采样相隔不超过 gap)。
type ActivitySegment struct {
	NodeID int64     `json:"node_id"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Bytes  int64     `json:"bytes"`
}
