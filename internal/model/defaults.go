package model

import "strconv"

// 只在配置里留空、由使用方补默认值的字段,集中在这里给默认值,
// 免得渲染、下发、CLI 三处各写一份。

func (n *Node) SSHPortOrDefault() int {
	if n.SSHPort == 0 {
		return 22
	}
	return n.SSHPort
}

func (n *Node) SSHUserOrDefault() string {
	if n.SSHUser == "" {
		return "root"
	}
	return n.SSHUser
}

func (n *Node) APIPortOrDefault() int {
	if n.APIPort == 0 {
		return 10085
	}
	return n.APIPort
}

// ConfigPathOrDefault 是节点上 xray 配置的位置。
//
// 放在 /etc/mpx 下面而不是 xray 官方的 /usr/local/etc/xray:那是别人装的核心会
// 用的路径。我们自己的核心、自己的服务、自己的配置归在同一个名字下,「哪些东西
// 是 mpx 装的」一眼能看出来,也不会和机器上原有的 xray 互相覆盖。
func (n *Node) ConfigPathOrDefault() string {
	if n.ConfigPath == "" {
		return "/etc/mpx/xray/config.json"
	}
	return n.ConfigPath
}

// ServiceNameOrDefault 是节点上跑 xray 的 systemd 服务名。
//
// 同理不叫 xray:一台机器上完全可能同时住着别人的 xray(144 和 206 上就都和
// Marzban 的那份并存),同名会互相 restart、互相顶掉。
func (n *Node) ServiceNameOrDefault() string {
	if n.ServiceName == "" {
		return "mpx-xray"
	}
	return n.ServiceName
}

// Deployed 报告这台节点有没有被下发过配置。false 表示控制面从没推过东西上去
// (刚加进来的节点、或者一直是手工维护的机器)。
func (n *Node) Deployed() bool { return n.DeployedHash != "" }

// Drift 报告「节点上的配置被人在机器上改过了」。
//
// 判据只有一个:节点上实际那份 config.json 的哈希,和控制面最后一次推上去的
// 哈希不一样。三种情况分开:
//
//   - 从没下发过(DeployedHash 为空)→ 不是漂移。没有"我们推的"这个基准,
//     就谈不上偏离它;把手工维护的机器报成漂移只会制造噪音。CLI 显示成
//     "未下发"。
//   - 节点连不上(观察值为空)→ 不是漂移,是不知道。说"有漂移"是编出来的结论。
//   - 两边都有值且不等 → 漂移。
//
// 比较的是哈希而不是文件内容:节点上那份文件控制面拿不到,而且要的也只是
// "不一样"这一个事实。
func (n *Node) Drift(s *NodeStatus) bool {
	if !n.Deployed() || s == nil || !s.Reachable {
		return false
	}
	return s.ObservedHash != n.DeployedHash
}

// ListenOrDefault 是入站绑定的地址。
func (i *Inbound) ListenOrDefault() string {
	if i.Listen == "" {
		return "0.0.0.0"
	}
	return i.Listen
}

// TagOrDefault 在用户没给 tag 时按协议和端口拼一个。
// xray 要求 inbound tag 唯一,所以这个默认值把端口也算进去了。
func (i *Inbound) TagOrDefault() string {
	if i.Tag != "" {
		return i.Tag
	}
	return string(i.Protocol) + "-" + strconv.Itoa(i.Port)
}
