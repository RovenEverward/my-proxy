package model

import "time"

// TrafficSample 是某个用户在某节点某一时刻的流量计数。
//
// Uplink / Downlink 是指定核心报的**累计值**,不是两次采样之间的增量。采样不再
// 「读走即清零」之后,节点上的计数会一直涨,增量由相邻两次累计值相减得到。
type TrafficSample struct {
	ID         int64
	Username   string
	Core       Core   `json:"core"`
	Generation string `json:"generation,omitempty"`
	NodeID     int64
	SampledAt  time.Time
	Uplink     int64
	Downlink   int64
}

// TrafficDelta 是两次累计采样之间的增量。
type TrafficDelta struct {
	Uplink   int64
	Downlink int64
	// Restarted 表示相对上次采样出现了回退。xray 的计数只增不减,所以回退只有
	// 一种解释:节点上的 xray 重启过,计数从零重来。这里只报告事实,怎么记由
	// 调用方决定。
	Restarted bool
}

// DeltaFrom 用上一次的累计值算出这一次的增量。
//
// prev 为 nil 表示这个用户在这个节点上是第一次被采到:此时增量取本次的累计值。
// xray 的计数从它启动那一刻就在涨,控制面第一次看到某人时,那之前已经产生的流量
// 只有这一个数能代表;若当成 0,这段用量就永远对不上账了。
//
// 任一方向的累计值变小就整笔按「重启」处理:一次重启会把两个方向一起清零,只坏
// 一个方向说明这假设不成立,那也该停下来看,而不是把负增量当成正常差值累加。
// 回退时增量取 0(不把用户用量倒扣回去),由调用方把新的累计值存成基准。
func (s TrafficSample) DeltaFrom(prev *TrafficSample) TrafficDelta {
	if prev == nil {
		return TrafficDelta{Uplink: s.Uplink, Downlink: s.Downlink}
	}
	up := s.Uplink - prev.Uplink
	down := s.Downlink - prev.Downlink
	if up < 0 || down < 0 {
		return TrafficDelta{Restarted: true}
	}
	return TrafficDelta{Uplink: up, Downlink: down}
}
