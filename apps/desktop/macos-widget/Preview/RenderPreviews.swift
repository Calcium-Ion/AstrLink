import AppKit
import SwiftUI
import WidgetKit

/// Draws the widget's sizes, appearances and states offscreen, since widgets
/// have no previews without Xcode. Builds with swiftc against the extension
/// sources: `bun scripts/build-macos-widget.mjs --render <dir>`.
@main
struct RenderPreviews {
  @MainActor
  static func main() {
    let arguments = CommandLine.arguments
    guard arguments.count == 3 else {
      print("usage: render-previews <output dir> <macos-widget/Resources>")
      exit(2)
    }
    let output = URL(fileURLWithPath: arguments[1], isDirectory: true)
    QuotaLogo.resources = URL(fileURLWithPath: arguments[2], isDirectory: true)
    try? FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)

    for scheme in [ColorScheme.light, .dark] {
      let name = scheme == .light ? "light" : "dark"
      write(Board(scheme: scheme, sections: deskSections()), to: output, "a2-\(name).png")
      write(Board(scheme: scheme, sections: stateSections()), to: output, "states-\(name).png")
      write(
        Board(scheme: scheme, sections: languageSections()), to: output, "languages-\(name).png")
      write(
        Board(scheme: scheme, sections: renderingSections(), wallpaper: true), to: output,
        "rendering-\(name).png")
    }
  }

  @MainActor
  static func write<V: View>(_ view: V, to directory: URL, _ name: String) {
    let renderer = ImageRenderer(content: view)
    renderer.scale = 2
    guard let image = renderer.nsImage, let tiff = image.tiffRepresentation,
      let png = NSBitmapImageRep(data: tiff)?.representation(using: .png, properties: [:])
    else {
      print("could not render \(name)")
      exit(1)
    }
    try! png.write(to: directory.appendingPathComponent(name))
    print(directory.appendingPathComponent(name).path)
  }

  // MARK: Sample data: Sunday 2026-11-01 15:20 local, as in the mockup.

  static let calendar = Calendar.current
  static let now = at(day: 1, hour: 15, minute: 20)

  static func at(day: Int, hour: Int, minute: Int) -> Date {
    calendar.date(
      from: DateComponents(year: 2026, month: 11, day: day, hour: hour, minute: minute))!
  }

  static func window(
    _ seconds: Int?, used: Double, reset: Date?, secondary: Bool = false, label: String? = nil
  ) -> QuotaWindow {
    QuotaWindow(
      label: label, limitWindowSeconds: seconds, secondary: secondary, usedPercent: used,
      resetAt: reset)
  }

  static func plan(
    _ name: String, _ kind: String, five: Double, fiveReset: Date, week: Double, weekReset: Date
  ) -> QuotaPlan {
    QuotaPlan(
      id: name, name: name, kind: kind,
      windows: [
        window(18_000, used: five, reset: fiveReset),
        window(604_800, used: week, reset: weekReset, secondary: true),
      ])
  }

  static let codex = plan(
    "Codex", "codex_subscription", five: 36, fiveReset: at(day: 1, hour: 17, minute: 18),
    week: 59, weekReset: at(day: 4, hour: 9, minute: 0))
  static let claude = plan(
    "Claude Code", "claude_subscription", five: 88, fiveReset: at(day: 1, hour: 16, minute: 7),
    week: 42, weekReset: at(day: 6, hour: 10, minute: 0))
  static let glm = plan(
    "GLM Coding Plan", "glm_coding", five: 100, fiveReset: at(day: 1, hour: 16, minute: 20),
    week: 24, weekReset: at(day: 7, hour: 8, minute: 0))
  static let kimi = plan(
    "Kimi Coding", "kimi_coding", five: 13, fiveReset: at(day: 1, hour: 18, minute: 20),
    week: 21, weekReset: at(day: 5, hour: 12, minute: 0))
  static let weeklyOut = plan(
    "Codex Team", "codex_subscription", five: 20, fiveReset: at(day: 1, hour: 19, minute: 5),
    week: 100, weekReset: at(day: 4, hour: 9, minute: 0))
  static let kimiMonthly = QuotaPlan(
    id: "Kimi Monthly", name: "Kimi Coding", kind: "kimi_coding",
    windows: [window(nil, used: 21, reset: at(day: 30, hour: 8, minute: 0), label: "Monthly")])
  static let copilot = QuotaPlan(
    id: "Copilot", name: "GitHub Copilot", kind: "copilot_subscription",
    windows: [window(2_592_000, used: 47, reset: at(day: 30, hour: 8, minute: 0))])
  static let minimax = plan(
    "MiniMax Coding Plan", "minimax_coding", five: 52, fiveReset: at(day: 1, hour: 20, minute: 0),
    week: 30, weekReset: at(day: 2, hour: 9, minute: 0))
  static let droid = plan(
    "Droid", "droid_subscription", five: 70, fiveReset: at(day: 1, hour: 17, minute: 45),
    week: 81, weekReset: at(day: 2, hour: 9, minute: 0))

  static func snapshot(
    _ plans: [QuotaPlan], locale: String = "zh-CN", mode: QuotaDisplay = .remaining,
    age: TimeInterval = 120
  ) -> QuotaSnapshot {
    QuotaSnapshot(
      version: 1, generatedAt: now.addingTimeInterval(-age), locale: locale, quotaDisplay: mode,
      plans: plans)
  }

  static let mockup = snapshot([codex, claude, glm, kimi])

  // MARK: Boards

  static func desk(
    _ title: String, _ snapshot: QuotaSnapshot, smalls: [String] = ["Codex", "GLM Coding Plan"]
  )
    -> Section
  {
    Section(
      title: title,
      cells: [
        Cell(.systemSmall, snapshot, planID: smalls[0]),
        Cell(.systemSmall, snapshot, planID: smalls[1]),
        Cell(.systemMedium, snapshot), Cell(.systemLarge, snapshot),
      ], desk: true)
  }

  static func deskSections() -> [Section] {
    [desk("A2 · mockup data", mockup)]
  }

  static func stateSections() -> [Section] {
    let stale = snapshot([codex, claude, glm, kimi], age: 25 * 60)
    let many = snapshot([codex, claude, glm, kimi, copilot, minimax])
    let mixed = snapshot([kimiMonthly, codex, copilot, weeklyOut])
    return [
      Section(
        title: "Small · warning, weekly used up, one window, stale, droid, used mode",
        cells: [
          Cell(.systemSmall, mockup, planID: "Claude Code"),
          Cell(.systemSmall, snapshot([weeklyOut]), planID: "Codex Team"),
          Cell(.systemSmall, snapshot([kimiMonthly]), planID: "Kimi Monthly"),
          Cell(.systemSmall, stale, planID: "Codex"),
          Cell(.systemSmall, snapshot([droid]), planID: "Droid"),
          Cell(.systemSmall, snapshot([codex], mode: .used), planID: "Codex"),
        ]),
      Section(
        title: "Small · no data, no plans, chosen plan unavailable",
        cells: [
          Cell(.systemSmall, nil, planID: nil),
          Cell(.systemSmall, snapshot([]), planID: nil),
          Cell(.systemSmall, mockup, planID: "removed"),
        ]),
      Section(
        title: "Medium · mixed quotas, stale, more than 4 plans, no plans",
        cells: [
          Cell(.systemMedium, mixed), Cell(.systemMedium, stale), Cell(.systemMedium, many),
          Cell(.systemMedium, snapshot([])),
        ]),
      Section(
        title: "Large · more than 4 plans, mixed quotas, stale, no data",
        cells: [
          Cell(.systemLarge, many), Cell(.systemLarge, mixed), Cell(.systemLarge, stale),
          Cell(.systemLarge, nil),
        ]),
      Section(
        title: "Fewer plans",
        cells: [
          Cell(.systemMedium, snapshot([codex, glm])), Cell(.systemMedium, snapshot([kimiMonthly])),
          Cell(.systemLarge, snapshot([codex, weeklyOut])),
        ]),
    ]
  }

  static func languageSections() -> [Section] {
    [
      desk("English", snapshot([codex, claude, glm, kimi], locale: "en")),
      desk("日本語", snapshot([codex, claude, glm, kimi], locale: "ja")),
      desk(
        "English · mixed, weekly used up",
        snapshot([kimiMonthly, weeklyOut, glm, codex], locale: "en"),
        smalls: ["Codex Team", "Kimi Monthly"]),
      desk(
        "日本語 · mixed, weekly used up",
        snapshot([kimiMonthly, weeklyOut, glm, codex], locale: "ja"),
        smalls: ["Codex Team", "Kimi Monthly"]),
      desk("已用 mode", snapshot([codex, claude, glm, kimi], mode: .used)),
    ]
  }

  static func renderingSections() -> [Section] {
    [
      Section(
        title: "Vibrant (desktop not in front)",
        cells: [
          Cell(.systemSmall, mockup, planID: "GLM Coding Plan", rendering: .vibrant),
          Cell(.systemSmall, snapshot([weeklyOut]), planID: "Codex Team", rendering: .vibrant),
          Cell(.systemMedium, mockup, rendering: .vibrant),
          Cell(.systemLarge, mockup, rendering: .vibrant),
        ], desk: true),
      Section(
        title: "Accented",
        cells: [
          Cell(.systemSmall, mockup, planID: "Codex", rendering: .accented),
          Cell(.systemSmall, mockup, planID: "GLM Coding Plan", rendering: .accented),
          Cell(.systemMedium, mockup, rendering: .accented),
          Cell(.systemLarge, mockup, rendering: .accented),
        ], desk: true),
    ]
  }
}

