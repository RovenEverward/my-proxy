# mpx 设计

mpx 是一个自建代理节点的控制面:一台机器上跑一个服务,管住若干台服务器上的 xray 与 sing-box,对外托管用户订阅。操作者在本机敲命令行,那是它唯一的入口;`mpx ui` 把同一套命令搬进本机浏览器,是 CLI 的另一种界面,不是第二个入口。

这份文档讲清三件事:它为什么要有、基本形态是什么、哪些事明确不做。它是这组工程文档的入口,其余几篇各回答一个问题,见[文档分工](#文档分工)。

## 目标

**给谁用。** 自己有几台到几十台服务器、要给几十到上千人分配代理的人。通常只有一个人,没有团队,也没有值班。

**规模。** 节点十几到几十台,用户数百到上千。单库单进程;每轮后台开销与节点数成正比,与用户数无关。

**要它回答的问题。** 谁该在哪些节点上、他到期了吗、这个月用超了吗;他手上那份订阅此刻该长什么样;加一个人的时候怎么只动他一个,不把别人的连接碰断;以及节点上要装什么才能被管起来。

## 背景

一台服务器跑 xray,手工维护还行:改 `config.json`、重启、给人发一条链接。

机器变成几台、人变成几百个,手工就要开始出错:

- **真相会散掉。** 谁在哪些节点上、什么时候到期、这个月用了多少,分别记在每台机器的配置文件、一张表格、某次聊天记录里。要看全得挨个登机器。
- **改动会牵连。** 端口和人都是写在配置文件里的,改一处就要重启,重启就把这台机器上所有人的连接断一次。
- **订阅会过期。** 客户端手里那份是静态链接。节点换了地址、用户超了配额,那份还是旧的,而且没有人会知道。
- **超限和到期看不见。** xray 只统计,不判断;谁该被停掉,得有人定期去算。

这四件事不解决,规模一上来,维护就变成一件必须一直盯着的事——而它本来只是「谁在哪儿、还剩多少」几个问题。

所以需要一个地方存**唯一一份真相**:由它算出「每个节点上该是什么」「每个人该拿到什么」,再送到该去的地方。mpx 就是这个东西——一个控制面,加一个在本机敲的命令行。

## 基本形态

<figure class="mpx-figure">
<svg viewBox="0 0 620 316" role="img" aria-label="mpx 的三层结构与两条主要路径" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <marker id="mp-ar" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path class="mp-ah" d="M 0 0 L 10 5 L 0 10 z"/>
    </marker>
    <marker id="mp-ard" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
      <path class="mp-ahd" d="M 0 0 L 10 5 L 0 10 z"/>
    </marker>
  </defs>
  <g font-family="'Mona Sans VF', -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Sans', Helvetica, Arial, sans-serif">
    <rect x="16" y="42" width="150" height="60" rx="10" class="mp-bx"/>
    <text x="91" y="68" class="mp-t" text-anchor="middle">操作机</text>
    <text x="91" y="88" class="mp-s" text-anchor="middle">mpx CLI</text>
    <rect x="16" y="216" width="150" height="60" rx="10" class="mp-bx"/>
    <text x="91" y="242" class="mp-t" text-anchor="middle">终端客户端</text>
    <text x="91" y="262" class="mp-s" text-anchor="middle">mihomo · sing-box</text>
    <rect x="216" y="20" width="196" height="286" rx="14" class="mp-bx"/>
    <text x="314" y="46" class="mp-ti" text-anchor="middle">控制面</text>
    <rect x="230" y="60" width="168" height="46" rx="8" class="mp-bd"/>
    <text x="241" y="79" class="mp-t">唯一一份真相</text>
    <text x="241" y="96" class="mp-s">节点 / 入站 / 入口 / 用户</text>
    <rect x="230" y="114" width="168" height="46" rx="8" class="mp-bd"/>
    <text x="241" y="133" class="mp-t">订阅分发</text>
    <text x="241" y="150" class="mp-s">links · clash · sing-box</text>
    <rect x="230" y="168" width="168" height="46" rx="8" class="mp-bd"/>
    <text x="241" y="187" class="mp-t">后台循环 · 每 30 秒</text>
    <text x="241" y="204" class="mp-s">采样 · 重置 · 收敛</text>
    <rect x="230" y="222" width="168" height="46" rx="8" class="mp-bd"/>
    <text x="241" y="241" class="mp-t">巡检 · 每 5 分钟</text>
    <text x="241" y="258" class="mp-s">服务 · 版本 · 哈希 · 证书</text>
    <text x="314" y="292" class="mp-s" text-anchor="middle">不进数据通路</text>
    <rect x="470" y="36" width="134" height="56" rx="10" class="mp-bx"/>
    <text x="537" y="60" class="mp-t" text-anchor="middle">节点 A</text>
    <text x="537" y="79" class="mp-s" text-anchor="middle">xray</text>
    <rect x="470" y="142" width="134" height="56" rx="10" class="mp-bx"/>
    <text x="537" y="166" class="mp-t" text-anchor="middle">节点 B</text>
    <text x="537" y="185" class="mp-s" text-anchor="middle">xray</text>
    <rect x="470" y="248" width="134" height="56" rx="10" class="mp-bx"/>
    <text x="537" y="272" class="mp-t" text-anchor="middle">节点 C</text>
    <text x="537" y="291" class="mp-s" text-anchor="middle">sing-box</text>
    <path d="M 166 72 L 212 72" class="mp-ac" marker-end="url(#mp-ar)"/>
    <text x="189" y="62" class="mp-k" text-anchor="middle">/api</text>
    <path d="M 166 246 L 212 246" class="mp-ac" marker-end="url(#mp-ar)"/>
    <text x="189" y="236" class="mp-k" text-anchor="middle">/sub</text>
    <path d="M 412 64 L 466 64" class="mp-ac" marker-end="url(#mp-ar)"/>
    <text x="439" y="54" class="mp-k" text-anchor="middle">SSH</text>
    <path d="M 412 170 L 466 170" class="mp-ac" marker-end="url(#mp-ar)"/>
    <path d="M 412 276 L 466 276" class="mp-ac" marker-end="url(#mp-ar)"/>
  </g>
</svg>
<p class="mpx-caption">三层各自的职责,以及控制面碰节点的三条通道。用户流量不经过控制面:终端客户端拿到订阅后直连节点。</p>
</figure>

三层,各管一段:

| | 做什么 | 不做什么 |
| --- | --- | --- |
| **CLI**(含 `mpx ui`) | 把参数拼成请求,把结果显示成人能看的;`mpx ui` 把同样的事放进本机浏览器 | 不碰数据库,不碰节点。它是 `/api` 的纯客户端,每条命令都能用 `curl` 等价做出来 |
| **控制面** | 持有唯一一份真相;渲染节点配置与订阅;决定谁该在哪个节点上 | **不进数据通路**,用户流量直接打到节点上 |
| **节点** | 跑控制面推过去的 xray;按用户计量流量 | 不装任何 mpx 组件,不主动联系控制面,也不需要能连外网 |

CLI 那一行有一个例外:`mpx init` 会 SSH。它是**在控制面存在之前**运行的那条命令——装控制面的那一刻 `/api` 还不存在,没有谁可以给它调用。所以它只做 SSH 编排,建对象的活交给 VPS 上跑的 `mpx bootstrap`,后者仍然是不折不扣的 `/api` 客户端。

**关键对象**是节点、入站、对外入口、用户、订阅分流规则,各住一张表。一个用户按 `inbound_ids` 决定能用哪些入站,留空表示全部;一个入站下挂若干对外入口,决定订阅里发什么地址。字段与状态判定见[数据模型与语义](internal/model.md)。

**边界**在于控制面只在两件事上碰节点——改**结构**(下发配置、重启)和收**流量**(读计数)。所以控制面挂了不影响已经连上的用户,它的开销也不随流量增长。

## 核心机制

控制面把节点上的 xray 当**执行器**:自己算好该是什么样子,再推过去。于是改动分两条路,按性质走:

- **改结构**(端口、入站、证书)全量渲染配置下发,会重启。
- **改名单**(加人、摘人、超限、续期)走每 30 秒一轮的收敛,只调 xray 的接口,不重启。

分开是因为配额恢复和到期续费都会触发加人;每次加人都重启的话,节点上所有人的连接都会跟着断一次。

两条保证值得单独说:

- **账不会丢。** 流量读的是节点上的**累计值**,不是读走即清零。xray 的计数只在进程内存里,漏采一轮、控制面重启一次、采样间隔里 xray 崩过一回,读走即清零的那段用量就永远找不回来,事后还分不清是「用户没用」还是「账被吞了」。改读累计值之后,上次的值就是库里的最近一行,控制面重启不丢。
- **不可用的用户看得出来。** 超限、到期、停用的用户拿到的是**占位节点**——一份连不通、但各家客户端都肯接受的配置,名字按状态分开写「已停用 / 已过期 / 已超限」。给真配置只会让客户端连不上还不报错,而这三件事用户该做的动作不一样。

订阅的格式不写在地址里,而是**按客户端认**:先看路径里有没有点名格式,再看 User-Agent 逐条匹配规则,都没命中就退回分享链接。用户拿到的永远是同一条地址,Clash、sing-box、v2rayN 各取各的格式。

用户实际拿到的东西长这样:用量、到期、按格式分好的链接,以及一个换地址的按钮。

<figure class="mpx-shot" markdown>

![浏览器中的订阅页](/subscription-dashboard.png)

</figure>

后台每一轮具体跑哪三步、顺序为什么不能换、下发的文件与订阅响应长什么样,见[mpx 是怎么搭起来的](internal/index.md)。

## 关键取舍

挑几条承重的。完整的清单在[关键决策与理由](internal/decisions.md),每条写明换掉会怎样。

| 取舍 | 为什么 | 代价 |
| --- | --- | --- |
| 节点上什么都不装,只用系统 `ssh` 和 xray 自带的 API | 节点零维护:不用升级组件、不用守护进程、不用给它签证书 | 拿不到结构化状态,要问就得 ssh 上去执行命令 |
| 配置全量渲染下发,不做增量修改 | 节点上的文件永远等于控制面算出来的那一份,不会两边各有一半真相 | 改结构必须重启 |
| 流量读累计值,不读走即清零 | 漏采、重启、xray 崩过,账都还在 | 多一张 `traffic_samples` 表,多一步差值 |
| SQLite 单连接加 WAL | 零依赖、单文件,备份就是拷一个文件 | 写入串行,规模上限在一台机器 |
| 订阅渲染与节点配置渲染不共用代码 | 两边目标不同:一个是「怎么连」,一个是「怎么被连」;硬合成会到处是分支 | 两处模板各自维护 |
| 排水和容量上限只在订阅层生效 | 不动节点上已经建立的连接 | 不是硬踢人,要等客户端下次刷新 |

## 不做的事

| 不做 | 为什么 |
| --- | --- |
| 管理员与多租户 | 只有一个人操作。多管理员要带出权限模型、审计、越权测试一整条链,而现在也没有操作审计、不能分权 |
| 自动升级自己 | 替换的正是控制面本身;换来一个起不来的版本就失去排障手段。只报有新版本,升级由人重跑安装脚本 |
| 仓库内的 CHANGELOG.md | 发布说明跟发布走,写在 GitHub Releases 上,能被 `mpx status` 直接指过去 |
| 控制面直接对外的管理 UI | 管理页面若由 `mpx serve` 提供,就会和 `/sub` 一起经反代暴露到公网,控制面得为它加会话、CSRF、登录限流,token 也要进浏览器。本机的 `mpx ui` 不在此列,理由见下 |
| 自己维护 xray 构建 | 只用 XTLS 官方发布包原样推送。自己编就等于把上游的安全修复变成我们的事 |
| 证书签发与续期 | 假定证书已在节点上,库里只存路径。REALITY 公私钥由本工具生成 |
| 节点侧常驻 agent | 节点只用 SSH,不装任何 mpx 组件 |

这张表和上面的取舍一样,是显式排除的:要推翻其中一条,得先推翻它后面的理由。

**推翻过的一条:「不做 Web UI」。** 原来的理由是「入口是 CLI;UI 得跟着模型一起演进」。用户到了几百上千,「谁快到期、谁超限、哪台节点在退避」要连敲几条 `list` / `status` 再在脑子里拼起来,CLI 一屏看不全(见 roadmap 批 4)。原来那条理由里真正要守的是两件事:入口只有一个、UI 不能长出 CLI 没有的能力。`mpx ui` 两件都守住:它跑在操作机上、只听回环地址,页面的请求带上 CLI 那份 token 原样转给 `/api`,控制面一行不改;页面能做的事以 CLI 已有的命令为界,每个写操作都附等价命令。于是这一条改成上表里的「控制面直接对外的管理 UI」。方案、替代方案与安全设计见 [管理 UI 方案](internal/ui-rfc.md)。

## 文档分工

| 文档 | 回答什么 |
| --- | --- |
| **设计**(本篇) | 它为什么要有、基本形态是什么、哪些事不做 |
| [对外契约](internal/contracts.md) | CLI、管理接口、订阅接口、节点侧契约的确切形状 |
| [数据模型与语义](internal/model.md) | 数据长什么样、状态怎么判 |
| [关键决策与理由](internal/decisions.md) | 每一条取舍为什么这么定,换掉会怎样 |
| [现状](internal/status.md) | 做到哪、验到什么程度、哪些还没验过 |
| [路线图](internal/roadmap.md) | 分批演进计划,以及排队的依据 |
| [缺口与待定项](internal/open-questions.md) | 还有哪些缺口、哪些需要拍板 |
| [管理 UI 方案](internal/ui-rfc.md) | `mpx ui` 为什么是本机转发、页面以什么为界、怎么防跨站与 DNS rebinding |
| [自动升级方案](internal/update-rfc.md) | 三部分的升级、校验、回退与 `mpx update`;发布件校验和(M0) |
| [用途订阅验证](internal/purpose-validation.md)、[能力检测验证](internal/capability-validation.md)、[一键体检验证](internal/diagnostic-validation.md)、[QUIC 验证](internal/quic-validation.md) | 这几块功能的真实证据与限制 |

安装与日常操作看[用户文档导航](user/index.md)。
