// Types of the SDK a plugin's interface code imports (plugin platform v2,
// §6). The plugin's client entry, src/client.tsx, is built into client.js;
// the desktop runs it in its main window once the user grants ui.code, and
// the web console when its settings allow. React and the components below
// are the window's own, so a plugin bundles none of them: install
// @types/react for the editor only.

declare module "@astrlink/plugin/client" {
  import type * as React from "react";

  /**
   * The places an interface can fill, and the context each passes its
   * component:
   * - page: a page of its own, under Plugins in the sidebar.
   * - plugin.tab: a tab on the plugin's own page; context { plugin }.
   * - overview.widget: a card at the bottom of the overview.
   * - request.tab: a tab of a conversation's detail; context { session, request }.
   * - session.section: a section of a conversation's plugins tab; context { session }.
   * - service.section: a section of a provider's connection tab; context { service }.
   */
  export type Slot =
    | "page"
    | "plugin.tab"
    | "overview.widget"
    | "request.tab"
    | "session.section"
    | "service.section";

  export interface SlotProps {
    context: unknown;
  }

  export interface SlotOptions {
    /** The page's or tab's name; the plugin's name when absent. */
    title?: string;
    /** Lower comes first. */
    order?: number;
  }

  /** Where navigate() goes: a page of AstrLink, or a page the plugin registered. */
  export type Navigation =
    | { page: "overview" | "records" | "plugins" | "settings" }
    | { page: "plugin"; entry?: string };

  /** The plugin's key. */
  export const key: string;

  /** Registers a component for a slot; the function returned takes it back. */
  export function registerSlot(
    slot: Slot,
    component: React.ComponentType<SlotProps>,
    options?: SlotOptions,
  ): () => void;

  /**
   * Calls the plugin's onUiCall hook, which runs in its sandbox under its
   * grants: the interface code itself reaches no network.
   */
  export function call<T = unknown>(name: string, input?: unknown): Promise<T>;

  /** The plugin's settings, as its settings form saves them. */
  export function getSettings(): Promise<Record<string, unknown>>;
  /** Replaces the plugin's settings. */
  export function saveSettings(
    config: Record<string, unknown>,
  ): Promise<Record<string, unknown>>;

  /** The interface language: en, zh-CN or ja. */
  export function language(): string;
  /**
   * The text of the interface language, from one per language; {{name}}
   * takes vars.name.
   */
  export function t(
    texts: string | Record<string, string>,
    vars?: Record<string, unknown>,
  ): string;
  export function navigate(to: Navigation): void;
  export function toast(
    message: string,
    tone?: "info" | "success" | "warning" | "error",
  ): void;
  /** The theme in effect, for a component that draws its own colors. */
  export function useTheme(): "light" | "dark";
  /** The interface language, rendering again when it changes. */
  export function useLanguage(): string;

  /** A component of the AstrLink interface; its props are the window's. */
  type HostComponent = React.ComponentType<{
    [prop: string]: unknown;
    children?: React.ReactNode;
  }>;

  export const Badge: HostComponent;
  export const Button: HostComponent;
  export const Checkbox: HostComponent;
  export const ConfirmDialog: HostComponent;
  export const DataField: HostComponent;
  export const DataRow: HostComponent;
  export const EmptyState: HostComponent;
  export const Field: HostComponent;
  export const FormMessage: HostComponent;
  export const HelpPopover: HostComponent;
  export const Input: HostComponent;
  export const Label: HostComponent;
  export const LoadingState: HostComponent;
  export const Panel: HostComponent;
  export const PanelHeader: HostComponent;
  export const StatusBadge: HostComponent;
  export const Switch: HostComponent;
  export const Tabs: HostComponent;
  export const TabsContent: HostComponent;
  export const TabsList: HostComponent;
  export const TabsTrigger: HostComponent;
  export const Textarea: HostComponent;
}
