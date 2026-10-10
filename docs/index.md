---
layout: home

hero:
  name: mpx
  text: 自建 xray 多节点代理的控制面
  tagline: 一台控制面管住所有节点上的 xray，本地命令行是全部入口。用户流量直连节点，控制面不进数据通路——它挂掉，已经连上的人不受影响。
  actions:
    - theme: brand
      text: 从零搭一套
      link: /user/tutorial/getting-started
    - theme: alt
      text: 它是怎么搭起来的
      link: /internal/

features:
  - title: 免证书防探测
    details: 原生 VLESS REALITY，借主流域名的 TLS 指纹，不用自己签证书。
  - title: 加人不重启
    details: 增删用户走 xray 的 gRPC 接口，实时生效，不动节点上别人的连接。
  - title: 按客户端出配置
    details: 同一条订阅地址，Clash、sing-box、v2rayN 各拿各的格式；浏览器打开是自助页。
  - title: 节点零维护
    details: 节点上只用系统 ssh 和 xray 自带的 API，没有常驻进程要看照。
---

<div class="mpx-home" markdown>

## 先看它长什么样

<div class="mpx-figure" markdown>

### 节点和用户，一张表看完

节点装没装、配置漂没漂、每人的用量和到期，都在这几列里。

```console
$ mpx user list
ID  用户名   可用  状态  已用  上限   到期         重置策略  入站   备注
1   alice   启用  active  0B   100GB  2026-12-31  monthly   全部   老朋友
2   bob     启用  active  0B   50GB   不过期      monthly   全部
3   carol   启用  active  0B   不限   不过期      none      全部
4   dave    停用  disabled 0B  10GB   不过期      none      全部
共 4 个用户
```

<p class="mpx-caption">实测输出，一台本地控制面，四名用户。</p>

</div>

<div class="mpx-row mpx-row--flip" markdown>

<div class="mpx-row__text" markdown>

### 每人一个订阅页

订阅地址用浏览器打开就是一个自助页：状态、用量、到期，以及按格式分好的订阅链接，逐个复制就行。

地址外泄了，用户自己就能换掉。客户端拉同一条地址拿到的是配置，不会拿到这个页面。

</div>

<div class="mpx-row__media" markdown>

<figure class="mpx-shot" markdown>

![浏览器中的订阅页](/subscription-dashboard.png)

</figure>

<p class="mpx-caption">订阅页，实测截图。</p>

</div>

</div>

## 从这里开始

- [使用指南](/user/)：装控制面、纳管节点、开用户、发订阅，以及出问题怎么查。
- [内部工程](/internal/)：整体架构、对外契约、数据模型，以及三十条决策和它们的理由。

</div>
