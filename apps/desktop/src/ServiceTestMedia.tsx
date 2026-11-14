import { useState } from "react";

import { cn } from "@/lib/utils";
import { ExternalLink } from "./components/ExternalLink";
import { Download } from "./components/icons";
import { Button } from "./components/ui/button";
import { saveGatewayFile } from "./bridge";
import { controlErrorMessage } from "./control-error";
import { isWebEdition } from "./edition";
import { useT } from "./i18n";
import { notify } from "./notify";
import type { ServiceTestMedia } from "./service-test-model";

/** The extension of a file of a media type, for the name it saves under. */
const extensions: Record<string, string> = {
  "image/png": "png",
  "image/jpeg": "jpg",
  "image/webp": "webp",
  "image/gif": "gif",
  "video/mp4": "mp4",
  "video/webm": "webm",
  "video/quicktime": "mov",
  "audio/mpeg": "mp3",
  "audio/mp4": "m4a",
  "audio/aac": "aac",
  "audio/wav": "wav",
  "audio/x-wav": "wav",
  "audio/ogg": "ogg",
  "audio/flac": "flac",
};

/** What a kind of file most likely is when its media type is unknown. */
const kindExtensions: Record<ServiceTestMedia["kind"], string> = {
  image: "png",
  video: "mp4",
  audio: "mp3",
  file: "",
};

/** The name a generated file saves under: its key and an extension. */
export function mediaFileName(item: ServiceTestMedia): string {
  const type = item.mime_type?.split(";")[0].trim().toLowerCase() ?? "";
  const extension = extensions[type] ?? kindExtensions[item.kind];
  return extension ? `${item.name}.${extension}` : item.name;
}

/**
 * What a test of a task plugin's provider generated: each image shown, each
 * video and piece of music playable, any file named, each with a way to
 * open it and to download it. The links are the gateway's own.
 */
export function ServiceTestMediaView({ media }: { media: ServiceTestMedia[] }) {
  const t = useT();
  const [saving, setSaving] = useState<string | null>(null);
  // A lone image or video fills the view, scaled down to fit, so its
  // actions stay in sight; several files sit side by side.
  const single =
    media.length === 1 &&
    (media[0].kind === "image" || media[0].kind === "video");
  const visual = single ? "h-full min-h-0" : "h-32";

  async function save(item: ServiceTestMedia) {
    setSaving(item.url);
    try {
      const path = await saveGatewayFile(item.url, mediaFileName(item));
      // The browser shows its own download; the desktop says where.
      if (path && !isWebEdition)
        notify.success(t("serviceTest.media.saved", { path }));
    } catch (error) {
      notify.error(
        controlErrorMessage(error, t("serviceTest.media.saveFailed")),
      );
    } finally {
      setSaving(null);
    }
  }

  return (
    <div
      className={cn(
        "grid gap-3",
        single
          ? "h-full min-h-0 grid-rows-[minmax(0,1fr)]"
          : "grid-cols-[repeat(auto-fit,minmax(14rem,1fr))]",
      )}
      data-testid="service-test-media"
    >
      {media.map((item) => (
        <figure
          className={cn(
            "grid min-w-0 gap-2 rounded-md border bg-muted/20 p-2",
            single && "min-h-0 grid-rows-[minmax(0,1fr)_auto]",
          )}
          data-kind={item.kind}
          key={item.url}
        >
          {item.kind === "image" ? (
            <img
              alt={item.name}
              className={cn("w-full object-scale-down", visual)}
              src={item.url}
            />
          ) : item.kind === "video" ? (
            <video
              className={cn("w-full rounded-sm bg-black", visual)}
              controls
              preload="metadata"
              src={item.url}
            />
          ) : item.kind === "audio" ? (
            <audio
              className="w-full"
              controls
              preload="metadata"
              src={item.url}
            />
          ) : null}
          <figcaption className="flex min-w-0 items-center gap-2 text-xs">
            <span className="min-w-0 flex-1 truncate text-muted-foreground">
              {t(`serviceTest.media.kinds.${item.kind}`)}
              {" · "}
              <span className="font-mono">{mediaFileName(item)}</span>
            </span>
            <ExternalLink className="shrink-0 text-xs" href={item.url}>
              {t("serviceTest.media.open")}
            </ExternalLink>
            <Button
              disabled={saving === item.url}
              onClick={() => void save(item)}
              size="xs"
              type="button"
              variant="outline"
            >
              <Download aria-hidden="true" />
              {t("serviceTest.media.download")}
            </Button>
          </figcaption>
        </figure>
      ))}
    </div>
  );
}
