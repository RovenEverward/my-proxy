package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 用户在线情况。
//
// 「在线」有两份来源,缺一不可:user_activity 记的是「某人 × 某节点最后一次有
// 流量的时刻」,它说明这个人最近用过、用的是哪台节点;在线名单快照是后台每轮从
// 节点上读到的此刻连着的人,它比最近一次流量更近一步 —— 一个用户可能已经连上
// 还没开始传数据,只有快照看得见。两者都只在内存或活跃表里,这里只读不写。

const (
	// presenceOnlineWindow 是「刚有流量」的窗口:最近两轮采样(每轮 30 秒)内
	// 有流量就算在线。再多一轮就说不准了 —— 采样慢的那一轮里下线的人会被算进来。
	presenceOnlineWindow = 75 * time.Second
	// presenceStaleAfter 是节点数据算旧的门槛:超过两轮没采到采样时间,这台节点上
	// 的时间就不能说明「此刻」了,再按它判在线会把早就下线的人挂成在线。
	presenceStaleAfter = 90 * time.Second
	// activityGap 是活跃记录里相邻两次有流量的采样相隔多近算同一段使用。
	activityGap = 2 * time.Minute
	// activityDays 是活跃记录往回看几天。
	activityDays = 7
	// activityMaxSegments 是一份活跃记录最多给几段:界面画的是最近的使用情况,
	// 更早的历史翻不完也看不清。
	activityMaxSegments = 4
	// activityStaleSlack 是「旧节点上的最后活跃」还能算作该节点在用多久:数据晚到
	// 几分钟是常事,余量之内仍算这段使用还没结束。
	activityStaleSlack = 2 * time.Minute
)

// OnlineSnapshotSource 是控制面后台手里那份在线名单快照的可选扩展。
//
// 和 DiagnosticOps 一样是可选接口:NodeOps 的实现(测试里的假通道、别处接进来
// 的实现)不必有它。接不上时在线只能按活跃时间算,快照那一半缺席。
type OnlineSnapshotSource interface {
	OnlineSnapshots() map[int64]model.OnlineSnapshot
}

// userPresence 是一个用户在列表里的在线情况。
//
// 它只挂在列表的条目上,不进 userView:userView 是 PUT 的整体替换形状,往里加
// 字段等于让人能写一份「在线」上来,而它是算出来的。GET /api/users/{name}/activity
// 的回答里也带一份(字段名同样是 presence),那只是读,不是能写回去的东西。
type userPresence struct {
	Online       bool       `json:"online"`
	OnlineIPs    int        `json:"online_ips"`
	OnlineNodes  []int64    `json:"online_nodes"`
	LastActiveAt *time.Time `json:"last_active_at"`
	LastNodeID   *int64     `json:"last_node_id"`
	// Stale 表示这个人最后用的那台节点已经很久没采到数据,「在线」判不了。
	Stale bool `json:"stale,omitempty"`
}

// staleNode 是一台超过两轮没采到数据的节点。
type staleNode struct {
	NodeID int64     `json:"node_id"`
	Since  time.Time `json:"since"`
}

// presenceSummary 是整份在线情况的抬头:数据是什么时候的、此刻几个人在线、
// 最近一天多少人活跃、哪些节点的数据旧了。
type presenceSummary struct {
	UpdatedAt  *time.Time  `json:"updated_at"`
	Online     int         `json:"online"`
	Active24h  int         `json:"active_24h"`
	StaleNodes []staleNode `json:"stale_nodes"`
}

// userListItem 是用户列表的一条:用户本身,外加算出来的在线情况。
//
// 嵌入 userView 而不是复制一份:列表里每个已有字段的 JSON 键与单独读一个用户
// 时完全一样,旧客户端看到的东西不变,只是多了一个 presence。
type userListItem struct {
	userView
	Presence userPresence `json:"presence"`
}

