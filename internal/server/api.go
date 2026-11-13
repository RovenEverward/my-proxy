package server

// 管理接口的各域处理器分在各自的文件里:nodes.go(节点)、inbounds.go(入站)、
// hosts.go(对外入口)、users.go(用户)、settings.go(设置)、status.go(状态),
// 另有 plans.go、sources.go、routing_api.go 等按域命名的文件。
// 这一层只做三件事:解析请求、校验、调存储。任何判断逻辑都不放在这里,
// 免得 CLI 与订阅两条路径出现两套规则。
