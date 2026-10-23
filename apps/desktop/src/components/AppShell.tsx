import type { ReactNode } from "react";

export function AppShell({
  children,
  sidebar,
  sidebarCollapsed = false,
}: {
  children: ReactNode;
  sidebar: ReactNode;
  sidebarCollapsed?: boolean;
}) {
  // 196px gives the 164px nav rows equal 16px gutters to the window edge and
  // to the workspace card, which sits inset from the other three edges.
  return (
    <div
      data-sidebar-collapsed={sidebarCollapsed}
      className="grid h-dvh grid-cols-[196px_minmax(0,1fr)] overflow-hidden bg-sidebar data-[sidebar-collapsed=true]:grid-cols-[56px_minmax(0,1fr)]"
    >
      {sidebar}
      <div className="min-h-0 min-w-0 pt-[var(--workspace-card-top)] pr-2 pb-2">
        <div className="h-full min-h-0 overflow-hidden rounded-lg border border-sidebar-border bg-background">
          {children}
        </div>
      </div>
    </div>
  );
}
