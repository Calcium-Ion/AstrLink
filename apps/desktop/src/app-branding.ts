import logo from "./assets/astrlink-logo.svg";
import developmentLogo from "./assets/astrlink-dev-logo.svg";

export function getAppBranding() {
  const development =
    (typeof window !== "undefined"
      ? window.__ASTRLINK_ISOLATED__
      : undefined) ?? process.env.NODE_ENV === "development";

  return {
    development,
    name: development ? "AstrLink Dev" : "AstrLink",
    logo: development ? developmentLogo : logo,
  };
}
