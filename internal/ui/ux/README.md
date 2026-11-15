# MPX UX 规范

本目录是 MPX 界面规范的入口。维护界面时先读本页，再读相关模式和组件。

## 设计意图

MPX 是操作者本机使用的代理控制工具。用户进入主页后，应能判断节点与用户是否需要处理，并进入对应对象。

采用已认可的 Terminus 密度与字体层级。中性表面承载内容，小面积蓝色突出主要操作和链接。深色侧栏使用中性炭灰。深色当前导航使用中性浅底和明确文字。普通列表不使用斑马纹。组件不以粗描边表达选中。

正文使用 Inter 与本地 Noto Sans SC。MPX 品牌资产保持独立。通用图标统一使用固定版本的官方 Lucide 组件，经共享的 `Icon` 原语调用。禁止自行绘制、近似重画或在页面追加 SVG 几何路径。

深浅主题由用户显式选择。首次打开沿用既有浅色默认值。减少运动时保留状态与操作结果。

## 四层入口

| 层 | 责任 | 真源与入口 |
| --- | --- | --- |
| 设计意图与 Token | 视觉角色、主题、字体、间距、尺寸、圆角、动效 | 本页；[tokens.css](../assets/styles/tokens.css) |
| UX 模式 | 导航、读取、表单、浮层、反馈与恢复 | [交互模式](patterns.md) |
| UI 组件 | 可复用实现、参数、状态、调用案例 | [组件契约](components.md)；[React 组件](../frontend/components/)；组件目录 `/components.html` |
| UX 验收 | 结构、交互、可访问性与画面证据 | [验收规范](evaluation.md) |

## Token 架构

[Token 真源](../assets/styles/tokens.css) 包含四部分：基础尺度、语义角色、主题和兼容绑定。CSS 是唯一数值真源。文档不维护第二份色板。

- `--ui-font-size-*`、`--ui-space-*`：字体层级和间距。
- `--ui-bg-*`、`--ui-text-*`、`--ui-border-*`：表面、文字和边界。
- `--ui-action-*`、`--ui-selected-*`、`--ui-nav-current-*`：操作、对象选中和当前导航。
- `--ui-control-*`、`--ui-field-*`、`--ui-row-*`：控件、字段和列表尺度。
- `--ui-radius-*`、`--ui-shadow-*`、`--ui-duration-*`：形状、高度与反馈。

新样式读取语义角色，不在页面另建色板。Token 数值只在真源修改。图标描边、字号和点击区分别定义。React 样式入口 [index.css](../frontend/styles/index.css) 首个导入即真源；shadcn 角色经 `@theme` 绑定到同一份 Token，不建第二份色板。

所有页面共用七档字号。常规页面、区块和详情标题使用 title；只有开场标题和展示数字使用 display。对象名称、正文和输入使用 body。导航、状态、按钮、标签和菜单使用 navigation/control。次要说明和列标题使用 caption。时间与只读技术标识使用 meta。微型标记使用 micro，不承担关键状态。

字号不随经典配色或密度切换。带 mono 的输入仍使用 body；只读代码使用 code。地区代码的相对字号只用于匹配旗帜图形高度。

根元素的 `data-theme` 选择主题，`data-density` 选择密度，`data-ui` 选择迁移阶段。密度不改变字号。

路由持有 `data-ui`。总览使用 `workspace`。其余现有页面使用 `classic` 兼容绑定。抽屉、菜单和提示继承当前根主题。页面迁移须显式切换绑定并验证相关组件，不把新色板自动推广到全部页面。

## 实现边界

界面源码位于 [frontend/](../frontend/)：React 18、TypeScript（strict）、Vite、shadcn/ui 与 radix。[icon-map.ts](../frontend/components/icon-map.ts) 由 `scripts/sync-ui-icons.mjs` 从官方素材生成。[components/ui/](../frontend/components/ui/) 是 vendored shadcn 包装。[primitives.tsx](../frontend/components/primitives.tsx) 提供无业务状态的展示原语。[app/App.tsx](../frontend/app/App.tsx) 持有路由、读取、浮层与反馈装配。页面与领域代码继续持有各自的表单和草稿。

`npm run ui:build` 产出 `assets/index.html`、`assets/components.html`、`assets/boot.js` 与 `assets/build/`(含 `UI-SOURCES` 源码指纹)。它们是生成物，不手工编辑，由 Go 静态服务提供。开发时 `npm run ui` 一条命令编译并启动页面。旧页面脚本已删除。页面源码只在 `frontend/` 维护。

组件目录 `/components.html` 加载同一份 Token、产品 CSS 和真实 React 组件，不加载业务启动代码。复杂交互使用真实页面测试，避免复制第二套表单或菜单逻辑。

业务规则与控制面契约见[本机入口](../../../docs/atlas/operator-entry.md)。本规范不增加管理能力或改变授权范围。

## 维护顺序

1. 确定用户任务与受影响状态。
2. 读取相关交互模式和真实调用方。
3. 复用现有 Token、React 组件和官方图标。
4. 更新组件目录案例或真实流程检查。
5. 执行 `npm run ui:check`、`npm run ui:typecheck` 和相关浏览器验收。
6. 查看实际画面后记录结果。
