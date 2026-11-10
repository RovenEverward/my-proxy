# 首次安装与接入 React 模块

本模块保留既有首次安装流程、DOM 选择器和 HTTP 契约。界面沿用项目 UX 四层规范、Token 和共享组件。

调用方只装配三个入口：

```tsx
import { SetupPage, ConnectForm } from "@/pages/setup";
import { ProfilesDialog } from "@/runtime/profiles";

<SetupPage />
<ConnectForm mode="add" onBack={closeConnect} />
openModal(<ProfilesDialog onAdd={openConnect} />);
```

`AppContext` 在安装模式下也必须存在。App 根据 `meta.mode` 装配全屏安装页。正常模式通过全屏接入表单添加控制面。控制面列表只读。用户选定控制面后，弹窗通过 App 的 `runGuarded` 执行草稿离页守卫。弹窗不复制守卫。

## 结构选择

采用页面持有表单、专用 hook 持有安装任务的结构。备选结构使用一个 reducer 管理安装、接入和弹窗。备选结构会让独立流程共用事件与状态，因此未采用。

- [index.tsx](./index.tsx) 持有安装草稿、当前屏幕和机器检查结果。
- [ssh-steps.tsx](./ssh-steps.tsx) 显示 SSH 连接与机器检查。
- [settings-step.tsx](./settings-step.tsx) 显示安装设置与字段错误。
- [use-installation.ts](./use-installation.ts) 启动或恢复任务，独立管理轮询。
- [install-steps.tsx](./install-steps.tsx) 显示安装、失败重试、链接与二维码。
- [ConnectForm.tsx](./ConnectForm.tsx) 独立持有地址、指纹核对和接入密码。
- [profiles.tsx](../../runtime/profiles.tsx) 读取控制面列表并执行切换。

表单和任务各有唯一状态所有者。机器检查成功时应用一次端口建议。返回设置页不会重复覆盖用户修改的端口。SSH 密码只随检查请求发送，请求发出时清空字段。接入密码只随接入请求发送，请求发出时清空字段。安装设备密码只在当前组件内保留以支持失败重跑，安装成功时清空。密码不写 URL、日志或浏览器存储。

## 轮询契约

任务从 `since=0` 开始，后续使用响应的 `seq`。输出最多保留最近 50 行。收到步骤或输出时恢复 2 秒间隔。连续 3 次空增量后使用 5 秒，连续 6 次后使用 10 秒。网络失败采用倍增退避，最长 10 秒。`more=true` 立即补拉。页面隐藏时清除计时器，恢复可见时立即补拉。组件卸载时停止请求和计时器。业务状态不挂载到 `window`。

启动接口返回 409 时，通过状态接口读取当前任务 ID 并接续。此方式保留共享 API 的错误契约，代价是竞争启动时多一次状态读取。

## 验收入口

使用现有 `wizard-b.spec.mjs`、`connect.spec.mjs` 和 `profiles.spec.mjs`。轮询退避用例应通过 Playwright fake clock 和请求次数验证。生产代码不提供轮询测试全局变量。共享结构检查使用 `npm run ui:check`，类型检查使用前端严格 TypeScript 配置。
