# RFC:节点展示名「🇺🇸 洛杉矶 A」

> 状态:已批准(2026-10-15,采纳全部推荐项,包括控制面 API 的加法改动)· 按里程碑 N1–N6 实现中 · 起草时基于 main 96bfaff

## 结论

1. **`name` 继续作为 ASCII 内部标识**(`la-1`),CLI、日志和 webhook 都用它。**另外新增一个展示名**,格式为 `国旗 + 中文地区 + 字母`,由三项拼出来:国家(ISO-2)、中文城市名、字母。三项都存在已有的 `node_metadata` 里,`region` 字段本来就有。
2. **订阅里的默认节点名改用展示名。**host 上手动设的 remark 原样保留。
3. **地区由控制面一次性给出建议,用户确认或修改后才固定下来。**建议的依据:用节点地址去问 ipwho.is,这个服务 `node diagnose` 已经在用;拿到的英文城市名再用内置的对照表转成中文。查不准或查不到时,只显示国家,例如 `🇺🇸 美国 A`。之后**不再自动改**,要改用 `mpx node edit --region US --city 洛杉矶`。
4. **字母分配:**同一个「国家 + 城市」里,取最小的未用字母,分配后就固定下来。删掉节点后**不重排**,因为订阅名一变,客户端里选中的节点和规则都会失效。
5. **一个节点在同一份订阅里有多个入口时**,在名字后面加协议短名,例如 `🇺🇸 洛杉矶 A · Reality`。还是重名的,再交给现有的 `DedupeRemarks` 加序号。
6. **控制面 API 只做加法:**metadata 增加 `city`、`letter` 两个字段;节点视图增加只读的 `display`;新增一个 `POST /api/nodes/{id}/locate` 用来给出建议。现有字段的含义一个都不改。

## 现状事实(已在代码里核对)

| 事实 | 出处 |
| --- | --- |
| `nodes.name` 是 `TEXT NOT NULL UNIQUE`。服务端只校验非空,**空格和 emoji 现在就允许** | `store/store.go`、`server/validate.go` 的 `validateNode` |
| name **不进**文件名,也不进 systemd 单元名:服务名和配置路径是另外的字段(`service_name` 不允许空白和斜杠) | `validateNode`、`node/install.go` |
| CLI 的节点参数先按数字 ID 匹配,不是数字就按 name **精确匹配** | `cli/node.go` 第 325–340 行 |
| name 出现在日志、错误文案、webhook 的 `NodeName` 和能力检测的 `Location` 里 | `control/*.go`、`node/*.go`、`server/api.go` |
| 订阅显示名的来源:`host.Remark` 展开变量;为空时**退回 `node.Name`**。可用变量有 `{SERVER_IP}`、`{NODE_NAME}`、`{PROTOCOL}`、`{TRANSPORT}`;`{USERNAME}` 会被抹成空串 | `render/ir.go` 的 `endpoint`、`model.Host` 注释 |
| 重名处理:`DedupeRemarks` 按顺序加 ` 2`、` 3`;sing-box 和 xray 的 tag 用 `uniqueTag` | `render/ir.go` |
| Clash 的 `name`、sing-box 的 `tag`、purpose 分组都直接用这个 Remark | `render/clash.go`、`singbox.go`、`purpose.go` |
| **已经有国家级别的地区标签:**`node_metadata` / `host_metadata` 里的 `region`(校验格式 `^[A-Z]{2}$`),通过 `mpx purpose metadata` 读写,用途路由会用到 | `model/purpose.go`、`store/purpose.go`、`cli/purpose.go` |
| **能力检测里的 egress:**经过某个入口的代理出口去请求 `api.ip.sb/geoip`,**只解析 IP 和 `country_code`**,没有城市。需要给这个入口单独开启检测(`CapabilityConfig.Enabled`) | `node/capability.go`、`model/capability.go` |
| **`node diagnose` 的 IP 检测**会请求 ipwho.is,解析出 country / region / city(英文)和 ASN | `diagnostics/ip.go` 的 `parseGeoIP` |
| 仓库里**没有**离线 GeoIP 库,也没有中文地名表 | 全仓库搜索 |

## 设计

### 1. 内部标识和展示名分开

不直接把 `name` 改成 emoji 加中文。理由:

