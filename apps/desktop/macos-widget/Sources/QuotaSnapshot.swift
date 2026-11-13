import Foundation

/// What the AstrLink host writes into the shared App Group container: the
/// tray's plan digest, in tray order, plus the app settings the widget follows.
/// The host owns every upstream read; the widget only reads this file.
struct QuotaSnapshot: Codable, Equatable {
  static let version = 1
  static let fileName = "quota-widget.json"

  var version: Int
  /// When the host last read the plan quotas.
  var generatedAt: Date
  /// The app's language setting: "zh-CN", "en" or "ja".
  var locale: String
  var quotaDisplay: QuotaDisplay
  var plans: [QuotaPlan]

  enum CodingKeys: String, CodingKey {
    case version
    case generatedAt = "generated_at"
    case locale
    case quotaDisplay = "quota_display"
    case plans
  }
}

/// `QuotaDisplayMode` in the host preferences.
enum QuotaDisplay: String, Codable {
  case remaining
  case used
}

struct QuotaPlan: Codable, Equatable, Identifiable {
  /// The service id; stable across renames, so a configured widget keeps it.
  var id: String
  var name: String
  /// One of the host's `SUBSCRIPTION_KINDS`; picks the logo.
  var kind: String
  var windows: [QuotaWindow]
}

struct QuotaWindow: Codable, Equatable {
  /// Provider-named limit (Claude "Opus", Kimi "Monthly"); nil for the
  /// primary and secondary windows.
  var label: String?
  var limitWindowSeconds: Int?
  var secondary: Bool
  var usedPercent: Double
  var resetAt: Date?

  enum CodingKeys: String, CodingKey {
    case label
    case limitWindowSeconds = "limit_window_seconds"
    case secondary
    case usedPercent = "used_percent"
    case resetAt = "reset_at"
  }
}

extension QuotaSnapshot {
  static func decode(_ data: Data) -> QuotaSnapshot? {
    let decoder = JSONDecoder()
    decoder.dateDecodingStrategy = .iso8601
    guard let snapshot = try? decoder.decode(QuotaSnapshot.self, from: data),
      snapshot.version == version
    else { return nil }
    return snapshot
  }

  /// The snapshot in the App Group the extension's Info.plist names. Release
  /// and development builds name different groups, so a development host
  /// never feeds the release widget.
  static func load(bundle: Bundle = .main) -> QuotaSnapshot? {
    guard let group = bundle.object(forInfoDictionaryKey: "AstrLinkAppGroup") as? String,
      let container = FileManager.default.containerURL(
        forSecurityApplicationGroupIdentifier: group),
      let data = try? Data(contentsOf: container.appendingPathComponent(fileName))
    else { return nil }
    return decode(data)
  }
}
