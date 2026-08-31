/**
 * Triggers a browser/WebView download without a native save dialog.
 * WKWebView typically lands the file in the user's Downloads folder.
 */
export function downloadTextFile(
  filename: string,
  content: string,
  mimeType = "text/plain;charset=utf-8",
): void {
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  anchor.rel = "noopener";
  anchor.style.display = "none";
  document.body.append(anchor);
  try {
    anchor.click();
  } finally {
    anchor.remove();
    URL.revokeObjectURL(url);
  }
}
