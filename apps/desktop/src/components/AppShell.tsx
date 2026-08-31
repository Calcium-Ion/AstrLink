import type { ReactNode } from "react";

export function AppShell({
  children,
  sidebar,
}: {
  children: ReactNode;
  sidebar: ReactNode;
}) {
  return (
    <div className="grid h-dvh overflow-hidden grid-cols-[224px_minmax(0,1fr)] max-[900px]:grid-cols-[56px_minmax(0,1fr)]">
      {sidebar}
      <div className="min-h-0 min-w-0 overflow-hidden">{children}</div>
    </div>
  );
}
