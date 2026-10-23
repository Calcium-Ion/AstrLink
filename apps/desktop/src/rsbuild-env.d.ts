/// <reference types="@rsbuild/core/types" />

declare global {
  interface Window {
    __ASTRLINK_DESKTOP_PLATFORM__?: string;
    __ASTRLINK_ISOLATED__?: boolean;
  }
}

export {};
