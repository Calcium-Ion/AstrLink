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
import { i18n } from "./i18n";

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
            <SectionKicker>{i18n.t("errorBoundary.kicker")}</SectionKicker>
            <CardTitle className="text-xl">{i18n.t("errorBoundary.title")}</CardTitle>
            <CardDescription>
              {i18n.t("errorBoundary.description")}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => window.location.reload()} type="button">
              {i18n.t("errorBoundary.reload")}
            </Button>
          </CardContent>
        </Card>
      </main>
    );
  }
}
