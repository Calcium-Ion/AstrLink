import { Boxes, ChevronRight, ScanText, Search } from "@/components/icons";

import { toolNameKind } from "../tool-call-summary";

/** The mark for what a tool does: files, commands, searches, or anything else. */
export function ToolKindIcon({
  className,
  name,
}: {
  className?: string;
  name: string;
}) {
  const kind = toolNameKind(name);
  const Icon =
    kind === "file"
      ? ScanText
      : kind === "search"
        ? Search
        : kind === "command"
          ? ChevronRight
          : Boxes;
  return <Icon aria-hidden="true" className={className} />;
}
