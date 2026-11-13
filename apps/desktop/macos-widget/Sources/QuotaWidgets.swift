import SwiftUI
import WidgetKit

enum QuotaTimeline {
  /// Entries at each moment the text changes by itself, so reset times and
  /// the stale mark stay right without a reload. The host reloads the
  /// timelines when it writes new numbers.
  static func make(snapshot: QuotaSnapshot?, planID: String?, now: Date) -> Timeline<QuotaEntry> {
    let dates = QuotaPresentation.timelineDates(snapshot: snapshot, now: now)
    let entries = dates.map { QuotaEntry(date: $0, snapshot: snapshot, planID: planID) }
    let next = dates.count > 1 ? dates[dates.count - 1] : now.addingTimeInterval(3600)
    return Timeline(entries: entries, policy: .after(next))
  }

  /// What the widget gallery shows before the host has written anything.
  static func preview(now: Date) -> QuotaSnapshot {
    QuotaSnapshot.load() ?? .sample(now: now)
  }
}

struct PlanQuotaProvider: AppIntentTimelineProvider {
  func placeholder(in context: Context) -> QuotaEntry {
    QuotaEntry(date: .now, snapshot: .sample(now: .now), planID: nil)
  }

  func snapshot(for configuration: SelectPlanIntent, in context: Context) async -> QuotaEntry {
    let now = Date()
    let snapshot = context.isPreview ? QuotaTimeline.preview(now: now) : QuotaSnapshot.load()
    return QuotaEntry(date: now, snapshot: snapshot, planID: configuration.plan?.id)
  }

  func timeline(for configuration: SelectPlanIntent, in context: Context) async -> Timeline<
    QuotaEntry
  > {
    QuotaTimeline.make(snapshot: QuotaSnapshot.load(), planID: configuration.plan?.id, now: Date())
  }
}

struct QuotaOverviewProvider: TimelineProvider {
  func placeholder(in context: Context) -> QuotaEntry {
    QuotaEntry(date: .now, snapshot: .sample(now: .now), planID: nil)
  }

  func getSnapshot(in context: Context, completion: @escaping (QuotaEntry) -> Void) {
    let now = Date()
    let snapshot = context.isPreview ? QuotaTimeline.preview(now: now) : QuotaSnapshot.load()
    completion(QuotaEntry(date: now, snapshot: snapshot, planID: nil))
  }

  func getTimeline(in context: Context, completion: @escaping (Timeline<QuotaEntry>) -> Void) {
    completion(QuotaTimeline.make(snapshot: QuotaSnapshot.load(), planID: nil, now: Date()))
  }
}

/// One plan, chosen in the widget's settings (the first plan by default).
struct PlanQuotaWidget: Widget {
  var body: some WidgetConfiguration {
    AppIntentConfiguration(
      kind: "PlanQuota", intent: SelectPlanIntent.self, provider: PlanQuotaProvider()
    ) { entry in
      QuotaWidgetView(entry: entry)
    }
    .configurationDisplayName("Subscription Quota")
    .description("One plan's quota and when it resets.")
    .supportedFamilies([.systemSmall])
    .contentMarginsDisabled()
  }
}

/// The first four plans in tray order.
struct QuotaOverviewWidget: Widget {
  var body: some WidgetConfiguration {
    StaticConfiguration(kind: "QuotaOverview", provider: QuotaOverviewProvider()) { entry in
      QuotaWidgetView(entry: entry)
    }
    .configurationDisplayName("Subscription Quota")
    .description("Your plans' quotas and when they reset.")
    .supportedFamilies([.systemMedium, .systemLarge])
    .contentMarginsDisabled()
  }
}

extension QuotaSnapshot {
  /// The approved mockup's plans, for the gallery and placeholders.
  static func sample(now: Date) -> QuotaSnapshot {
    func window(_ seconds: Int, used: Double, in hours: Double, secondary: Bool = false)
      -> QuotaWindow
    {
      QuotaWindow(
        label: nil, limitWindowSeconds: seconds, secondary: secondary, usedPercent: used,
        resetAt: now.addingTimeInterval(hours * 3600))
    }
    func plan(_ id: String, _ name: String, _ kind: String, five: Double, week: Double)
      -> QuotaPlan
    {
      QuotaPlan(
        id: id, name: name, kind: kind,
        windows: [
          window(18_000, used: five, in: 2),
          window(604_800, used: week, in: 80, secondary: true),
        ])
    }
    return QuotaSnapshot(
      version: version,
      generatedAt: now,
      locale: Locale.preferredLanguages.first ?? "en",
      quotaDisplay: .remaining,
      plans: [
        plan("sample-codex", "Codex", "codex_subscription", five: 36, week: 59),
        plan("sample-claude", "Claude Code", "claude_subscription", five: 88, week: 42),
        plan("sample-glm", "GLM Coding Plan", "glm_coding", five: 100, week: 24),
        plan("sample-kimi", "Kimi Coding", "kimi_coding", five: 13, week: 21),
      ])
  }
}