- CLI 要能直接敲:`mpx node sync la-1`,而不是 `'🇺🇸 洛杉矶 A'`;
- 日志和 grep 要好用;
- name 的唯一约束可以继续承担「标识」的职责。

展示名**不单独存成一个字符串**,而是由三项实时拼出来,这样「事实只存一份」:

```
display = flag(region) + " " + (city || 国家中文名) + " " + letter
```

- `flag(region)`:由 ISO-2 换算成区域指示符号,不需要任何数据。
- 国家中文名:内置一张 ISO-3166 对照表,取自 CLDR 的 zh 数据。
- 三项都没设(例如老节点还没迁移):`display` 退回 `name`,和现在的行为完全一样。

### 2. 地区怎么判定

| 方式 | 精度 | 依赖 | 隐私 | 结论 |
| --- | --- | --- | --- | --- |
| 离线 GeoIP 库(GeoLite2 City、DB-IP Lite) | 机房 IP 的城市级精度一般,常常落在注册地 | GeoLite2 要注册 license key;库文件是几十 MB 级(**具体大小没有实测**),还要定期更新 | 最好,不外发 | 不选:为一次性的建议背上数据包和更新负担,不值 |
| **控制面请求 ipwho.is** | 和上面同一类数据,精度相当 | 不新增依赖,`diagnose` 已经在用 | 节点 IP 会发给第三方 | **推荐**,只在建议时调一次 |
| 在节点上探测出口 | 能看到真实出口 IP(地址是域名或 CDN 时更准) | 要走 SSH,并且节点上要有 curl | 和上一行一样会发给第三方 | 作为补充:节点地址不是 IP 时用它 |
| 只用 egress 能力检测 | 只有国家 | 要先开启检测 | 和上面一样 | 只拿来校对国家 |

**定案方式(2026-10-15 更新:用户同意新建节点时自动识别并写入,可关闭,手动设过的不覆盖):**添加节点时(以及执行 `mpx node locate ID` 时)给出建议,CLI 和 UI 显示「建议:🇺🇸 洛杉矶(ipwho.is,依据 IP 1.2.3.4)」,用户确认后才写入。用户写过的值**永远不会被自动覆盖**。

### 3. 城市中文名

- 内置一张**常见 VPS 城市对照表**(英文 → 中文),例如 Los Angeles → 洛杉矶、San Jose → 圣何塞、Tokyo → 东京、Hong Kong → 香港,几十条。手工维护,能查到出处。
- 表里查不到的:只显示国家中文名,并提示用 `--city` 补上。**不做机器翻译**,免得出现「圣何西」这类错译。
- `--city` 接受任意中文,只限制长度,并且不能含控制字符。

### 4. 字母

- 字母组的范围是 `(region, city)` 这一对。同城的节点按 A、B、C 排;城市为空时按国家分组。
- 新节点取组内最小的未用字母;超过 26 个之后用 AA、AB。
- **删除后不重排,也不补位**:删掉 A 之后,B 还是 B;再新增的节点会拿到空出来的 A。客户端里的节点选择和规则是按名字绑定的,重排会让其他节点全部改名。
- 可以用 `mpx node edit --letter B` 手动指定,冲突时报错。

### 5. 订阅里的名字

- 默认 remark 改成 `display`(原来是 `node.Name`)。
- host 设了 remark 的仍以它为准。新增变量 `{NODE_DISPLAY}`。`{NODE_NAME}` **仍然代表 name**,这样已有的 remark 不会悄悄变样。
- 同一节点在一份订阅里有多个入口时,自动加 ` · 协议短名`:由 Protocol、Network、Security 推出来,例如 Reality、Hy2、TUIC、WS。仍然重名的,交给 `DedupeRemarks` 加 ` 2`。
- **已知局限:**Windows 自带字体不显示国旗 emoji,会显示成「US」两个字母。各个客户端实际怎么显示**需要实测**,这份 RFC 没有核实。

### 6. 已有节点迁移(用户已同意默认改)

- 新增 `mpx node relabel [--all] [--dry-run]`:
  1. 对每个没有展示名的节点做一次 locate;
  2. 字母按节点 ID 顺序分配,所以 `la-1` 是 A、`la-2` 是 B;
  3. 打印「旧名 → 新名」对照表;
  4. 用户确认后写入。
- **升级时不自动执行**,因为它要访问外网,而且结果需要人看一眼。
- `name` 不改,所以 CLI 脚本照常能用。客户端里的节点名会变一次,用户可能需要重新选一次节点。

