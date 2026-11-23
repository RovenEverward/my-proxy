---
kind: part
parent: README.md
code: [internal/control/, internal/node/, internal/render/xray.go, internal/render/serverconfig.go, internal/render/singbox_server.go, internal/xrayrelease/, internal/singboxbuild/]
verified: 2026-11-23 @ 3c427e3
---
# 节点：下发、收敛与巡检

> **30 秒版**：节点是真正承载流量的服务器，上面只有 xray（可选再加一个 sing-box 承载 Hysteria2 / TUIC），不装任何 MPX 组件。控制面经系统自带的 `ssh` 把算好的配置推过去，每 30 秒一轮按人增删、按人读流量，每 5 分钟巡检一次有没有被人改过。

## 边界

负责：装核心与升级核心、渲染并下发节点配置、周期收敛用户名单、采样流量、巡检与告警、不通节点的退避。升级 xray 时由节点自己从 GitHub 拉官方包，控制面只递 URL 和取自官方 `.dgst` 的摘要，新核心先过 `xray run -test` 再顶替、失败回滚（v0.8.0 起，编排见 [安装与升级](install-update.md)）。

不负责：决定谁该在哪（读 [用户、套餐与计量](users.md) 的结果）；ACME 证书签发与轮换（QUIC 入站的自签证书由控制面在下发时签发，见 UC-09；非 QUIC 的 TLS 证书由操作者自备）；自己编译 xray（只推官方发布包；sing-box 用官方源码带统计标签在控制面编）。

## 怎么运转

两条路按改动性质分开走：

| 改动 | 走哪条路 | 会不会断别人的连接 |
|---|---|---|
| 结构：端口、入站、证书、出口策略、订阅镜像 | `mpx node sync`：全量渲染 → 备份 → 写入 → `xray -test` → 不过就回滚 → 重启 → 起不来再回滚 | 会，重启 |
| 名单：加人、摘人、超限、续期 | 后台循环第 3 步，调 xray API 的 `adu` / `rmu` | 不会 |

后台每一轮的顺序是有意的：先**采样**（读累计值，减去 `traffic_samples` 里的上次值），再**重置**到期周期，最后**收敛**名单（[internal/control/loop.go](../../internal/control/loop.go)）。每 10 轮顺路**巡检**：一条 ssh 读回服务状态、核心版本、配置哈希、证书剩余天数，和「最后一次下发记下的哈希」比对发现漂移；整轮 45 秒预算，第一轮就跑。完整走查和真实输出见 [mpx 是怎么搭起来的](../internal/index.md)。

SSH 和核心安装在 [internal/node]。

## 为什么这样设计

节点零维护：不用升级组件、不用守护进程、不用给它签证书。

### 订阅镜像(UC-10)

名单内一台节点可同时是订阅入口:`sub_mirrors` 登记「节点 + 端口 + 上游」,渲染时在该节点的 xray 配置里追加一个 `dokodemo-door` 入站,把公开端口的 TCP 转给控制面。镜像只在主核心 xray 上渲染 —— sing-box 是第二核心,同机同端口只能绑一边;纯 QUIC 节点有镜像时强制下发 xray。镜像不进数据通路、不持真相、不参与计量;内容与鉴权以控制面为唯一来源。加/删/改上游都是结构性变更,`mpx mirror add|rm|set` 默认走 `DeployNode` 下发(会重启核心),`--sync=false` 只登记。上游指到控制面的 `--sub-addr` 专用口时,`/api` 不会出现在镜像端口。

### TLS 证书:托管下发与自签兜底(UC-09 / UC-27)

QUIC 入站(hysteria2 / tuic)走 sing-box,它的 `sing-box check` 会打开 `certificate_path` / `key_path`,缺文件的 `node sync` 死在校验而不是起不来。所以下发前先 `node.EnsureTLSCertificates`,一条 ssh 把每对 cert/key 的「缺失 / 在位 / 内容哈希」一次报回,再按材料来源分两条路:

