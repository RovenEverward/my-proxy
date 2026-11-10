# MPX 界面开发入口

开始修改界面前，按以下顺序读取：

1. [UX 规范入口](ux/README.md)：设计意图与 Token 所有权。
2. [交互模式](ux/patterns.md)：目标流程、状态、焦点与错误恢复。
3. [组件契约](ux/components.md)：真实组件、调用方和可运行目录。
4. [验收规范](ux/evaluation.md)：结构、交互与视觉证据。

Token 数值只改 [styles/tokens.css](assets/styles/tokens.css)。页面只使用语义角色。主题由根元素持有，浮层继承当前页面主题。

图标只使用 [icons.json](icons.json) 中映射的官方 Lucide 素材。执行生成脚本后使用 `ic(name)`。禁止手绘图标、直接追加路径或编辑生成物。

共享展示原语位于 [components.js](assets/components.js)。产品和组件目录共用该文件。业务状态继续归路由、页面或领域组件所有。

修改后先执行 `npm run ui:check`，再执行相关浏览器测试。仓库原有 Go、全量 UI 和文档验证要求继续有效。
