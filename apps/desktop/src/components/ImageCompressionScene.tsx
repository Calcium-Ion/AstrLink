import { motion, useReducedMotion } from "motion/react";

import astrlinkLogo from "@/assets/astrlink-logo.svg";
import type { ImageCompression } from "@/failure-policy-model";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { formatImageBytes } from "@/request-trajectory-model";
import { sceneFrameClass } from "./SettingSceneBody";

/**
 * The example image of each mode and its decoded bytes: a flat screenshot,
 * which lossless shrinks, and a photo, which only lossy shrinks.
 */
const exampleImages = {
  lossless: { id: "screenshot", type: "png", bytes: 1_929_380 },
  lossy: { id: "photo", type: "jpg", bytes: 2_516_582 },
} as const;

type ExampleImage = (typeof exampleImages)[keyof typeof exampleImages];

/** The WebP of the mode's example. A photo's size climbs steeply with quality. */
function webpBytes({ mode, quality }: ImageCompression) {
  if (mode === "lossless") return 1_174_405;
  return Math.round(
    exampleImages.lossy.bytes * (0.06 + 0.74 * (quality / 100) ** 4),
  );
}

const thumbnailClass =
  "relative h-9 w-12 shrink-0 overflow-hidden rounded-sm bg-card shadow-sm ring-1 ring-border @[24rem]:h-[2.875rem] @[24rem]:w-16";

const screenshotLines = [
  "w-[78%] bg-primary/20",
  "w-[54%] bg-tide/40",
  "w-[66%] bg-primary/20",
  "w-[40%] bg-destructive/30",
  "w-[58%] bg-primary/20",
];

/** A small drawing of the image: a window with text, or a landscape. */
function Thumbnail({ id }: { id: ExampleImage["id"] }) {
  if (id === "screenshot")
    return (
      <span className={thumbnailClass}>
        <span className="flex h-[9px] items-center gap-0.5 bg-muted pl-1">
          {[0, 1, 2].map((dot) => (
            <span
              className="size-[3px] rounded-full bg-muted-foreground/30"
              key={dot}
            />
          ))}
        </span>
        {screenshotLines.map((line) => (
          <span
            className={cn("mx-1.5 mt-[3px] block h-[3px] rounded-full", line)}
            key={line}
          />
        ))}
      </span>
    );
  return (
    <span className={cn(thumbnailClass, "bg-tide-wash")}>
      <span className="absolute top-1.5 right-2 size-2.5 rounded-full bg-warning/70" />
      <span className="absolute -bottom-5 -left-2 size-10 rotate-45 rounded-sm bg-success/45" />
      <span className="absolute -right-3 -bottom-6 size-11 rotate-45 rounded-sm bg-success/60" />
    </span>
  );
}

/** One side of an image's trip: its file name, its size and a bar for it. */
function ImageFile({
  compressed = false,
  image,
  bytes,
}: {
  compressed?: boolean;
  image: ExampleImage;
  bytes: number;
}) {
  const t = useT();
  const reducedMotion = useReducedMotion();
  return (
    <span className="flex min-w-0 items-center gap-2.5">
      <Thumbnail id={image.id} />
      <span className="grid min-w-0 flex-1 gap-0.5">
        <span className="flex min-w-0 text-micro text-muted-foreground">
          <span className="truncate">
            {t(`imageCompression.demo.files.${image.id}`)}
          </span>
          <span className="shrink-0">
            {compressed ? ".webp" : `.${image.type}`}
          </span>
        </span>
        <span
          className={cn(
            "text-base leading-tight font-semibold tracking-tight tabular-nums",
            compressed && "text-success-foreground",
          )}
        >
          {formatImageBytes(bytes)}
        </span>
        <span className="h-[5px] overflow-hidden rounded-full bg-border">
          <motion.span
            animate={{ width: `${(bytes / image.bytes) * 100}%` }}
            className={cn(
              "block h-full rounded-full",
              compressed ? "bg-success" : "bg-muted-foreground/40",
            )}
            initial={false}
            transition={{ duration: reducedMotion ? 0 : 0.35 }}
          />
        </span>
      </span>
    </span>
  );
}

/**
 * What the saved setting does to an example image on its way through
 * AstrLink. Off, it arrives as it left.
 */
export function ImageCompressionScene({ value }: { value: ImageCompression }) {
  const image = exampleImages[value.mode];
  const webp = value.enabled ? webpBytes(value) : null;
  const compressed = webp !== null;
  return (
    <div
      aria-hidden="true"
      className={cn(
        sceneFrameClass,
        "@container grid content-center px-3 py-2.5",
      )}
      data-mode={value.enabled ? value.mode : "off"}
      data-visual="compression"
    >
      <div
        className="grid grid-cols-[minmax(0,2fr)_minmax(2.75rem,1fr)_minmax(0,2fr)] items-center gap-3"
        data-compressed={compressed || undefined}
        data-image={image.id}
      >
        <ImageFile bytes={image.bytes} image={image} />
        <span
          className={cn(
            "relative h-0.5 rounded-full",
            compressed
              ? "bg-linear-to-r from-muted-foreground/30 to-success"
              : "bg-muted-foreground/30",
          )}
        >
          <span className="absolute top-1/2 left-1/2 flex size-[1.625rem] -translate-1/2 items-center justify-center rounded-md bg-card shadow-sm ring-1 ring-border">
            <img
              alt=""
              className="size-4"
              height={16}
              src={astrlinkLogo}
              width={16}
            />
          </span>
          <span
            className={cn(
              "absolute -top-1 -right-0.5 border-y-[5px] border-l-[7px] border-y-transparent",
              compressed ? "border-l-success" : "border-l-muted-foreground/40",
            )}
          />
        </span>
        <ImageFile
          bytes={webp ?? image.bytes}
          compressed={compressed}
          image={image}
        />
      </div>
    </div>
  );
}
