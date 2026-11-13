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
	NodeID    int64     `json:"node_id"`
	NodeName  string    `json:"node_name"`
	CheckedAt time.Time `json:"checked_at"`
	Verdict   string    `json:"verdict"`
	Local     SourceSummary `json:"local"`
	CP        SourceSummary `json:"cp"`
	Rows      []Row     `json:"rows,omitempty"`
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
