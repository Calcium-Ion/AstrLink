// The desktop pets page: its own place in the sidebar, not a settings tab.
import { useEffect, useRef, useState } from "react";

import { FormMessage } from "@/components/FormMessage";
import { LoadingState } from "@/components/LoadingState";
import { ScrollWorkspace } from "@/components/ScrollWorkspace";

import { getPreferences, updateDesktopToys } from "../bridge";
import { i18n, useT } from "../i18n";
import { PageHeader } from "../PageHeader";
import type {
  DesktopToysPreferences,
  SettingsSnapshot,
} from "../preferences-model";
import { getDesktopPlatform } from "../window-chrome";
import { useWorkspaceSnapshot } from "../workspace-snapshots";
import { DesktopToysSettings } from "./DesktopToysSettings";

/** The host says whether the pet can come out here: not on Wayland. */
export const hasDesktopToys = () =>
  getDesktopPlatform() !== "browser" &&
  window.__ASTRLINK_DESKTOP_TOYS__ === true;

const messageOf = (error: unknown) =>
  error instanceof Error ? error.message : i18n.t("settings.failed");

export function DesktopToysPage() {
  const t = useT();
  // The same cache the settings page reads, so neither shows stale values.
  const [settings, cacheSettings] =
    useWorkspaceSnapshot<SettingsSnapshot | null>(
      "preferences",
      null,
      "desktop",
    );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const saves = useRef(0);

  useEffect(() => {
    let cancelled = false;
    const version = saves.current;
    void getPreferences()
      .then((next) => {
        if (!cancelled && saves.current === version) cacheSettings(next);
      })
      .catch((reason: unknown) => {
        if (!cancelled) setError(messageOf(reason));
      });
    return () => {
      cancelled = true;
    };
  }, [cacheSettings]);

  const apply = async (next: DesktopToysPreferences) => {
    if (busy || !settings) return;
    const previous = settings;
    saves.current += 1;
    setBusy(true);
    setError(null);
    cacheSettings({
      ...settings,
      values: { ...settings.values, desktop_toys: next },
    });
    try {
      cacheSettings(await updateDesktopToys(next));
    } catch (reason) {
      cacheSettings(previous);
      setError(messageOf(reason));
    } finally {
      setBusy(false);
    }
  };

  return (
    <ScrollWorkspace
      className="gap-0"
      contentClassName="pb-2"
      contentSlot="desktop-toys-content"
      header={<PageHeader title={t("desktopToys.title")} variant="compact" />}
    >
      {error ? (
        <FormMessage className="mb-3 [overflow-wrap:anywhere]" tone="error">
          {error}
        </FormMessage>
      ) : null}
      {settings ? (
        <DesktopToysSettings
          disabled={busy}
          onChange={(next) => void apply(next)}
          value={settings.values.desktop_toys}
        />
      ) : error ? null : (
        <LoadingState label={t("settings.loading")} />
      )}
    </ScrollWorkspace>
  );
}
