package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Plan 是一个权限组:包含若干节点(连同这些节点上以后新加的入站)和若干入站。
//
// 与 User.InboundIDs 为空表示「全部入站」正好相反,空套餐表示什么都不能用;
// 「全部」由 AllNodes 显式表达,免得空套餐被误当成全开。见
// docs/internal/plans-rfc.md。
type Plan struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
	// AllNodes 为真表示包含全部节点(含以后新加的)。此时 NodeIDs / InboundIDs
	// 不再决定授权范围,留着只是为了以后关掉这个开关时不用重选。
	AllNodes bool `json:"all_nodes"`
	// IsDefault 表示系统自带的默认套餐:恰好一个,可编辑不可删除。
	IsDefault bool `json:"is_default"`
	// NodeIDs 是勾选的节点 id;InboundIDs 是单独勾选的入站 id。两者取并集,
	// 节点下的入站按读授权时的当前情况展开。
	NodeIDs    []int64 `json:"node_ids,omitempty"`
	InboundIDs []int64 `json:"inbound_ids,omitempty"`
	// Users 是挂在这个套餐上的用户数,存储层读取时填充,写入时忽略。
	Users int `json:"users"`
	// ExtraAuth 是挂在这个套餐、同时还有额外授权(inbound_ids 非空)的用户数。
	// 与 Users 一样只在读取时填充,用来提示「套餐的设置没有完全生效」。
	ExtraAuth int `json:"extra_auth"`
}

// 套餐字段的上限。名字与备注是给人看的短文本;节点/入站条数挡在写入口,免得
// 一次请求把关联表写爆,也免得列表页一次渲染上千个标签。
const (
	PlanNameMaxBytes = 160
	PlanNoteMaxBytes = 1000
	PlanMaxNodes     = 200
	PlanMaxInbounds  = 1000
)

// Normalize 去掉零值与重复的节点/入站 id,并按升序排列,保证同一组合在任何
// 写法下都是同一份。
func (p *Plan) Normalize() {
	p.Name = strings.TrimSpace(p.Name)
	p.Note = strings.TrimSpace(p.Note)
	p.NodeIDs = normalizePlanIDs(p.NodeIDs)
	p.InboundIDs = normalizePlanIDs(p.InboundIDs)
}

// Validate 检查套餐名、备注长度与节点/入站条数上限。引用的节点与入站是否存在
// 由存储层校验。
func (p Plan) Validate() error {
	if p.Name == "" {
		return errors.New("套餐名不能为空")
	}
	if len(p.Name) > PlanNameMaxBytes {
		return fmt.Errorf("套餐名最多 %d 字节", PlanNameMaxBytes)
	}
	if len(p.Note) > PlanNoteMaxBytes {
		return fmt.Errorf("备注最多 %d 字节", PlanNoteMaxBytes)
	}
	if len(p.NodeIDs) > PlanMaxNodes {
		return fmt.Errorf("一个套餐最多包含 %d 个节点", PlanMaxNodes)
	}
	if len(p.InboundIDs) > PlanMaxInbounds {
		return fmt.Errorf("一个套餐最多单独勾选 %d 个入站", PlanMaxInbounds)
	}
	return nil
}

// normalizePlanIDs 去重、去除非正数并升序排列。
func normalizePlanIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
