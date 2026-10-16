# RFC：「用途」改为「分流规则」

> 状态：已批准（2026-10-16，待定问题全部按推荐项定案）· 按里程碑 R1–R6 实现 · 起草时基于 main `559fcb8`（v0.5.1）
> 前提决策（2026-10-16）：① 去掉「线路」，标签只作为策略组的可选条件；② 用户级只能在全局之上加规则、改「其他全部」；③ 内置规则组继续指向上游规则集；④ 规则去向只能是策略组、直连或拦截，不能固定到某个节点。

## 结论

1. **分流从「整份模板文本」变成结构化数据：**一张有序规则表，加上策略组和兜底去向。控制面用这些数据**生成** Clash Meta 和 sing-box 订阅里的 `rule-providers`/`rule_set`、`proxy-groups`/选择器类出站和 `rules`。模板只保留和分流无关的部分（DNS、嗅探、入站、日志）。
2. **「用途」整体退役。**它表达的「按条件挑节点 + 某些域名走它们」，在新模型里就是「一个策略组 + 几条规则」。`?purpose=` 保留一个版本，按迁移后的等价规则出订阅，响应里带弃用提示；下一个版本删掉。
3. **作用范围分两层：**全局规则，以及按用户追加。用户层只能在全局规则**前面**加规则，并覆盖「其他全部」；不能删除、停用或改写某条全局规则。
4. **内置规则组是写在代码里的目录：**一个键（如 `ads`）对应每种格式各自的上游规则集 URL，规则内容由客户端自己下载更新，和现在的默认模板一样。
5. **规则去向只有三种：**策略组、`DIRECT`、`REJECT`。策略组按条件收入口（地区、协议、能力检测、标签），成员在**渲染时**计算。

## 动机

- 用户反馈：「用途」页的术语、JSON 和 ID 都看不懂；用户真正想做的是「调代理规则」，习惯的是 Clash 类客户端里「一行一条规则、从上往下匹配」的样子。
- 现状下，调规则只能编辑整份模板。Clash 默认模板有 1176 行，sing-box 默认模板里有 112 个规则集。而且两种格式各存一份，改一处不会同步到另一处。
- 「用途」只是在模板之上**叠**一层（加一个组，再把域名规则插到最前），概念和模板、订阅页下拉框三处纠缠，而且一份订阅只能叠一个非日常用途。

## 现状事实（已在代码里核对）

| 事实 | 出处 |
| --- | --- |
| 模板按格式存在 `settings.Templates`（全局，没有用户级）；API 是 `GET/PUT/DELETE /api/settings/templates/{format}`；没存时用内置底稿 | `model/model.go` 的 `TemplateFor`，`server/templates.go`，`server/server.go` |
| clash-meta：在底稿上**只注入 `proxies`**，`rule-providers`/`proxy-groups`/`rules`/`dns` 原样带出；分组靠 `include-all-proxies` 自动收进全部节点 | `render/clash.go` 的 `clashMeta`，`templates/clash-default.yaml` |
| 原版 `clash` 格式不读模板，写死 `PROXY`/`AUTO` 两个组和 `GEOIP,LAN` / `GEOIP,CN` / `MATCH` 三条规则 | `render/clash.go` |
| sing-box：在底稿上注入节点出站，并生成 `proxy`（选择器）、`auto`（urltest）、`direct`、`block` 四个出站；保证 `route.final` 存在 | `render/singbox.go` 的 `singboxOutbounds`、`singboxEnsureFinal` |
| Clash 默认模板：128 条 `RULE-SET`（blackmatrix7、Loyalsoldier 等上游）、29 条 `DOMAIN-SUFFIX`（「你自己加的」）、1 条 `GEOIP`、1 条 `MATCH`；15 个组，除「自动选择」外 14 个都是 `select`，包括 12 个分类组（广告拦截、人工智能、流媒体……）和「漏网之鱼」，用户可以在客户端里切换去向 | `templates/clash-default.yaml` |
| sing-box 默认模板：112 个 `rule_set`（SagerNet sing-geosite 的 `.srs`）、17 条 route 规则，`final` 是 `漏网之鱼`；分类出站名称和 Clash 的大体对应。**两种格式用的上游规则源不同** | `templates/singbox-default.json` |
| 用途：`purposes` 表存整份 JSON；`node_metadata`/`host_metadata` 存 `region`、`line`、`purposes`（标签），**以及命名功能后加的 `city`、`letter`** | `store/purpose.go`，`model/purpose.go` |
| 用途订阅：`?purpose=slug`，只支持 clash-meta 和 sing-box；加一个 `mpx-<slug>` 组；非日常用途把域名后缀规则插到最前，日常用途把模板里所有代理去向改写到这个组；候选为空时返回 422 | `server/purpose.go`，`render/purpose.go` |
| 能力检测的目标只有 `egress`、`youtube`、`netflix`，按 (入站, host) 记录结果，有有效期，连续两次一致才定论 | `model/capability.go`，`server/capability_purpose.go` |
| 订阅有缓存；规则相关的写操作和能力检测结果变化都会调用 `invalidateSubCache` | `server/subcache.go`，`server/capability.go` |
| UA→格式的识别规则在 `settings.Rules` 里（字段名也叫 Rules），和本文的分流规则**无关** | `model/model.go` 的 `SubscriptionRule` |