struct Cell: Identifiable {
  let id = UUID()
  var family: WidgetFamily
  var snapshot: QuotaSnapshot?
  var planID: String?
  var rendering: QuotaRendering

  init(
    _ family: WidgetFamily, _ snapshot: QuotaSnapshot?, planID: String? = nil,
    rendering: QuotaRendering = .fullColor
  ) {
    self.family = family
    self.snapshot = snapshot
    self.planID = planID
    self.rendering = rendering
  }

  var size: CGSize {
    switch family {
    case .systemSmall: return CGSize(width: 164, height: 164)
    case .systemMedium: return CGSize(width: 344, height: 164)
    default: return CGSize(width: 344, height: 344)
    }
  }
}

struct Section: Identifiable {
  let id = UUID()
  var title: String
  var cells: [Cell]
  /// The mockup's arrangement: two smalls over a medium, the large beside.
  var desk = false
}

struct CellView: View {
  var cell: Cell
  var scheme: ColorScheme

  var body: some View {
    let palette = QuotaPalette(scheme: scheme, rendering: cell.rendering)
    let card: Color =
      cell.rendering == .fullColor
      ? palette.card
      : (scheme == .dark ? Color(white: 0.12).opacity(0.3) : Color.white.opacity(0.34))
    QuotaWidgetView(
      entry: QuotaEntry(date: RenderPreviews.now, snapshot: cell.snapshot, planID: cell.planID)
    )
    .environment(\.quotaFamilyOverride, cell.family)
    .environment(\.quotaRenderingOverride, cell.rendering)
    .frame(width: cell.size.width, height: cell.size.height)
    .background(card)
    .clipShape(RoundedRectangle(cornerRadius: 22, style: .continuous))
    .overlay(
      RoundedRectangle(cornerRadius: 22, style: .continuous)
        .strokeBorder(Color.black.opacity(scheme == .dark ? 0 : 0.06), lineWidth: 0.5)
    )
    .shadow(color: .black.opacity(scheme == .dark ? 0.45 : 0.16), radius: 12, y: 8)
  }
}