### 7. API 和 UI

- **API(只做加法):**
  - `node_metadata` 增加 `city`、`letter`;
  - `GET /api/nodes` 的每一项增加只读的 `display`;
  - 新增 `POST /api/nodes/{id}/locate`,只返回建议,不写入。
- **UI:**节点列表和详情以展示名为主、name 为辅。编辑抽屉里放「地区 / 城市 / 字母」和一个「重新识别」按钮。

## 失败与边界

| 情况 | 行为 |
| --- | --- |
| ipwho.is 不通或限流 | 不给建议,提示手动填写 `--region/--city`;不阻止添加节点 |
| 节点地址是 CDN 或域名 | 改用节点上探测出口;两种都没结果就不给建议 |
| 查到的城市不在对照表里 | 只显示国家,提示补 `--city` |
| 改了城市 | 字母在新组里重新取最小的未用值,旧组不重排 |
| 用户在 host 上手写了 remark | 不动 |


## 8. CLI 里怎么指定对象(补充)

### 现状:哪些命令只收数字 id(已在代码里核对)

| 对象 | 只收 id 的地方 | 已经支持名字的地方 |
| --- | --- | --- |
| 节点 | `node show/rm/edit/config/sync/install/drain/undrain/online <id>`;`node core show/edit <节点 ID>`;`node probe/capabilities/capability-set <id>`;`node diagnostics <id>`、`diagnose [id]`;`inbound add/list --node ID` | `node status [名称或 ID]`:走 `resolveNodeArgs`,先当数字 id,否则按 name **精确**匹配 |
| 入站 | `inbound show/rm/edit <id>`;`host list/add/edit --inbound ID`;`user list --inbound ID` | 无。入站有 `tag`,但**不唯一**(库里没有 UNIQUE 约束);没设时 `TagOrDefault` 退回成 `协议-端口` |
| 入口(host) | `host edit/rm <id>` | 无。host 只有 remark,没有名字 |
| 用户 | 无 | 所有命令都收 `<username>`(`UNIQUE`) |
| 设备 | 无 | `device revoke <id\|名称>` |
| 用途元数据 | `purpose metadata` 的 `args[0]/args[1]` 直接拼进 URL(`/api/nodes/{id}/metadata`) | 无 |

统一的入口只有 `idArg`(`strconv.ParseInt`,失败就报「id 不是整数」)。cobra 默认的 `completion` 子命令在(代码里没有禁用),但**没有任何 `ValidArgsFunction`**,所以现在只能补全子命令,补全不了对象名。

### 设计:一个解析器,所有收节点或入站的位置都用它

`resolve(kind, arg)` 按顺序尝试,**某一步命中唯一一个就停**:

1. **纯数字**:当作 id(兼容现有的脚本和习惯)。如果某个节点的 name 恰好也是这串数字,仍按 id 处理,并在 stderr 提示可以写成 `name:<x>`。
2. **完全相等**,不区分大小写:比对 name(`LA-1` 能匹配 `la-1`)。
3. **展示名的规范形式完全相等**:去掉国旗(区域指示符号)、空白和 `·`,再转成小写。例如 `洛杉矶A`、`洛杉矶 a`、`🇺🇸 洛杉矶 A` 都会匹配到同一台。
4. **唯一前缀**:对 name 和展示名的规范形式做前缀匹配,例如 `la` → `la-1`(前提是只有它一个)、`洛杉` → 洛杉矶 A(前提是只有一台)。

- **有歧义时直接报错**,不自动猜:

  ```
  错误:「洛杉矶」匹配到 2 个节点,请写得更具体:
    1  la-1  🇺🇸 洛杉矶 A
    3  la-2  🇺🇸 洛杉矶 B
  ```