## 设计

### 1. 心智模型

```
请求的域名/IP ──▶ 用户层规则（按顺序）──▶ 全局规则（按顺序）──▶ 其他全部
                    第一条命中的生效：去向 = 策略组 | DIRECT | REJECT
策略组 ──渲染时──▶ 当前用户有权限、未排水、满足条件的入口（按显示名）
```

### 2. 数据模型（SQLite）

```sql
CREATE TABLE route_rules (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id  INTEGER REFERENCES users(id) ON DELETE CASCADE,  -- NULL = 全局
  position INTEGER NOT NULL,          -- 同一作用域内从 0 连续编号
  type     TEXT NOT NULL,             -- suffix | keyword | set | geoip | cidr
  value    TEXT NOT NULL,             -- 域名 / 关键词 / 内置组键 / 国家码 / CIDR
  target   TEXT NOT NULL,             -- 'DIRECT' | 'REJECT' | 'group:<id>'
  enabled  INTEGER NOT NULL DEFAULT 1,
  note     TEXT NOT NULL DEFAULT ''
);
CREATE TABLE route_final (user_id INTEGER UNIQUE REFERENCES users(id) ON DELETE CASCADE, target TEXT NOT NULL); -- NULL 行 = 全局
CREATE TABLE proxy_groups (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, config TEXT NOT NULL);
-- config JSON: {icon, mode: auto|manual, regions[], protocols[], capabilities[], tags[], test_url, interval}
```

- 规则去向是 `group:<id>` 而不是组名：组改名不会打断规则。删除一个仍被引用的组返回 409，并列出引用它的规则。
- `type=set` 的 `value` 必须是目录里的键；目录删掉某个键时，渲染跳过这条规则并在 `mpx rule list` 里标「已失效」，不报错让整份订阅失败。
- `node_metadata`/`host_metadata` **保留**（命名功能的 `region`/`city`/`letter` 在里面）。`line` 字段停止使用：读的时候忽略，写的时候拒绝非空值。`purposes` 字段语义改为「标签」，JSON 接受 `tags` 并兼容旧名一个版本。
- 能力条件只接受现有的三个目标（`egress`/`youtube`/`netflix`）。不提供 AI 能力条件（定案 2）。

### 3. 内置规则组目录

