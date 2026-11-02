import SwiftUI
import WidgetKit

/// How the system draws the widget. Full color is the desktop in front; when
/// it is not, macOS drains color (vibrant) or tints it (accented), so state
/// must never rest on color alone.
enum QuotaRendering {
  case fullColor
  case vibrant
  case accented

  init(_ mode: WidgetRenderingMode) {
    if mode == .vibrant {
      self = .vibrant
    } else if mode == .accented {
      self = .accented
    } else {
      self = .fullColor
    }
  }
}

private struct QuotaRenderingOverrideKey: EnvironmentKey {
  static let defaultValue: QuotaRendering? = nil
}

private struct QuotaFamilyOverrideKey: EnvironmentKey {
  static let defaultValue: WidgetFamily? = nil
}

extension EnvironmentValues {
  /// Lets the preview renderer draw the vibrant and accented looks, which
  /// only the system can switch on in a real widget.
  var quotaRenderingOverride: QuotaRendering? {
    get { self[QuotaRenderingOverrideKey.self] }
    set { self[QuotaRenderingOverrideKey.self] = newValue }
  }

  /// The size outside WidgetKit, for the preview renderer.
  var quotaFamilyOverride: WidgetFamily? {
    get { self[QuotaFamilyOverrideKey.self] }
    set { self[QuotaFamilyOverrideKey.self] = newValue }
  }
}

/// The app's flat tokens (`globals.css`), light and dark. No gradients.
struct QuotaPalette {
  var rendering: QuotaRendering
  var card: Color
  var tile: Color
  var foreground: Color
  var secondary: Color
  var muted: Color
  var line: Color
  var track: Color
  var success: Color
  var warning: Color
  var warningText: Color
  var danger: Color

  init(scheme: ColorScheme, rendering: QuotaRendering) {
    self.rendering = rendering
    if rendering != .fullColor {
      // Hierarchy through opacity; the system supplies the color.
      card = .clear
      tile = .clear
      foreground = .primary
      secondary = .primary.opacity(0.78)
      muted = .primary.opacity(0.58)
      line = .primary.opacity(0.16)
      track = .primary.opacity(0.18)
      success = .primary
      warning = .primary
      warningText = .primary
      danger = .primary
      return
    }
    if scheme == .dark {
      card = Color(hex: 0x202328)
      tile = Color(hex: 0x1A1C20)
      foreground = Color(hex: 0xCBD0D8)
      secondary = Color(hex: 0xADB5C0)
      muted = Color(hex: 0x969EAA)
      line = Color(hex: 0x363C45)
      track = Color(hex: 0x30343B)
      success = Color(hex: 0x78AA94)
      warning = Color(hex: 0xBFA16E)
      warningText = Color(hex: 0xCFB58D)
      danger = Color(hex: 0xCD8C96)
    } else {
      card = Color(hex: 0xFFFFFF)
      tile = Color(hex: 0xFFFFFF)
      foreground = Color(hex: 0x1A2A3D)
      secondary = Color(hex: 0x45566D)
      muted = Color(hex: 0x6B788C)
      line = Color(hex: 0xE5E7F0)
      track = Color(hex: 0xECEEF3)
      success = Color(hex: 0x1F9D6B)
      warning = Color(hex: 0xD2911A)
      warningText = Color(hex: 0x8A6013)
      danger = Color(hex: 0xC2384F)
    }
  }

  func fill(_ tone: QuotaTone) -> Color {
    switch tone {
    case .normal: return success
    case .warning: return warning
    case .critical: return danger
    }
  }

  /// Percent text keeps the default foreground unless the quota runs low.
  func number(_ tone: QuotaTone) -> Color {
    switch tone {
    case .normal: return foreground
    case .warning: return warningText
    case .critical: return danger
    }
  }

  /// The secondary line is quieter than a number when all is well.
  func line(_ tone: QuotaTone) -> Color {
    tone == .normal ? secondary : number(tone)
  }
}

extension Color {
  init(hex: UInt32) {
    self.init(
      .sRGB,
      red: Double((hex >> 16) & 0xFF) / 255,
      green: Double((hex >> 8) & 0xFF) / 255,
      blue: Double(hex & 0xFF) / 255)
  }
}