// presence 算出此刻的在线情况:整份的抬头,以及按用户名索引的每人一份。
//
// 结果按 now 算,不读时钟:调用方拿同一个 now 判活跃段是否还没结束,两步才对得上。
// 读库失败直接返回错误 —— 没有活跃表的列表不该假装「谁都不在线」。
func (s *Server) presence(now time.Time) (presenceSummary, map[string]*userPresence, error) {
	sum := presenceSummary{StaleNodes: []staleNode{}}
	byUser := map[string]*userPresence{}

	activity, err := s.store.ListUserActivity()
	if err != nil {
		return sum, nil, err
	}
	sampled, err := s.store.NodeSampledTimes()
	if err != nil {
		return sum, nil, err
	}
	active24h, err := s.store.CountActiveSince(now.Add(-24 * time.Hour))
	if err != nil {
		return sum, nil, err
	}
	sum.Active24h = active24h

	// 旧节点:超过两轮没采到数据。UpdatedAt 取最晚的一次采样 —— 数据整体新到哪一刻。
	staleSince := map[int64]time.Time{}
	var updated *time.Time
	for id, at := range sampled {
		if updated == nil || at.After(*updated) {
			t := at
			updated = &t
		}
		if now.Sub(at) > presenceStaleAfter {
			staleSince[id] = at
		}
	}
	sum.UpdatedAt = updated
	staleIDs := make([]int64, 0, len(staleSince))
	for id := range staleSince {
		staleIDs = append(staleIDs, id)
	}
	sort.Slice(staleIDs, func(i, j int) bool { return staleIDs[i] < staleIDs[j] })
	for _, id := range staleIDs {
		sum.StaleNodes = append(sum.StaleNodes, staleNode{NodeID: id, Since: staleSince[id]})
	}

	// 最近一次有流量:取每个人最大的那个时刻,以及它所在的节点。读出来的时刻不一定
	// 按顺序排,一律按时刻比,不靠先来后到。
	type lastActive struct {
		at     time.Time
		nodeID int64
	}
	last := map[string]lastActive{}
	for _, a := range activity {
		cur, ok := last[a.Username]
		if !ok || a.LastActiveAt.After(cur.at) || (a.LastActiveAt.Equal(cur.at) && a.NodeID < cur.nodeID) {
			last[a.Username] = lastActive{at: a.LastActiveAt, nodeID: a.NodeID}
		}
	}
	for name, l := range last {
		if _, stale := staleSince[l.nodeID]; stale {
			// 旧节点:最后活跃时间照给,余量内标 stale(说不准还在用),过了余量只
			// 当作已经不在用 —— 两种情况都不算在线,区别只在这一个标记。旧的判定
			// 只看这台节点自己的采样时间,不看这人别的节点上更晚的流量。
			at, id := l.at, l.nodeID
			p := &userPresence{OnlineNodes: []int64{}, LastActiveAt: &at, LastNodeID: &id}
			if !l.at.Before(staleSince[l.nodeID].Add(-activityStaleSlack)) {
				p.Stale = true
			}
			byUser[name] = p
			continue
		}
		at, id := l.at, l.nodeID
		p := &userPresence{OnlineNodes: []int64{}, LastActiveAt: &at, LastNodeID: &id}
		if now.Sub(l.at) < presenceOnlineWindow {
			p.Online = true
			p.OnlineNodes = []int64{l.nodeID}
		}
		byUser[name] = p
	}

	// 在线名单快照:最近一轮(两轮之内)读到的每个人都算在线。快照只出现在采样
	// 正常的节点上,旧节点的快照不作数。
	if src, ok := s.nodeOps.(OnlineSnapshotSource); ok {
		ips := map[string]map[string]bool{}
		nodes := map[string]map[int64]bool{}
		for id, snap := range src.OnlineSnapshots() {
			if now.Sub(snap.At) > presenceStaleAfter {
				continue
			}
			if _, stale := staleSince[id]; stale {
				continue
			}
			for name, list := range snap.Users {
				if ips[name] == nil {
					ips[name] = map[string]bool{}
					nodes[name] = map[int64]bool{}
				}
				// 空列表也算「在这个节点上」:刚连上还没传数据的人也要出现在在线里。
				for _, ip := range list {
					ips[name][ip] = true
				}
				nodes[name][snap.NodeID] = true
			}
		}
		for name, set := range ips {
			p := byUser[name]
			if p == nil {
				p = &userPresence{OnlineNodes: []int64{}}
				byUser[name] = p
			}
			p.Online = true
			p.OnlineIPs = len(set)
			for id := range nodes[name] {
				if !containsInt64(p.OnlineNodes, id) {
					p.OnlineNodes = append(p.OnlineNodes, id)
				}
			}
			// 在线就不旧:有人在传数据,只是它那台节点的采样晚了。
			p.Stale = false
		}
	}

	for _, p := range byUser {
		sort.Slice(p.OnlineNodes, func(i, j int) bool { return p.OnlineNodes[i] < p.OnlineNodes[j] })
		if p.Online {
			sum.Online++
		}
	}
	return sum, byUser, nil
}

