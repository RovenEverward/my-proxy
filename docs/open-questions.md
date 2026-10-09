# 缺口与待定项

按影响排序。**这一份是需要拍板的地方。**

## 1. 经跳板机连节点做不到(暂不做)

跳板机是这么一种布置:节点自己不开 ssh,得先 ssh 到一台入口机器(跳板机),再从那里连节点。常见于节点在私网里、或者运维规定只能从一台机器登录。

这种布置下 `model.Node` 的一个 `Address` 不够用了 —— 它同时在当两件事:

- 控制面 ssh 往哪连(`internal/node/ssh.go`);
- 订阅里发给客户端的地址(`internal/render/ir.go` 的 `endpoint()` → `ResolveExposure`)。

ssh 要连跳板机,而订阅要发节点自己那个客户端能到达的地址,两者不是同一个值,现在没有第二个字段可写。

**已定:暂不做。** 今天每台节点都能从控制面直连,这是给一个还没出现的问题加字段。真出现了再加:`Node` 已有 `Address` / `SSHPort` / `SSHUser` 三个连接用的字段,再加一个「ssh 目标,空则用 `Address`」不影响既有数据,也不牵动渲染。

**已经澄清的**:真实 SSH 传输验证过了(两台 Ubuntu 24.04 真机,`BatchMode=yes` 免密,控制面的 `HOME=/var/lib/mpx`,`node install` / `node sync` 都跑通)。原先「要不要加一个只连不写的探针」的问题,现在由 `node install` 的失败信息回答:连不上会直接报出来。

## 2. 单 token,没有管理员

对外多用户服务(上百至上千)只靠一个固定 token,意味着:

- 没有操作审计;
- 无法分权;
- token 轮换要改所有 CLI 的配置。

要不要引入管理员表,取决于这个控制面是否只有一个人操作。

## 3. `{USERNAME}` 只在订阅名里生效,在节点名里是空串

同一个变量名,两处行为不一致:

| 写在哪 | 结果 | 落点 |
| --- | --- | --- |
| 订阅名(`settings.profile_title`) | 展开成用户名,实测 `{USERNAME} 的订阅` → `alice 的订阅` | `internal/server/subscribe.go` |
| 节点名(`hosts.remark`) | **展开成空串** | `internal/render/ir.go:123` 写死 `"USERNAME": ""` |

**已定:撤下节点名的这段承诺,不接上。** 理由:节点名会出现在客户端的节点列表里,截图和转发都会带出去,不该写账号名。

落地的改动:CLI 帮助、`model.Host.Remark` 的注释、[model.md](model.md) 都改成只列 `{SERVER_IP}` / `{NODE_NAME}` / `{PROTOCOL}` / `{TRANSPORT}`,并写明 `{USERNAME}` 在这里被抹成空串。

**没有改渲染**:`ir.go` 里那一行照旧。去掉它的话 `{USERNAME}` 会原样留在节点名里,比抹掉更难看;抹掉至少是干净的。要按用户区分节点名,用订阅名那个字段。

## 4. 其他待定

| 项 | 现状 | 需要定什么 |
| --- | --- | --- |
| 节点用量比例 | 没有消费系数,各节点流量直接相加 | 多节点成本不同时要不要加权 |
| 用户自助改期 | 没有 | 续费是否只走 CLI |
| 入站 tag 冲突 | 未强制唯一 | 同一节点上两个入站撞 tag 会怎样 |
| `settings.rules` 顺序 | 按数组顺序匹配,顺序即优先级 | 要不要独立的优先级字段 |

> 注：原待定项中「HTML 订阅页」「Webhook 告警通知」「CSV 数据导出」「SSH ControlMaster 连接复用」已分别在批 4 与批 6 中实现并验证落地，移出待定列表。

## 5. hysteria2 与 tuic 不在 xray 支持里

批 5 最初列了这两个协议,实测 xray 26.3.27 的 `inbound.protocol` 不认 `hysteria2` /
`tuic`,配置直接被拒。它们只在 sing-box / mihomo 有。这一批只做 xray 能跑的:
shadowsocks-2022、wireguard、xhttp / splithttp / httpupgrade、mux、fragment。
要加 hy2 / tuic 就得让节点同时跑第二种协议实现,是另一个数量级的改动,先记在这里。

## 待确认项

1. [README.md](../README.md) 的「不做什么」表里,哪几条不该是不做,而是还没排在前面?
2. [model.md](model.md) 的数据模型有没有缺的对象或字段?
3. [decisions.md](decisions.md) 的三十条决策,有没有哪条的理由不成立、该换方案?
4. 第 4 节那张表里,哪些是现在就要定的?
