import { Component, type ErrorInfo, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app/App";
import { ErrorState, PageHeader } from "./components/primitives";
import "./styles/index.css";

class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) { return { error }; }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error(error, info.componentStack); }
  render() { return this.state.error ? <div className="page"><PageHeader title="MPX" /><ErrorState title="页面" error={this.state.error} onRetry={() => location.reload()} /></div> : this.props.children; }
}
// 登录态在 App 里自己拿(/api/admin/meta → 200 工作区 / 401 登录页),
// 入口只管挂上树,不再先取一次 meta。
createRoot(document.getElementById("root")!).render(<ErrorBoundary><App /></ErrorBoundary>);
