# React 界面视觉验收

基线：`097ad97`。前端采用 React、TypeScript、Vite 和 shadcn/ui。Go 服务内嵌前端构建产物。

本次取证覆盖 Chromium 中的四个真实页面。数据通过 CLI 和控制面 API 持久化。节点使用文档保留网段；页面显示未体检、未部署的实际状态。取证未替换业务响应。

## 页面证据

所有截图均采用 2× 像素密度，已逐张查看。

| 页面 | 主题 | CSS 视口 | PNG 尺寸 | 截图 |
| --- | --- | --- | --- | --- |
| 总览 | 浅色 | 1440 × 1000 | 2880 × 2000 | [总览桌面](./overview-desktop-light.png) |
| 节点详情 | 深色 | 1440 × 1000 | 2880 × 2000 | [节点详情桌面](./node-detail-desktop-dark.png) |
| 用户列表 | 浅色 | 390 × 844 | 780 × 1688 | [用户列表手机](./users-mobile-light.png) |
| 分流规则 | 深色 | 390 × 844 | 780 × 1688 | [分流规则手机](./rules-mobile-dark.png) |

## 已核对结果

- 四页截图前均等待 `document.fonts.ready`。CDP 确认中文标题使用已加载的 Noto Sans SC。
- 四页的 `html`、`body` 和 `main` 均无横向溢出。
- 四页标题仍为 `document.activeElement`。计算样式为 `outline-style: none`，画面无标题焦点框。
- 节点详情显示两条真实连接记录，包含 Reality 和 WebSocket，以及自定义对外地址。
- 手机用户列表的四条普通行使用相同底色。过期用户仍通过文字与灰度区分。
- 手机规则页头说明占一整行，两个按钮位于下一行。
- 手机规则的三个域名值均有 318 CSS 像素可用宽度。直连、手动切换和拦截去向完整可见。
- 三条普通规则行使用相同底色。兜底行保留独立状态底色。
- 未记录 JavaScript 页面异常、CSP 拒绝或外部浏览器请求。

## 自动验收

- 严格 TypeScript、Vite 生产构建与 `npm run ui:check` 通过。
- 完整 Playwright 回归通过：350 项通过，8 项按需截图用例跳过。
- 外部订阅新增、授权、混合预览、刷新与策略组来源条件流程通过。
- `go vet ./...` 与 `go test ./... -count=1` 通过。
- 文档构建与 Atlas 审计通过。

## 兼容边界与记录

本记录覆盖上述视口、主题和真实页面装配，不代表全量 E2E 结果。浏览器画面不验证节点实际部署或外部服务连通性。

浏览器控制台记录五条 HTTP 404：普通浏览器环境的 `/local/app/theme` 共四次；无体检报告节点的 `/api/nodes/1/diagnostics?from=node` 一次。页面继续显示对应状态，未出现 JavaScript 异常。
