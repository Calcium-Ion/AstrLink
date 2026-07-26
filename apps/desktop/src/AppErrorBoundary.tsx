import { Component, type ErrorInfo, type ReactNode } from "react";

interface AppErrorBoundaryProps {
  children: ReactNode;
}

interface AppErrorBoundaryState {
  failed: boolean;
}

export class AppErrorBoundary extends Component<
  AppErrorBoundaryProps,
  AppErrorBoundaryState
> {
  state: AppErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): AppErrorBoundaryState {
    return { failed: true };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error("AstrLink interface render failed", error, info);
  }

  render(): ReactNode {
    if (!this.state.failed) return this.props.children;

    return (
      <main className="app-error" role="alert">
        <section className="app-error__card">
          <span className="eyebrow">界面恢复</span>
          <h1>AstrLink 界面遇到问题</h1>
          <p>Core 与本地配置不会因此被删除。重新加载界面即可继续。</p>
          <button
            className="btn-primary"
            onClick={() => window.location.reload()}
            type="button"
          >
            重新加载
          </button>
        </section>
      </main>
    );
  }
}