struct Board: View {
  var scheme: ColorScheme
  var sections: [Section]
  var wallpaper = false

  var body: some View {
    VStack(alignment: .leading, spacing: 28) {
      ForEach(sections) { section in
        VStack(alignment: .leading, spacing: 12) {
          Text(section.title)
            .font(.system(size: 13, weight: .semibold))
            .foregroundStyle(scheme == .dark ? Color.white.opacity(0.7) : Color.black.opacity(0.6))
          if section.desk, section.cells.count == 4 {
            HStack(alignment: .top, spacing: 16) {
              VStack(spacing: 16) {
                HStack(spacing: 16) {
                  CellView(cell: section.cells[0], scheme: scheme)
                  CellView(cell: section.cells[1], scheme: scheme)
                }
                CellView(cell: section.cells[2], scheme: scheme)
              }
              CellView(cell: section.cells[3], scheme: scheme)
            }
          } else {
            HStack(alignment: .top, spacing: 16) {
              ForEach(section.cells) { cell in CellView(cell: cell, scheme: scheme) }
            }
          }
        }
      }
    }
    .padding(32)
    .background(background)
    .environment(\.colorScheme, scheme)
  }

  private var background: Color {
    switch (scheme, wallpaper) {
    case (.dark, true): return Color(red: 0.24, green: 0.17, blue: 0.36)
    case (.dark, false): return Color(red: 0.08, green: 0.10, blue: 0.13)
    case (_, true): return Color(red: 0.72, green: 0.64, blue: 0.86)
    default: return Color(red: 0.80, green: 0.84, blue: 0.90)
    }
  }
}
