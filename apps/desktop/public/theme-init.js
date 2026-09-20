// Apply the cached preference before styles and the application are painted.
// The native preferences store reconciles this cache when IPC is ready.
(() => {
  let preference = "system";
  try {
    const cached = localStorage.getItem("astrlink.theme");
    if (cached === "light" || cached === "dark") preference = cached;
  } catch {}
  const theme = preference === "system"
    ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light")
    : preference;
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
})();
