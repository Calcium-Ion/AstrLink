import { Blocks } from "@/components/icons";
import { cn } from "@/lib/utils";

/**
 * A plugin's icon as the plugin ships it, a text avatar for a plugin that
 * names one, or the plugins glyph for one with neither. It sits beside the
 * plugin's name, so it is decoration. A plugin's art is drawn for light
 * paper: on the dark theme it keeps a light plate.
 */
export function PluginIcon({
  icon,
  label,
  className,
}: {
  /** A data URL of a PNG or SVG image. */
  icon?: string;
  /** The text of a text avatar, shown when there is no image. */
  label?: string;
  className?: string;
}) {
  if (icon)
    return (
      <img
        alt=""
        aria-hidden="true"
        className={cn(
          "size-5 shrink-0 object-contain dark:rounded-sm dark:bg-foreground dark:p-px",
          className,
        )}
        data-slot="plugin-icon"
        draggable={false}
        src={icon}
      />
    );
  if (label)
    return (
      <span
        aria-hidden="true"
        className={cn(
          "inline-flex size-5 shrink-0 items-center justify-center overflow-hidden rounded-sm bg-muted text-[0.625rem] leading-none font-semibold tracking-tight text-text-secondary select-none",
          className,
        )}
        data-slot="plugin-icon"
      >
        {label}
      </span>
    );
  return (
    <Blocks
      aria-hidden="true"
      className={cn("size-5 shrink-0 text-muted-foreground", className)}
      data-slot="plugin-icon"
    />
  );
}

/**
 * The text avatar of a task plugin without an image: the label its icon
 * names (text:<label>), else the first two characters of its name. Other
 * plugins keep the plugins glyph.
 */
export function pluginAvatar(plugin: {
  name: string;
  task?: { icon?: string };
}): string | undefined {
  if (!plugin.task) return undefined;
  const named = plugin.task.icon?.startsWith("text:")
    ? plugin.task.icon.slice(5).trim()
    : "";
  const label = named || Array.from(plugin.name.trim()).slice(0, 2).join("");
  return label ? label.toUpperCase() : undefined;
}
