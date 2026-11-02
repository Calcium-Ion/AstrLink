import AppKit
import SwiftUI
import WidgetKit

/// Vendor marks as the app draws them (`ServiceKindIcon`), rasterized from
/// `@lobehub/icons` by `render-logos.mjs`. Asset catalogs need Xcode, so the
/// marks ship as plain PNGs.
enum QuotaLogo {
  /// Overridden by the preview renderer, which has no extension bundle.
  nonisolated(unsafe) static var resources: URL? = Bundle.main.resourceURL

  /// `kindMarks` in ServiceKindIcon.tsx, for the plan-backed kinds.
  static func mark(for kind: String) -> String? {
    switch kind {
    case "codex_subscription": return "codex"
    case "claude_subscription": return "claude"
    case "grok_subscription": return "grok"
    case "antigravity_subscription": return "antigravity"
    case "copilot_subscription": return "copilot"
    case "opencode_go": return "opencode"
    case "kimi_coding": return "kimi"
    case "glm_coding": return "zhipu"
    case "minimax_coding": return "minimax"
    default: return nil
    }
  }

  /// Single-color marks follow the text color, as `currentColor` does in the app.
  static let templates: Set<String> = ["grok", "copilot", "opencode", "kimi"]

  static func image(_ mark: String) -> NSImage? {
    guard let url = resources?.appendingPathComponent("Logos/\(mark).png") else { return nil }
    return NSImage(contentsOf: url)
  }
}

struct LogoView: View {
  var kind: String
  var size: CGFloat
  var palette: QuotaPalette

  var body: some View {
    Group {
      if let mark = QuotaLogo.mark(for: kind), let image = QuotaLogo.image(mark) {
        if QuotaLogo.templates.contains(mark) {
          Image(nsImage: image).resizable().renderingMode(.template)
            .foregroundStyle(palette.foreground)
        } else {
          ColorMark(image: image)
        }
      } else {
        // Droid publishes no mark; the app draws the same monogram tile.
        RoundedRectangle(cornerRadius: size * 5 / 24)
          .fill(palette.foreground)
          .overlay(
            Text("D").font(.system(size: size * 14 / 24, weight: .semibold))
              .foregroundStyle(palette.rendering == .fullColor ? palette.card : Color.black)
              .blendMode(palette.rendering == .fullColor ? .normal : .destinationOut)
          )
          .compositingGroup()
      }
    }
    .frame(width: size, height: size)
    .accessibilityHidden(true)
  }
}

/// A color mark stays recognizable when the system tints the widget.
private struct ColorMark: View {
  var image: NSImage

  var body: some View {
    if #available(macOS 15.0, *) {
      Image(nsImage: image).resizable().widgetAccentedRenderingMode(.desaturated)
    } else {
      Image(nsImage: image).resizable()
    }
  }
}

/// The small widget's 22 pt logo tile with a hairline border.
struct LogoTile: View {
  var kind: String
  var palette: QuotaPalette

  var body: some View {
    LogoView(kind: kind, size: 14, palette: palette)
      .frame(width: 22, height: 22)
      .background(RoundedRectangle(cornerRadius: 7).fill(palette.tile))
      .overlay(RoundedRectangle(cornerRadius: 7).strokeBorder(palette.line, lineWidth: 1))
  }
}
