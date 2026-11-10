# UI 组件契约

先复用真实实现。组件目录位于 `/components.html`，从已登录的本机界面打开。它加载同一份产品 CSS、官方图标和共享渲染原语。

本机独立预览：运行 `npm run ui:components`，打开 `http://127.0.0.1:5179/components.html`。此服务只读取界面静态资源。

## 共享展示原语

统一入口：[components.js](../assets/components.js)。加载顺序为图标、共享原语、业务启动、页面脚本，见[index.html](../assets/index.html)。

```js
H`<button type="button" class="btn pri" data-act="user-new">${ic("plus")}添加用户</button>`
stBadge("warn", "需要处理")
emptyBox("nodes", "还没有节点", "添加节点后，列表会显示在这里。")
errorBox("节点", new Error("连接超时"))
```

| 原语 | 参数与结果 | 约束 |
| --- | --- | --- |
| `H` | 模板与插值 → 已转义片段 | 普通文本自动转义；仅可信片段使用 `raw` |
| `ic(name, cls)` | 注册名称与样式类 → SVG | 缺失名称报错；图标装饰性隐藏，名称由外层控件提供 |
| `stBadge(cls, text)` | 状态类别与文字 → 状态点和文字 | 类别不代替文字；领域判定仍在页面或领域展示函数中 |
| `avatar(name)` | 名称 → 首字符头像 | 只用于用户身份，不替代官方品牌标志 |
| `skRows(rows, columns)` | 行列数 → 表格骨架 | 容器标明读取状态；不得伪装真实数据 |
| `emptyBox` | 图标、标题、说明、可选操作 → 空态 | 区分没有数据和筛选无结果 |
| `errorBox` | 对象与错误 → 错误和重试 | 保留真实错误；401 显示重新打开提示 |
| `dom` | 可信片段 → DOM 元素 | 只用于插入已转义的组件输出 |

## CSS 组件

实现：[app.css](../assets/app.css)。主题与尺寸由 [Token 真源](../assets/styles/tokens.css) 提供。主页布局补充位于 [home.css](../assets/home.css)。

| 组件 | 使用方式 | 必查状态 |
| --- | --- | --- |
| 操作按钮 | `.btn`；主要 `.pri`；次要 `.ghost`；危险 `.danger` | 默认、悬停、按下、焦点、禁用、忙态 |
| 图标按钮 | 原生 button 与 `.ib`；提供 `aria-label`、`title` | 命中区、焦点、忙态与不可用原因 |
| 字段 | `.field`、关联 label、`.in`、提示与错误 | 输入、焦点、只读、禁用、错误 |
| 状态 | `.st`、`.dot`、`.tag` | 文字与形状、长文本、深浅主题 |
| 列表与表格 | `.row`、原生 table 与 `.u` | 正常、空、加载、失败、长名、窄屏 |
| 分段与页签 | `.seg` 与 `.tabs` | 当前项、键盘、不可用、URL 状态 |
| 卡片与告警 | `.card`、`.callout` | 内容层级、操作可见、边界与溢出 |

`.sm` 是已有兼容变体，字号与常规操作一致。工作区主题中常规与行内文字操作沿用标准控件尺度。所有页面共用七档字号；密度只调整控件和间距。对象名称和输入不因使用 mono、tag 或 button 而降为技术标识字号。

组件目录的按钮和字段展示真实 CSS 契约。状态、空态、错误和图标直接调用共享函数。目录中的操作仅用于演示，不写业务数据。

## 有状态组件

复杂组件继续复用产品入口，不在目录复制实现：

| 组件 | API 与所有者 | 真实案例与验收 |
| --- | --- | --- |
| 异步反馈 | `withFeedback(element, operation)`；当前操作持有状态 | [app.js](../assets/app.js)；[交互延迟测试](../e2e/interaction-latency.spec.mjs) |
| 抽屉 | `openDrawer`、`readDrawer`；浮层代次隔离响应 | [导航加载测试](../e2e/nav-loading.spec.mjs) |
| 确认框 | `confirmBox({title,text,ok,run,typed,lock,...})`；调用方持有业务进度 | [过期用户测试](../e2e/expired-users.spec.mjs) |
| 行菜单 | `rowMenu`；当前行拥有展开状态 | [过期用户测试](../e2e/expired-users.spec.mjs) |
| 编辑器 | `codeEditor(host, options)` → textarea、值、定位与焦点操作 | [editor.js](../assets/editor.js)；[设置测试](../e2e/settings.spec.mjs) |
| 连接列表 | `CONN.load/use/groups`；连接模块持有缓存与编辑状态 | [conn.js](../assets/conn.js)；[连接测试](../e2e/inbounds.spec.mjs) |
| 订阅草稿 | 页面 `SUB` 状态与统一保存条 | [订阅测试](../e2e/sub.spec.mjs) |

## 官方图标

名称与来源映射：[icons.json](../icons.json)。官方原件与许可：[Lucide 目录](../assets/icons/lucide/)。运行时生成物：[icons.js](../assets/icons.js)。

```sh
npm run ui:icons:check
node scripts/sync-ui-icons.mjs path/to/lucide-static-1.70.0.tgz
```

新增图标时，从固定版本官方包选择原件并更新映射，再生成和检查。不要编辑生成物或 SVG 几何路径。MPX 品牌资产、地区旗帜和业务二维码不作为通用操作图标。
