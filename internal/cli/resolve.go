package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/RovenEverward/my-proxy/internal/model"
)

// 用名字指定对象(docs/internal/node-naming-rfc.md §8)。
//
// 规则按顺序试,某一步恰好命中一个就停;命中多个就列出候选报错,绝不猜:
//  1. 纯数字 → id(兼容旧写法与脚本)。想按名字找一个叫 "1" 的节点写 name:1。
//  2. 与名字完全相等(不区分大小写)。
//  3. 与展示名的规范形式完全相等(去国旗、空白、`·`,见 model.LabelKey)。
//  4. 名字或展示名的唯一前缀(同样规范化后比较)。

// candidate 是一个可被指定的对象。Display 为空表示没有展示名。
type candidate struct {
	ID      int64
	Name    string
	Display string
}

func (c candidate) label() string {
	if c.Display != "" && c.Display != c.Name {
		return fmt.Sprintf("%s(%s)", c.Name, c.Display)
	}
	return c.Name
}

// pick 在 cs 里按上面的规则找 arg。kind 只用于报错文案(「节点」「入站」)。
// prefixed 报告是不是靠前缀命中的:写操作据此先回显解析结果。
func pick(kind, arg string, cs []candidate) (c candidate, prefixed bool, err error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return c, false, fmt.Errorf("没给%s", kind)
	}
	forceName := strings.HasPrefix(arg, "name:")
	if forceName {
		arg = strings.TrimPrefix(arg, "name:")
	} else if id, e := strconv.ParseInt(arg, 10, 64); e == nil {
		for _, x := range cs {
			if x.ID == id {
				return x, false, nil
			}
		}
		return c, false, fmt.Errorf("没有 id 为 %d 的%s", id, kind)
	}

	key := model.LabelKey(arg)
	steps := []func(candidate) bool{
		func(x candidate) bool { return strings.EqualFold(x.Name, arg) },
		func(x candidate) bool { return x.Display != "" && model.LabelKey(x.Display) == key },
		func(x candidate) bool {
			return key != "" && (strings.HasPrefix(model.LabelKey(x.Name), key) || (x.Display != "" && strings.HasPrefix(model.LabelKey(x.Display), key)))
		},
	}
	for i, match := range steps {
		var hit []candidate
		for _, x := range cs {
			if match(x) {
				hit = append(hit, x)
			}
		}
		switch {
		case len(hit) == 1:
			return hit[0], i == 2, nil
		case len(hit) > 1:
			return c, false, ambiguous(kind, arg, hit)
		}
	}
	return c, false, fmt.Errorf("没有叫 %q 的%s(按 id、名字、展示名或唯一前缀都没找到)", arg, kind)
}

func ambiguous(kind, arg string, hit []candidate) error {
	sort.Slice(hit, func(i, j int) bool { return hit[i].ID < hit[j].ID })
	var b strings.Builder
	fmt.Fprintf(&b, "「%s」匹配到 %d 个%s,请写得更具体:", arg, len(hit), kind)
	for _, x := range hit {
		fmt.Fprintf(&b, "\n  %-4d %s", x.ID, x.label())
	}
	return errors.New(b.String())
}

func nodeCandidates(vs []model.NodeView) []candidate {
	cs := make([]candidate, 0, len(vs))
	for _, v := range vs {
		cs = append(cs, candidate{ID: v.Node.ID, Name: v.Node.Name, Display: v.Display})
	}
	return cs
}

// resolveNode 把一个节点参数解析成节点。写操作(echo=true)靠前缀命中时,先在 stderr
// 回显解析结果,免得「以为是 A、其实是 B」。
func resolveNode(c *Client, arg string, echo bool) (model.Node, error) {
	all, err := listNodes(c)
	if err != nil {
		return model.Node{}, err
	}
	x, prefixed, err := pick("节点", arg, nodeCandidates(all))
	if err != nil {
		return model.Node{}, err
	}
	if prefixed && echo {
		fmt.Fprintf(os.Stderr, "→ 节点 %d %s\n", x.ID, x.label())
	}
	for _, v := range all {
		if v.Node.ID == x.ID {
			return v.Node, nil
		}
	}
	return model.Node{}, fmt.Errorf("没有 id 为 %d 的节点", x.ID)
}

// resolveNodeID 是 resolveNode 只要 id 的简写。
func resolveNodeID(arg string, echo bool) (int64, error) {
	if id, e := strconv.ParseInt(strings.TrimSpace(arg), 10, 64); e == nil {
		return id, nil // 纯数字不必先拉列表:id 由控制面去认
	}
	c, err := newClient()
	if err != nil {
		return 0, err
	}
	n, err := resolveNode(c, arg, echo)
	return n.ID, err
}

func listInbounds(c *Client) ([]model.Inbound, error) {
	raw, err := c.do("GET", "/api/inbounds", nil)
	if err != nil {
		return nil, err
	}
	return decode[[]model.Inbound](raw)
}

// resolveInboundID 解析入站参数:id、`节点/tag`,或全局唯一的 tag。
// tag 部分在节点内按「不区分大小写 → 唯一前缀」匹配;入站没设 tag 时用 协议-端口。
func resolveInboundID(arg string, echo bool) (int64, error) {
	arg = strings.TrimSpace(arg)
	if id, e := strconv.ParseInt(arg, 10, 64); e == nil {
		return id, nil
	}
	c, err := newClient()
	if err != nil {
		return 0, err
	}
	ins, err := listInbounds(c)
	if err != nil {
		return 0, err
	}
	nodeArg, tagArg, scoped := strings.Cut(arg, "/")
	var nodes []model.NodeView
	if scoped {
		if nodes, err = listNodes(c); err != nil {
			return 0, err
		}
	} else {
		tagArg = arg
	}
	names := map[int64]string{}
	var nodeID int64
	if scoped {
		n, _, err := pick("节点", nodeArg, nodeCandidates(nodes))
		if err != nil {
			return 0, err
		}
		nodeID = n.ID
		for _, v := range nodes {
			names[v.Node.ID] = v.Node.Name
		}
	}
	var cs []candidate
	for i := range ins {
		in := &ins[i]
		if scoped && in.NodeID != nodeID {
			continue
		}
		cs = append(cs, candidate{ID: in.ID, Name: in.TagOrDefault()})
	}
	x, prefixed, err := pick("入站", "name:"+tagArg, cs)
	if err != nil {
		return 0, err
	}
	if prefixed && echo {
		where := ""
		if scoped {
			where = names[nodeID] + "/"
		}
		fmt.Fprintf(os.Stderr, "→ 入站 %d %s%s\n", x.ID, where, x.Name)
	}
	return x.ID, nil
}

// refFlag 让 --node / --inbound 这类 flag 也收名字:解析在 flag 赋值时完成,
// 之后代码照旧读 int64。纯数字不发请求。
type refFlag struct {
	p       *int64
	kind    string
	resolve func(string, bool) (int64, error)
}

func (f refFlag) String() string {
	if f.p == nil || *f.p == 0 {
		return ""
	}
	return strconv.FormatInt(*f.p, 10)
}
func (f refFlag) Type() string { return f.kind }
func (f refFlag) Set(s string) error {
	if s == "" || s == "0" { // 默认值:没给
		*f.p = 0
		return nil
	}
	id, err := f.resolve(s, false)
	if err != nil {
		return err
	}
	*f.p = id
	return nil
}

func nodeFlag(p *int64) refFlag    { return refFlag{p, "节点", resolveNodeID} }
func inboundFlag(p *int64) refFlag { return refFlag{p, "入站", resolveInboundID} }
