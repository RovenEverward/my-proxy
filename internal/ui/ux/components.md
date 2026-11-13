# UI 组件契约

先复用真实实现。组件目录位于 `/components.html`，从已登录的本机界面打开。它加载同一份产品 CSS、官方图标和真实 React 组件。

本机独立预览：运行 `npm run ui:components`，打开 `http://127.0.0.1:5179/components.html`。该命令先构建资源，再提供静态预览。业务联调通过重新构建的 `mpx ui` 进行。

## 共享展示原语

统一入口：[primitives.tsx](../frontend/components/primitives.tsx)。shadcn/ui 包装位于 [components/ui/](../frontend/components/ui/)。

```tsx
<Button variant="primary" data-act="user-new"><Icon name="plus" />添加用户</Button>
<StatusBadge tone="warn">需要处理</StatusBadge>
<EmptyState icon="nodes" title="还没有节点" description="添加节点后，列表会显示在这里。" />
<ErrorState title="节点" error={new Error("连接超时")} />
```

| 组件 | 参数与结果 | 约束 |
| --- | --- | --- |
| `Button` | shadcn 包装；`variant`、`size`、`loading` | 忙态用 `loading`；禁用保留原因 |
| `Icon(name)` | 注册别名 → 官方 Lucide 组件 | 缺失名称报错；装饰性隐藏，名称由外层控件提供 |
| `StatusBadge(tone)` | 状态类别与文字 → 状态点和文字 | 类别不代替文字；领域判定仍在页面或领域展示函数中 |
| `Avatar(name)` | 名称 → 首字符头像 | 只用于用户身份，不替代官方品牌标志 |
| `LoadingTable` | 行列数 → 表格骨架 | 容器标明读取状态；不得伪装真实数据 |
| `EmptyState` | 图标、标题、说明、可选操作 → 空态 | 区分没有数据和筛选无结果 |
| `ErrorState` | 对象与错误 → 错误和重试 | 保留真实错误；401 显示重新打开提示 |
| `Field` | label、hint、error 与控件的关联 | 错误说明关联具体字段 |
| `FlagText` | 文本中的国旗 → 旗帜图形与降级 | 旗帜不代替名称文字 |

## CSS 组件

实现：[frontend/styles/](../frontend/styles/)，由 [index.css](../frontend/styles/index.css) 统一导入。主题与尺寸由 [Token 真源](../assets/styles/tokens.css) 提供。shadcn 角色经 [shadcn.css](../frontend/styles/shadcn.css) 绑定到同一份 Token。

| 组件 | 使用方式 | 必查状态 |
| --- | --- | --- |
| 操作按钮 | `.btn`；主要 `.pri`；次要 `.ghost`；危险 `.danger` | 默认、悬停、按下、焦点、禁用、忙态 |
| 图标按钮 | 原生 button 与 `.ib`；提供 `aria-label`、`title` | 命中区、焦点、忙态与不可用原因 |
| 字段 | `.field`、关联 label、`.in`、提示与错误 | 输入、焦点、只读、禁用、错误 |
| 状态 | `.st`、`.dot`、`.tag` | 文字与形状、长文本、深浅主题 |
| 列表与表格 | `.row`、原生 table 与 `.u` | 正常、空、加载、失败、长名、窄屏 |
| 分段与页签 | `.seg` 与 `.tabs` | 当前项、键盘、不可用、URL 状态 |
| 卡片与告警 | `.card`、`.callout` | 内容层级、操作可见、边界与溢出 |

`size="sm"` 是已有兼容变体，字号与常规操作一致。工作区主题中常规与行内文字操作沿用标准控件尺度。所有页面共用七档字号；密度只调整控件和间距。对象名称和输入不因使用 mono、tag 或 button 而降为技术标识字号。

组件目录的按钮和字段展示真实 CSS 契约。状态、空态、错误和图标直接调用共享组件。目录中的操作仅用于演示，不写业务数据。

## 有状态组件

复杂组件继续复用产品入口，不在目录复制实现：

| 组件 | API 与所有者 | 真实案例与验收 |
| --- | --- | --- |
| 异步反馈 | `Button loading`、`useApp().toast`；当前操作持有状态 | [app-context](../frontend/runtime/app-context.tsx)；[交互延迟测试](../e2e/interaction-latency.spec.mjs) |
| 抽屉 | `useApp().openDrawer` + `usePageData` 异步填充；浮层代次隔离响应 | [导航加载测试](../e2e/nav-loading.spec.mjs) |
| 确认框 | `useApp().confirmBox({title,text,ok,run,typed,...})`；调用方持有业务进度 | [过期用户测试](../e2e/expired-users.spec.mjs) |
| 行菜单 | `ActionMenu`；Radix 管理菜单、门户和焦点 | [过期用户测试](../e2e/expired-users.spec.mjs) |
| 编辑器 | [code-editor](../frontend/components/code-editor/) → 值、定位与焦点操作 | [设置测试](../e2e/settings.spec.mjs) |
| 连接列表 | [connections](../frontend/components/connections/)；连接模块持有缓存与编辑状态 | [连接测试](../e2e/inbounds.spec.mjs) |
| 订阅草稿 | [订阅页](../frontend/pages/sub/SubscriptionPage.tsx) 草稿状态与统一保存条 | [订阅测试](../e2e/sub.spec.mjs) |
| 更新 | [runtime/update.tsx](../frontend/runtime/update.tsx)；pill 与更新弹窗 | [更新测试](../e2e/update.spec.mjs) |

## 官方图标

名称与来源映射：[icons.json](../icons.json)。官方原件与许可：[Lucide 目录](../assets/icons/lucide/)。运行时生成物：[icon-map.ts](../frontend/components/icon-map.ts)。

```sh
npm run ui:icons:check
node scripts/sync-ui-icons.mjs path/to/lucide-static-1.70.0.tgz
```

新增图标时，从固定版本官方包选择原件并更新映射，再重新生成 `icon-map.ts` 并检查。不要编辑生成物或 SVG 几何路径。MPX 品牌资产、地区旗帜和业务二维码不作为通用操作图标。
