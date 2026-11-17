// Whether the page runs with the desktop host, apart from ./core so a test
// that mocks the commands keeps it.
/** Reports whether the page has the desktop host: MyGo's runtime. */
export function hasNativeHost(): boolean {
  return typeof window !== "undefined" && "mygo" in window;
}
