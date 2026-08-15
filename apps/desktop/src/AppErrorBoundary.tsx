import { Component, type ErrorInfo, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { SectionKicker } from "@/components/SectionKicker";

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
      <main
        className="grid min-h-screen place-items-center p-6"
        role="alert"
      >
        <Card className="w-full max-w-lg">
          <CardHeader>
            <SectionKicker>界面恢复</SectionKicker>
            <CardTitle className="text-xl">AstrLink 界面遇到问题</CardTitle>
            <CardDescription>
              Core 与本地配置不会因此被删除。重新加载界面即可继续。
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => window.location.reload()} type="button">
              重新加载
            </Button>
          </CardContent>
        </Card>
      </main>
    );
  }
}