写在代码里（`model/rulecatalog_gen.go`，由两份默认模板生成，测试检查两边一致），每个键包含：中文名、说明、默认去向，以及每种格式的上游规则集列表。上游沿用现在两份默认模板里的源：Clash 用 blackmatrix7、Loyalsoldier 等，sing-box 用 SagerNet geosite。首批键按默认模板现有的分类整理（局域网、广告拦截、人工智能、谷歌、苹果、微软、社交、流媒体、游戏、开发、国内媒体、国内网站等，外加 GitHub、Telegram）。

**代价：**同一个键在两种格式下命中的域名不完全相同，因为上游不同。另外，sing-box 默认模板把国内规则（`private`、`geolocation-cn`、`cn`）放在分类规则之前，而 Clash 放在最后；规则表只有一个顺序，按 Clash 的来，所以改用规则表后，sing-box 订阅里的国内规则会排到分类规则之后（R2 实现时核实）。「测试网址」的结果只能以一种格式为准，界面上要写明。

### 4. 渲染

公共步骤：先算出当前用户的有效规则（用户层 + 全局层，跳过停用的规则）和兜底去向；再对每个被引用的策略组，从用户有权限的端点里按条件筛出成员（能力条件沿用 `filterPurposeCapabilities` 的判定）。

**Clash Meta（clash-meta）**
- `proxies`：和现在一样。
- `rule-providers`：只放被引用的内置组的规则集。
- `proxy-groups`：每个被引用的策略组输出一个组，`auto` 对应 `url-test`，`manual` 对应 `select`，成员**显式列出**显示名，不用 `include-all-proxies`。
- `rules`：每条规则输出一行，`set` 展开为多行 `RULE-SET`，最后加 `MATCH,<兜底>`。IP 类规则保留 `no-resolve`，写法照默认模板。
- 底稿中这四段**由控制面生成并覆盖**，`dns`/`sniffer` 等其他段原样保留。
- **成员为空的组：**输出一个只含 `DIRECT` 的 `select` 组，整份订阅不失败；用途现在的 422 行为不再沿用（定案 3）。

**sing-box**
- `route.rule_set` 和 `route.rules` 由规则生成，`route.final` 等于兜底去向。保留底稿开头连续的 `sniff`/`hijack-dns` 等前置规则：这正是 `purpose-validation.md` 里验证过的插入位置。
- 每个策略组输出一个 `urltest` 或 `selector` 出站；`DIRECT`/`REJECT` 映射到 `direct` 出站和 `reject` 动作。WireGuard 端点不能放进 urltest，沿用 `render/purpose.go` 的现有处理。

**其他格式**
- `links`、`xray` 不带分流，不受影响。
- 原版 `clash` 维持写死的三条规则（定案 5）。

### 5. API（全部是新增；`/api/purposes*` 保留一个版本）

| 方法与路径 | 说明 |
| --- | --- |
| `GET /api/rules?user=<name>` | 返回 `{global:[...], user:[...], final, user_final}`；不带 user 时只返回全局 |
| `PUT /api/rules?user=<name>` | **整表替换**一个作用域（排序、增删一次提交），带 `If-Match` 版本号，避免两台设备同时编辑互相覆盖 |
| `PATCH /api/rules/{id}` | 单条开关或编辑（UI 的快速编辑、开关） |
| `PUT /api/rules/final?user=` | 改兜底；对用户传 `null` 表示跟随全局 |
| `GET/POST/PUT/DELETE /api/proxy-groups[/{id}]` | 策略组增删改查 |
| `POST /api/proxy-groups/preview` | 传一个草稿组，返回会收进哪些入口、每个入口为什么被收或被排除（不含凭证） |
| `GET /api/rule-catalog` | 内置规则组目录 |
| `POST /api/rules/test` | `{domain, user?, format?}` → 命中的规则、去向，以及策略组当前会选哪个入口。domain 类规则精确判定；`set` 规则需要控制面拉取并缓存上游规则集来判断；`geoip`/`cidr` 需要先解析 DNS，结果标为近似 |

每个写操作都调用 `invalidateSubCache`。权限沿用现有的 token 和 `mpx ui` 放行表。

