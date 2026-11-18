package reach

import "time"

// Protos 是一个来源对一个入站的两种协议探测结果。
type Protos struct {
	TCP *Cell `json:"tcp,omitempty"`
	UDP *Cell `json:"udp,omitempty"`
}

// Row 是一个入站两来源×两协议的明细行:页面抽屉与 CLI 明细都用它。
type Row struct {
	InboundID int64   `json:"inbound_id"`
	Label     string  `json:"label"`
	Address   string  `json:"address"`
	Port      int     `json:"port"`
	Protocol  string  `json:"protocol"`
	Network   string  `json:"network"`
	Local     *Protos `json:"local,omitempty"`
	CP        *Protos `json:"cp,omitempty"`
}

// NodeCheck 是一台节点的一次触达检查:节点级判定 + 逐入站明细。
type NodeCheck struct {
	NodeID    int64         `json:"node_id"`
	NodeName  string        `json:"node_name"`
	CheckedAt time.Time     `json:"checked_at"`
	Verdict   string        `json:"verdict"`
	Local     SourceSummary `json:"local"`
	CP        SourceSummary `json:"cp"`
	Rows      []Row         `json:"rows,omitempty"`
	// UC-12:自动剔除状态。Excluded 为真表示该节点当前被自动剔除出订阅;
	// Kept 为真表示操作者设了保留豁免(剔除对它不生效)。
	Excluded bool `json:"excluded,omitempty"`
	Kept     bool `json:"kept,omitempty"`
}

// ExtCheck 是一个外部订阅端点的触达存档(UC-12)。判定与明细同源,身份是
// render.EndpointKey;展示名沿来源名 + 端点名。
type ExtCheck struct {
	SubjectKey string         `json:"subject_key"`
	SourceID   int64          `json:"source_id"`
	SourceName string         `json:"source_name"`
	Name       string         `json:"name"`
	CheckedAt  time.Time      `json:"checked_at"`
	Verdict    string         `json:"verdict"`
	Local      SourceSummary  `json:"local"`
	CP         SourceSummary  `json:"cp"`
	Address    string         `json:"address"`
	Port       int            `json:"port"`
	Protocol   string         `json:"protocol"`
	Rows       []Row          `json:"rows,omitempty"`
	Excluded   bool           `json:"excluded,omitempty"`
	Kept       bool           `json:"kept,omitempty"`
}

// SourceCheck 是(对象 × 来源)的原始探测结果存档(UC-12):判定从它重算。
// Scope 固定为 "domestic" 或 "cp";SourceID 区分同侧不同探测端。
// Results 按 Target.Key() 归集:自建是 "inbound:<id>",外部端点是 EndpointKey。
type SourceCheck struct {
	SubjectKey string             `json:"subject_key"`
	SourceID   string             `json:"source_id"`
	Scope      string             `json:"scope"` // domestic / cp
	CheckedAt  time.Time          `json:"checked_at"`
	Summary    SourceSummary      `json:"summary"`
	Results    map[string]*Result `json:"results,omitempty"`
}

const (
	ScopeDomestic = "domestic"
	ScopeCP       = "cp"
)

// FoldSources 把同一侧(domestic 或 cp)的多个来源摘要合并成一个:
// 任一可达记可达、任一确定不通记不通;取最快可达时延。过期判定不进折叠,
// 调用方先按新鲜度过滤。
func FoldSources(sums []SourceSummary) SourceSummary {
	out := SourceSummary{}
	for _, s := range sums {
		if s.Probed {
			out.Probed = true
		}
		if s.Reachable {
			out.Reachable = true
			if out.LatencyMs == 0 || s.LatencyMs < out.LatencyMs {
				out.LatencyMs = s.LatencyMs
			}
		}
		if s.Failed {
			out.Failed = true
		}
	}
	return out
}

// SubjectRow 是对象级存档里一行的两来源明细:自建是入站级,外部端点就是端点级。
type SubjectRow struct {
	Row
	TID string `json:"tid,omitempty"`
}

// MergeKeyed 把两个侧的探测结果按 Target.Key() 合成明细行。
// 与 Merge 的区别只在归并键:外部端点的 InboundID 都是零值,要靠 TID 对位。
func MergeKeyed(targets []Target, domestic, cp map[string]*Result) (rows []Row) {
	rows = make([]Row, 0, len(targets))
	for _, t := range targets {
		row := Row{InboundID: t.InboundID, Label: t.Label, Address: t.Address, Port: t.Port, Protocol: t.Protocol, Network: t.Network}
		if r := domestic[t.Key()]; r != nil {
			row.Local = &Protos{TCP: r.TCP, UDP: r.UDP}
		}
		if r := cp[t.Key()]; r != nil {
			row.CP = &Protos{TCP: r.TCP, UDP: r.UDP}
		}
		rows = append(rows, row)
	}
	return rows
}

// SummarizeKeyed 与 Summarize 同口径,按 Target.Key() 收结果。
func SummarizeKeyed(targets []Target, results map[string]*Result) SourceSummary {
	s := SourceSummary{}
	for _, t := range targets {
		r := results[t.Key()]
		if r == nil {
			continue
		}
		cell := r.TCP
		if t.udp() {
			cell = r.UDP
		}
		if cell == nil {
			continue
		}
		s.Probed = true
		switch cell.Status {
		case CellReachable:
			s.Reachable = true
			if s.LatencyMs == 0 || cell.LatencyMs < s.LatencyMs {
				s.LatencyMs = cell.LatencyMs
			}
		case CellTimeout, CellRefused, CellUnreachable:
			s.Failed = true
		}
	}
	return s
}

// Merge 把两来源的探测结果按入站合并成明细行,顺带算出节点级判定。
func Merge(targets []Target, local, cp map[int64]*Result) (rows []Row, lv, cv SourceSummary) {
	rows = make([]Row, 0, len(targets))
	for _, t := range targets {
		row := Row{InboundID: t.InboundID, Label: t.Label, Address: t.Address, Port: t.Port, Protocol: t.Protocol, Network: t.Network}
		if r := local[t.InboundID]; r != nil {
			row.Local = &Protos{TCP: r.TCP, UDP: r.UDP}
		}
		if r := cp[t.InboundID]; r != nil {
			row.CP = &Protos{TCP: r.TCP, UDP: r.UDP}
		}
		rows = append(rows, row)
	}
	return rows, Summarize(targets, local), Summarize(targets, cp)
}