- **写操作(rm、sync、install、drain)**用前缀命中时,先回显「→ 节点 3 la-2(🇺🇸 洛杉矶 B)」再执行。危险操作原有的确认步骤不变。
- **入站:**`节点/tag` 的形式,例如 `la-1/vless-443`、`洛杉矶A/reality`。tag 部分同样按「不区分大小写 → 唯一前缀」匹配,且只在这个节点内部查。如果所有节点里只有一个入站的 tag 是这个,也可以只写 tag。
- **入口(host)**没有名字,继续用 id;`--inbound` 改用上面的入站解析。
- **用户**本来就按用户名。「不区分大小写」和「唯一前缀」**暂缓**:用户列表是分页的(`limit/offset`),用户数又可能上千,要做前缀匹配就得先拉全表或新增服务端查询;而用户名本身已经是名字,收益小。N1 不做,另行评估。
- **实现方式:**只在 CLI 这一侧做,拿 `GET /api/nodes` 和 `/api/inbounds` 的列表在本地匹配,**不改控制面 API**。展示名的规范化函数和服务端拼 `display` 用的是同一个包,确保两边算出来的一致。

### 列表输出以名字为主

- `node list` 的第一列改成「名称」(展示名),第二列是 name,id 放到最后一列。`-o json` 不变。
- 所有提示和报错里提到节点时,统一写成「la-2(🇺🇸 洛杉矶 B)」,不再只写 id。

### shell 补全

- 给节点、入站、用户这几类参数和 `--node`、`--inbound` 加上 `ValidArgsFunction`,候选值来自上面那两个列表接口。
- **补全插入的是 ASCII 的 name**,展示名放在描述里(zsh 和 fish 会显示描述)。这样就不用往命令行里插 emoji,也不用加引号。
- 拉列表设 1 秒超时,失败就静默不补全,不能让 Tab 卡住。
- `mpx completion bash|zsh|fish|powershell` 是 cobra 自带的,在 README 里写明怎么装。

## 里程碑(每期都能独立合并)

| 期 | 内容 | 验收标准 |
| --- | --- | --- |
| N1 统一解析 | `resolve` 加上前面说的四步规则;所有 `idArg` 位置和 `--node`、`--inbound` 都改用它;歧义时报错;id 兼容 | 测试表覆盖:数字 id、不区分大小写、唯一前缀、歧义报错、name 是纯数字的冲突;现有 e2e 里用 id 的写法全部照常通过 |
| N2 补全和列表 | `ValidArgsFunction`;`node list` 以名称为主;提示统一格式 | 在 bash 和 zsh 里 Tab 能补出 name;控制面不通时 Tab 不卡;`-o json` 输出不变 |
| N3 展示名数据 | metadata 加 `city` 和 `letter`;节点视图加 `display`;国家和城市对照表;字母分配;`node edit --region/--city/--letter` | 字母分配和删除后不重排的测试;N1 自动支持按展示名匹配 |
| N4 识别建议 | `POST /api/nodes/{id}/locate`(ipwho.is,地址是域名时在节点上探测);`mpx node locate` | ipwho.is 不通时可以手填,也不阻止添加节点;城市不在表里时退回只显示国家 |
| N5 订阅名和迁移 | 默认 remark 改用 `display`、`{NODE_DISPLAY}`、多入口时的协议后缀;`node relabel --all --dry-run` | 渲染快照测试;host 上手设的 remark 不变;relabel 先给对照表再确认 |
| N6 UI | 列表和详情以展示名为主;编辑抽屉里放地区、城市、字母和「重新识别」 | Playwright 测试,写操作的请求和 CLI 一致 |

N1 和 N2 不依赖展示名,可以先做;做完 N3 之后,按展示名匹配就自动生效了。

## 待拍板问题(附推荐)

1. **name 和展示名分开,还是直接把 name 改成展示名?**推荐**分开**。直接改在技术上可行(约束允许),但 CLI 输入、shell 转义和日志检索都会变差。
2. **建议来源用 ipwho.is(会把节点 IP 发给第三方),还是引入离线库?**推荐 **ipwho.is**,只在 locate 时调用一次,并提供开关可以完全手动。
3. **删除节点后字母要不要重排?**推荐**不重排**,保证名字稳定。
4. **有多个入口时的后缀用协议短名(`· Reality`),还是数字(`2`)?**推荐**协议短名**,用户一眼能看出区别。
5. **迁移方式:**推荐**显式执行 `mpx node relabel --all`,先给对照表再确认**,而不是升级时自动改。
6. **前缀匹配用于写操作时,是否要额外确认?**推荐**不额外确认**,但先回显解析结果;删除等危险操作的确认保持现状。
7. **name 是纯数字时(例如节点叫 `1`)以谁为准?**推荐**以 id 为准**,并提示用 `name:1`;更好的办法是新建节点时直接禁止纯数字的 name,只对新节点生效。