### 6. CLI

```
mpx rule list [--user U]                      # 带序号、去向、开关
mpx rule add suffix example.com --to 自动选择 [--at 3] [--user U]
mpx rule add set ads --to REJECT
mpx rule move 5 --to 2 | mpx rule toggle 5 | mpx rule rm 5
mpx rule final 自动选择 [--user U | --user U --inherit]
mpx rule test chatgpt.com [--user U]
mpx rule catalog
mpx group list | add 美国节点 --region US --mode auto | edit … | rm …
```

去向和组名都用名字指定，复用命名功能 N1 的解析器（唯一前缀匹配，有歧义时报错）。`-o json` 输出和 API 字段一致。

## 迁移与兼容

升级后第一次启动时执行**一次性迁移**，结果写进 `route_*` 表，原数据不删。

1. **模板 → 规则：**
   - 没存过模板（使用内置底稿）：按内置底稿的结构直接生成等价的全局规则、策略组和兜底。生成结果要用快照测试锁定，确认和现在的默认订阅**逐条等价**。
   - 存过自定义 clash-meta 模板：尝试解析其中的 `rules`，`RULE-SET`、`DOMAIN-SUFFIX`、`DOMAIN-KEYWORD`、`GEOIP`、`IP-CIDR` 和 `MATCH` 能对上的就转换；对不上的规则（`SUB-RULE`、`PROCESS-NAME`、`AND`/`OR`、指向未知规则集或组的规则）**整份不迁移**，这个用户继续走「旧模板模式」：订阅照原样生成，页面顶部提示「有 N 条无法转换，确认后改用规则表」。不静默丢规则。
   - sing-box 模板不单独解析：转换后两种格式都由同一张规则表生成。如果用户存过自定义 sing-box 模板，迁移前在日志和 UI 里提示「sing-box 的分流将改为跟随规则表」，并要求确认（定案 4）。
2. **分类组的客户端可切换性：**默认模板里的分类组都是 `select`，用户现在能在客户端里把「流媒体」切到别的组。迁移后规则的去向是策略组；为了不丢这个习惯，内置组的规则生成为「一个以配置去向为首选的 `select` 组」（定案 1）。
3. **用途 → 策略组 + 规则：**每个用途生成一个策略组（`regions`→地区，`tags`→标签，`required_capabilities`→能力；`lines` 丢弃并在迁移报告里列出），再把 `domains` 转成指向这个组的 `suffix` 规则，插在全局规则最前；`daily` 用途转成兜底去向。`capability_region`、`templates`、`test_url`/`interval` 尽量转换，转换不了的写进迁移报告。
4. **`?purpose=` 保留一个版本：**规则表就绪后，`?purpose=<slug>` 返回和普通订阅相同的内容（用途已经合并进规则表），响应头加 `Deprecation: true` 和说明，订阅页的「用途」下拉框改为提示文字。`mpx purpose` 和 `/api/purposes*` 保留只读一个版本，写操作返回 410 并指向 `mpx rule`。下一个次版本删除这些接口和 `purposes` 表。
5. **迁移报告：**`mpx rule migrate --report` 和 UI 顶部都能看到：转换了什么、丢了什么、哪些用户仍在旧模板模式。

## 风险与回滚

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| 生成的配置和原模板不等价 | 用户流量走错（国内站绕道、广告漏拦） | 默认底稿的快照等价测试；沿用 `purpose-validation.md` 的方法，用官方 mihomo 和 sing-box 解析配置，再发真实 SOCKS 请求 |
| 规则组改名或成员变动，客户端里的手动选择失效 | 用户需要重新选节点 | 组名稳定，去向按 id 引用；改名时在 UI 里提示 |
| 上游规则集 URL 失效或被墙 | 客户端下载规则失败 | 和现在一样由客户端处理；目录里集中维护 URL，方便一次修好 |
| 「测试网址」和客户端实际行为不一致 | 用户误判 | 界面写明以哪种格式、哪份上游为准；IP 类规则标近似 |
| 并发编辑 | 后写的覆盖先写的 | 整表 PUT 加版本号；冲突返回 409 |

