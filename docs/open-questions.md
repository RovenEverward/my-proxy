# 缺口与待定项

按影响排序。**这一份是需要拍板的地方。**

## 1. 客户端分流规则不可管理

立项时用户的原话是「操作订阅的 Clash 以及 SingBox 的规则」。当前实现里,「规则」只有一层意思:**User-Agent → 返回哪种格式**。

客户端配置内部的分流规则(哪些域名走代理、哪些直连)是**写死在模板里**的:

- `settings` 表没有模板字段;
- `render.Clash(eps, meta)` **不接受模板参数**,规则完全写死在代码里;
- `render.SingBox(eps, nil)` 与 `render.Xray(eps, nil)` 的调用处一律传 `nil`,永远用内嵌的默认模板。

**要做的话大致是这个形状**(需要定):

1. `settings` 加「每个格式一份模板」;
2. CLI 能把模板取下来、改、传回去;
3. 模板里定义若干可被工具替换的位置(节点列表、规则列表、规则集地址);
4. 模板缺失时回落内置默认。

**待决策**:这是不是立项时要的「规则」?是就继续做;不是就把 [status.md](status.md) 对照表里那一行改成「显式不做」。

## 2. 经跳板机连节点做不到(暂不做)

跳板机是这么一种布置:节点自己不开 ssh,得先 ssh 到一台入口机器(跳板机),再从那里连节点。常见于节点在私网里、或者运维规定只能从一台机器登录。

这种布置下 `model.Node` 的一个 `Address` 不够用了 —— 它同时在当两件事:

- 控制面 ssh 往哪连(`internal/node/ssh.go`);
- 订阅里发给客户端的地址(`internal/render/ir.go` 的 `endpoint()` → `ResolveExposure`)。

ssh 要连跳板机,而订阅要发节点自己那个客户端能到达的地址,两者不是同一个值,现在没有第二个字段可写。

**已定:暂不做。** 今天每台节点都能从控制面直连,这是给一个还没出现的问题加字段。真出现了再加:`Node` 已有 `Address` / `SSHPort` / `SSHUser` 三个连接用的字段,再加一个「ssh 目标,空则用 `Address`」不影响既有数据,也不牵动渲染。

**已经澄清的**:真实 SSH 传输验证过了(两台 Ubuntu 24.04 真机,`BatchMode=yes` 免密,控制面的 `HOME=/var/lib/mpx`,`node install` / `node sync` 都跑通)。原先「要不要加一个只连不写的探针」的问题,现在由 `node install` 的失败信息回答:连不上会直接报出来。

## 3. 单 token,没有管理员

对外多用户服务(上百至上千)只靠一个固定 token,意味着:

- 没有操作审计;
- 无法分权;
- token 轮换要改所有 CLI 的配置。

要不要引入管理员表,取决于这个控制面是否只有一个人操作。

## 4. `{USERNAME}` 只在订阅名里生效,在节点名里是空串

同一个变量名,两处行为不一致:

| 写在哪 | 结果 | 落点 |
| --- | --- | --- |
| 订阅名(`settings.profile_title`) | 展开成用户名,实测 `{USERNAME} 的订阅` → `alice 的订阅` | `internal/server/subscribe.go` |
| 节点名(`hosts.remark`) | **展开成空串** | `internal/render/ir.go:123` 写死 `"USERNAME": ""` |

**已定:撤下节点名的这段承诺,不接上。** 理由:节点名会出现在客户端的节点列表里,截图和转发都会带出去,不该写账号名。

落地的改动:CLI 帮助、`model.Host.Remark` 的注释、[model.md](model.md) 都改成只列 `{SERVER_IP}` / `{NODE_NAME}` / `{PROTOCOL}` / `{TRANSPORT}`,并写明 `{USERNAME}` 在这里被抹成空串。

**没有改渲染**:`ir.go` 里那一行照旧。去掉它的话 `{USERNAME}` 会原样留在节点名里,比抹掉更难看;抹掉至少是干净的。要按用户区分节点名,用订阅名那个字段。

## 5. HTTP 建用户的 `enabled` 零值(已修)

原来的样子:`handleCreateUser` 直接 `decodeBody` 进 `model.User`,`Enabled` 是 Go 的零值 `false`:

```console
$ curl -X POST .../api/users -d '{"username":"dave"}'
{ "username": "dave", "enabled": false, "status": "disabled", ... }
```

而 `mpx user add` 不写 `--disabled` 就是启用。同一个字段两套默认值,而且不报错 —— 手工调 API 漏掉一个布尔字段就得到一批连不上的用户。

**已修:请求体上的 `enabled` 读成 `*bool`,分得清「没说」和「说了 false」。** 「没说」时建用户按启用(和 CLI 对齐)、改用户按保持原样(只想改流量上限的 PUT 不会把停用的人悄悄启用)。

落在 `internal/server/validate.go` 的 `userRequest`:嵌入 `model.User`,外层覆写 `Enabled *bool`,靠 JSON 同名取浅的那个遮住内层字段。这样三态只存在于真正需要它的地方(HTTP 请求体),`model.User.Enabled` 仍是裸 bool,存储、渲染和十几处测试一个字没动。

另外两条没采纳:

- 把 `model.User.Enabled` 整个换成 `*bool` —— 会把三态扩散到领域模型和所有构造点,而只有请求体需要它;
- 要求请求必须带 `enabled`,不带就 400 —— 调用方多写一个字段,而「新建默认启用」本来就是更合理的默认。

## 6. 其他待定

| 项 | 现状 | 需要定什么 |
| --- | --- | --- |
| 订阅页 | 只返回配置,**没有** HTML 订阅页 | 要不要给一个能看到用量和到期日的页面 |
| 节点用量比例 | 没有消费系数,各节点流量直接相加 | 多节点成本不同时要不要加权 |
| 节点容错 | 单节点失败只记日志,不影响别的节点 | 连续失败要不要告警或摘除 |
| 数据导出 | 没有 | 备份/迁移是否只靠拷 SQLite 文件 |
| 用户自助改期 | 没有 | 续费是否只走 CLI |
| 入站 tag 冲突 | 未强制唯一 | 同一节点上两个入站撞 tag 会怎样 |
| 每条远端命令各付一次 ssh 握手 | 没有开连接复用,实测一次 `node sync` 是 6 次 ssh(约 11 秒)、一次 `node install` 是 14 次(约 25 秒),大头是握手不是传输 | 要不要开 `ControlMaster` / `ControlPersist`;代价是多一份连接状态要管(套接字路径、残留、和后台任务并发共用) |
| `settings.rules` 顺序 | 按数组顺序匹配,顺序即优先级 | 要不要独立的优先级字段 |

## 待确认项

1. **第 1 节**:分流规则是不是立项时要的「规则」?
2. [README.md](../README.md) 的「不做什么」表里,哪几条不该是不做,而是还没排在前面?
3. [model.md](model.md) 的数据模型有没有缺的对象或字段?
4. [decisions.md](decisions.md) 的十五条决策,有没有哪条的理由不成立、该换方案?
5. 第 6 节那张表里,哪些是现在就要定的?
