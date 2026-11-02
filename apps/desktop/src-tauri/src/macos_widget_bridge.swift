// The host's side of the macOS widget, called from `macos_widget.rs`.
// WidgetCenter is a Swift-only API, so build.rs compiles this file into a
// static library linked into the app.
import Foundation
import Security
import WidgetKit

/// The App Group container this process is entitled to, as a malloc'd C
/// string the caller frees; null when the build carries no App Group (an
/// unbundled development run), so nothing is written for a widget.
@_cdecl("astrlink_widget_container")
public func astrlinkWidgetContainer() -> UnsafeMutablePointer<CChar>? {
  guard let task = SecTaskCreateFromSelf(nil),
    let value = SecTaskCopyValueForEntitlement(
      task, "com.apple.security.application-groups" as CFString, nil),
    let group = (value as? [String])?.first,
    let url = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: group)
  else { return nil }
  return strdup(url.path)
}

/// Asks WidgetKit to redraw AstrLink's widgets from the snapshot just written.
@_cdecl("astrlink_widget_reload")
public func astrlinkWidgetReload() {
  WidgetCenter.shared.reloadAllTimelines()
}

private let placedLock = NSLock()
nonisolated(unsafe) private var placedCount: Int32 = -1

/// Starts a query for how many AstrLink widgets are on the desktop; the
/// answer arrives asynchronously and is read with `astrlink_widget_placed`.
@_cdecl("astrlink_widget_query_placed")
public func astrlinkWidgetQueryPlaced() {
  WidgetCenter.shared.getCurrentConfigurations { result in
    let count = (try? result.get()).map { Int32(clamping: $0.count) } ?? -1
    placedLock.lock()
    placedCount = count
    placedLock.unlock()
  }
}

/// The last answer to `astrlink_widget_query_placed`; -1 before the first.
@_cdecl("astrlink_widget_placed")
public func astrlinkWidgetPlaced() -> Int32 {
  placedLock.lock()
  defer { placedLock.unlock() }
  return placedCount
}