**回滚：**迁移不删除 `settings.Templates`、`purposes` 和 metadata。新增一个设置 `routing_mode: rules|template`，迁移成功后默认 `rules`；出问题时 `mpx settings set routing_mode template` 立即回到旧渲染路径（模板原样，用途照旧）。降级到旧版本二进制时新表会被忽略，旧数据仍然完整。这个开关在删除用途的那个版本一起移除。

## 里程碑（每期都能独立合并）

| 期 | 内容 | 验收标准 |
| --- | --- | --- |
| R1 数据与目录 | `route_*`、`proxy_groups` 表，内置目录，store 层，`routing_mode` 开关（默认 `template`，行为不变） | store 测试；现有测试全部照常通过 |
| R2 渲染 | Clash Meta 和 sing-box 生成器；默认底稿 → 规则的转换 | 快照显示和默认订阅等价；官方客户端解析，真实请求验证 |
| R3 API 与 CLI | 第 5、6 节的接口和命令，含 preview 和 test | e2e 测试对照 CLI 和页面发出的请求 |
| R4 迁移 | 自定义模板和用途的转换、迁移报告、`?purpose=` 兼容、默认切到 `rules` | 在 144 控制面的数据副本上演练迁移，订阅对比前后差异，并逐条解释 |
| R5 UI | 按原型实现分流规则页（规则、策略组、用户覆盖、测试网址、空状态） | Playwright 测试；截图给用户确认后再合并 |
| R6 用途下线 | v0.6.0 废弃、v0.7.0 删除，见下方「R6 用途下线计划」 | v0.6.0 发布后才开始删除 |

## 替代方案

- **继续让用户编辑模板文本，只做一个更好的编辑器：**改动最小，但两种格式仍要各改一份，「测试网址」和用户覆盖都做不了。不采纳。
- **规则只存 Clash 格式的文本行，sing-box 从 Clash 翻译：**省去一套数据模型，但两种语法的语义有差异（`SUB-RULE`、逻辑规则），翻译不全反而更难排查。不采纳。
- **用户层允许停用或改写全局规则：**更灵活，但需要「覆盖哪一条」的引用关系，全局规则调整顺序时容易错位。已按决策 ② 不做。

## 已定案（2026-10-16）

1. **内置组在客户端里仍可手选。**内置规则组的规则生成为一个 `select` 组：配置的去向排第一，其后是其他策略组和 `DIRECT`。客户端里切换的习惯保留，服务端规则也不会固定到节点。
2. **不提供「能用 AI 服务」的能力条件。**能力条件只接受 `egress`、`youtube`、`netflix`。AI 组按地区筛选。
3. **策略组没有成员时**，输出只含 `DIRECT` 的组，订阅照常可用；UI 和 `mpx rule list` 显示警告。不沿用用途的 422。
4. **存过自定义 sing-box 模板的控制面，迁移要显式确认。**确认之前保持 `template` 模式。
5. **原版 `clash` 格式不改**，维持写死的三条规则。

仍需在 R4 演练时确认的事实：控制面上是否存过自定义 clash-meta / sing-box 模板，决定走哪条迁移路径。

## R4 迁移演练（2026-10-16）

在线上控制面数据库的只读副本上跑迁移（2 个用户、3 个节点、6 个入站；**没有自定义模板，也没有用途**，所以走「内置底稿」路径，不需要确认）。迁移结果：策略组 `手动切换`（manual）、`自动选择`（auto），43 条全局规则，其他全部 → `自动选择`。迁移前后各取两个用户的五种格式订阅对比：

