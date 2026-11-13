// Whether the page runs with the desktop host, apart from ./core so a test
// that mocks the commands keeps it.
/**
 * Reports whether the page has the desktop host. Tests still stub the Tauri
 * marker, which counts too.
 */
export function hasNativeHost(): boolean {
  return (
    typeof window !== "undefined" &&
    ("mygo" in window || "__TAURI_INTERNALS__" in window)
  );
}
