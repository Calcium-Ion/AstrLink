# Animated Icons

AstrLink's UI icons come from [lucide-animated](https://lucide-animated.com/)
by pqoqubbw, under the included MIT [LICENSE](./LICENSE). The selected official
registry components were imported on 2026-09-18. Each file links to its source.

Import icons and the `AnimatedIcon` type from `@/components/icons`. Artwork,
Motion variants, and transitions are from the official components. The common
`createAnimatedIcon` wrapper replaces their repeated div/ref/hover boilerplate
with a single SVG, preserving SVG props, refs, Radix `asChild`, and existing
control sizing. Root SVG animations run on an inner group so CSS rotation and
loading indicators do not conflict with Motion transforms.

Icons animate when their containing control is hovered or focused, and stop
on leaving it. Disabled controls and `prefers-reduced-motion` suppress these
animations. Standalone icons respond to their own hover/focus. Loading icons
use the existing CSS spin with `motion-reduce:animate-none` and disable hover
animation. Radio selection dots are CSS control indicators, not icon glyphs.

The upstream library does not provide every static Lucide icon. Consumers use
explicit import aliases for these semantic replacements:

| Previous Icon         | Animated Icon     |
| --------------------- | ----------------- |
| House                 | Home              |
| KeyRound              | Key               |
| Cable                 | Connect           |
| Ellipsis              | Menu              |
| FlaskConical          | Flask             |
| ListFilter, Settings2 | SlidersHorizontal |
| Info                  | CircleHelp        |
| OctagonX              | Ban               |
| Pencil                | SquarePen         |
| Pin, PinOff           | MapPin, MapPinOff |
| ScanLine              | ScanText          |
| Trash2                | Shredder          |
| TriangleAlert         | BadgeAlert        |
| Loader2               | LoaderCircle      |

AI provider/model marks continue to use `@lobehub/icons` and its public model
mapping. Unknown models use the animated `Brain` instead of that package's
static fallback. AstrLink branding
and SVG data visualizations are separate from the UI icon library.
