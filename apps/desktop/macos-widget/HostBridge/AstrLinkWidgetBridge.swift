// The host's handle on WidgetKit, called from internal/widget. WidgetCenter
// is a Swift-only API, so the macOS build compiles this file into
// libastrlink_widget_bridge.dylib among the app's resources, which the host
// opens at run time. Without the library the host still writes snapshots,
// and widgets redraw on their own timeline.
import Foundation
import WidgetKit

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
