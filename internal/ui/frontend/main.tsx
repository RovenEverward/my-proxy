import { Component, type ErrorInfo, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./app/App";
import { api } from "./runtime/api";
import type { Meta } from "./runtime/types";
import { ErrorState, LoadingTable, PageHeader } from "./components/primitives";
import "./styles/index.css";

class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) { return { error }; }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error(error, info.componentStack); }
  render() { return this.state.error ? <div className="page"><PageHeader title="MPX" /><ErrorState title="页面" error={this.state.error} onRetry={() => location.reload()} /></div> : this.props.children; }
}
const root = createRoot(document.getElementById("root")!);
root.render(<div className="page"><PageHeader title="MPX" /><LoadingTable /></div>);
api<Meta>("/ui/meta").then(meta => root.render(<ErrorBoundary><App meta={meta} /></ErrorBoundary>)).catch((error: Error) => root.render(<div className="page"><PageHeader title="MPX" /><ErrorState title="本机服务" error={error} onRetry={() => location.reload()} /></div>));