- `clash`、`links`、`xray`：逐字节相同（定案 5）。
- `clash-meta`：`proxies`、`rule-providers`（128 个）完全相同，规则仍是 159 条、顺序和匹配条件逐条相同，只有去向的**组名**变了：
  - `本地直连` 拆成 `局域网与本机`（private/lan/lancidr/applications）和 `国内网站`（direct/cncidr）；两条直连域名和 `GEOIP,CN` 直接写 `DIRECT`。
  - `全球代理` 不再单独成组：27 条自定义域名直接指向 `自动选择`，原来那组 RULE-SET 归到 `海外常用`。
  - `人工智能` → `AI 服务`，`漏网之鱼` → `其他全部`；其余分类组同名。
  - 分类组的选项由 `[全球代理, 手动切换, DIRECT]` 变成 `[自动选择, 手动切换, DIRECT]`。原来 `全球代理` 的首选就是自动选择，所以默认去向不变。
  - `手动切换` 和 `自动选择` 由 `include-all-proxies` 改为显式列出节点，成员相同。
- `sing-box`：节点出站相同，规则匹配条件不变（每个域名后缀拆成单独一条，从 17 条变成 46 条）。行为上有两处变化，都是 §「内置规则组」里预告过的代价：
  1. 国内规则（`geolocation-cn`、`cn`）从最前面挪到分类规则之后，和 Clash 一致。同时出现在国内列表和分类列表里的域名（例如苹果、微软的国内域名）以前直连，现在跟着分类组（默认是代理）走。
  2. 新增 `geoip-cn → direct`（多了一个 `geoip-cn` 规则集），和 Clash 的 `GEOIP,CN` 一致。以前没有被域名列表收录的国内 IP 会走 `漏网之鱼`，现在直连。
- 客户端影响：组名变了的分类组，用户在客户端里的手动选择会重置一次。
- 迁移后的配置都用官方 mihomo 1.19.32（`-t`）和 sing-box 1.14.3（`check`）校验通过。

**兼容性取舍：**按「控制面接口只增不改」的要求，`/api/purposes*` 的写操作**没有**改成 410，仍然照常工作。改为在 rules 模式下给响应加 `Deprecation: true` 和 `X-Mpx-Deprecated` 头，说明这些改动不再影响订阅。`?purpose=` 在 rules 模式下返回普通订阅，带同样的弃用头。

## R6 用途下线计划（2026-10-16 定）

**v0.6.0：废弃（随分流规则一起发布）**
- `/api/purposes*` 继续工作，但无论哪种分流模式，响应都带 `Deprecation: true` 和 `X-Mpx-Deprecated`，写明 v0.7.0 删除，并指向 `/api/rules`、`/api/proxy-groups`。
- `?purpose=` 订阅继续可用，响应带同样的头。rules 模式下返回普通订阅。
- `mpx purpose` 的每个子命令都在 stderr 打废弃警告，帮助里标「已废弃，v0.7.0 删除」。
- 用途页顶部提示已合并进分流规则。

**v0.7.0：删除**
- 接口：`/api/purposes`、`/api/purposes/{slug}`、`/api/purposes/{slug}/preview/...`。
- 命令：`mpx purpose` 全部子命令。节点和入口的地区改用 `mpx node edit --region`；`/api/nodes|hosts/{id}/metadata` 仍保留给地区和城市使用，删除其中的 `line`、`purposes` 字段。
- 订阅：`?purpose=` 兼容逻辑，以及 `render/purpose.go` 等用途渲染代码。
- 数据：`purposes` 表，加一个迁移来删表。
- UI：用途页、`mpx ui` 放行列表里的用途写操作、相关 e2e 用例。
- 开关：`routing_mode` 的 template 回退路径（旧模板渲染依赖用途，一起移除），`mpx rule mode` 只保留查看。
- 文档：用途相关指南和 `purpose-validation.md`。

**前提：**v0.6.0 已经发布，并且至少运行一个版本周期。删除时以 GitHub issue「v0.7.0 删除用途接口」为清单逐项勾掉。
