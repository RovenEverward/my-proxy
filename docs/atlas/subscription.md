---
kind: part
parent: README.md
code: [internal/server/subscribe.go, internal/server/subcache.go, internal/server/routing_api.go, internal/render/clash.go, internal/render/singbox.go, internal/render/links.go, internal/render/routing.go, internal/model/routing.go]
verified: 2026-10-17 @ 36a3f1f
---
# 订阅与分流

> **30 秒版**：每个用户有一条订阅地址 `/sub/{token}`。同一条地址，Clash Meta、sing-box、v2rayN 各自拿到认识的格式，浏览器打开是用量页。Clash Meta 和 sing-box 的分流部分由一张从上往下匹配的规则表生成：每条规则指向直连、拦截或一个策略组，策略组在渲染时按条件收入口。

## 边界

负责：认格式、渲染六种订阅格式和用量页、响应头里的用量信息、规则表与策略组、订阅缓存、排水和容量上限。

不负责：节点上的配置（[节点](nodes.md)，代码也不共用）；用户能不能用、能用哪些入站（读 [用户、套餐与计量](users.md) 的结果）。

## 怎么运转

1. 认格式按顺序：路径里点名的格式 → User-Agent 逐条匹配订阅规则，取第一条命中 → 都没命中退回分享链接。正则编译不过的规则跳过，不让整个订阅报错。
2. 渲染先把用户能用的入站和入口整理成中间表示，再按格式输出（[internal/render](../../internal/render/)）。不可用的用户输出占位节点，名字写明已停用、已过期还是已超限。
3. 分流：全局规则表加按用户追加的规则（只能加在全局前面、改「其他全部」，不能删改全局规则）；去向只有策略组、`DIRECT`、`REJECT`；内置规则组指向上游规则集，由客户端自己下载。策略组的筛选条件来自三处：节点和入口的地区元数据（`mpx node edit --region` 或自动识别）、入站协议、[能力检测](diagnostics.md)留下的结果（比如出口在美国、能访问某网站）；渲染时按条件实时算出成员，所以加节点或重新检测后不用改组。来龙去脉见 [分流规则方案](../internal/routing-rules-rfc.md)。
4. 订阅缓存整批失效；排水和容量上限只在订阅层生效，不踢已建立的连接（decisions 第 30 条）。

用户实际看到的格式和规则怎么配，写在用户文档站的 [客户端格式](../guides/client-format.md) 和 [订阅参考](../reference/subscription.md)。

## 坑

- 策略组用「标签」条件 → v0.7.0 删掉用途后没有标签来源，匹配不到任何节点 → 改用地区、协议或能力检测筛选。
- 订阅链接带 `?purpose=` → v0.7.0 起被忽略，返回完整订阅 → 不报错是有意的，老客户端不会坏。
- UA 规则表为空 → 表示「没配过」，用内置默认，不是「关掉分流」（decisions 第 36 条）。
- 节点名里写 `{USERNAME}` → 会被抹成空串，只在订阅名里生效 → 见 [缺口与待定项](../internal/open-questions.md) 第 3 条。
- 订阅走明文 HTTP → token 可被链路窃听 → 已接受的代价，换地址用 `/sub/{token}/rotate`。
