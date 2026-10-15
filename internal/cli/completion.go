package cli

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RovenEverward/my-proxy/internal/model"
	"github.com/spf13/cobra"
)

// shell 补全(docs/internal/node-naming-rfc.md §8)。
//
// 按命令的 Use 认参数是什么:`<节点>`/`[节点]`、`<入站>`、`<username>`;--node 与
// --inbound 两个 flag 也补。补进命令行的是 ASCII 的名字,展示名放在描述里(zsh/fish
// 会显示),免得往命令行里插 emoji 和引号。
//
// 拉列表限时 1 秒,失败就静默不补:Tab 卡住比补不出来更糟。

const completeTimeout = time.Second

type completeFunc = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)

// wireCompletion 给命令树挂上补全。Execute 调一次;重复调用无害。
func wireCompletion(root *cobra.Command) {
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if f := argCompleter(c.Use); f != nil && c.ValidArgsFunction == nil {
			c.ValidArgsFunction = f
		}
		for name, f := range map[string]completeFunc{"node": completeNodeArg, "inbound": completeInboundArg} {
			if c.LocalFlags().Lookup(name) != nil {
				_ = c.RegisterFlagCompletionFunc(name, f)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// argCompleter 按 Use 里的参数占位符选补全函数。
func argCompleter(use string) completeFunc {
	fields := strings.Fields(use)
	if len(fields) < 2 {
		return nil
	}
	var per []completeFunc
	repeat := false
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			break
		}
		switch strings.Trim(strings.TrimSuffix(f, "..."), "<>[]") {
		case "节点":
			per = append(per, completeNodeArg)
		case "入站":
			per = append(per, completeInboundArg)
		case "username":
			per = append(per, completeUserArg)
		case "xray|sing-box":
			per = append(per, staticComplete("xray", "sing-box"))
		default:
			per = append(per, nil)
		}
		repeat = strings.HasSuffix(f, "...")
	}
	if len(per) == 0 {
		return nil
	}
	return func(cmd *cobra.Command, args []string, to string) ([]string, cobra.ShellCompDirective) {
		i := len(args)
		if i >= len(per) {
			if !repeat {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			i = len(per) - 1
		}
		if per[i] == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return per[i](cmd, args, to)
	}
}

func staticComplete(vals ...string) completeFunc {
	return func(_ *cobra.Command, _ []string, to string) ([]string, cobra.ShellCompDirective) {
		return withPrefix(vals, to), cobra.ShellCompDirectiveNoFileComp
	}
}

// withPrefix 留下以 to 开头(不区分大小写)的候选;候选里 Tab 之后是描述。
func withPrefix(vals []string, to string) []string {
	var out []string
	for _, v := range vals {
		name, _, _ := strings.Cut(v, "\t")
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(to)) {
			out = append(out, v)
		}
	}
	return out
}

func completeClient() *Client {
	c, err := newClient()
	if err != nil {
		return nil
	}
	return c
}

func completeNodeArg(_ *cobra.Command, _ []string, to string) ([]string, cobra.ShellCompDirective) {
	c := completeClient()
	if c == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	raw, err := c.doWithin(completeTimeout, "GET", "/api/nodes", nil)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	vs, err := decode[[]model.NodeView](raw)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var vals []string
	for _, cand := range nodeCandidates(vs) {
		desc := cand.Display
		if desc == "" {
			desc = "id " + strconv.FormatInt(cand.ID, 10)
		}
		vals = append(vals, cand.Name+"\t"+desc)
	}
	return withPrefix(vals, to), cobra.ShellCompDirectiveNoFileComp
}

func completeInboundArg(_ *cobra.Command, _ []string, to string) ([]string, cobra.ShellCompDirective) {
	c := completeClient()
	if c == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	rawN, err1 := c.doWithin(completeTimeout, "GET", "/api/nodes", nil)
	rawI, err2 := c.doWithin(completeTimeout, "GET", "/api/inbounds", nil)
	if err1 != nil || err2 != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	vs, err1 := decode[[]model.NodeView](rawN)
	ins, err2 := decode[[]model.Inbound](rawI)
	if err1 != nil || err2 != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := map[int64]string{}
	for _, v := range vs {
		names[v.Node.ID] = v.Node.Name
	}
	var vals []string
	for i := range ins {
		in := &ins[i]
		vals = append(vals, names[in.NodeID]+"/"+in.TagOrDefault()+"\t"+string(in.Protocol)+" :"+strconv.Itoa(in.Port)+" · id "+strconv.FormatInt(in.ID, 10))
	}
	return withPrefix(vals, to), cobra.ShellCompDirectiveNoFileComp
}

func completeUserArg(_ *cobra.Command, _ []string, to string) ([]string, cobra.ShellCompDirective) {
	c := completeClient()
	if c == nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	q := url.Values{"limit": {"50"}}
	if to != "" {
		q.Set("search", to)
	}
	raw, err := c.doWithin(completeTimeout, "GET", "/api/users?"+q.Encode(), nil)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	page, err := decode[userPage](raw)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var vals []string
	for _, u := range page.Users {
		vals = append(vals, u.Username)
	}
	return withPrefix(vals, to), cobra.ShellCompDirectiveNoFileComp
}
