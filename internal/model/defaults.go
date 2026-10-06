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

func (n *Node) ConfigPathOrDefault() string {
	if n.ConfigPath == "" {
		return "/usr/local/etc/xray/config.json"
	}
	return n.ConfigPath
}

func (n *Node) ServiceNameOrDefault() string {
	if n.ServiceName == "" {
		return "xray"
	}
	return n.ServiceName
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
