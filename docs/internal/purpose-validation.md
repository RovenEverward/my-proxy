# 用途订阅验证记录

本记录覆盖用途订阅首批实现及路由审查修复。客户端使用官方 Darwin arm64 二进制：Mihomo v1.19.32（Go 1.26.8）与 sing-box v1.14.3（revision `7054cac5124465ffdb145870d7c01885566711b1`）。

## 配置解析与回归

全仓库 `go build ./...`、`go vet ./...`、`go test ./... -count=1 -race` 通过。路由审查修复后重新执行 render/server race 与 vet；最后的初始预处理块位置修复也通过 render/server race。

通过环境变量 `MPX_TEST_MIHOMO` / `MPX_TEST_SINGBOX` 指向官方二进制，执行：

```bash
go test ./internal/render ./internal/server -count=1 -race
```

共 **19 个官方解析检查**：daily / media / download / game × Mihomo / sing-box × 默认 / 自定义底稿，共 16 个；额外 3 个覆盖 Clash SUB-RULE 与嵌套直连/拦截组、sing-box 嵌套默认组与自定义 direct final。非日常配置实际叠加独立日常候选组。

回归断言同时覆盖：默认直连/拦截 selector 的目的地保留；默认选择递归解析与循环保留；Clash SUB-RULE 引用保留并改写子规则实际代理目标；sing-box direct/block final 保留；用途规则在初始连续 sniff / hijack-dns / resolve 后、首个普通路由前插入，后面的 DNS 规则不影响用途优先级。

## 官方客户端真实请求

本地临时 Go spike 调用相同 `PurposeSubscription` / `AddPurposeRouting` 渲染器，生成两个 VLESS TCP endpoint。两个本地模拟 VLESS 出口解析真实请求的目的地址，分别返回 `DAILY:<domain>` 或 `MEDIA:<domain>` HTTP 标记。官方客户端使用这些真实生成节点，通过本地 SOCKS 接收请求；直连验证另用本地 HTTP 服务返回 `DIRECT`。

Mihomo 与 sing-box，各默认/自定义底稿，四种运行场景全部通过。每种场景执行以下五项，共 **20 个真实 SOCKS 请求**：

| 请求 | 默认 Mihomo | 自定义 Mihomo | 默认 sing-box | 自定义 sing-box |
| --- | --- | --- | --- | --- |
| media.test | MEDIA:media.test | MEDIA:media.test | MEDIA:media.test | MEDIA:media.test |
| video.media.test | MEDIA:video.media.test | MEDIA:video.media.test | MEDIA:video.media.test | MEDIA:video.media.test |
| ordinary.test | DAILY:ordinary.test | DAILY:ordinary.test | DAILY:ordinary.test | DAILY:ordinary.test |
| localhost 私有目标 | DIRECT | DIRECT | DIRECT | DIRECT |
| ads.test | 拦截 | 拦截 | 拦截 | 拦截 |

自定义 sing-box 还包含初始 sniff、匹配 media.test 的旧 proxy 路由与后置 DNS 劫持规则，真实请求仍命中媒体出口，证明用途规则位置修复有效。出口接收记录中仅普通测试域进入 daily，仅媒体测试域进入 media；私有与广告测试域均未进入这两个出口。所有四个客户端子进程在场景结束后终止并 `Wait` 回收，本地监听器关闭。

## 验证范围

默认模板的公网规则数据替换成同名 inline fixture：private 匹配 localhost，reject / category-ads-all 匹配 ads.test，其余规则集使用不匹配测试域；Mihomo GEOIP 改成不匹配的保留相同目的组的 IP-CIDR fixture。目的组、候选节点和用途路由来自实际渲染配置。运行设施使用回环 SOCKS，sing-box TUN 替换为 SOCKS，DNS 使用本地配置；不依赖公网规则下载或 TUN 权限。客户端默认自动探测的请求也由本地模拟出口接收，未转发公网。

因此，这些结果证明用途分流和默认/自定义模板直连、拦截行为的真实 TCP 请求路径；未覆盖真实流媒体解锁、生产公网规则数据、TUN 全链路、TLS/UDP、下载吞吐或游戏 RTT。临时 spike 与运行配置留在忽略的本地目录，不提交；配置解析与行为回归测试已提交。