// containsInt64 报告 list 里有没有 v。在线节点最多几台,不值得为它建一张 map。
func containsInt64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// userActivity 是 GET /api/users/{username}/activity 的响应。
//
// Presence 与列表条目里的 presence 同一个形状:用户详情抽屉读的是这个接口,没有它
// 就看不出这个人此刻在不在线。没有活跃记录的人是零值,online_nodes 仍是 []。
type userActivity struct {
	Segments  []userActivitySegment `json:"segments"`
	IPs       []userActivityIP      `json:"ips"`
	UpdatedAt *time.Time            `json:"updated_at"`
	Presence  userPresence          `json:"presence"`
}

// userActivitySegment 是一段连续使用。End 为 null 表示这段还没结束:用户此刻
// 正连在这台节点上。
type userActivitySegment struct {
	NodeID int64      `json:"node_id"`
	Start  time.Time  `json:"start"`
	End    *time.Time `json:"end"`
	Bytes  int64      `json:"bytes"`
}

// userActivityIP 是这个用户此刻在一台节点上的一个来源 IP。
type userActivityIP struct {
	IP       string    `json:"ip"`
	NodeID   int64     `json:"node_id"`
	LastSeen time.Time `json:"last_seen"`
}

// handleUserActivity 给出一个用户最近几天的使用记录与此刻的在线 IP。
func (s *Server) handleUserActivity(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("username")
	if _, err := s.store.GetUser(name); err != nil {
		s.writeStoreError(w, err, "用户")
		return
	}
	now := time.Now()
	sum, pres, err := s.presence(now)
	if err != nil {
		s.writeStoreError(w, err, "用户活跃记录")
		return
	}

	// 往回多看一段:段是按采样切出来的,窗口起点之后的第一条采样没有上一条可
	// 相减,不算流量。起点往前挪一个 gap,第一段就不会被削掉开头。
	from := now.AddDate(0, 0, -activityDays).Add(-activityGap)
	segs, err := s.store.ActivitySegments(name, from, activityGap, activityMaxSegments)
	if err != nil {
		s.writeStoreError(w, err, "用户活跃记录")
		return
	}
	out := userActivity{Segments: make([]userActivitySegment, 0, len(segs)), IPs: []userActivityIP{}, UpdatedAt: sum.UpdatedAt}
	online := map[int64]bool{}
	if p := pres[name]; p != nil {
		out.Presence = *p
		for _, id := range p.OnlineNodes {
			online[id] = true
		}
	} else {
		out.Presence.OnlineNodes = []int64{}
	}
	for _, seg := range segs {
		end := seg.End
		var endPtr *time.Time
		if !(online[seg.NodeID] && now.Sub(end) < presenceOnlineWindow) {
			endPtr = &end
		}
		out.Segments = append(out.Segments, userActivitySegment{NodeID: seg.NodeID, Start: seg.Start, End: endPtr, Bytes: seg.Bytes})
	}

	if src, ok := s.nodeOps.(OnlineSnapshotSource); ok {
		for _, snap := range freshSnapshots(src.OnlineSnapshots(), now) {
			for _, ip := range snap.Users[name] {
				out.IPs = append(out.IPs, userActivityIP{IP: ip, NodeID: snap.NodeID, LastSeen: snap.At})
			}
		}
		sort.Slice(out.IPs, func(i, j int) bool {
			if out.IPs[i].IP != out.IPs[j].IP {
				return out.IPs[i].IP < out.IPs[j].IP
			}
			return out.IPs[i].NodeID < out.IPs[j].NodeID
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// freshSnapshots 只留最近一轮(两轮之内)的在线名单快照,按节点 id 排好序 ——
// 从 map 里直接取的话每次顺序都不一样,同一个请求的回答会飘。
func freshSnapshots(snaps map[int64]model.OnlineSnapshot, now time.Time) []model.OnlineSnapshot {
	out := make([]model.OnlineSnapshot, 0, len(snaps))
	for _, snap := range snaps {
		if now.Sub(snap.At) <= presenceStaleAfter {
			out = append(out, snap)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}
