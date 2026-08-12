import type { ReactNode } from "react";

export function AppShell({
  children,
  sidebar,
}: {
  children: ReactNode;
  sidebar: ReactNode;
}) {
  return (
    <div className="grid min-h-screen grid-cols-[208px_minmax(0,1fr)] max-[900px]:grid-cols-[68px_minmax(0,1fr)]">
      {sidebar}
      <div className="min-w-0">{children}</div>
    </div>
  );
}