- **托管证书(UC-27)**:入站带了 `cert_pem`/`key_pem`(`inbounds` 表同名列,`json:"-"` 待遇同 `reality_priv`,只经 `PUT /api/inbounds/{id}/cert` 写入,响应只回 `cert` 展示信息)时,以库里的 PEM 为准按 sha256 比对,不一致或缺失就覆盖 —— 证书换过要重启核心才生效,而证书变化不进 config.json 哈希,所以 EnsureTLS 返回「动了托管材料」时 deploySingBox 不再走「hash 相同跳过」,照常走 stop/start。
- **自签兜底(UC-09)**:没带 PEM 的入站回到旧语义 —— 缺的由控制面 `model.GenerateSelfSignedCertificate` 按 SNI 签一份 RSA 2048 自签(10 年,CN=SNI),同 SNI 多条入站共用一对;两文件都在的一对一字节不动,半对整对重签。补自签不重载服务:进程内存里已有证书,客户端反正跳过校验。

写文件经 `PutFileMode` 落 `/etc/mpx/sing-box/<tag>-cert.pem`(644)/`-key.pem`(600),先 .tmp 再 mv。探活放在「hash 相同要跳过」的比对**之前**:有人在节点上删了证书而配置没变时,光对哈希会判「不用发」,证书永远补不回来。

`inbound add/edit` 里 QUIC + TLS 的 `--cert-file`/`--key-file` 可以省略,服务端按 tag 分配默认路径;非 QUIC 的 TLS(vless+tls 这类 CDN 入口)仍必填,证书归 CDN 侧管。`node status` 的「证书最短」列读这些路径的到期时间。续期流水线留在 mpx 之外:acme.sh cron 续期后钩子把新 PEM 走 `mpx inbound cert set` + `mpx node sync` 推上去,同一个入口覆盖「换证书」与「换节点」。

挂托管证书的 QUIC 入站,SNI 必须换成证书 SAN 覆盖的域名(校验只看 SNI),但对外入口地址保持回落节点 IP:`Host.Address` 一旦偏离节点地址,这个入口就是独立对象 `host:<id>` —— CDN IP 优选按 `host:` 对象逐个选路,hy2 这种节点本体的入站不要这样做。

## 坑

- sing-box 下发的"hash 相同就跳过"只信「最后一次下发成功」的基准:失败的 apply(mv/start 失败)会把候选配置留在磁盘、服务留在停机,基准已脱节 —— 此时 `deploySingBox` 经 `StaleDeployError` 清掉 `DeployedHash`。而 `node sync`(verifyRemote)会额外对一次节点上的 `sha256sum`,磁盘对不上就重发;周期收敛(无 verify)仍是零 ssh 快路径。
- 订阅镜像只在主核心 xray 渲染:sing-box 是第二核心,同机同端口只能绑一边,两边都写会撞绑让 sing-box 起不来(实测踩过)。纯 QUIC 节点有镜像时强制下发 xray。
- `sub_url_prefix` 只是"客户端看到的地址",可能指到迁移前的死机;镜像上游推导以本次请求的 Host 为准,前缀只兜底。

- 名单变化 → sing-box 承载的 Hysteria2 / TUIC 会重启该节点全部 QUIC 连接；xray 摘人后已建立的 TCP 连接可能继续传 → 这是已接受的限制，。
- 读数比上次小 → 说明节点上的 xray 重启过 → 记异常日志，不当成负用量。
- ssh 连接复用的 socket 路径太长 → 超过 unix socket 长度上限直接失败 → 路径必须短。
- 直连出口没设 `domainStrategy` → 双栈网站会优先走 IPv6，VPS 的 v6 段常被限流 → v0.7.3 起默认 `UseIPv4v6`，节点可用 `outbound_ip` 覆盖。
- 双栈节点「部分站点不通」：先查 IPv6 默认路由与网关（`ip -6 route`、ping6 网关），AAAA 域名被 glibc 优选 v6 而 v6 段死路时表现为整组站点超时；处理 = `mpx node edit N --outbound-ip v4` + `node sync`（xray freedom 改 UseIPv4），如需裸进程也优先 v4 再开节点 `/etc/gai.conf` 的 `precedence ::ffff:0:0/96 100`。`netplan apply` 会断 SSH 属正常；上游 v6 恢复后改回 `v4v6`。
- 节点不通 → 每轮重试会拖慢整轮 → 按退避跳过：短期 30s 起翻倍封顶 5 分钟；持续不通一小时后放缓到每小时一探。修好了想立刻回来 → `mpx node retry <节点>` 或 `POST /api/nodes/{id}/backoff/retry` 当场跑一轮采样与收敛；`GET /api/nodes/{id}?fresh=1` 现场确认可达也会清退避。
